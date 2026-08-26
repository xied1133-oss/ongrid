package logs

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/logs"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/logquery"
)

type queryLogQLRepo struct {
	Repo
	selected *model.Backend
	err      error
}

func (r queryLogQLRepo) SelectedBackend(context.Context) (*model.Backend, error) {
	return r.selected, r.err
}

type queryLogQLSecrets map[string]map[string]string

func (s queryLogQLSecrets) ResolveFields(_ context.Context, name string) (map[string]string, error) {
	return s[name], nil
}

type queryLogQLLoki struct {
	opts logquery.QueryRangeOptions
	resp *logquery.QueryRangeResult
}

type queryLogQLPagedSearcher struct {
	logquery.Searcher
	requests []logquery.SearchRequest
	pages    []*logquery.SearchResult
	closed   []string
}

func (s *queryLogQLPagedSearcher) Search(_ context.Context, req logquery.SearchRequest) (*logquery.SearchResult, error) {
	s.requests = append(s.requests, req)
	page := s.pages[0]
	s.pages = s.pages[1:]
	return page, nil
}

func (s *queryLogQLPagedSearcher) CloseCursor(_ context.Context, cursor string) error {
	s.closed = append(s.closed, cursor)
	return nil
}

func (l *queryLogQLLoki) QueryRange(_ context.Context, opts logquery.QueryRangeOptions) (*logquery.QueryRangeResult, error) {
	l.opts = opts
	return l.resp, nil
}

func (*queryLogQLLoki) Search(context.Context, logquery.SearchRequest) (*logquery.SearchResult, error) {
	return &logquery.SearchResult{}, nil
}

func (*queryLogQLLoki) Count(context.Context, logquery.SearchRequest) (uint64, error) {
	return 0, nil
}

func (*queryLogQLLoki) Fields(context.Context, time.Time, time.Time, logquery.Scope) ([]logquery.Field, error) {
	return nil, nil
}

func (*queryLogQLLoki) FieldValues(context.Context, logquery.FieldValuesRequest) ([]string, error) {
	return nil, nil
}

func (*queryLogQLLoki) Histogram(context.Context, logquery.SearchRequest, time.Duration) ([]logquery.HistogramBucket, error) {
	return nil, nil
}

func TestServiceQueryLogQLDelegatesNativeLogQLToLokiUnchanged(t *testing.T) {
	raw := json.RawMessage(`[{"metric":{"device_id":"42"},"values":[[1,"3"]]}]`)
	loki := &queryLogQLLoki{resp: &logquery.QueryRangeResult{ResultType: "matrix", Result: raw}}
	service := NewService(queryLogQLRepo{err: errs.ErrNotFound}, nil, loki)
	start := time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC)
	opts := logquery.QueryRangeOptions{
		Query: `sum(count_over_time({device_id="42"} |= "error" [5m]))`,
		Start: start, End: start.Add(time.Hour), Limit: 99, Step: time.Minute, Direction: "forward",
	}
	result, err := service.QueryLogQL(t.Context(), opts)
	if err != nil {
		t.Fatalf("QueryLogQL() error = %v", err)
	}
	if loki.opts.Query != opts.Query || loki.opts.Step != opts.Step || loki.opts.Limit != opts.Limit {
		t.Fatalf("delegated options = %#v", loki.opts)
	}
	lokiResult, ok := result.(*logquery.QueryRangeResult)
	if !ok || lokiResult.ResultType != "matrix" || string(lokiResult.Result) != string(raw) {
		t.Fatalf("result = %#v", result)
	}
}

func TestServiceQueryLogQLCompilesPortableLogQLForSelectedElasticsearch(t *testing.T) {
	searchCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "ApiKey query-key" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/_pit"):
			writeQueryLogQLJSON(t, w, map[string]any{"id": "pit-query-logql"})
		case r.Method == http.MethodPost && r.URL.Path == "/_search":
			searchCalls++
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("ReadAll() error = %v", err)
			}
			raw := string(body)
			for _, want := range []string{
				`"resource.attributes.device_id":"42"`,
				`"match_phrase":{"body.text":"error"}`,
				`"match_phrase":{"body.text":"panic"}`,
			} {
				if !strings.Contains(raw, want) {
					t.Fatalf("Elasticsearch request missing %s: %s", want, raw)
				}
			}
			writeQueryLogQLJSON(t, w, map[string]any{
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
			writeQueryLogQLJSON(t, w, map[string]any{"succeeded": true})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	backend := &model.Backend{
		ID: 7, Generation: 2, Type: model.BackendTypeElasticsearch,
		QueryEndpoint: server.URL, QueryCredentialRef: "query-ref",
		IndexPattern: "logs-ongrid.*.otel-*", TLSInsecure: true,
	}
	service := NewService(
		queryLogQLRepo{selected: backend},
		queryLogQLSecrets{"query-ref": {"api_key": "query-key"}},
		nil,
	)
	start := time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC)
	result, err := service.QueryLogQL(t.Context(), logquery.QueryRangeOptions{
		Query: `{device_id="42"} |~ "(?i)(error|panic)"`,
		Start: start, End: start.Add(time.Hour), Limit: 20, Direction: "backward",
	})
	if err != nil {
		t.Fatalf("QueryLogQL() error = %v", err)
	}
	searchResult, ok := result.(*logquery.SearchResult)
	if searchCalls != 1 || !ok {
		t.Fatalf("searchCalls=%d result=%#v", searchCalls, result)
	}
	if len(searchResult.Records) != 1 || searchResult.Records[0].ResourceAttributes["device_id"] != "42" || searchResult.Records[0].Backend != "elasticsearch" || searchResult.Records[0].Message != "panic: upstream timeout" {
		t.Fatalf("search result = %#v", searchResult)
	}
}

func TestServiceQueryLogQLRejectsMetricLogQLWhenElasticsearchIsSelected(t *testing.T) {
	backend := &model.Backend{ID: 7, Generation: 2, Type: model.BackendTypeElasticsearch}
	service := NewService(queryLogQLRepo{selected: backend}, nil, nil)
	start := time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC)
	_, err := service.QueryLogQL(t.Context(), logquery.QueryRangeOptions{
		Query: `sum(count_over_time({device_id="42"}[5m]))`,
		Start: start, End: start.Add(time.Hour), Limit: 20,
	})
	if err == nil {
		t.Fatalf("QueryLogQL() error = %v", err)
	}
}

func TestSearchLogQLPagesPreservesQueryLogQLPublicLimit(t *testing.T) {
	firstRecords := make([]logquery.Record, logquery.MaxSearchLimit)
	secondRecords := make([]logquery.Record, 500)
	searcher := &queryLogQLPagedSearcher{pages: []*logquery.SearchResult{
		{Records: firstRecords, HasMore: true, NextCursor: "next-page"},
		{Records: secondRecords},
	}}
	result, err := searchLogQLPages(t.Context(), searcher, logquery.SearchRequest{}, 1500)
	if err != nil {
		t.Fatalf("searchLogQLPages() error = %v", err)
	}
	if len(result.Records) != 1500 || len(searcher.requests) != 2 {
		t.Fatalf("records=%d requests=%d", len(result.Records), len(searcher.requests))
	}
	if searcher.requests[0].Limit != 1000 || searcher.requests[0].Cursor != "" {
		t.Fatalf("first request = %#v", searcher.requests[0])
	}
	if searcher.requests[1].Limit != 500 || searcher.requests[1].Cursor != "next-page" {
		t.Fatalf("second request = %#v", searcher.requests[1])
	}
}

func TestSearchLogQLPagesClosesCursorWhenPublicLimitStopsPagination(t *testing.T) {
	searcher := &queryLogQLPagedSearcher{pages: []*logquery.SearchResult{{
		Records: make([]logquery.Record, 10), HasMore: true, NextCursor: "abandoned-pit",
	}}}
	result, err := searchLogQLPages(t.Context(), searcher, logquery.SearchRequest{}, 10)
	if err != nil {
		t.Fatalf("searchLogQLPages() error = %v", err)
	}
	if !result.HasMore || len(result.Records) != 10 {
		t.Fatalf("result = %#v", result)
	}
	if len(searcher.closed) != 1 || searcher.closed[0] != "abandoned-pit" {
		t.Fatalf("closed cursors = %#v", searcher.closed)
	}
}

func writeQueryLogQLJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
}
