package promquery

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClient_QueryRange_RoundTrip(t *testing.T) {
	var gotPath string
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": "success",
			"data": {
				"resultType": "matrix",
				"result": [
					{"metric": {"__name__": "up", "edge_id": "1"}, "values": [[1700000000, "1"]]}
				]
			}
		}`))
	}))
	defer srv.Close()

	c := New(srv.URL, slog.Default())
	start := time.Unix(1700000000, 0)
	end := start.Add(5 * time.Minute)
	ir, err := c.QueryRange(context.Background(), "up", start, end, 15*time.Second)
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if gotPath != "/api/v1/query_range" {
		t.Errorf("path = %q, want /api/v1/query_range", gotPath)
	}
	if !strings.Contains(gotQuery, "query=up") {
		t.Errorf("query string missing query=up: %q", gotQuery)
	}
	if !strings.Contains(gotQuery, "step=15") {
		t.Errorf("query string missing step=15: %q", gotQuery)
	}
	if ir.ResultType != "matrix" {
		t.Errorf("resultType = %q, want matrix", ir.ResultType)
	}

	// Result is raw JSON; round-trip it.
	var rows []map[string]any
	if err := json.Unmarshal(ir.Result, &rows); err != nil {
		t.Fatalf("decode rows: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("rows = %d, want 1", len(rows))
	}
}

func TestClient_Query_Instant(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{
			"status": "success",
			"data": {"resultType":"vector","result":[]}
		}`))
	}))
	defer srv.Close()
	c := New(srv.URL, slog.Default())

	ir, err := c.Query(context.Background(), "up", time.Now())
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if gotPath != "/api/v1/query" {
		t.Errorf("path = %q", gotPath)
	}
	if ir.ResultType != "vector" {
		t.Errorf("resultType = %q", ir.ResultType)
	}
}

func TestClient_QueryRange_PromError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"parse error"}`))
	}))
	defer srv.Close()
	c := New(srv.URL, slog.Default())

	start := time.Unix(1, 0)
	end := time.Unix(2, 0)
	_, err := c.QueryRange(context.Background(), "**garbage", start, end, time.Second)
	if err == nil {
		t.Fatalf("expected error from prom error envelope")
	}
	if !strings.Contains(err.Error(), "parse error") {
		t.Errorf("err = %v, want to contain 'parse error'", err)
	}
}

func TestClient_QueryRange_BadStep(t *testing.T) {
	c := New("http://example", slog.Default())
	start := time.Unix(1, 0)
	end := time.Unix(2, 0)
	if _, err := c.QueryRange(context.Background(), "up", start, end, 0); err == nil {
		t.Errorf("expected error for step=0")
	}
}

func TestClient_QueryRange_EndBeforeStart(t *testing.T) {
	c := New("http://example", slog.Default())
	start := time.Unix(2, 0)
	end := time.Unix(1, 0)
	if _, err := c.QueryRange(context.Background(), "up", start, end, time.Second); err == nil {
		t.Errorf("expected error for end<=start")
	}
}
