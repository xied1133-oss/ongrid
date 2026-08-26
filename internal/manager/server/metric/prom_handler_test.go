// Tests for the generic /v1/metrics/query_range PromQL passthrough that
// EdgeDetail's multi-dim panels lean on. The legacy /v1/edges/{id}/metrics
// path is exercised in http_test.go.
package metric

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ongridio/ongrid/internal/pkg/promquery"
)

// fakePromQuerier captures the last QueryRange call and returns a
// pre-canned response.
type fakePromQuerier struct {
	gotExpr  string
	gotStart time.Time
	gotEnd   time.Time
	gotStep  time.Duration

	resp *promquery.InstantResult
	err  error
}

func (f *fakePromQuerier) QueryRange(_ context.Context, expr string, start, end time.Time, step time.Duration) (*promquery.InstantResult, error) {
	f.gotExpr = expr
	f.gotStart = start
	f.gotEnd = end
	f.gotStep = step
	return f.resp, f.err
}

func newPromRouter(h *PromHandler) http.Handler {
	r := chi.NewRouter()
	h.Register(r)
	return r
}

func TestQueryRange_HappyPath(t *testing.T) {
	matrix := json.RawMessage(`[{"metric":{"cpu":"0"},"values":[[1714572000,"12.3"],[1714572060,"15.0"]]}]`)
	q := &fakePromQuerier{resp: &promquery.InstantResult{ResultType: "matrix", Result: matrix}}
	h := NewPromHandler(q, nil)
	router := newPromRouter(h)

	from := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	to := time.Date(2026, 5, 1, 18, 0, 0, 0, time.UTC).Format(time.RFC3339)
	url := "/v1/metrics/query_range?expr=" + "rate(node_cpu_seconds_total[5m])" +
		"&start=" + from + "&end=" + to + "&step=1m"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d; body=%s", w.Code, w.Body.String())
	}
	var body rangeResp
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, w.Body.String())
	}
	if body.Resolution != "1m0s" {
		t.Errorf("resolution = %q, want 1m0s", body.Resolution)
	}
	if string(body.Matrix) != string(matrix) {
		t.Errorf("matrix not passed through: got %s want %s", body.Matrix, matrix)
	}
	if q.gotStep != time.Minute {
		t.Errorf("step propagated = %v, want 1m", q.gotStep)
	}
	if !strings.Contains(q.gotExpr, "node_cpu_seconds_total") {
		t.Errorf("expr propagated = %q", q.gotExpr)
	}
}

func TestQueryRange_MissingExpr(t *testing.T) {
	q := &fakePromQuerier{}
	h := NewPromHandler(q, nil)
	router := newPromRouter(h)

	from := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().UTC().Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet, "/v1/metrics/query_range?start="+from+"&end="+to+"&step=30s", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d; body=%s", w.Code, w.Body.String())
	}
}

func TestQueryRange_ExprTooLarge(t *testing.T) {
	q := &fakePromQuerier{}
	h := NewPromHandler(q, nil)
	router := newPromRouter(h)

	expr := strings.Repeat("a", maxExprBytes+1)
	from := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().UTC().Format(time.RFC3339)
	url := "/v1/metrics/query_range?expr=" + expr + "&start=" + from + "&end=" + to + "&step=30s"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", w.Code)
	}
}

func TestQueryRange_BadStep(t *testing.T) {
	q := &fakePromQuerier{}
	h := NewPromHandler(q, nil)
	router := newPromRouter(h)

	from := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().UTC().Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet, "/v1/metrics/query_range?expr=up&start="+from+"&end="+to+"&step=garbage", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", w.Code)
	}
}

func TestQueryRange_EndBeforeStart(t *testing.T) {
	q := &fakePromQuerier{}
	h := NewPromHandler(q, nil)
	router := newPromRouter(h)

	now := time.Now().UTC().Format(time.RFC3339)
	earlier := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	// start=now, end=earlier -> invalid
	req := httptest.NewRequest(http.MethodGet, "/v1/metrics/query_range?expr=up&start="+now+"&end="+earlier+"&step=30s", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", w.Code)
	}
}

func TestQueryRange_PromError(t *testing.T) {
	q := &fakePromQuerier{err: errors.New("prom returned 400")}
	h := NewPromHandler(q, nil)
	router := newPromRouter(h)

	from := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().UTC().Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet, "/v1/metrics/query_range?expr=invalid_expr&start="+from+"&end="+to+"&step=30s", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", w.Code)
	}
}

func TestQueryRange_PromDisabled(t *testing.T) {
	h := NewPromHandler(nil, nil)
	router := newPromRouter(h)

	from := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().UTC().Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet, "/v1/metrics/query_range?expr=up&start="+from+"&end="+to+"&step=30s", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// ErrNotWiredYet maps to 501 via errs.HTTPStatus.
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("code = %d, want 501", w.Code)
	}
}

func TestQueryRange_NonMatrixYieldsEmpty(t *testing.T) {
	// query_range should always return matrix; if Prom hands back something
	// else (vector / scalar), we ship "[]" rather than guessing.
	q := &fakePromQuerier{resp: &promquery.InstantResult{ResultType: "vector", Result: json.RawMessage(`[]`)}}
	h := NewPromHandler(q, nil)
	router := newPromRouter(h)

	from := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().UTC().Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet, "/v1/metrics/query_range?expr=up&start="+from+"&end="+to+"&step=30s", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	var body rangeResp
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if string(body.Matrix) != "[]" {
		t.Errorf("matrix = %s, want []", body.Matrix)
	}
}
