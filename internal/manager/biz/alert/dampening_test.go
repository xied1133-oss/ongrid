package alert

import (
	"context"
	"strings"
	"testing"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/alert"
	"github.com/ongridio/ongrid/internal/pkg/notify"
)

// TestRuleInputRoundTripsSendPolicy confirms that NotifyWindowSeconds /
// NotifyMinFires survive CreateRule + GetRule unchanged.
func TestRuleInputRoundTripsSendPolicy(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, nil)

	in := RuleInput{
		RuleKey:             "cpu_high",
		Kind:                model.RuleKindMetricRaw,
		Name:                "CPU High",
		ScopeType:           model.RuleScopeGlobal,
		JoinMode:            model.RuleJoinModeAll,
		Severity:            "warning",
		Enabled:             true,
		Spec:                map[string]any{"expr": "node_load1 > 1"},
		NotifyWindowSeconds: 600,
		NotifyMinFires:      3,
	}
	row, err := uc.CreateRule(context.Background(), in, nil)
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	if row.NotifyWindowSeconds != 600 {
		t.Errorf("NotifyWindowSeconds = %d, want 600", row.NotifyWindowSeconds)
	}
	if row.NotifyMinFires != 3 {
		t.Errorf("NotifyMinFires = %d, want 3", row.NotifyMinFires)
	}

	got, err := uc.GetRule(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if got.NotifyWindowSeconds != 600 || got.NotifyMinFires != 3 {
		t.Errorf("round-trip lost send policy: window=%d threshold=%d",
			got.NotifyWindowSeconds, got.NotifyMinFires)
	}
}

// TestRuleInputRejectsMixedSendPolicy confirms the validator refuses to
// accept just one of (window, threshold) — that's a user error and would
// silently disable dampening if we let it through.
func TestRuleInputRejectsMixedSendPolicy(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, nil)

	cases := []struct {
		name   string
		window int
		thresh int
	}{
		{"window-only", 600, 0},
		{"threshold-only", 0, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := RuleInput{
				RuleKey:             "cpu_high_" + strings.ReplaceAll(tc.name, "-", "_"),
				Kind:                model.RuleKindMetricRaw,
				Name:                "CPU High",
				ScopeType:           model.RuleScopeGlobal,
				JoinMode:            model.RuleJoinModeAll,
				Severity:            "warning",
				Spec:                map[string]any{"expr": "node_load1 > 1"},
				NotifyWindowSeconds: tc.window,
				NotifyMinFires:      tc.thresh,
			}
			_, err := uc.CreateRule(context.Background(), in, nil)
			if err == nil {
				t.Fatalf("expected error for mixed send policy, got nil")
			}
			if !strings.Contains(err.Error(), "send policy") {
				t.Errorf("error doesn't mention send policy: %v", err)
			}
		})
	}
}

// TestRuleInputBothZeroSendPolicyOK confirms both-zero (disabled) is the
// happy default — this is what every existing rule looks like, and it
// must keep working.
func TestRuleInputBothZeroSendPolicyOK(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, nil)

	in := RuleInput{
		RuleKey:   "cpu_high",
		Kind:      model.RuleKindMetricRaw,
		Name:      "CPU High",
		ScopeType: model.RuleScopeGlobal,
		JoinMode:  model.RuleJoinModeAll,
		Severity:  "warning",
		Spec:      map[string]any{"expr": "node_load1 > 1"},
		// both zero
	}
	row, err := uc.CreateRule(context.Background(), in, nil)
	if err != nil {
		t.Fatalf("CreateRule (both zero): %v", err)
	}
	if row.NotifyWindowSeconds != 0 || row.NotifyMinFires != 0 {
		t.Errorf("expected both zero, got window=%d threshold=%d",
			row.NotifyWindowSeconds, row.NotifyMinFires)
	}
}

// TestMaybeNotifyDampeningBelowThreshold drives RecordFiring + MaybeNotify
// twice with a rule whose threshold is 3. The notifier mock must receive
// zero calls, and a repeat_suppressed event must be recorded with the
// "dampened" reason for each suppressed firing.
func TestMaybeNotifyDampeningBelowThreshold(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, nil)
	notifier := &fakeNotifier{}
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	// Rule with dampening enabled: 10-min window, threshold 3.
	if _, err := uc.CreateRule(context.Background(), RuleInput{
		RuleKey:             "noisy_rule",
		Kind:                model.RuleKindMetricRaw,
		Name:                "Noisy Rule",
		ScopeType:           model.RuleScopeGlobal,
		JoinMode:            model.RuleJoinModeAll,
		Severity:            "warning",
		Enabled:             true,
		Spec:                map[string]any{"expr": "vector(1) > 0"},
		NotifyWindowSeconds: 600,
		NotifyMinFires:      3,
	}, nil); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	for i := 0; i < 2; i++ {
		at := now.Add(time.Duration(i) * time.Second)
		res, err := uc.RecordFiring(context.Background(), FiringInput{
			ScopeType:  model.RuleScopeGlobal,
			Rule:       "noisy_rule",
			Severity:   "warning",
			Summary:    "noisy",
			OccurredAt: at,
		})
		if err != nil {
			t.Fatalf("RecordFiring %d: %v", i, err)
		}
		uc.MaybeNotify(context.Background(), res, notify.Message{
			Subject: "noisy", Severity: notify.SeverityWarning, OccurredAt: at,
		}, NotifyOpts{
			Notifier:        notifier,
			DefaultChannels: []string{"log"},
			Cooldown:        0, // disable cooldown so dampening is the sole gate
		})
	}

	if got := len(notifier.msgs); got != 0 {
		t.Errorf("notifier received %d messages, want 0 (dampened: 2 < 3)", got)
	}
	// Each suppressed firing must leave a repeat_suppressed event with a
	// "dampened" reason for operator visibility.
	dampened := 0
	for _, ev := range repo.events {
		if ev.EventType == model.EventTypeRepeatSuppressed && strings.Contains(ev.Reason, "dampened") {
			dampened++
		}
	}
	if dampened != 2 {
		t.Errorf("dampened repeat_suppressed events = %d, want 2", dampened)
	}
}

// TestMaybeNotifyDampeningReleasesAtThreshold drives RecordFiring +
// MaybeNotify three times — the third firing pushes count ≥ 3 and the
// notifier is invoked exactly once.
func TestMaybeNotifyDampeningReleasesAtThreshold(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, nil)
	notifier := &fakeNotifier{}
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	repo.channels["log"] = &model.Channel{
		ID: 1, Name: "log", ChannelType: model.ChannelTypeWebhook, Enabled: true,
		ConfigJSON: `{"url":"http://test.local/hook"}`,
	}

	if _, err := uc.CreateRule(context.Background(), RuleInput{
		RuleKey:             "noisy_rule",
		Kind:                model.RuleKindMetricRaw,
		Name:                "Noisy Rule",
		ScopeType:           model.RuleScopeGlobal,
		JoinMode:            model.RuleJoinModeAll,
		Severity:            "warning",
		Enabled:             true,
		Spec:                map[string]any{"expr": "vector(1) > 0"},
		NotifyWindowSeconds: 600,
		NotifyMinFires:      3,
	}, nil); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	for i := 0; i < 3; i++ {
		at := now.Add(time.Duration(i) * time.Second)
		res, err := uc.RecordFiring(context.Background(), FiringInput{
			ScopeType:  model.RuleScopeGlobal,
			Rule:       "noisy_rule",
			Severity:   "warning",
			Summary:    "noisy",
			OccurredAt: at,
		})
		if err != nil {
			t.Fatalf("RecordFiring %d: %v", i, err)
		}
		uc.MaybeNotify(context.Background(), res, notify.Message{
			Subject: "noisy", Severity: notify.SeverityWarning, OccurredAt: at,
		}, NotifyOpts{
			Notifier:        notifier,
			DefaultChannels: []string{"log"},
			Cooldown:        0,
		})
	}

	if got := len(notifier.msgs); got != 1 {
		t.Errorf("notifier received %d messages, want exactly 1 (3rd firing releases gate)", got)
	}
}

// TestMaybeNotifyDampeningDisabledNotifiesOnEveryFiring confirms the
// default-zero send policy is a no-op: every firing gets a notification
// (subject only to the cooldown / silence / inhibit gates).
func TestMaybeNotifyDampeningDisabledNotifiesOnEveryFiring(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, nil)
	notifier := &fakeNotifier{}
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	repo.channels["log"] = &model.Channel{
		ID: 1, Name: "log", ChannelType: model.ChannelTypeWebhook, Enabled: true,
		ConfigJSON: `{"url":"http://test.local/hook"}`,
	}

	// Both zero — dampening disabled.
	if _, err := uc.CreateRule(context.Background(), RuleInput{
		RuleKey:   "loud_rule",
		Kind:      model.RuleKindMetricRaw,
		Name:      "Loud Rule",
		ScopeType: model.RuleScopeGlobal,
		JoinMode:  model.RuleJoinModeAll,
		Severity:  "warning",
		Enabled:   true,
		Spec:      map[string]any{"expr": "vector(1) > 0"},
	}, nil); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	for i := 0; i < 2; i++ {
		at := now.Add(time.Duration(i) * time.Second)
		res, err := uc.RecordFiring(context.Background(), FiringInput{
			ScopeType:  model.RuleScopeGlobal,
			Rule:       "loud_rule",
			Severity:   "warning",
			Summary:    "loud",
			OccurredAt: at,
		})
		if err != nil {
			t.Fatalf("RecordFiring %d: %v", i, err)
		}
		uc.MaybeNotify(context.Background(), res, notify.Message{
			Subject: "loud", Severity: notify.SeverityWarning, OccurredAt: at,
		}, NotifyOpts{
			Notifier:        notifier,
			DefaultChannels: []string{"log"},
			Cooldown:        0,
		})
	}

	if got := len(notifier.msgs); got != 2 {
		t.Errorf("notifier received %d messages, want 2 (no dampening)", got)
	}
}
