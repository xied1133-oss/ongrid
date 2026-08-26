package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/alert"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/logquery"
)

// legacyLogStreamGroupBy is the product representation of the Loki stream
// identity configured by deploy/install/loki-config.yaml. Persisting these
// dimensions keeps legacy per-stream thresholds and dedupe keys stable while
// allowing Elasticsearch to execute the same grouping with OTel fields.
var legacyLogStreamGroupBy = []string{
	"device_id", "cluster_id", "source_id", "namespace", "service_name",
}

// MigrateLegacyLogRules rewrites every persisted Loki-only log rule to the
// canonical backend-neutral log_search shape. The conversion happens before
// Elasticsearch selection: if the subsequent selection fails, the migrated
// rules continue to run against the currently selected Loki backend.
func (u *Usecase) MigrateLegacyLogRules(ctx context.Context) (int, error) {
	if u.repo == nil {
		return 0, errs.ErrNotWiredYet
	}
	rules, err := u.repo.ListRules(ctx, "")
	if err != nil {
		return 0, fmt.Errorf("list alert rules for log migration: %w", err)
	}
	migrations := make([]LegacyLogRuleMigration, 0)
	for _, rule := range rules {
		if rule == nil {
			continue
		}
		kind := model.NormalizeKind(rule.Kind)
		if kind != model.RuleKindLogMatch && kind != model.RuleKindLogVolume {
			continue
		}
		conditions, err := migrateLegacyLogRule(rule, kind)
		if err != nil {
			return 0, fmt.Errorf("migrate alert rule %q: %w", rule.RuleKey, err)
		}
		migrations = append(migrations, LegacyLogRuleMigration{
			ID: rule.ID, FromKind: rule.Kind, ConditionsJSON: conditions,
		})
	}
	if len(migrations) > 0 {
		if err := u.repo.MigrateLegacyLogRules(ctx, migrations); err != nil {
			return 0, fmt.Errorf("persist backend-neutral log alerts: %w", err)
		}
	}
	// Refresh even when another process already migrated the rows. This closes
	// the brief window where an in-memory legacy evaluator could query the old
	// Loki backend after the selected backend has changed.
	if u.ruleCacheRefresher != nil {
		if err := u.ruleCacheRefresher.Refresh(ctx); err != nil {
			return len(migrations), fmt.Errorf("refresh alert rules after log migration: %w", err)
		}
	}
	return len(migrations), nil
}

func migrateLegacyLogRule(rule *model.Rule, kind string) (string, error) {
	selector, lineFilter, window, operator, threshold, err := legacyLogRuleParts(rule.ConditionsJSON, kind)
	if err != nil {
		return "", err
	}
	duration, err := time.ParseDuration(window)
	if err != nil || duration <= 0 {
		return "", fmt.Errorf("%w: invalid window %q", errs.ErrInvalid, window)
	}
	query := strings.TrimSpace(selector)
	if lineFilter != "" {
		query += " |~ " + strconv.Quote(lineFilter)
	}
	start := time.Unix(1, 0).UTC()
	req, err := logquery.CompileLogQLSearch(logquery.QueryRangeOptions{
		Query: query, Start: start, End: start.Add(duration), Limit: 1,
		Direction: string(logquery.SortBackward),
	})
	if err != nil {
		return "", fmt.Errorf("%w: legacy LogQL is not portable: %v", errs.ErrConflict, err)
	}
	spec, _, err := normalizeLogSearchSpec(logSearchSpec{
		Keywords: req.Keywords,
		Filters:  req.Filters,
		GroupBy:  append([]string(nil), legacyLogStreamGroupBy...),
		Window:   window,
		Operator: operator,
		Threshold: func() *float64 {
			value := threshold
			return &value
		}(),
	})
	if err != nil {
		return "", fmt.Errorf("%w: converted log_search rule is invalid: %v", errs.ErrInvalid, err)
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("encode converted log_search rule: %w", err)
	}
	normalized, err := normalizeLogSearchConditionsForScope(string(encoded), effectiveScope(rule.ScopeType, kind))
	if err != nil {
		return "", fmt.Errorf("normalize converted log_search rule scope: %w", err)
	}
	return normalized, nil
}

func legacyLogRuleParts(raw, kind string) (selector, lineFilter, window, operator string, threshold float64, err error) {
	switch kind {
	case model.RuleKindLogMatch:
		var spec logMatchSpec
		if decodeErr := json.Unmarshal([]byte(raw), &spec); decodeErr != nil {
			err = fmt.Errorf("%w: decode log_match conditions: %v", errs.ErrInvalid, decodeErr)
			return
		}
		selector, lineFilter, window, operator, threshold = spec.StreamSelector, spec.LineFilter, spec.Window, spec.Operator, spec.Threshold
		if operator == "" {
			operator = ">="
		}
	case model.RuleKindLogVolume:
		var spec logVolumeSpec
		if decodeErr := json.Unmarshal([]byte(raw), &spec); decodeErr != nil {
			err = fmt.Errorf("%w: decode log_volume conditions: %v", errs.ErrInvalid, decodeErr)
			return
		}
		selector, lineFilter, window, operator, threshold = spec.StreamSelector, spec.LineFilter, spec.Window, spec.RatioOp, spec.RatioThreshold
		if operator == "" {
			operator = ">="
		}
	default:
		err = fmt.Errorf("%w: unsupported legacy log rule kind %q", errs.ErrInvalid, kind)
		return
	}
	selector = strings.TrimSpace(selector)
	lineFilter = strings.TrimSpace(lineFilter)
	window = strings.TrimSpace(window)
	if selector == "" {
		err = fmt.Errorf("%w: stream_selector is required", errs.ErrInvalid)
		return
	}
	if window == "" {
		window = "5m"
	}
	return
}
