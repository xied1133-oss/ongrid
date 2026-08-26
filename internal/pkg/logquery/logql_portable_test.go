package logquery

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCompileLogQLSearchSupportsPortableSelectorAndLineFilters(t *testing.T) {
	start := time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC)
	req, err := CompileLogQLSearch(QueryRangeOptions{
		Query:     `{device_id="42",level=~"ERROR|WARN",namespace!="kube-system"} |~ "(?i)(error|panic|fatal)" !~ "health check"`,
		Start:     start,
		End:       start.Add(time.Hour),
		Limit:     50,
		Direction: "forward",
	})
	if err != nil {
		t.Fatalf("CompileLogQLSearch() error = %v", err)
	}
	if req.Limit != 50 || req.Direction != SortForward || req.Keywords.Mode != MatchAny {
		t.Fatalf("request controls = %#v", req)
	}
	if got := strings.Join(req.Keywords.Include, ","); got != "error,panic,fatal" {
		t.Fatalf("include keywords = %q", got)
	}
	if got := strings.Join(req.Keywords.Exclude, ","); got != "health check" {
		t.Fatalf("exclude keywords = %q", got)
	}
	wantFilters := []FieldFilter{
		{Field: "device_id", Operator: FilterEqual, Values: []string{"42"}},
		{Field: "level", Operator: FilterIn, Values: []string{"ERROR", "WARN"}},
		{Field: "namespace", Operator: FilterNotEqual, Values: []string{"kube-system"}},
	}
	if body, want := mustJSON(t, req.Filters), mustJSON(t, wantFilters); body != want {
		t.Fatalf("filters = %s, want %s", body, want)
	}
}

func TestCompileLogQLSearchPreservesExactLineFilterSemantics(t *testing.T) {
	start := time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC)
	req, err := CompileLogQLSearch(QueryRangeOptions{
		Query: `{ongrid_source="kubernetes:pod",filename="/var/log/app.log"} |= "timeout" |= "upstream" != "health"`,
		Start: start, End: start.Add(time.Hour), Limit: 20,
	})
	if err != nil {
		t.Fatalf("CompileLogQLSearch() error = %v", err)
	}
	if req.Keywords.Mode != MatchAll || strings.Join(req.Keywords.Include, ",") != "timeout,upstream" {
		t.Fatalf("keywords = %#v", req.Keywords)
	}
	if req.Filters[0].Field != "source_id" || req.Filters[1].Field != "file" {
		t.Fatalf("mapped filters = %#v", req.Filters)
	}
}

func TestCompileLogQLSearchConvertsLegacySelectorPrefixes(t *testing.T) {
	start := time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC)
	req, err := CompileLogQLSearch(QueryRangeOptions{
		Query: `{ongrid_source=~"journald:.+",namespace=~".+"} |~ "(?i)panic|oom|fatal"`,
		Start: start, End: start.Add(time.Hour), Limit: 20,
	})
	if err != nil {
		t.Fatalf("CompileLogQLSearch() error = %v", err)
	}
	wantFilters := []FieldFilter{
		{Field: "source_id", Operator: FilterPrefix, Values: []string{"journald:"}},
		{Field: "namespace", Operator: FilterExists},
	}
	if got, want := mustJSON(t, req.Filters), mustJSON(t, wantFilters); got != want {
		t.Fatalf("filters = %s, want %s", got, want)
	}
	if got := strings.Join(req.Keywords.Include, ","); got != "panic,oom,fatal" {
		t.Fatalf("include keywords = %q", got)
	}
	lokiQuery, err := compileLogQL(req)
	if err != nil {
		t.Fatalf("compileLogQL() error = %v", err)
	}
	if !strings.Contains(lokiQuery, `ongrid_source=~"journald:.*"`) || !strings.Contains(lokiQuery, `namespace=~".+"`) {
		t.Fatalf("Loki prefix/exists query = %q", lokiQuery)
	}
	esQuery, err := buildElasticsearchQuery(req)
	if err != nil {
		t.Fatalf("buildElasticsearchQuery() error = %v", err)
	}
	esJSON := mustJSON(t, esQuery)
	if !strings.Contains(esJSON, `"prefix":{"resource.attributes.ongrid_source":"journald:"}`) || !strings.Contains(esJSON, `"exists":{"field":"resource.attributes.namespace"}`) {
		t.Fatalf("Elasticsearch prefix/exists query = %s", esJSON)
	}
}

func TestCompileLogQLSearchKeepsPublicLimitWhileCappingOneESPage(t *testing.T) {
	start := time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC)
	req, err := CompileLogQLSearch(QueryRangeOptions{
		Query: `{device_id="42"}`, Start: start, End: start.Add(time.Hour), Limit: MaxQueryLogQLLimit,
	})
	if err != nil {
		t.Fatalf("CompileLogQLSearch() error = %v", err)
	}
	if req.Limit != MaxSearchLimit {
		t.Fatalf("single Elasticsearch page limit = %d, want %d", req.Limit, MaxSearchLimit)
	}
}

func TestCompileLogQLSearchRejectsSyntaxThatCannotKeepItsMeaning(t *testing.T) {
	start := time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		query string
		step  time.Duration
	}{
		{name: "metric expression", query: `sum(count_over_time({device_id="42"}[5m]))`},
		{name: "parser stage", query: `{device_id="42"} | json | level="error"`},
		{name: "arbitrary regex", query: `{device_id=~"4[0-9].*"} |= "error"`},
		{name: "negative prefix", query: `{ongrid_source!~"journald:.*"} |= "error"`},
		{name: "metric step", query: `{device_id="42"}`, step: time.Minute},
		{name: "unknown label", query: `{job="api"} |= "error"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CompileLogQLSearch(QueryRangeOptions{
				Query: tt.query, Start: start, End: start.Add(time.Hour), Limit: 20, Step: tt.step,
			})
			if err == nil {
				t.Fatalf("CompileLogQLSearch(%q) unexpectedly succeeded", tt.query)
			}
		})
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return string(body)
}

func FuzzCompileLogQLSearch_DoesNotPanicOnUntrustedExpressions(f *testing.F) {
	for _, seed := range []string{
		`{}`,
		`{device_id="42"} |= "error"`,
		`{namespace=~"prod|staging"} |~ "(?i)(panic|fatal)"`,
		`sum(count_over_time({device_id="42"}[5m]))`,
		`{device_id="42"} | json`,
		`{device_id="unterminated}`,
	} {
		f.Add(seed)
	}
	start := time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, query string) {
		if _, err := CompileLogQLSearch(QueryRangeOptions{
			Query: query, Start: start, End: start.Add(time.Hour), Limit: 20,
		}); err != nil {
			return
		}
	})
}
