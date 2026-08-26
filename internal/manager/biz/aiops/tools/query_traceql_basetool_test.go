package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ongridio/ongrid/internal/pkg/tracequery"
)

func TestQueryTraceQLTool_Info(t *testing.T) {
	tool := NewQueryTraceQLTool(&fakeTraceQuerier{}, nil)
	info, err := tool.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Name != ToolNameQueryTraceQL {
		t.Errorf("Name = %q", info.Name)
	}
	if !strings.Contains(strings.ToLower(info.WhenToUse), "not") {
		t.Errorf("WhenToUse needs reverse guard")
	}
}

func TestQueryTraceQLTool_TagMode(t *testing.T) {
	tq := &fakeTraceQuerier{resp: &tracequery.SearchResult{Traces: json.RawMessage("[]")}}
	tool := NewQueryTraceQLTool(tq, nil)

	if _, err := tool.InvokableRun(context.Background(),
		`{"service":"web","operation":"GET /api","max_duration":"5s"}`); err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if tq.got.Tags["service.name"] != "web" {
		t.Errorf("Tags[service.name] = %q", tq.got.Tags["service.name"])
	}
	if tq.got.Tags["name"] != "GET /api" {
		t.Errorf("Tags[name] = %q", tq.got.Tags["name"])
	}
	if tq.got.MaxDuration != 5*time.Second {
		t.Errorf("MaxDuration = %v", tq.got.MaxDuration)
	}
}

func TestQueryTraceQLTool_DeviceScope(t *testing.T) {
	tq := &fakeTraceQuerier{resp: &tracequery.SearchResult{Traces: json.RawMessage("[]")}}
	tool := NewQueryTraceQLTool(tq, nil)

	if _, err := tool.InvokableRun(context.Background(), `{"device_id":24,"query":"{ resource.service.name = \"web\" }"}`); err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if got, want := tq.got.Query, `{ resource.device_id = "24" && resource.service.name = "web" }`; got != want {
		t.Errorf("Query = %q, want %q", got, want)
	}
}

func TestQueryTraceQLTool_RequiresAFilter(t *testing.T) {
	tool := NewQueryTraceQLTool(&fakeTraceQuerier{}, nil)
	if _, err := tool.InvokableRun(context.Background(), `{}`); err == nil {
		t.Errorf("expected error when no filter is given")
	}
}

func TestQueryTraceQLTool_BadArgs(t *testing.T) {
	tool := NewQueryTraceQLTool(&fakeTraceQuerier{resp: &tracequery.SearchResult{}}, nil)
	if _, err := tool.InvokableRun(context.Background(), `not json`); err == nil {
		t.Errorf("expected error for non-JSON")
	}
	if _, err := tool.InvokableRun(context.Background(),
		`{"service":"web","min_duration":"not-a-duration"}`); err == nil {
		t.Errorf("expected error for bad duration")
	}
}

func TestQueryTraceQLTool_NilQuerier(t *testing.T) {
	tool := NewQueryTraceQLTool(nil, nil)
	_, err := tool.InvokableRun(context.Background(), `{"service":"web"}`)
	if err == nil {
		t.Errorf("expected error when traceQuery nil")
	}
}

func TestQueryTraceQLTool_DispatchError(t *testing.T) {
	tool := NewQueryTraceQLTool(&fakeTraceQuerier{err: errors.New("tempo 5xx")}, nil)
	_, err := tool.InvokableRun(context.Background(), `{"service":"web"}`)
	if err == nil || !strings.Contains(err.Error(), "tempo 5xx") {
		t.Errorf("expected wrapped dispatch error, got %v", err)
	}
}
