package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/ongridio/ongrid/internal/manager/biz/aiops/tools/basetool"
	alertmodel "github.com/ongridio/ongrid/internal/manager/model/alert"
)

// QueryAlertRulesTool is the BaseTool form of query_alert_rules. Mirrors
// executeQueryAlertRules in query_alert_rules.go.
type QueryAlertRulesTool struct {
	alertUC AlertUsecase
	log     *slog.Logger
}

// NewQueryAlertRulesTool builds the BaseTool variant.
func NewQueryAlertRulesTool(alertUC AlertUsecase, log *slog.Logger) *QueryAlertRulesTool {
	if log == nil {
		log = slog.Default()
	}
	return &QueryAlertRulesTool{alertUC: alertUC, log: log}
}

// queryAlertRulesWhenToUse — reverse-guard against confusing this with
// listing incidents (rules vs incidents are different objects).
const queryAlertRulesWhenToUse = "When the user wants to LIST alert RULE definitions (the templates that fire incidents) " +
	"— '这条规则是谁在用' / 'show all metric_threshold rules'. " +
	"NOT for incidents themselves (use query_incidents). " +
	"NOT for an individual rule's history of firings (use query_incidents with rule_key). " +
	"NOT for editing rules (read-only)."

// Info returns metadata. Class=read.
func (t *QueryAlertRulesTool) Info(_ context.Context) (*basetool.ToolInfo, error) {
	return &basetool.ToolInfo{
		Name:        ToolNameQueryAlertRules,
		Description: QueryAlertRulesDescription,
		WhenToUse:   queryAlertRulesWhenToUse,
		Parameters:  QueryAlertRulesSchema,
		Class:       "read",
	}, nil
}

// InvokableRun lists alert rules. Mirror of executeQueryAlertRules.
func (t *QueryAlertRulesTool) InvokableRun(ctx context.Context, argsJSON string, _ ...basetool.InvokeOption) (string, error) {
	if t.alertUC == nil {
		return "", fmt.Errorf("query_alert_rules: alert usecase not configured")
	}
	var in QueryAlertRulesArgs
	if err := json.Unmarshal([]byte(argsJSON), &in); err != nil {
		return "", fmt.Errorf("query_alert_rules: bad args: %w", err)
	}
	if in.Limit <= 0 {
		in.Limit = 100
	}
	if in.Limit > 500 {
		in.Limit = 500
	}
	if in.Kind != "" && !alertmodel.IsKnownKind(in.Kind) {
		return "", fmt.Errorf("query_alert_rules: invalid kind %q", in.Kind)
	}

	callCtx, cancel := context.WithTimeout(ctx, queryAlertRulesCallTimeout)
	defer cancel()
	all, err := t.alertUC.ListRules(callCtx, "")
	if err != nil {
		return "", fmt.Errorf("query_alert_rules: list: %w", err)
	}

	rows := make([]AlertRuleRow, 0, len(all))
	wantKind := alertmodel.NormalizeKind(in.Kind)
	for _, rule := range all {
		if in.Kind != "" && alertmodel.NormalizeKind(rule.Kind) != wantKind {
			continue
		}
		if in.Enabled != nil && rule.Enabled != *in.Enabled {
			continue
		}
		if in.NameContains != "" {
			if !strings.Contains(rule.Name, in.NameContains) && !strings.Contains(rule.RuleKey, in.NameContains) {
				continue
			}
		}
		rows = append(rows, AlertRuleRow{
			ID:         rule.ID,
			RuleKey:    rule.RuleKey,
			Kind:       rule.Kind,
			Name:       rule.Name,
			ScopeType:  rule.ScopeType,
			Severity:   rule.Severity,
			Enabled:    rule.Enabled,
			SourceType: rule.SourceType,
			UpdatedAt:  rule.UpdatedAt,
		})
		if len(rows) >= in.Limit {
			break
		}
	}

	out, err := json.Marshal(map[string]any{
		"rules": rows,
		"count": len(rows),
	})
	if err != nil {
		return "", fmt.Errorf("query_alert_rules: marshal: %w", err)
	}
	return string(out), nil
}
