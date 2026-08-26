package metric

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	model "github.com/ongridio/ongrid/internal/manager/model/metric"
	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

// Ingester batches incoming samples and flushes them to the Writer every
// flushAt or every batchSz rows, whichever comes first (writes).
//
// Push is non-blocking: when bufCh fills past capacity the oldest entry
// is evicted and an ongrid_ingest_dropped_total{reason="buffer_full"}
// counter is incremented. Metrics are lossy-OK; callers do not retry.
//
// Flusher goroutine retries failed writes with 100ms / 500ms / 2s
// exponential backoff; if all three fail, the batch is written to the
// dead-letter table.
type Ingester struct {
	writer  Writer
	log     *slog.Logger
	metrics *ingestMetrics

	bufCh   chan model.Point
	batchSz int
	flushAt time.Duration

	// bufMu serialises the drop-oldest path so two concurrent Pushes do
	// not both try to evict from an empty channel.
	bufMu sync.Mutex
}

// IngestService is the narrow contract the tunnel-side handler consumes.
// The service package re-exports the same method set.
type IngestService interface {
	Push(ctx context.Context, edgeID uint64, points []tunnel.HostMetricPoint) error
}

// Defaults
const (
	defaultBatchSize    = 500
	defaultFlushAt      = 5 * time.Second
	bufferCapMultiplier = 4 // bufCh capacity = batchSz * bufferCapMultiplier
)

// Retry schedule for a failing Writer.WriteRaw call.
var flushBackoffs = []time.Duration{
	100 * time.Millisecond,
	500 * time.Millisecond,
	2 * time.Second,
}

// ingestMetrics is the owned prometheus counter/histogram handle set.
type ingestMetrics struct {
	writes     *prometheus.CounterVec   // label: result=success|fail
	flushFails *prometheus.CounterVec   // label: reason
	dropped    *prometheus.CounterVec   // label: reason
	batchSize  prometheus.Histogram     // observed batch size on flush
}

// NewIngester builds an Ingester with defaults and registers its
// prom metrics on reg. A nil reg falls back to prometheus.DefaultRegisterer
// with a WARN log; registration errors (e.g. duplicate metrics) are also
// downgraded to WARN so a double-wired test does not crash the process.
func NewIngester(w Writer, reg *prometheus.Registry, log *slog.Logger) *Ingester {
	if log == nil {
		log = slog.Default()
	}

	m := &ingestMetrics{
		writes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ongrid_ingest_writes_total",
			Help: "Host-metric ingest batches persisted, labelled by flush result.",
		}, []string{"result"}),
		flushFails: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ongrid_ingest_flush_failures_total",
			Help: "Host-metric ingest batches that exhausted retries.",
		}, []string{"reason"}),
		dropped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ongrid_ingest_dropped_total",
			Help: "Host-metric points dropped before flush.",
		}, []string{"reason"}),
		batchSize: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "ongrid_ingest_batch_size",
			Help:    "Size of host-metric batches at flush time.",
			Buckets: []float64{10, 50, 100, 250, 500},
		}),
	}

	var registerer prometheus.Registerer = reg
	if registerer == nil {
		log.Warn("metric ingester: nil prometheus registry; using default registerer")
		registerer = prometheus.DefaultRegisterer
	}
	for _, c := range []prometheus.Collector{m.writes, m.flushFails, m.dropped, m.batchSize} {
		if err := registerer.Register(c); err != nil {
			// Already-registered is expected in tests that reuse the default
			// registerer; do not crash the process.
			log.Warn("metric ingester: register prom collector", "err", err)
		}
	}

	batchSz := defaultBatchSize
	return &Ingester{
		writer:  w,
		log:     log,
		metrics: m,
		bufCh:   make(chan model.Point, batchSz*bufferCapMultiplier),
		batchSz: batchSz,
		flushAt: defaultFlushAt,
	}
}

// Start runs the flusher loop until ctx is done. Safe to call from a
// goroutine; returns nil when ctx cancels after a final drain attempt.
func (i *Ingester) Start(ctx context.Context) error {
	batch := make([]model.Point, 0, i.batchSz)
	ticker := time.NewTicker(i.flushAt)
	defer ticker.Stop()

	for {
		select {
		case p := <-i.bufCh:
			batch = append(batch, p)
			if len(batch) >= i.batchSz {
				i.flush(ctx, batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				i.flush(ctx, batch)
				batch = batch[:0]
			}
		case <-ctx.Done():
			// Drain what we can without blocking, then final flush.
			for drained := true; drained; {
				select {
				case p := <-i.bufCh:
					batch = append(batch, p)
				default:
					drained = false
				}
			}
			if len(batch) > 0 {
				// Use a fresh context: the parent is already done, but
				// writers may still be usable. We cap the final flush
				// so shutdown is bounded.
				fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				i.flush(fctx, batch)
				cancel()
			}
			return nil
		}
	}
}

// Push enqueues points for async flush. Never returns an error to the
// caller: metrics are lossy-OK and the tunnel handler should not retry.
// A full buffer triggers drop-oldest + a dropped counter bump.
func (i *Ingester) Push(_ context.Context, edgeID uint64, points []tunnel.HostMetricPoint) error {
	for _, p := range points {
		i.enqueue(model.FromTunnelPoint(edgeID, p))
	}
	return nil
}

// enqueue sends one point to bufCh, evicting the oldest queued point
// under backpressure. The lock keeps drop-oldest + re-send atomic.
func (i *Ingester) enqueue(p model.Point) {
	select {
	case i.bufCh <- p:
		return
	default:
	}

	i.bufMu.Lock()
	defer i.bufMu.Unlock()

	// Drop oldest, make room, re-attempt. If still full (producer faster
	// than drain), drop p itself.
	select {
	case <-i.bufCh:
		i.metrics.dropped.WithLabelValues("buffer_full").Inc()
		i.log.Warn("metric ingester: buffer full, dropped oldest point")
	default:
	}
	select {
	case i.bufCh <- p:
	default:
		i.metrics.dropped.WithLabelValues("buffer_full").Inc()
	}
}

// flush writes one batch with the retry/dead-letter policy.
func (i *Ingester) flush(ctx context.Context, batch []model.Point) {
	i.metrics.batchSize.Observe(float64(len(batch)))

	// Defensive copy: callers reuse the slice after flush returns.
	payload := make([]model.Point, len(batch))
	copy(payload, batch)

	var lastErr error
	for attempt := 0; attempt <= len(flushBackoffs); attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(flushBackoffs[attempt-1]):
			case <-ctx.Done():
				lastErr = ctx.Err()
				break
			}
		}
		if err := i.writer.WriteRaw(ctx, payload); err != nil {
			lastErr = err
			i.log.Warn("metric ingester: write raw failed", "attempt", attempt, "err", err)
			continue
		}
		i.metrics.writes.WithLabelValues("success").Inc()
		return
	}

	// All retries exhausted.
	i.metrics.writes.WithLabelValues("fail").Inc()
	reason := "write_error"
	if lastErr != nil {
		reason = classifyReason(lastErr)
	}
	i.metrics.flushFails.WithLabelValues(reason).Inc()
	i.log.Error("metric ingester: flush retries exhausted; routing to dead letter",
		"batch", len(payload), "err", lastErr)

	dlReason := "flush_retries_exhausted"
	if lastErr != nil {
		dlReason = lastErr.Error()
		if len(dlReason) > 256 {
			dlReason = dlReason[:256]
		}
	}
	if err := i.writer.WriteDeadLetter(ctx, payload, dlReason); err != nil {
		i.log.Error("metric ingester: dead-letter write failed", "err", err)
	}
}

// classifyReason bucketises error strings into a small label set so the
// flush-failures counter stays low-cardinality.
func classifyReason(err error) string {
	if err == nil {
		return "unknown"
	}
	// Keep this intentionally coarse; avoid turning err strings into
	// labels (cardinality blow-up).
	if ctxErr := err.Error(); ctxErr == context.Canceled.Error() || ctxErr == context.DeadlineExceeded.Error() {
		return "ctx_cancel"
	}
	return "write_error"
}
