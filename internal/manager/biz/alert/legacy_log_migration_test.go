package alert

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	model "github.com/ongridio/ongrid/internal/manager/model/alert"
	"github.com/ongridio/ongrid/internal/pkg/logquery"
)

type recordingRuleCacheRefresher struct {
	calls int
	err   error
}

func (r *recordingRuleCacheRefresher) Refresh(_ context.Context) error {
	r.calls++
	return r.err
}

func TestMigrateLegacyLogRulesCanonicalizesPortableRules(t *testing.T) {
	repo := newFakeRepo()
	repo.rules["panic"] = &model.Rule{
		ID: 1, RuleKey: "panic", Kind: model.RuleKindLogMatch, ScopeType: model.RuleScopeGlobal,
		ConditionsJSON: `{"stream_selector":"{ongrid_source=~\"journald:.+\"}","line_filter":"(?i)panic|oom|fatal","window":"5m","operator":">=","threshold":1}`,
	}
	repo.rules["errors"] = &model.Rule{
		ID: 2, RuleKey: "errors", Kind: model.RuleKindLogVolume, ScopeType: model.RuleScopeGlobal,
		ConditionsJSON: `{"stream_selector":"{level=\"error\"}","window":"5m","ratio_op":">=","ratio_threshold":2}`,
	}
	cache := &recordingRuleCacheRefresher{}
	uc := NewUsecase(repo, nil)
	uc.SetRuleCacheRefresher(cache)

	count, err := uc.MigrateLegacyLogRules(t.Context())
	if err != nil {
		t.Fatalf("MigrateLegacyLogRules() error = %v", err)
	}
	if count != 2 || cache.calls != 1 {
		t.Fatalf("count=%d refresh calls=%d", count, cache.calls)
	}
	for _, key := range []string{"panic", "errors"} {
		rule := repo.rules[key]
		if rule.Kind != model.RuleKindLogSearch {
			t.Fatalf("%s kind = %q", key, rule.Kind)
		}
		if _, err := compileLogSearchRule(rule); err != nil {
			t.Fatalf("compile migrated %s: %v", key, err)
		}
	}
	var panicSpec logSearchSpec
	if err := json.Unmarshal([]byte(repo.rules["panic"].ConditionsJSON), &panicSpec); err != nil {
		t.Fatalf("decode migrated panic rule: %v", err)
	}
	if len(panicSpec.Filters) != 1 || panicSpec.Filters[0].Operator != logquery.FilterPrefix || panicSpec.Filters[0].Values[0] != "journald:" {
		t.Fatalf("migrated filters = %#v", panicSpec.Filters)
	}
	if !reflect.DeepEqual(panicSpec.GroupBy, legacyLogStreamGroupBy) {
		t.Fatalf("migrated group_by = %v, want %v", panicSpec.GroupBy, legacyLogStreamGroupBy)
	}
}

func TestMigrateLegacyLogRulesPreservesHostStreamGrouping(t *testing.T) {
	repo := newFakeRepo()
	repo.rules["host_error"] = &model.Rule{
		ID: 1, RuleKey: "host_error", Kind: model.RuleKindLogMatch, ScopeType: model.RuleScopeHost,
		ConditionsJSON: `{"stream_selector":"{level=\"error\"}","line_filter":"(?i)error","window":"5m","operator":">=","threshold":1}`,
	}
	count, err := NewUsecase(repo, nil).MigrateLegacyLogRules(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("MigrateLegacyLogRules() count=%d error=%v", count, err)
	}
	rule := repo.rules["host_error"]
	if rule.Kind != model.RuleKindLogSearch || rule.ScopeType != model.RuleScopeHost {
		t.Fatalf("migrated rule = %#v", rule)
	}
	compiled, err := compileLogSearchRule(rule)
	if err != nil {
		t.Fatalf("compile migrated host rule: %v", err)
	}
	if !reflect.DeepEqual(compiled.GroupBy, legacyLogStreamGroupBy) {
		t.Fatalf("compiled group_by = %v, want %v", compiled.GroupBy, legacyLogStreamGroupBy)
	}
	if !logSearchFiltersRequireDevice(compiled.Query.Filters) {
		t.Fatalf("host query filters = %#v, want device_id existence constraint", compiled.Query.Filters)
	}
}

func TestCreateRuleCanonicalizesLegacyInput(t *testing.T) {
	repo := newFakeRepo()
	row, err := NewUsecase(repo, nil).CreateRule(t.Context(), RuleInput{
		RuleKey: "legacy_client", Kind: model.RuleKindLogMatch, ScopeType: model.RuleScopeGlobal,
		Name: "Legacy client", Severity: "warning", Enabled: true,
		Spec: map[string]any{
			"stream_selector": `{level="error"}`, "line_filter": "(?i)timeout|panic",
			"window": "5m", "operator": ">=", "threshold": float64(1),
		},
	}, nil)
	if err != nil {
		t.Fatalf("CreateRule() error = %v", err)
	}
	if row.Kind != model.RuleKindLogSearch {
		t.Fatalf("stored kind = %q", row.Kind)
	}
	if _, err := compileLogSearchRule(row); err != nil {
		t.Fatalf("compile canonical rule: %v", err)
	}
}

func TestCreateHostLogSearchDefaultsToDeviceGrouping(t *testing.T) {
	repo := newFakeRepo()
	row, err := NewUsecase(repo, nil).CreateRule(t.Context(), RuleInput{
		RuleKey: "host_logs", Kind: model.RuleKindLogSearch, ScopeType: model.RuleScopeHost,
		Name: "Host logs", Severity: "warning", Enabled: true,
		Spec: map[string]any{"window": "5m", "operator": ">=", "threshold": float64(1)},
	}, nil)
	if err != nil {
		t.Fatalf("CreateRule() error = %v", err)
	}
	compiled, err := compileLogSearchRule(row)
	if err != nil {
		t.Fatalf("compile host rule: %v", err)
	}
	if !reflect.DeepEqual(compiled.GroupBy, []string{"device_id"}) {
		t.Fatalf("group_by = %v, want device_id", compiled.GroupBy)
	}
	if !logSearchFiltersRequireDevice(compiled.Query.Filters) {
		t.Fatalf("host query filters = %#v, want device_id existence constraint", compiled.Query.Filters)
	}
}

func TestCreateHostLogSearchPrependsDeviceToExplicitGrouping(t *testing.T) {
	repo := newFakeRepo()
	row, err := NewUsecase(repo, nil).CreateRule(t.Context(), RuleInput{
		RuleKey: "host_service_logs", Kind: model.RuleKindLogSearch, ScopeType: model.RuleScopeHost,
		Name: "Host service logs", Severity: "warning", Enabled: true,
		Spec: map[string]any{
			"group_by": []string{"service_name"}, "window": "5m",
			"operator": ">=", "threshold": float64(1),
		},
	}, nil)
	if err != nil {
		t.Fatalf("CreateRule() error = %v", err)
	}
	compiled, err := compileLogSearchRule(row)
	if err != nil {
		t.Fatalf("compile host rule: %v", err)
	}
	if !reflect.DeepEqual(compiled.GroupBy, []string{"device_id", "service_name"}) {
		t.Fatalf("group_by = %v", compiled.GroupBy)
	}
}

func TestMigrateLegacyLogRulesSurfacesCacheRefreshFailure(t *testing.T) {
	repo := newFakeRepo()
	cache := &recordingRuleCacheRefresher{err: errors.New("refresh failed")}
	uc := NewUsecase(repo, nil)
	uc.SetRuleCacheRefresher(cache)
	count, err := uc.MigrateLegacyLogRules(t.Context())
	if count != 0 || err == nil || cache.calls != 1 {
		t.Fatalf("count=%d err=%v refresh calls=%d", count, err, cache.calls)
	}
}
