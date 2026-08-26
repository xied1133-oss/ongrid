package alert

import (
	"context"
	"strings"
	"testing"

	model "github.com/ongridio/ongrid/internal/manager/model/alert"
)

// TestCachedRulesProviderRefresh verifies the post-Phase-3-collapse path:
// metric_threshold rule rows live in the DB after the friendly UI form
// compiles them at save time, but they're stored as kind=metric_raw
// (single PromQL expression). After NewCachedRulesProvider.Refresh, the
// cache surfaces them via MetricRawRules() — there is no longer a
// HostRules() bucket.
func TestCachedRulesProviderRefresh(t *testing.T) {
	repo := newFakeRepo()
	repo.seedMetricRawRules(t)
	src := ruleSourceAdapter{repo: repo}
	cache := NewCachedRulesProvider(src, 0, nil)
	if got := cache.MetricRawRules(); len(got) != 0 {
		t.Errorf("pre-refresh snapshot = %d, want 0", len(got))
	}
	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	got := cache.MetricRawRules()
	if len(got) != 4 {
		t.Errorf("post-refresh snapshot = %d, want 4 seeded rules", len(got))
	}
}

// TestCompileMetricThresholdExpr_Single verifies the compiler emits a
// terse `(<metricExprFor>) <op> <thr>` string for single-condition
// rules — that's the predicate the metric_raw evaluator runs.
func TestCompileMetricThresholdExpr_Single(t *testing.T) {
	conds := []model.RuleCondition{{Metric: "cpu_pct", Operator: ">=", Threshold: 90}}
	expr, err := compileMetricThresholdExpr(conds, model.RuleJoinModeAll)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if expr == "" {
		t.Fatalf("compile: empty expression")
	}
	if !strings.Contains(expr, "node_cpu_seconds_total") {
		t.Errorf("expr should include cpu base, got %s", expr)
	}
	if !strings.Contains(expr, ">= 90") {
		t.Errorf("expr should include comparison, got %s", expr)
	}
}

// TestCompileMetricThresholdExpr_JoinModes verifies the join_mode
// dispatch: ALL emits `and on(device_id)`, ANY emits `or`.
func TestCompileMetricThresholdExpr_JoinModes(t *testing.T) {
	conds := []model.RuleCondition{
		{Metric: "cpu_pct", Operator: ">", Threshold: 80},
		{Metric: "mem_pct", Operator: ">", Threshold: 80},
	}

	all, err := compileMetricThresholdExpr(conds, model.RuleJoinModeAll)
	if err != nil {
		t.Fatalf("compile all: %v", err)
	}
	if !strings.Contains(all, "and on(device_id)") {
		t.Errorf("ALL should join with and on(device_id), got %s", all)
	}

	any, err := compileMetricThresholdExpr(conds, model.RuleJoinModeAny)
	if err != nil {
		t.Fatalf("compile any: %v", err)
	}
	if !strings.Contains(any, " or ") {
		t.Errorf("ANY should join with or, got %s", any)
	}
}

// TestCompileMetricThresholdExpr_RejectsUnknownMetric ensures an
// unknown closed-set name surfaces as a compile error so the UI's
// 试算 / 保存 path returns 400 instead of the evaluator silently
// dropping the rule.
func TestCompileMetricThresholdExpr_RejectsUnknownMetric(t *testing.T) {
	conds := []model.RuleCondition{{Metric: "made_up_metric", Operator: ">", Threshold: 1}}
	if _, err := compileMetricThresholdExpr(conds, model.RuleJoinModeAll); err == nil {
		t.Errorf("compile should reject unknown metric")
	}
}

type ruleSourceAdapter struct{ repo *fakeRepo }

func (r ruleSourceAdapter) ListAllEnabledRules(ctx context.Context) ([]*model.Rule, error) {
	return r.repo.ListAllEnabledRules(ctx)
}
