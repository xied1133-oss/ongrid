package alert

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/alert"
	"github.com/ongridio/ongrid/internal/pkg/logquery"
)

func TestCompileLogMatch_Defaults(t *testing.T) {
	spec, _ := json.Marshal(map[string]any{
		"stream_selector": `{job="varlogs"}`,
		"line_filter":     "(?i)error",
		"threshold":       50.0,
	})
	row := &model.Rule{ID: 1, RuleKey: "log_err", ConditionsJSON: string(spec)}
	r, err := compileLogMatchRule(row)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if r.Window != "5m" {
		t.Errorf("default Window = %q, want 5m", r.Window)
	}
	if r.Operator != ">=" {
		t.Errorf("default Operator = %q, want >=", r.Operator)
	}
	if r.Threshold != 50 {
		t.Errorf("Threshold = %v, want 50", r.Threshold)
	}
}

func TestCompileLogSearchAndEvaluateThroughStructuredCounter(t *testing.T) {
	spec, _ := json.Marshal(map[string]any{
		"keywords": map[string]any{
			"include": []string{"error", "panic"},
			"mode":    "any",
		},
		"scope":     map[string]any{"namespaces": []string{"production"}},
		"window":    "10m",
		"operator":  ">=",
		"threshold": 3,
	})
	compiled, err := compileLogSearchRule(&model.Rule{
		ID: 7, RuleKey: "production_errors", Name: "Production errors",
		Kind: model.RuleKindLogSearch, ScopeType: model.RuleScopeGlobal,
		Severity: "warning", ConditionsJSON: string(spec),
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if compiled.Window != 10*time.Minute || compiled.Query.Keywords.Mode != logquery.MatchAny {
		t.Fatalf("compiled rule = %#v", compiled)
	}

	repo := newFakeRepo()
	searcher := &scriptedStructuredLogSearcher{count: 4}
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	eval := newPipelineEvaluator(t, repo, &fakeNotifier{},
		NewStaticRulesProvider(WithLogSearchRules([]LogSearchRule{compiled})),
		PipelineEvaluatorOpts{
			LogSearcher: searcher,
			Cooldown:    time.Minute,
			Now:         func() time.Time { return now },
		})
	eval.EvaluateOnce(context.Background())
	if len(repo.incidents) != 1 {
		t.Fatalf("incidents = %d, want 1", len(repo.incidents))
	}
	if !searcher.request.Start.Equal(now.Add(-10*time.Minute)) || !searcher.request.End.Equal(now) {
		t.Fatalf("count range = %s..%s", searcher.request.Start, searcher.request.End)
	}
	for _, incident := range repo.incidents {
		if incident.Value == nil || *incident.Value != 4 {
			t.Fatalf("incident value = %v, want 4", incident.Value)
		}
	}
}

func TestEvaluateLogSearchComparesAndRecoversEachGroup(t *testing.T) {
	repo := newFakeRepo()
	searcher := &scriptedStructuredLogSearcher{groups: []logquery.CountGroup{
		{Labels: map[string]string{"device_id": "42", "source_id": "journald"}, Count: 3},
		{Labels: map[string]string{"device_id": "43", "source_id": "journald"}, Count: 1},
	}}
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	rule := LogSearchRule{
		RuleKey: "host_errors", Name: "Host errors", ScopeType: model.RuleScopeHost,
		GroupBy: []string{"device_id", "source_id"}, Window: 5 * time.Minute,
		WindowText: "5m", Operator: ">=", Threshold: 2,
	}
	eval := newPipelineEvaluator(t, repo, &fakeNotifier{},
		NewStaticRulesProvider(WithLogSearchRules([]LogSearchRule{rule})),
		PipelineEvaluatorOpts{LogSearcher: searcher, Cooldown: time.Minute, Now: func() time.Time { return now }},
	)
	eval.EvaluateOnce(t.Context())
	if len(repo.incidents) != 1 {
		t.Fatalf("incidents = %d, want one independently breaching group", len(repo.incidents))
	}
	var incident *model.Incident
	for _, candidate := range repo.incidents {
		incident = candidate
	}
	if incident.DeviceID == nil || *incident.DeviceID != 42 || incident.Value == nil || *incident.Value != 3 {
		t.Fatalf("incident = %#v", incident)
	}
	if strings.Join(searcher.groupBy, ",") != "device_id,source_id" {
		t.Fatalf("group_by = %v", searcher.groupBy)
	}

	searcher.groups = []logquery.CountGroup{
		{Labels: map[string]string{"device_id": "42", "source_id": "journald"}, Count: 1},
		{Labels: map[string]string{"device_id": "43", "source_id": "journald"}, Count: 1},
	}
	eval.EvaluateOnce(t.Context())
	if incident.Status != model.IncidentStatusResolved {
		t.Fatalf("incident status = %q, want resolved", incident.Status)
	}
}

func TestEvaluateLogSearchKeepsSourceIDInIncidentIdentity(t *testing.T) {
	repo := newFakeRepo()
	searcher := &scriptedStructuredLogSearcher{groups: []logquery.CountGroup{
		{Labels: map[string]string{"device_id": "42", "source_id": "journald"}, Count: 3},
		{Labels: map[string]string{"device_id": "42", "source_id": "kubernetes:pod"}, Count: 4},
	}}
	rule := LogSearchRule{
		RuleKey: "host_errors", Name: "Host errors", ScopeType: model.RuleScopeHost,
		GroupBy: []string{"device_id", "source_id"}, Window: 5 * time.Minute,
		WindowText: "5m", Operator: ">=", Threshold: 2,
	}
	eval := newPipelineEvaluator(t, repo, &fakeNotifier{},
		NewStaticRulesProvider(WithLogSearchRules([]LogSearchRule{rule})),
		PipelineEvaluatorOpts{LogSearcher: searcher, Cooldown: time.Minute},
	)

	eval.EvaluateOnce(t.Context())
	if len(repo.incidents) != 2 {
		t.Fatalf("incidents = %d, want one per source_id", len(repo.incidents))
	}
	sources := make(map[string]bool, len(repo.incidents))
	for _, incident := range repo.incidents {
		labels, err := incident.Labels()
		if err != nil {
			t.Fatalf("decode incident labels: %v", err)
		}
		sources[labels["source_id"]] = true
	}
	if !sources["journald"] || !sources["kubernetes:pod"] {
		t.Fatalf("incident sources = %#v", sources)
	}
}

func TestLogSearchGroupKeyIsUnambiguous(t *testing.T) {
	groupBy := []string{"device_id", "source_id"}
	first := logSearchGroupKey(groupBy, map[string]string{
		"device_id": "42,source_id=journald", "source_id": "kubernetes:pod",
	})
	second := logSearchGroupKey(groupBy, map[string]string{
		"device_id": "42", "source_id": "journald,source_id=kubernetes:pod",
	})
	if first == second {
		t.Fatalf("distinct structured-log groups share key %q", first)
	}
}

func TestCompileLogMatch_Errors(t *testing.T) {
	for _, c := range []struct {
		name, spec string
	}{
		{"empty selector", `{}`},
		{"bad operator", `{"stream_selector":"{a=\"b\"}","operator":"~~"}`},
	} {
		row := &model.Rule{RuleKey: "k", ConditionsJSON: c.spec}
		if _, err := compileLogMatchRule(row); err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}
}

func TestCompileLogVolume_PreservesLineFilter(t *testing.T) {
	spec, _ := json.Marshal(map[string]any{
		"stream_selector": `{ongrid_source=~"journald(:.*)?",level!="7"}`,
		"line_filter":     "(?i)(error|failed)",
		"window":          "10m",
		"ratio_op":        ">",
		"ratio_threshold": 3.0,
	})
	row := &model.Rule{ID: 2, RuleKey: "log_volume_err", ConditionsJSON: string(spec)}
	r, err := compileLogVolumeRule(row)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if r.LineFilter != "(?i)(error|failed)" {
		t.Fatalf("LineFilter = %q, want content regex", r.LineFilter)
	}
	if q := buildLogMatchQuery(r.StreamSelector, r.LineFilter, r.Window); !strings.Contains(q, `|~ "(?i)(error|failed)"`) {
		t.Fatalf("query = %q, want log volume filter applied", q)
	}
}

func TestLogMatchHostScopeUsesDeviceIDLabel(t *testing.T) {
	repo := newFakeRepo()
	rules := NewStaticRulesProvider(WithLogMatchRules([]LogMatchRule{{
		ID:             1,
		RuleKey:        "system_log_error_keywords",
		Name:           "System log error keywords",
		Severity:       "warning",
		ScopeType:      "host",
		StreamSelector: `{ongrid_source=~"journald(:.*)?"}`,
		LineFilter:     "(?i)(error|fatal|panic)",
		Window:         "5m",
		Operator:       ">=",
		Threshold:      1,
	}}))
	logq := &scriptedLogRange{result: lokiMatrixEntry(map[string]string{
		"device_id":     "2",
		"ongrid_source": "journald",
		"unit":          "sshd.service",
	}, "3")}
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	eval := newPipelineEvaluator(t, repo, &fakeNotifier{}, rules, PipelineEvaluatorOpts{
		LogQuerier: logq,
		Cooldown:   time.Minute,
		Now:        func() time.Time { return now },
	})

	eval.EvaluateOnce(context.Background())

	if len(repo.incidents) != 1 {
		t.Fatalf("incidents = %d, want 1", len(repo.incidents))
	}
	for _, inc := range repo.incidents {
		if inc.DeviceID == nil || *inc.DeviceID != 2 {
			t.Fatalf("DeviceID = %v, want 2", inc.DeviceID)
		}
	}
}

func TestCompileTraceLatency_BuildsPromExpr(t *testing.T) {
	spec, _ := json.Marshal(map[string]any{
		"service":      "ongrid-edge",
		"operation":    "tunnel.dial",
		"quantile":     "p99",
		"window":       "5m",
		"threshold_ms": 250.0,
	})
	row := &model.Rule{RuleKey: "edge_p99", ConditionsJSON: string(spec)}
	r, err := compileTraceLatencyRule(row)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	want := []string{
		"histogram_quantile(0.99",
		`service_name="ongrid-edge"`,
		`span_name="tunnel.dial"`,
		"[5m]",
		"* 1000 > 250",
	}
	for _, s := range want {
		if !strings.Contains(r.Expr, s) {
			t.Errorf("expr missing %q\nfull: %s", s, r.Expr)
		}
	}
}

func TestCompileTraceErrorRate_BuildsPromExpr(t *testing.T) {
	spec, _ := json.Marshal(map[string]any{
		"service":       "checkout",
		"window":        "10m",
		"operator":      ">",
		"threshold_pct": 1.5,
	})
	row := &model.Rule{RuleKey: "checkout_err", ConditionsJSON: string(spec)}
	r, err := compileTraceErrorRateRule(row)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, s := range []string{
		`service_name="checkout"`,
		`status_code="STATUS_CODE_ERROR"`,
		"traces_spanmetrics_calls_total",
		"[10m]",
		"> 1.5",
	} {
		if !strings.Contains(r.Expr, s) {
			t.Errorf("expr missing %q\nfull: %s", s, r.Expr)
		}
	}
}

func TestBuildLogMatchQuery(t *testing.T) {
	q := buildLogMatchQuery(`{job="x"}`, "", "")
	if q != `count_over_time({job="x"} [5m])` {
		t.Errorf("default window: %q", q)
	}
	q = buildLogMatchQuery(`{job="x"}`, "(?i)error", "10m")
	if q != `count_over_time({job="x"} |~ "(?i)error" [10m])` {
		t.Errorf("with filter: %q", q)
	}
}

type scriptedLogRange struct {
	result *logquery.QueryRangeResult
	err    error
}

type scriptedStructuredLogSearcher struct {
	count   uint64
	groups  []logquery.CountGroup
	groupBy []string
	request logquery.SearchRequest
}

func (*scriptedStructuredLogSearcher) Search(context.Context, logquery.SearchRequest) (*logquery.SearchResult, error) {
	return &logquery.SearchResult{}, nil
}

func (s *scriptedStructuredLogSearcher) Count(_ context.Context, req logquery.SearchRequest) (uint64, error) {
	s.request = req
	return s.count, nil
}

func (s *scriptedStructuredLogSearcher) CountGrouped(_ context.Context, req logquery.SearchRequest, groupBy []string) ([]logquery.CountGroup, error) {
	s.request = req
	s.groupBy = append([]string(nil), groupBy...)
	return append([]logquery.CountGroup(nil), s.groups...), nil
}

func (*scriptedStructuredLogSearcher) Fields(context.Context, time.Time, time.Time, logquery.Scope) ([]logquery.Field, error) {
	return logquery.AllowedFields(), nil
}

func (*scriptedStructuredLogSearcher) FieldValues(context.Context, logquery.FieldValuesRequest) ([]string, error) {
	return nil, nil
}

func (*scriptedStructuredLogSearcher) Histogram(context.Context, logquery.SearchRequest, time.Duration) ([]logquery.HistogramBucket, error) {
	return nil, nil
}

func (s *scriptedLogRange) QueryRange(_ context.Context, _ logquery.QueryRangeOptions) (*logquery.QueryRangeResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.result, nil
}

func lokiMatrixEntry(labels map[string]string, value string) *logquery.QueryRangeResult {
	body := []map[string]interface{}{{
		"metric": labels,
		"values": [][]interface{}{
			{time.Now().UnixNano(), value},
		},
	}}
	raw, _ := json.Marshal(body)
	return &logquery.QueryRangeResult{ResultType: "matrix", Result: raw}
}
