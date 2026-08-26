package metric

import (
	"context"
	"testing"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/metric"
)

// dsReader is a fakeReader specialised for downsample tests. It captures
// the windows the downsampler asked for and returns fixtures.
type dsReader struct {
	rawRows  []model.Point
	fiveRows []model.Bucket5m

	scanRawFrom, scanRawTo time.Time
	scan5mFrom, scan5mTo   time.Time
}

func (r *dsReader) QueryRaw(context.Context, uint64, time.Time, time.Time) ([]model.Point, error) {
	return nil, nil
}
func (r *dsReader) Query5m(context.Context, uint64, time.Time, time.Time) ([]model.Bucket5m, error) {
	return nil, nil
}
func (r *dsReader) Query1h(context.Context, uint64, time.Time, time.Time) ([]model.Bucket1h, error) {
	return nil, nil
}
func (r *dsReader) ScanRawForDownsample(_ context.Context, from, to time.Time) ([]model.Point, error) {
	r.scanRawFrom, r.scanRawTo = from, to
	return r.rawRows, nil
}
func (r *dsReader) Scan5mForDownsample(_ context.Context, from, to time.Time) ([]model.Bucket5m, error) {
	r.scan5mFrom, r.scan5mTo = from, to
	return r.fiveRows, nil
}

// dsWriter captures the buckets Write5m / Write1h receive.
type dsWriter struct {
	bucket5m []model.Bucket5m
	bucket1h []model.Bucket1h
}

func (w *dsWriter) WriteRaw(context.Context, []model.Point) error         { return nil }
func (w *dsWriter) WriteDeadLetter(context.Context, []model.Point, string) error { return nil }
func (w *dsWriter) Write5m(_ context.Context, b []model.Bucket5m) error {
	w.bucket5m = append(w.bucket5m, b...)
	return nil
}
func (w *dsWriter) Write1h(_ context.Context, b []model.Bucket1h) error {
	w.bucket1h = append(w.bucket1h, b...)
	return nil
}
func (w *dsWriter) DeleteRawBefore(context.Context, time.Time, int) (int64, error) { return 0, nil }
func (w *dsWriter) Delete5mBefore(context.Context, time.Time, int) (int64, error)  { return 0, nil }
func (w *dsWriter) Delete1hBefore(context.Context, time.Time, int) (int64, error)  { return 0, nil }

func TestRun5m_AggregatesPerEdge(t *testing.T) {
	bucketEnd := time.Date(2026, 4, 23, 12, 5, 0, 0, time.UTC)
	bucketStart := bucketEnd.Add(-5 * time.Minute)

	// Two edges, each with points inside the bucket.
	r := &dsReader{rawRows: []model.Point{
		{EdgeID: 1, Ts: bucketStart.Add(10 * time.Second), CPUPct: 10, MemPct: 50, Load1: 1, NetRxBps: 100, NetTxBps: 200, DiskUsedPct: 20},
		{EdgeID: 1, Ts: bucketStart.Add(20 * time.Second), CPUPct: 30, MemPct: 70, Load1: 3, NetRxBps: 150, NetTxBps: 300, DiskUsedPct: 30},
		{EdgeID: 2, Ts: bucketStart.Add(30 * time.Second), CPUPct: 90, MemPct: 80, Load1: 7, NetRxBps: 1000, NetTxBps: 1, DiskUsedPct: 50},
	}}
	w := &dsWriter{}
	d := NewDownsampler(w, r, nil)

	if err := d.Run5m(context.Background(), bucketEnd); err != nil {
		t.Fatalf("Run5m: %v", err)
	}

	if len(w.bucket5m) != 2 {
		t.Fatalf("buckets written = %d, want 2", len(w.bucket5m))
	}
	// Find edge 1 bucket.
	var b1, b2 model.Bucket5m
	for _, b := range w.bucket5m {
		if b.EdgeID == 1 {
			b1 = b
		} else if b.EdgeID == 2 {
			b2 = b
		}
	}
	if !b1.Ts.Equal(bucketStart) {
		t.Errorf("b1.Ts = %v, want %v", b1.Ts, bucketStart)
	}
	if b1.CPUAvg != 20 || b1.CPUMax != 30 {
		t.Errorf("edge1 cpu avg/max = %v / %v, want 20 / 30", b1.CPUAvg, b1.CPUMax)
	}
	if b1.MemAvg != 60 || b1.MemMax != 70 {
		t.Errorf("edge1 mem avg/max = %v / %v, want 60 / 70", b1.MemAvg, b1.MemMax)
	}
	if b1.NetRxSum != 250 || b1.NetTxSum != 500 {
		t.Errorf("edge1 net = rx %d tx %d, want 250 / 500", b1.NetRxSum, b1.NetTxSum)
	}
	if b1.Load1Avg != 2 || b1.Load1Max != 3 {
		t.Errorf("edge1 load1 avg/max = %v / %v, want 2 / 3", b1.Load1Avg, b1.Load1Max)
	}
	// Edge 2: single point → avg == max.
	if b2.CPUAvg != 90 || b2.CPUMax != 90 {
		t.Errorf("edge2 cpu avg/max = %v / %v, want 90 / 90", b2.CPUAvg, b2.CPUMax)
	}
	if b2.NetRxSum != 1000 {
		t.Errorf("edge2 net rx sum = %d, want 1000", b2.NetRxSum)
	}
}

func TestRun5m_NoData_NoWrite(t *testing.T) {
	w := &dsWriter{}
	r := &dsReader{}
	d := NewDownsampler(w, r, nil)
	if err := d.Run5m(context.Background(), time.Now()); err != nil {
		t.Fatalf("Run5m empty: %v", err)
	}
	if len(w.bucket5m) != 0 {
		t.Errorf("wrote %d buckets on empty input", len(w.bucket5m))
	}
}

func TestRun1h_AggregatesFromFiveMBuckets(t *testing.T) {
	end := time.Date(2026, 4, 23, 13, 0, 0, 0, time.UTC)
	start := end.Add(-time.Hour)

	r := &dsReader{fiveRows: []model.Bucket5m{
		{EdgeID: 1, Ts: start, CPUAvg: 10, CPUMax: 20, MemAvg: 30, MemMax: 40, NetRxSum: 100, NetTxSum: 200},
		{EdgeID: 1, Ts: start.Add(5 * time.Minute), CPUAvg: 50, CPUMax: 60, MemAvg: 10, MemMax: 20, NetRxSum: 400, NetTxSum: 800},
	}}
	w := &dsWriter{}
	d := NewDownsampler(w, r, nil)

	if err := d.Run1h(context.Background(), end); err != nil {
		t.Fatalf("Run1h: %v", err)
	}
	if len(w.bucket1h) != 1 {
		t.Fatalf("buckets = %d, want 1", len(w.bucket1h))
	}
	b := w.bucket1h[0]
	if b.EdgeID != 1 || !b.Ts.Equal(start) {
		t.Errorf("bucket = %+v", b)
	}
	if b.CPUAvg != 30 || b.CPUMax != 60 {
		t.Errorf("cpu avg/max = %v / %v, want 30 / 60", b.CPUAvg, b.CPUMax)
	}
	if b.NetRxSum != 500 || b.NetTxSum != 1000 {
		t.Errorf("net = rx %d tx %d, want 500 / 1000", b.NetRxSum, b.NetTxSum)
	}
}

func TestNextBoundary(t *testing.T) {
	now := time.Date(2026, 4, 23, 12, 3, 17, 0, time.UTC)
	nb5 := nextBoundary(now, 5*time.Minute)
	if nb5 != time.Date(2026, 4, 23, 12, 5, 0, 0, time.UTC) {
		t.Errorf("next 5m boundary = %v", nb5)
	}
	// On exact boundary, must roll forward (strictly after now).
	now2 := time.Date(2026, 4, 23, 12, 5, 0, 0, time.UTC)
	nb2 := nextBoundary(now2, 5*time.Minute)
	if nb2 != time.Date(2026, 4, 23, 12, 10, 0, 0, time.UTC) {
		t.Errorf("boundary roll-forward = %v", nb2)
	}
}
