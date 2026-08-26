package tools

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	edgebiz "github.com/ongridio/ongrid/internal/manager/biz/edge"
	"github.com/ongridio/ongrid/internal/pkg/tracequery"
)

// fakeTraceQuerier captures the last SearchTraces call.
type fakeTraceQuerier struct {
	mu   sync.Mutex
	got  tracequery.SearchOptions
	resp *tracequery.SearchResult
	err  error
}

func (f *fakeTraceQuerier) SearchTraces(_ context.Context, opts tracequery.SearchOptions) (*tracequery.SearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = opts
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func TestQueryTraceQL_RoundTrip(t *testing.T) {
	tq := &fakeTraceQuerier{
		resp: &tracequery.SearchResult{
			Traces: json.RawMessage(`[{"traceID":"abc","rootServiceName":"web"}]`),
		},
	}
	uc := edgebiz.NewUsecase(newFakeEdgeRepo(), nil, nil, slog.Default())
	reg := NewRegistry(&fakeCaller{}, uc, nil, nil, nil, tq, nil, slog.Default())

	if !containsName(schemaNames(reg.Schemas()), ToolNameQueryTraceQL) {
		t.Errorf("query_traceql not registered: %v", schemaNames(reg.Schemas()))
	}

	args := json.RawMessage(`{"query":"{ resource.service.name = \"web\" }","limit":10,"min_duration":"100ms"}`)
	out, err := reg.Invoke(context.Background(), ToolNameQueryTraceQL, args)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if tq.got.Query != `{ resource.service.name = "web" }` {
		t.Errorf("Query = %q", tq.got.Query)
	}
	if tq.got.Limit != 10 {
		t.Errorf("Limit = %d, want 10", tq.got.Limit)
	}
	if tq.got.MinDuration != 100*time.Millisecond {
		t.Errorf("MinDuration = %v, want 100ms", tq.got.MinDuration)
	}
	span := tq.got.End.Sub(tq.got.Start)
	if span < 59*time.Minute || span > 61*time.Minute {
		t.Errorf("default range span = %v, want ~1h", span)
	}

	var sr tracequery.SearchResult
	if err := json.Unmarshal(out.ResultJSON, &sr); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(sr.Traces) == 0 {
		t.Errorf("traces empty")
	}
}

func TestQueryTraceQL_TagMode(t *testing.T) {
	tq := &fakeTraceQuerier{resp: &tracequery.SearchResult{Traces: json.RawMessage("[]")}}
	uc := edgebiz.NewUsecase(newFakeEdgeRepo(), nil, nil, slog.Default())
	reg := NewRegistry(&fakeCaller{}, uc, nil, nil, nil, tq, nil, slog.Default())

	args := json.RawMessage(`{"service":"web","operation":"GET /api","max_duration":"5s"}`)
	if _, err := reg.Invoke(context.Background(), ToolNameQueryTraceQL, args); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if tq.got.Tags["service.name"] != "web" {
		t.Errorf("Tags[service.name] = %q", tq.got.Tags["service.name"])
	}
	if tq.got.Tags["name"] != "GET /api" {
		t.Errorf("Tags[name] = %q", tq.got.Tags["name"])
	}
	if tq.got.MaxDuration != 5*time.Second {
		t.Errorf("MaxDuration = %v, want 5s", tq.got.MaxDuration)
	}
	if tq.got.Limit != 50 {
		t.Errorf("default limit = %d, want 50", tq.got.Limit)
	}
}

func TestQueryTraceQL_DeviceScope(t *testing.T) {
	tq := &fakeTraceQuerier{resp: &tracequery.SearchResult{Traces: json.RawMessage("[]")}}
	uc := edgebiz.NewUsecase(newFakeEdgeRepo(), nil, nil, slog.Default())
	reg := NewRegistry(&fakeCaller{}, uc, nil, nil, nil, tq, nil, slog.Default())

	args := json.RawMessage(`{"device_id":24,"service":"web","operation":"GET /api"}`)
	if _, err := reg.Invoke(context.Background(), ToolNameQueryTraceQL, args); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if got, want := tq.got.Query, `{ resource.device_id = "24" && resource.service.name = "web" && name = "GET /api" }`; got != want {
		t.Errorf("Query = %q, want %q", got, want)
	}
	if tq.got.Tags != nil {
		t.Errorf("Tags = %#v, want nil when device scope builds TraceQL", tq.got.Tags)
	}
}

func TestQueryTraceQL_DeviceScopeRejectsConflict(t *testing.T) {
	tq := &fakeTraceQuerier{resp: &tracequery.SearchResult{Traces: json.RawMessage("[]")}}
	uc := edgebiz.NewUsecase(newFakeEdgeRepo(), nil, nil, slog.Default())
	reg := NewRegistry(&fakeCaller{}, uc, nil, nil, nil, tq, nil, slog.Default())

	_, err := reg.Invoke(context.Background(), ToolNameQueryTraceQL,
		json.RawMessage(`{"device_id":24,"query":"{ resource.device_id = \"7\" }"}`))
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("Invoke error = %v, want device scope conflict", err)
	}
}

func TestRegistry_QueryTracePanelScopesToDevice(t *testing.T) {
	tq := &fakeTraceQuerier{resp: &tracequery.SearchResult{Traces: json.RawMessage("[]")}}
	reg := &Registry{traceQuery: tq}
	deviceID := uint64(24)

	if _, err := reg.queryTracePanel(context.Background(), "web", &deviceID, time.Now().Add(-time.Hour), time.Now()); err != nil {
		t.Fatalf("queryTracePanel: %v", err)
	}
	if got, want := tq.got.Query, `{ resource.device_id = "24" && resource.service.name = "web" }`; got != want {
		t.Errorf("Query = %q, want %q", got, want)
	}
	if tq.got.Tags != nil {
		t.Errorf("Tags = %#v, want nil for device-scoped TraceQL", tq.got.Tags)
	}
}

func TestQueryTraceQL_RequiresAFilter(t *testing.T) {
	tq := &fakeTraceQuerier{}
	uc := edgebiz.NewUsecase(newFakeEdgeRepo(), nil, nil, slog.Default())
	reg := NewRegistry(&fakeCaller{}, uc, nil, nil, nil, tq, nil, slog.Default())

	if _, err := reg.Invoke(context.Background(), ToolNameQueryTraceQL, json.RawMessage(`{}`)); err == nil {
		t.Errorf("expected error when no filter is given")
	}
}

func TestQueryTraceQL_BadDuration(t *testing.T) {
	tq := &fakeTraceQuerier{resp: &tracequery.SearchResult{}}
	uc := edgebiz.NewUsecase(newFakeEdgeRepo(), nil, nil, slog.Default())
	reg := NewRegistry(&fakeCaller{}, uc, nil, nil, nil, tq, nil, slog.Default())

	_, err := reg.Invoke(context.Background(), ToolNameQueryTraceQL,
		json.RawMessage(`{"service":"web","min_duration":"not-a-duration"}`))
	if err == nil {
		t.Errorf("expected error for bad min_duration")
	}
}

func TestQueryTraceQL_DispatchError(t *testing.T) {
	tq := &fakeTraceQuerier{err: errors.New("tempo 5xx")}
	uc := edgebiz.NewUsecase(newFakeEdgeRepo(), nil, nil, slog.Default())
	reg := NewRegistry(&fakeCaller{}, uc, nil, nil, nil, tq, nil, slog.Default())

	_, err := reg.Invoke(context.Background(), ToolNameQueryTraceQL,
		json.RawMessage(`{"service":"web"}`))
	if err == nil {
		t.Errorf("expected propagated dispatch error")
	}
}

func TestQueryTraceQL_NotRegisteredWhenNil(t *testing.T) {
	uc := edgebiz.NewUsecase(newFakeEdgeRepo(), nil, nil, slog.Default())
	reg := NewRegistry(&fakeCaller{}, uc, nil, nil, nil, nil, nil, slog.Default())

	if containsName(schemaNames(reg.Schemas()), ToolNameQueryTraceQL) {
		t.Errorf("query_traceql should NOT be registered when traceQuery is nil")
	}
	_, err := reg.Invoke(context.Background(), ToolNameQueryTraceQL,
		json.RawMessage(`{"service":"web"}`))
	if err == nil {
		t.Errorf("expected not-found error when trace disabled")
	}
}
