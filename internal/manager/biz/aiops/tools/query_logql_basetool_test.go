package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ongridio/ongrid/internal/pkg/logquery"
)

func TestQueryLogQLTool_Info(t *testing.T) {
	tool := NewQueryLogQLTool(&fakeLogQuerier{})
	info, err := tool.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Name != ToolNameQueryLogQL {
		t.Errorf("Name = %q", info.Name)
	}
	if !strings.Contains(strings.ToLower(info.WhenToUse), "not") {
		t.Errorf("WhenToUse needs reverse guard: %q", info.WhenToUse)
	}
	if !strings.Contains(strings.ToLower(info.WhenToUse), "log") {
		t.Errorf("WhenToUse should mention log content")
	}
}

func TestQueryLogQLTool_RoundTrip(t *testing.T) {
	lq := &fakeLogQuerier{
		resp: &logquery.QueryRangeResult{
			ResultType: "streams",
			Result:     json.RawMessage(`[{"stream":{"device_id":"1"},"values":[["1700000000000000000","oops"]]}]`),
		},
	}
	tool := NewQueryLogQLTool(lq)
	out, err := tool.InvokableRun(context.Background(),
		`{"query":"{device_id=\"1\"} |= \"error\"","limit":50,"direction":"forward"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if lq.got.Query != `{device_id="1"} |= "error"` {
		t.Errorf("Query = %q", lq.got.Query)
	}
	if lq.got.Limit != 50 {
		t.Errorf("Limit = %d, want 50", lq.got.Limit)
	}
	if lq.got.Direction != "forward" {
		t.Errorf("Direction = %q, want forward", lq.got.Direction)
	}
	span := lq.got.End.Sub(lq.got.Start)
	if span < 59*time.Minute || span > 61*time.Minute {
		t.Errorf("default range = %v", span)
	}
	var qr logquery.QueryRangeResult
	if err := json.Unmarshal([]byte(out), &qr); err != nil {
		t.Fatalf("decode result: %v", err)
	}
}

func TestQueryLogQLTool_DeviceIDScopesQuery(t *testing.T) {
	lq := &fakeLogQuerier{resp: &logquery.QueryRangeResult{ResultType: "streams", Result: json.RawMessage("[]")}}
	tool := NewQueryLogQLTool(lq)
	if _, err := tool.InvokableRun(context.Background(), `{"query":"{} |= \"error\"","device_id":24}`); err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if got, want := lq.got.Query, `{device_id="24"} |= "error"`; got != want {
		t.Errorf("Query = %q, want %q", got, want)
	}
}

func TestQueryLogQLTool_BadArgs(t *testing.T) {
	tool := NewQueryLogQLTool(&fakeLogQuerier{})
	if _, err := tool.InvokableRun(context.Background(), `not json`); err == nil {
		t.Errorf("expected error for non-JSON")
	}
	if _, err := tool.InvokableRun(context.Background(), `{}`); err == nil {
		t.Errorf("expected error for missing query")
	}
	if _, err := tool.InvokableRun(context.Background(),
		`{"query":"{a=\"b\"}","start":"not-a-time"}`); err == nil {
		t.Errorf("expected error for bad start")
	}
}

func TestQueryLogQLTool_NilQuerier(t *testing.T) {
	tool := NewQueryLogQLTool(nil)
	_, err := tool.InvokableRun(context.Background(), `{"query":"{a=\"b\"}"}`)
	if err == nil {
		t.Errorf("expected error when logQuery nil")
	}
}

func TestQueryLogQLTool_DispatchError(t *testing.T) {
	tool := NewQueryLogQLTool(&fakeLogQuerier{err: errors.New("loki 5xx")})
	_, err := tool.InvokableRun(context.Background(), `{"query":"{a=\"b\"}"}`)
	if err == nil || !strings.Contains(err.Error(), "loki 5xx") {
		t.Errorf("expected wrapped dispatch error, got %v", err)
	}
}
