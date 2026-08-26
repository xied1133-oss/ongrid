package metric

import (
	"context"
	"errors"
	"testing"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/metric"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// fakeReader records the last call and returns configurable fixtures.
type fakeReader struct {
	rawCalled    bool
	fivemCalled  bool
	onehrCalled  bool
	scanRawCalls int
	scan5mCalls  int

	rawRows   []model.Point
	fivemRows []model.Bucket5m
	onehrRows []model.Bucket1h
}

func (f *fakeReader) QueryRaw(context.Context, uint64, time.Time, time.Time) ([]model.Point, error) {
	f.rawCalled = true
	return f.rawRows, nil
}
func (f *fakeReader) Query5m(context.Context, uint64, time.Time, time.Time) ([]model.Bucket5m, error) {
	f.fivemCalled = true
	return f.fivemRows, nil
}
func (f *fakeReader) Query1h(context.Context, uint64, time.Time, time.Time) ([]model.Bucket1h, error) {
	f.onehrCalled = true
	return f.onehrRows, nil
}
func (f *fakeReader) ScanRawForDownsample(context.Context, time.Time, time.Time) ([]model.Point, error) {
	f.scanRawCalls++
	return nil, nil
}
func (f *fakeReader) Scan5mForDownsample(context.Context, time.Time, time.Time) ([]model.Bucket5m, error) {
	f.scan5mCalls++
	return nil, nil
}

func TestQuery_AutoResolutionTable(t *testing.T) {
	cases := []struct {
		name       string
		window     time.Duration
		wantRaw    bool
		want5m     bool
		want1h     bool
		wantResTxt Resolution
	}{
		{"1h → raw", time.Hour, true, false, false, ResRaw},
		{"6h boundary → raw", 6 * time.Hour, true, false, false, ResRaw},
		{"1d → 5m", 24 * time.Hour, false, true, false, Res5m},
		{"7d boundary → 5m", 7 * 24 * time.Hour, false, true, false, Res5m},
		{"30d → 1h", 30 * 24 * time.Hour, false, false, true, Res1h},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeReader{}
			u := NewQueryUsecase(r, nil)

			to := time.Now()
			from := to.Add(-tc.window)
			s, err := u.Query(context.Background(), RangeQuery{
				EdgeID: 1, From: from, To: to, Resolution: ResAuto,
			})
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if s.Resolution != tc.wantResTxt {
				t.Errorf("resolution = %q, want %q", s.Resolution, tc.wantResTxt)
			}
			if r.rawCalled != tc.wantRaw || r.fivemCalled != tc.want5m || r.onehrCalled != tc.want1h {
				t.Errorf("table routing wrong: raw=%v 5m=%v 1h=%v",
					r.rawCalled, r.fivemCalled, r.onehrCalled)
			}
		})
	}
}

func TestQuery_ExplicitResolutionOverridesAuto(t *testing.T) {
	r := &fakeReader{}
	u := NewQueryUsecase(r, nil)
	to := time.Now()
	from := to.Add(-30 * 24 * time.Hour) // auto would pick 1h
	_, err := u.Query(context.Background(), RangeQuery{
		EdgeID: 1, From: from, To: to, Resolution: ResRaw,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !r.rawCalled || r.fivemCalled || r.onehrCalled {
		t.Errorf("raw not chosen: raw=%v 5m=%v 1h=%v", r.rawCalled, r.fivemCalled, r.onehrCalled)
	}
}

func TestQuery_RejectsBadInputs(t *testing.T) {
	u := NewQueryUsecase(&fakeReader{}, nil)

	cases := []struct {
		name string
		q    RangeQuery
	}{
		{"zero edge", RangeQuery{From: time.Now().Add(-time.Hour), To: time.Now()}},
		{"from == to", RangeQuery{EdgeID: 1, From: time.Unix(1, 0), To: time.Unix(1, 0)}},
		{"from > to", RangeQuery{EdgeID: 1, From: time.Unix(2, 0), To: time.Unix(1, 0)}},
		{"window > 365d", RangeQuery{EdgeID: 1, From: time.Now().Add(-400 * 24 * time.Hour), To: time.Now()}},
		{"unknown resolution", RangeQuery{EdgeID: 1, From: time.Unix(0, 0), To: time.Unix(1, 0), Resolution: Resolution("whoopee")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := u.Query(context.Background(), tc.q)
			if !errors.Is(err, errs.ErrInvalid) {
				t.Errorf("err = %v, want wraps errs.ErrInvalid", err)
			}
		})
	}
}

func TestQuery_ReturnsReaderRows(t *testing.T) {
	r := &fakeReader{
		rawRows: []model.Point{{EdgeID: 1, Ts: time.Unix(1, 0), CPUPct: 11}},
	}
	u := NewQueryUsecase(r, nil)
	to := time.Now()
	from := to.Add(-time.Hour)
	s, err := u.Query(context.Background(), RangeQuery{EdgeID: 1, From: from, To: to})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(s.RawPoints) != 1 || s.RawPoints[0].CPUPct != 11 {
		t.Errorf("raw points = %+v", s.RawPoints)
	}
	if len(s.Buckets5m) != 0 || len(s.Buckets1h) != 0 {
		t.Errorf("non-raw slices populated: 5m=%d 1h=%d", len(s.Buckets5m), len(s.Buckets1h))
	}
}
