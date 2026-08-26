package metric

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	biz "github.com/ongridio/ongrid/internal/manager/biz/metric"
	model "github.com/ongridio/ongrid/internal/manager/model/metric"
)

// fakeSvc records the RangeQuery it received and returns a configured
// Series. Matches the MetricService interface structurally.
type fakeSvc struct {
	lastQuery biz.RangeQuery
	ret       *biz.Series
	err       error
}

func (f *fakeSvc) Query(_ context.Context, q biz.RangeQuery) (*biz.Series, error) {
	f.lastQuery = q
	return f.ret, f.err
}

func newRouter(h *Handler) http.Handler {
	r := chi.NewRouter()
	h.Register(r)
	return r
}

func TestMetrics_Get_Raw_HappyPath(t *testing.T) {
	ts := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	svc := &fakeSvc{ret: &biz.Series{
		Resolution: biz.ResRaw,
		RawPoints: []model.Point{
			{EdgeID: 7, Ts: ts, CPUPct: 33, MemPct: 44, Load1: 1, NetRxBps: 100, NetTxBps: 200, DiskUsedPct: 55},
		},
	}}
	h := NewHandler(svc)
	router := newRouter(h)

	from := ts.Add(-30 * time.Minute).Format(time.RFC3339)
	to := ts.Add(30 * time.Minute).Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet, "/v1/edges/7/metrics?from="+from+"&to="+to+"&resolution=raw", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d; body=%s", w.Code, w.Body.String())
	}
	var body queryResp
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, w.Body.String())
	}
	if body.Resolution != "raw" || len(body.Points) != 1 {
		t.Fatalf("body = %+v", body)
	}
	p := body.Points[0]
	// For raw, avg == max for gauges. After PR-C2 fix the wire shape uses
	// pointer types so missing data renders as null on the wire (chart
	// breaks the line at gaps). Raw points always have data so the
	// pointers should be non-nil here.
	if p.CPU.Avg == nil || *p.CPU.Avg != 33 || p.CPU.Max == nil || *p.CPU.Max != 33 {
		t.Errorf("cpu avg/max = %v/%v", p.CPU.Avg, p.CPU.Max)
	}
	if p.NetRxBps == nil || *p.NetRxBps != 100 || p.NetTxBps == nil || *p.NetTxBps != 200 {
		t.Errorf("net rx/tx = %v/%v", p.NetRxBps, p.NetTxBps)
	}
	if svc.lastQuery.EdgeID != 7 || svc.lastQuery.Resolution != biz.ResRaw {
		t.Errorf("lastQuery = %+v", svc.lastQuery)
	}
}

func TestMetrics_Get_5m_PropagatesAvgAndMax(t *testing.T) {
	ts := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	svc := &fakeSvc{ret: &biz.Series{
		Resolution: biz.Res5m,
		Buckets5m: []model.Bucket5m{{
			EdgeID: 1, Ts: ts,
			CPUAvg: 10, CPUMax: 80,
			MemAvg: 50, MemMax: 90,
			NetRxSum: 100_000, NetTxSum: 200_000,
		}},
	}}
	h := NewHandler(svc)
	router := newRouter(h)

	from := ts.Add(-time.Hour).Format(time.RFC3339)
	to := ts.Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet, "/v1/edges/1/metrics?from="+from+"&to="+to+"&resolution=5m", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	var body queryResp
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body.Points) != 1 {
		t.Fatalf("points = %+v", body.Points)
	}
	p := body.Points[0]
	if p.CPU.Avg == nil || *p.CPU.Avg != 10 || p.CPU.Max == nil || *p.CPU.Max != 80 {
		t.Errorf("cpu avg/max = %v/%v, want 10/80", p.CPU.Avg, p.CPU.Max)
	}
	if p.NetRxBps == nil || *p.NetRxBps != 100_000 || p.NetTxBps == nil || *p.NetTxBps != 200_000 {
		t.Errorf("net rx/tx = %v/%v", p.NetRxBps, p.NetTxBps)
	}
}

func TestMetrics_Get_BadID(t *testing.T) {
	svc := &fakeSvc{}
	h := NewHandler(svc)
	router := newRouter(h)

	from := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().UTC().Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet, "/v1/edges/abc/metrics?from="+from+"&to="+to, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestMetrics_Get_BadFrom(t *testing.T) {
	svc := &fakeSvc{}
	h := NewHandler(svc)
	router := newRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/v1/edges/1/metrics?from=not-a-date&to=2026-04-23T12:00:00Z", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", w.Code)
	}
}

func TestMetrics_Get_WindowTooLarge(t *testing.T) {
	// We rely on biz validation; the fake svc just forwards error.
	// Use a real QueryUsecase behind a reader that is never called.
	uc := biz.NewQueryUsecase(&nullReader{}, nil)
	// Wrap uc in a service-like adapter satisfying MetricService.
	adapter := &ucAdapter{uc: uc}
	h := NewHandler(adapter)
	router := newRouter(h)

	now := time.Now().UTC()
	from := now.Add(-400 * 24 * time.Hour).Format(time.RFC3339)
	to := now.Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet, "/v1/edges/1/metrics?from="+from+"&to="+to, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", w.Code)
	}
}

// --- tiny adapters for TestMetrics_Get_WindowTooLarge ---

type ucAdapter struct{ uc *biz.QueryUsecase }

func (a *ucAdapter) Query(ctx context.Context, q biz.RangeQuery) (*biz.Series, error) {
	return a.uc.Query(ctx, q)
}

type nullReader struct{}

func (nullReader) QueryRaw(context.Context, uint64, time.Time, time.Time) ([]model.Point, error) {
	return nil, nil
}
func (nullReader) Query5m(context.Context, uint64, time.Time, time.Time) ([]model.Bucket5m, error) {
	return nil, nil
}
func (nullReader) Query1h(context.Context, uint64, time.Time, time.Time) ([]model.Bucket1h, error) {
	return nil, nil
}
func (nullReader) ScanRawForDownsample(context.Context, time.Time, time.Time) ([]model.Point, error) {
	return nil, nil
}
func (nullReader) Scan5mForDownsample(context.Context, time.Time, time.Time) ([]model.Bucket5m, error) {
	return nil, nil
}
