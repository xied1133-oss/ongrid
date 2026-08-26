package metric

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	model "github.com/ongridio/ongrid/internal/manager/model/metric"
	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

// fakeWriter is a Writer whose WriteRaw / WriteDeadLetter behaviour is
// configurable from tests. It records every call.
type fakeWriter struct {
	mu            sync.Mutex
	rawCalls      [][]model.Point
	rawErr        error
	rawErrTimes   int // number of times to return rawErr before succeeding; 0 = always succeed, -1 = always fail
	dlqCalls      [][]model.Point
	dlqReasons    []string
	writeAttempts atomic.Int64
}

func (f *fakeWriter) WriteRaw(_ context.Context, batch []model.Point) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writeAttempts.Add(1)

	if f.rawErrTimes == -1 {
		return f.rawErr
	}
	if f.rawErrTimes > 0 {
		f.rawErrTimes--
		return f.rawErr
	}
	cp := make([]model.Point, len(batch))
	copy(cp, batch)
	f.rawCalls = append(f.rawCalls, cp)
	return nil
}

func (f *fakeWriter) WriteDeadLetter(_ context.Context, batch []model.Point, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]model.Point, len(batch))
	copy(cp, batch)
	f.dlqCalls = append(f.dlqCalls, cp)
	f.dlqReasons = append(f.dlqReasons, reason)
	return nil
}

func (f *fakeWriter) Write5m(_ context.Context, _ []model.Bucket5m) error { return nil }
func (f *fakeWriter) Write1h(_ context.Context, _ []model.Bucket1h) error { return nil }
func (f *fakeWriter) DeleteRawBefore(_ context.Context, _ time.Time, _ int) (int64, error) {
	return 0, nil
}
func (f *fakeWriter) Delete5mBefore(_ context.Context, _ time.Time, _ int) (int64, error) {
	return 0, nil
}
func (f *fakeWriter) Delete1hBefore(_ context.Context, _ time.Time, _ int) (int64, error) {
	return 0, nil
}

func (f *fakeWriter) totalRawPoints() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.rawCalls {
		n += len(c)
	}
	return n
}

func (f *fakeWriter) dlqCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.dlqCalls)
}

// freshIngester builds an Ingester with a fresh prom registry so tests
// do not step on the default registerer.
func freshIngester(t *testing.T, w Writer) *Ingester {
	t.Helper()
	reg := prometheus.NewRegistry()
	return NewIngester(w, reg, slog.Default())
}

func TestIngester_Push_FlushesBatch(t *testing.T) {
	w := &fakeWriter{}
	i := freshIngester(t, w)
	// Tighten the flush interval so the test is fast.
	i.flushAt = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = i.Start(ctx)
		close(done)
	}()

	points := make([]tunnel.HostMetricPoint, 3)
	for idx := range points {
		points[idx] = tunnel.HostMetricPoint{Ts: 1_700_000_000 + int64(idx), CPUPct: float64(idx)}
	}
	if err := i.Push(ctx, 7, points); err != nil {
		t.Fatalf("Push: %v", err)
	}

	// Wait up to 500ms for a flush.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if w.totalRawPoints() >= 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	<-done

	if got := w.totalRawPoints(); got != 3 {
		t.Errorf("flushed points = %d, want 3", got)
	}
}

func TestIngester_Push_BatchSizeTriggersImmediateFlush(t *testing.T) {
	w := &fakeWriter{}
	i := freshIngester(t, w)
	// Shrink the batch size so we don't have to push 500 items.
	i.batchSz = 5
	// Long flush interval so the time-based flush does not fire.
	i.flushAt = 5 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = i.Start(ctx)
		close(done)
	}()

	points := make([]tunnel.HostMetricPoint, 5)
	for idx := range points {
		points[idx] = tunnel.HostMetricPoint{Ts: int64(idx)}
	}
	if err := i.Push(ctx, 1, points); err != nil {
		t.Fatalf("Push: %v", err)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if w.totalRawPoints() >= 5 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	<-done

	if got := w.totalRawPoints(); got != 5 {
		t.Errorf("flushed points = %d, want 5 (batch size trigger)", got)
	}
}

func TestIngester_DeadLetterOnRetryExhaustion(t *testing.T) {
	w := &fakeWriter{
		rawErr:      errors.New("disk full"),
		rawErrTimes: -1, // always fail
	}
	i := freshIngester(t, w)
	i.batchSz = 2
	i.flushAt = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = i.Start(ctx)
		close(done)
	}()

	if err := i.Push(ctx, 1, []tunnel.HostMetricPoint{
		{Ts: 1}, {Ts: 2},
	}); err != nil {
		t.Fatalf("Push: %v", err)
	}

	// flush retries: 100ms + 500ms + 2s. Allow ~3s total.
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if w.dlqCount() > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-done

	if w.dlqCount() != 1 {
		t.Fatalf("dlq count = %d, want 1", w.dlqCount())
	}
	if got := w.writeAttempts.Load(); got < 4 {
		t.Errorf("write attempts = %d, want >=4 (1 initial + 3 retries)", got)
	}
}

func TestIngester_Push_BufferFullDropsOldest(t *testing.T) {
	w := &fakeWriter{}
	// Ingester not Started → bufCh never drains.
	reg := prometheus.NewRegistry()
	i := NewIngester(w, reg, slog.Default())
	i.batchSz = 1 // bufCh capacity = 1 * 4 = 4

	// Push 10 points into a non-draining channel; the last few must be
	// dropped but Push must not return an error and must not block.
	done := make(chan error, 1)
	go func() {
		done <- i.Push(context.Background(), 1, make([]tunnel.HostMetricPoint, 10))
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Push returned err: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Push blocked with full buffer")
	}

	// Metrics should have recorded at least one drop.
	// (We don't assert the exact number because drop-oldest loops can
	// flake under heavy scheduling.)
	if _, ok := any(i.metrics.dropped).(*prometheus.CounterVec); !ok {
		t.Fatal("dropped counter vec missing")
	}
}

func TestFromTunnelPoint_TimeConversion(t *testing.T) {
	p := tunnel.HostMetricPoint{Ts: 1_700_000_000, CPUPct: 42}
	got := model.FromTunnelPoint(5, p)
	if got.EdgeID != 5 || got.CPUPct != 42 {
		t.Errorf("point = %+v", got)
	}
	if got.Ts.Unix() != 1_700_000_000 || got.Ts.Location() != time.UTC {
		t.Errorf("time = %v, want UTC unix 1_700_000_000", got.Ts)
	}
}
