package metric

import (
	"context"
	"testing"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/metric"
)

// retentionWriter records the cutoff and limit for each Delete*Before
// call and returns configurable row counts so we can exercise the "loop
// until zero" logic.
type retentionWriter struct {
	rawCutoffs   []time.Time
	fivemCutoffs []time.Time
	onehrCutoffs []time.Time

	// rowsPerCall: each entry is the rows-affected value returned by the
	// next Delete*Before. Tier-agnostic — we share one script.
	rowsPerCall []int64
}

func (w *retentionWriter) WriteRaw(context.Context, []model.Point) error         { return nil }
func (w *retentionWriter) WriteDeadLetter(context.Context, []model.Point, string) error {
	return nil
}
func (w *retentionWriter) Write5m(context.Context, []model.Bucket5m) error { return nil }
func (w *retentionWriter) Write1h(context.Context, []model.Bucket1h) error { return nil }

func (w *retentionWriter) pop() int64 {
	if len(w.rowsPerCall) == 0 {
		return 0
	}
	n := w.rowsPerCall[0]
	w.rowsPerCall = w.rowsPerCall[1:]
	return n
}

func (w *retentionWriter) DeleteRawBefore(_ context.Context, cutoff time.Time, _ int) (int64, error) {
	w.rawCutoffs = append(w.rawCutoffs, cutoff)
	return w.pop(), nil
}
func (w *retentionWriter) Delete5mBefore(_ context.Context, cutoff time.Time, _ int) (int64, error) {
	w.fivemCutoffs = append(w.fivemCutoffs, cutoff)
	return w.pop(), nil
}
func (w *retentionWriter) Delete1hBefore(_ context.Context, cutoff time.Time, _ int) (int64, error) {
	w.onehrCutoffs = append(w.onehrCutoffs, cutoff)
	return w.pop(), nil
}

func TestRetention_RunOnce_CutoffsPerTier(t *testing.T) {
	w := &retentionWriter{}
	// One pass each (0 rows affected → loop exits after 1 call).
	r := NewRetention(w, nil)

	before := time.Now().UTC()
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	after := time.Now().UTC()

	if len(w.rawCutoffs) != 1 || len(w.fivemCutoffs) != 1 || len(w.onehrCutoffs) != 1 {
		t.Fatalf("calls: raw=%d 5m=%d 1h=%d; want 1 1 1",
			len(w.rawCutoffs), len(w.fivemCutoffs), len(w.onehrCutoffs))
	}

	assertWithin := func(name string, cutoff time.Time, ttl time.Duration) {
		expMin := before.Add(-ttl).Add(-time.Second)
		expMax := after.Add(-ttl).Add(time.Second)
		if cutoff.Before(expMin) || cutoff.After(expMax) {
			t.Errorf("%s cutoff %v outside [%v, %v]", name, cutoff, expMin, expMax)
		}
	}
	assertWithin("raw", w.rawCutoffs[0], 7*24*time.Hour)
	assertWithin("5m", w.fivemCutoffs[0], 90*24*time.Hour)
	assertWithin("1h", w.onehrCutoffs[0], 365*24*time.Hour)
}

func TestRetention_LoopsUntilZero(t *testing.T) {
	// Three non-zero returns then zero → 4 calls per tier.
	w := &retentionWriter{
		rowsPerCall: []int64{
			// raw tier
			1000, 1000, 500, 0,
			// 5m tier
			0,
			// 1h tier
			500, 0,
		},
	}
	r := NewRetention(w, nil)
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(w.rawCutoffs) != 4 {
		t.Errorf("raw calls = %d, want 4", len(w.rawCutoffs))
	}
	if len(w.fivemCutoffs) != 1 {
		t.Errorf("5m calls = %d, want 1", len(w.fivemCutoffs))
	}
	if len(w.onehrCutoffs) != 2 {
		t.Errorf("1h calls = %d, want 2", len(w.onehrCutoffs))
	}
}

func TestNextDailyRun(t *testing.T) {
	// Before 03:00 → today at 03:00.
	morning := time.Date(2026, 4, 23, 1, 30, 0, 0, time.UTC)
	if got := nextDailyRun(morning, 3); got != time.Date(2026, 4, 23, 3, 0, 0, 0, time.UTC) {
		t.Errorf("morning → %v", got)
	}
	// After 03:00 → tomorrow at 03:00.
	afternoon := time.Date(2026, 4, 23, 14, 0, 0, 0, time.UTC)
	if got := nextDailyRun(afternoon, 3); got != time.Date(2026, 4, 24, 3, 0, 0, 0, time.UTC) {
		t.Errorf("afternoon → %v", got)
	}
	// Exactly 03:00 → tomorrow (strictly-after semantics).
	exact := time.Date(2026, 4, 23, 3, 0, 0, 0, time.UTC)
	if got := nextDailyRun(exact, 3); got != time.Date(2026, 4, 24, 3, 0, 0, 0, time.UTC) {
		t.Errorf("exact → %v", got)
	}
}
