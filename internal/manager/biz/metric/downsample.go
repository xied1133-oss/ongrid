package metric

import (
	"context"
	"log/slog"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/metric"
)

// Downsampler rolls raw samples into 5m and 1h aggregates on a schedule.
//
// Run5m / Run1h are the functional core (one-shot aggregate + write).
// Loop runs the 5m / 1h cadences in a single goroutine, exiting on ctx.
type Downsampler struct {
	writer Writer
	reader Reader
	log    *slog.Logger
}

// Aggregation cadences. Kept package-private so tests can't race against
// wall-clock scheduling.
const (
	interval5m = 5 * time.Minute
	interval1h = time.Hour
)

// NewDownsampler constructs a Downsampler.
func NewDownsampler(w Writer, r Reader, log *slog.Logger) *Downsampler {
	if log == nil {
		log = slog.Default()
	}
	return &Downsampler{writer: w, reader: r, log: log}
}

// Run5m aggregates all raw points in the 5-minute bucket ending at
// bucketEnd (exclusive) and persists one row per edge_id.
//
// Bucket start is bucketEnd - 5m; the stored ts is the bucket START
// (aligned to 5 minutes).
func (d *Downsampler) Run5m(ctx context.Context, bucketEnd time.Time) error {
	bucketEnd = bucketEnd.Truncate(interval5m).UTC()
	bucketStart := bucketEnd.Add(-interval5m)

	// ScanRawForDownsample uses [from, to] inclusive; we pass to-1ns so
	// the next bucket's first sample does not leak in.
	pts, err := d.reader.ScanRawForDownsample(ctx, bucketStart, bucketEnd.Add(-time.Nanosecond))
	if err != nil {
		return err
	}
	if len(pts) == 0 {
		return nil
	}

	buckets := aggregate5m(pts, bucketStart)
	if len(buckets) == 0 {
		return nil
	}
	return d.writer.Write5m(ctx, buckets)
}

// Run1h aggregates all 5m buckets in the 1-hour bucket ending at
// bucketEnd and persists one row per edge_id.
func (d *Downsampler) Run1h(ctx context.Context, bucketEnd time.Time) error {
	bucketEnd = bucketEnd.Truncate(interval1h).UTC()
	bucketStart := bucketEnd.Add(-interval1h)

	bs, err := d.reader.Scan5mForDownsample(ctx, bucketStart, bucketEnd.Add(-time.Nanosecond))
	if err != nil {
		return err
	}
	if len(bs) == 0 {
		return nil
	}
	out := aggregate1h(bs, bucketStart)
	if len(out) == 0 {
		return nil
	}
	return d.writer.Write1h(ctx, out)
}

// Loop runs the 5m and 1h cadences forever (until ctx is done). It first
// aligns to the next wall-clock bucket boundary for each cadence so
// downsample writes do not race with the current (still-filling) bucket.
func (d *Downsampler) Loop(ctx context.Context) error {
	// Sleep until the next 5m / 1h boundary before the first tick.
	if err := sleepUntil(ctx, nextBoundary(time.Now(), interval5m)); err != nil {
		return err
	}

	t5 := time.NewTicker(interval5m)
	t1h := time.NewTicker(interval1h)
	defer t5.Stop()
	defer t1h.Stop()

	// Kick off an immediate run to catch the just-completed bucket.
	if err := d.Run5m(ctx, time.Now()); err != nil {
		d.log.Warn("downsampler: initial Run5m failed", "err", err)
	}

	for {
		select {
		case now := <-t5.C:
			if err := d.Run5m(ctx, now); err != nil {
				d.log.Warn("downsampler: Run5m failed", "err", err)
			}
		case now := <-t1h.C:
			if err := d.Run1h(ctx, now); err != nil {
				d.log.Warn("downsampler: Run1h failed", "err", err)
			}
		case <-ctx.Done():
			return nil
		}
	}
}

// ---------------------------------------------------------------------
// pure aggregation helpers (exported-internal for tests)
// ---------------------------------------------------------------------

// aggregate5m groups pts by edge_id and computes avg/max for gauges +
// sum for counters. Ts on the returned buckets is bucketStart.
func aggregate5m(pts []model.Point, bucketStart time.Time) []model.Bucket5m {
	if len(pts) == 0 {
		return nil
	}
	type acc struct {
		n                                       int
		cpuSum, memSum                          float64
		cpuMax, memMax                          float64
		l1Sum, l5Sum, l15Sum                    float64
		l1Max, l5Max, l15Max                    float64
		netRx, netTx                            uint64
		diskSum                                 float64
		diskMax                                 float64
	}
	by := make(map[uint64]*acc)
	for _, p := range pts {
		a := by[p.EdgeID]
		if a == nil {
			a = &acc{}
			by[p.EdgeID] = a
		}
		a.n++
		a.cpuSum += p.CPUPct
		if p.CPUPct > a.cpuMax {
			a.cpuMax = p.CPUPct
		}
		a.memSum += p.MemPct
		if p.MemPct > a.memMax {
			a.memMax = p.MemPct
		}
		a.l1Sum += p.Load1
		if p.Load1 > a.l1Max {
			a.l1Max = p.Load1
		}
		a.l5Sum += p.Load5
		if p.Load5 > a.l5Max {
			a.l5Max = p.Load5
		}
		a.l15Sum += p.Load15
		if p.Load15 > a.l15Max {
			a.l15Max = p.Load15
		}
		a.netRx += p.NetRxBps
		a.netTx += p.NetTxBps
		a.diskSum += p.DiskUsedPct
		if p.DiskUsedPct > a.diskMax {
			a.diskMax = p.DiskUsedPct
		}
	}
	out := make([]model.Bucket5m, 0, len(by))
	for id, a := range by {
		n := float64(a.n)
		out = append(out, model.Bucket5m{
			EdgeID:      id,
			Ts:          bucketStart,
			CPUAvg:      a.cpuSum / n,
			CPUMax:      a.cpuMax,
			MemAvg:      a.memSum / n,
			MemMax:      a.memMax,
			Load1Avg:    a.l1Sum / n,
			Load1Max:    a.l1Max,
			Load5Avg:    a.l5Sum / n,
			Load5Max:    a.l5Max,
			Load15Avg:   a.l15Sum / n,
			Load15Max:   a.l15Max,
			NetRxSum:    a.netRx,
			NetTxSum:    a.netTx,
			DiskUsedAvg: a.diskSum / n,
			DiskUsedMax: a.diskMax,
		})
	}
	return out
}

// aggregate1h groups 5m buckets by edge_id and computes weighted-free
// avg of avg-fields, max of max-fields, sum of counter-fields.
//
// We do NOT re-weight by sample count (the 5m buckets lose that info);
// all 12 same-hour buckets are weighted equally. This is the same
// trade-off Prometheus downsampling tools make.
func aggregate1h(bs []model.Bucket5m, bucketStart time.Time) []model.Bucket1h {
	if len(bs) == 0 {
		return nil
	}
	type acc struct {
		n                                         int
		cpuAvgSum, memAvgSum                      float64
		cpuMax, memMax                            float64
		l1AvgSum, l5AvgSum, l15AvgSum             float64
		l1Max, l5Max, l15Max                      float64
		netRx, netTx                              uint64
		diskAvgSum, diskMax                       float64
	}
	by := make(map[uint64]*acc)
	for _, b := range bs {
		a := by[b.EdgeID]
		if a == nil {
			a = &acc{}
			by[b.EdgeID] = a
		}
		a.n++
		a.cpuAvgSum += b.CPUAvg
		if b.CPUMax > a.cpuMax {
			a.cpuMax = b.CPUMax
		}
		a.memAvgSum += b.MemAvg
		if b.MemMax > a.memMax {
			a.memMax = b.MemMax
		}
		a.l1AvgSum += b.Load1Avg
		if b.Load1Max > a.l1Max {
			a.l1Max = b.Load1Max
		}
		a.l5AvgSum += b.Load5Avg
		if b.Load5Max > a.l5Max {
			a.l5Max = b.Load5Max
		}
		a.l15AvgSum += b.Load15Avg
		if b.Load15Max > a.l15Max {
			a.l15Max = b.Load15Max
		}
		a.netRx += b.NetRxSum
		a.netTx += b.NetTxSum
		a.diskAvgSum += b.DiskUsedAvg
		if b.DiskUsedMax > a.diskMax {
			a.diskMax = b.DiskUsedMax
		}
	}
	out := make([]model.Bucket1h, 0, len(by))
	for id, a := range by {
		n := float64(a.n)
		out = append(out, model.Bucket1h{
			EdgeID:      id,
			Ts:          bucketStart,
			CPUAvg:      a.cpuAvgSum / n,
			CPUMax:      a.cpuMax,
			MemAvg:      a.memAvgSum / n,
			MemMax:      a.memMax,
			Load1Avg:    a.l1AvgSum / n,
			Load1Max:    a.l1Max,
			Load5Avg:    a.l5AvgSum / n,
			Load5Max:    a.l5Max,
			Load15Avg:   a.l15AvgSum / n,
			Load15Max:   a.l15Max,
			NetRxSum:    a.netRx,
			NetTxSum:    a.netTx,
			DiskUsedAvg: a.diskAvgSum / n,
			DiskUsedMax: a.diskMax,
		})
	}
	return out
}

// nextBoundary returns the nearest future time strictly greater than now
// that is aligned to interval (e.g. the next wall-clock 5m or 1h mark).
func nextBoundary(now time.Time, interval time.Duration) time.Time {
	trunc := now.Truncate(interval)
	if !trunc.After(now) {
		trunc = trunc.Add(interval)
	}
	return trunc
}

// sleepUntil blocks until t or ctx.Done, whichever first. Returns the
// ctx error if cancelled; nil on scheduled wake.
func sleepUntil(ctx context.Context, t time.Time) error {
	d := time.Until(t)
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
