package tools

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	edgebiz "github.com/ongridio/ongrid/internal/manager/biz/edge"
	"github.com/ongridio/ongrid/internal/pkg/promquery"
)

// fakePromQuerier captures the last QueryRange call.
type fakePromQuerier struct {
	mu       sync.Mutex
	gotExpr  string
	gotStep  time.Duration
	gotStart time.Time
	gotEnd   time.Time
	resp     *promquery.InstantResult
	err      error
}

func (f *fakePromQuerier) QueryRange(_ context.Context, expr string, start, end time.Time, step time.Duration) (*promquery.InstantResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotExpr = expr
	f.gotStart = start
	f.gotEnd = end
	f.gotStep = step
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

// Query is the instant-form sibling of QueryRange. The PromQuerier
// interface grew it so correlate_incident.go can probe a single point
// in time; the smoke tests in this package don't exercise it, so the
// stub mirrors QueryRange's response/error behavior without recording
// timing.
func (f *fakePromQuerier) Query(_ context.Context, expr string, _ time.Time) (*promquery.InstantResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotExpr = expr
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func TestQueryPromQL_RoundTrip(t *testing.T) {
	pq := &fakePromQuerier{
		resp: &promquery.InstantResult{
			ResultType: "matrix",
			Result:     json.RawMessage(`[{"metric":{"__name__":"up"},"values":[[1,"1"]]}]`),
		},
	}
	uc := edgebiz.NewUsecase(newFakeEdgeRepo(), nil, nil, slog.Default())
	reg := NewRegistry(&fakeCaller{}, uc, nil, pq, nil, nil, nil, slog.Default())

	if !containsName(schemaNames(reg.Schemas()), ToolNameQueryPromQL) {
		t.Errorf("query_promql not registered: %v", schemaNames(reg.Schemas()))
	}

	out, err := reg.Invoke(context.Background(), ToolNameQueryPromQL,
		json.RawMessage(`{"expr":"up","lookback_seconds":600}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if pq.gotExpr != "up" {
		t.Errorf("expr = %q", pq.gotExpr)
	}
	if pq.gotStep != time.Minute {
		t.Errorf("step = %v, want 1m for 600s lookback", pq.gotStep)
	}
	span := pq.gotEnd.Sub(pq.gotStart)
	if span < 595*time.Second || span > 605*time.Second {
		t.Errorf("range span = %v, want ~600s", span)
	}

	var ir promquery.InstantResult
	if err := json.Unmarshal(out.ResultJSON, &ir); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if ir.ResultType != "matrix" {
		t.Errorf("resultType = %q", ir.ResultType)
	}
}

func TestQueryPromQL_DefaultLookback(t *testing.T) {
	pq := &fakePromQuerier{resp: &promquery.InstantResult{ResultType: "matrix", Result: json.RawMessage("[]")}}
	uc := edgebiz.NewUsecase(newFakeEdgeRepo(), nil, nil, slog.Default())
	reg := NewRegistry(&fakeCaller{}, uc, nil, pq, nil, nil, nil, slog.Default())

	if _, err := reg.Invoke(context.Background(), ToolNameQueryPromQL, json.RawMessage(`{"expr":"up"}`)); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if pq.gotStep != 15*time.Second {
		t.Errorf("step = %v, want 15s for default 300s lookback", pq.gotStep)
	}
}

func TestQueryPromQL_MissingExpr(t *testing.T) {
	pq := &fakePromQuerier{}
	uc := edgebiz.NewUsecase(newFakeEdgeRepo(), nil, nil, slog.Default())
	reg := NewRegistry(&fakeCaller{}, uc, nil, pq, nil, nil, nil, slog.Default())

	if _, err := reg.Invoke(context.Background(), ToolNameQueryPromQL, json.RawMessage(`{}`)); err == nil {
		t.Errorf("expected error for missing expr")
	}
}

func TestQueryPromQL_DispatchError(t *testing.T) {
	pq := &fakePromQuerier{err: errors.New("prom 5xx")}
	uc := edgebiz.NewUsecase(newFakeEdgeRepo(), nil, nil, slog.Default())
	reg := NewRegistry(&fakeCaller{}, uc, nil, pq, nil, nil, nil, slog.Default())

	_, err := reg.Invoke(context.Background(), ToolNameQueryPromQL, json.RawMessage(`{"expr":"up"}`))
	if err == nil {
		t.Errorf("expected propagated dispatch error")
	}
}

func TestQueryPromQL_NotRegisteredWhenPromNil(t *testing.T) {
	uc := edgebiz.NewUsecase(newFakeEdgeRepo(), nil, nil, slog.Default())
	reg := NewRegistry(&fakeCaller{}, uc, nil, nil, nil, nil, nil, slog.Default())

	if containsName(schemaNames(reg.Schemas()), ToolNameQueryPromQL) {
		t.Errorf("query_promql should NOT be registered when promQuery is nil")
	}
	_, err := reg.Invoke(context.Background(), ToolNameQueryPromQL, json.RawMessage(`{"expr":"up"}`))
	if err == nil {
		t.Errorf("expected not-found error when prom disabled")
	}
}

func TestStepFor(t *testing.T) {
	cases := []struct {
		lb   int
		step time.Duration
	}{
		{60, 15 * time.Second},
		{300, 15 * time.Second},
		{600, time.Minute},
		{3600, time.Minute},
		{6 * 3600, 5 * time.Minute},
		{12 * 3600, 15 * time.Minute},
		{24 * 3600, 15 * time.Minute},
		{48 * 3600, time.Hour},
	}
	for _, c := range cases {
		if got := stepFor(c.lb); got != c.step {
			t.Errorf("stepFor(%d) = %v, want %v", c.lb, got, c.step)
		}
	}
}
