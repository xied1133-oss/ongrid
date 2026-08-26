package logs_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	aiopstools "github.com/ongridio/ongrid/internal/manager/biz/aiops/tools"
	managerlogs "github.com/ongridio/ongrid/internal/manager/biz/logs"
	logsmodel "github.com/ongridio/ongrid/internal/manager/model/logs"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/logquery"
)

type queryLogQLSelectionRepo struct {
	managerlogs.Repo
	selected *logsmodel.Backend
	err      error
}

func (r queryLogQLSelectionRepo) SelectedBackend(context.Context) (*logsmodel.Backend, error) {
	return r.selected, r.err
}

type queryLogQLSecretResolver map[string]map[string]string

func (r queryLogQLSecretResolver) ResolveFields(_ context.Context, name string) (map[string]string, error) {
	return r[name], nil
}

func TestQueryLogQLTool_WhenLokiSelected_PreservesNativeLogQLAndResponse(t *testing.T) {
	start := time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC)
	end := start.Add(90 * time.Minute)
	query := `sum(count_over_time({device_id="42"} | json | level="error" [5m]))`
	wantResult := json.RawMessage(`[{"metric":{"device_id":"42"},"values":[[1787533200,"3"]]}]`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/loki/api/v1/query_range" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("X-Scope-OrgID"); got != "ongrid" {
			t.Errorf("X-Scope-OrgID = %q, want ongrid", got)
		}
		values := r.URL.Query()
		if got := values.Get("query"); got != query {
			t.Errorf("query = %q, want %q", got, query)
		}
		if got := values.Get("start"); got != strconv.FormatInt(start.UnixNano(), 10) {
			t.Errorf("start = %q", got)
		}
		if got := values.Get("end"); got != strconv.FormatInt(end.UnixNano(), 10) {
			t.Errorf("end = %q", got)
		}
		if got := values.Get("limit"); got != "37" {
			t.Errorf("limit = %q, want 37", got)
		}
		if got := values.Get("direction"); got != "forward" {
			t.Errorf("direction = %q, want forward", got)
		}
		writeQueryLogQLIntegrationJSON(t, w, map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "matrix",
				"result":     json.RawMessage(wantResult),
			},
		})
	}))
	t.Cleanup(server.Close)

	loki := logquery.NewWithHTTPClient(server.URL, server.Client(), slog.Default())
	service := managerlogs.NewService(queryLogQLSelectionRepo{err: errs.ErrNotFound}, nil, loki)
	tool := aiopstools.NewQueryLogQLTool(service)
	out, err := tool.InvokableRun(t.Context(), mustQueryLogQLArgs(t, map[string]any{
		"query": query, "start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339),
		"limit": 37, "direction": "forward",
	}))
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	var result logquery.QueryRangeResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode query_logql result: %v", err)
	}
	if result.ResultType != "matrix" || string(result.Result) != string(wantResult) {
		t.Fatalf("result = %#v", result)
	}
}

func TestQueryLogQLTool_WhenElasticsearchSelected_CompilesPortableQueryAndReturnsSearchResult(t *testing.T) {
	start := time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	searchCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "ApiKey query-key" {
			t.Errorf("Authorization = %q", got)
		}
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/_pit"):
			writeQueryLogQLIntegrationJSON(t, w, map[string]any{"id": "pit-query-logql"})
		case r.Method == http.MethodPost && r.URL.Path == "/_search":
			searchCalls++
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read Elasticsearch request: %v", err)
			}
			raw := string(body)
			for _, want := range []string{
				`"resource.attributes.device_id":"42"`,
				`"resource.attributes.namespace":"prod"`,
				`"match_phrase":{"body.text":"error"}`,
				`"match_phrase":{"body.text":"panic"}`,
				`"match_phrase":{"body.text":"health"}`,
				// Elasticsearch fetches one sentinel row beyond the public limit
				// so it can report has_more without changing query_logql's limit.
				`"size":26`,
				`"@timestamp":"desc"`,
			} {
				if !strings.Contains(raw, want) {
					t.Errorf("Elasticsearch request missing %s: %s", want, raw)
				}
			}
			writeQueryLogQLIntegrationJSON(t, w, map[string]any{
				"pit_id": "pit-query-logql",
				"hits": map[string]any{"hits": []any{map[string]any{
					"_id": "hit-1",
					"_source": map[string]any{
						"@timestamp":    "2026-08-24T01:30:00Z",
						"body":          map[string]any{"text": "panic: upstream timeout"},
						"severity_text": "ERROR",
						"resource": map[string]any{"attributes": map[string]any{
							"device_id": "42", "namespace": "prod", "ongrid_source": "kubernetes:pod",
						}},
					},
					"sort": []any{"2026-08-24T01:30:00Z", 1},
				}}},
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/_pit":
			writeQueryLogQLIntegrationJSON(t, w, map[string]any{"succeeded": true})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	backend := &logsmodel.Backend{
		ID: 7, Generation: 2, Type: logsmodel.BackendTypeElasticsearch,
		QueryEndpoint: server.URL, QueryCredentialRef: "query-ref",
		IndexPattern: "logs-ongrid.*.otel-*", TLSInsecure: true,
	}
	service := managerlogs.NewService(
		queryLogQLSelectionRepo{selected: backend},
		queryLogQLSecretResolver{"query-ref": {"api_key": "query-key"}},
		nil,
	)
	tool := aiopstools.NewQueryLogQLTool(service)
	out, err := tool.InvokableRun(t.Context(), mustQueryLogQLArgs(t, map[string]any{
		"query": `{namespace="prod"} |~ "(?i)(error|panic)" != "health"`, "device_id": 42,
		"start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339),
		"limit": 25, "direction": "backward",
	}))
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if searchCalls != 1 {
		t.Fatalf("Elasticsearch search calls = %d, want 1", searchCalls)
	}
	var result struct {
		Records []struct {
			Timestamp    time.Time `json:"timestamp"`
			Message      string    `json:"message"`
			SeverityText string    `json:"severity_text"`
			DeviceID     string    `json:"device_id"`
			Namespace    string    `json:"namespace"`
			SourceID     string    `json:"source_id"`
		} `json:"records"`
		HasMore  bool     `json:"has_more"`
		TookMS   int64    `json:"took_ms"`
		Backends []string `json:"backends"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode query_logql result: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].DeviceID != "42" || result.Records[0].Namespace != "prod" ||
		result.Records[0].SourceID != "kubernetes:pod" {
		t.Fatalf("result = %#v", result)
	}
	if result.Records[0].Message != "panic: upstream timeout" || result.Records[0].SeverityText != "error" ||
		!result.Records[0].Timestamp.Equal(time.Date(2026, 8, 24, 1, 30, 0, 0, time.UTC)) {
		t.Fatalf("record = %#v", result.Records[0])
	}
	if result.HasMore || result.TookMS < 0 || len(result.Backends) != 1 || result.Backends[0] != "elasticsearch" {
		t.Fatalf("query metadata = %#v", result)
	}
}

func TestQueryLogQLTool_WhenElasticsearchSelected_RejectsLokiOnlySyntaxClearly(t *testing.T) {
	backend := &logsmodel.Backend{ID: 7, Generation: 2, Type: logsmodel.BackendTypeElasticsearch}
	service := managerlogs.NewService(queryLogQLSelectionRepo{selected: backend}, nil, nil)
	tool := aiopstools.NewQueryLogQLTool(service)

	tests := []struct {
		name  string
		query string
	}{
		{name: "metric", query: `sum(count_over_time({device_id="42"}[5m]))`},
		{name: "parser", query: `{device_id="42"} | json | level="error"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.InvokableRun(t.Context(), mustQueryLogQLArgs(t, map[string]any{"query": tt.query}))
			if err == nil || !strings.Contains(err.Error(), "only available when Loki is selected") {
				t.Fatalf("InvokableRun() error = %v", err)
			}
		})
	}
}

func mustQueryLogQLArgs(t *testing.T, value map[string]any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode query_logql args: %v", err)
	}
	return string(body)
}

func writeQueryLogQLIntegrationJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode HTTP response: %v", err)
	}
}
