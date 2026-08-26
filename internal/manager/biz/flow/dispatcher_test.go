package flow

import (
	"encoding/json"
	"testing"
)

// TestAlertMatches covers the trigger.alert_fired matching gate: optional
// case-insensitive rule substring + optional min-severity rank.
func TestAlertMatches(t *testing.T) {
	cfg := func(rule, minSev string) json.RawMessage {
		b, _ := json.Marshal(alertTriggerConfig{Rule: rule, MinSeverity: minSev})
		return b
	}
	cases := []struct {
		name     string
		cfg      json.RawMessage
		rule     string
		severity string
		want     bool
	}{
		{"blank config matches anything", json.RawMessage(`{}`), "disk full", "warning", true},
		{"nil config matches anything", nil, "cpu high", "info", true},
		{"rule substring case-insensitive", cfg("DISK", ""), "node disk full", "warning", true},
		{"rule substring miss", cfg("network", ""), "disk full", "critical", false},
		{"min severity met", cfg("", "error"), "x", "critical", true},
		{"min severity exact", cfg("", "error"), "x", "error", true},
		{"min severity below", cfg("", "error"), "x", "warning", false},
		{"unknown severity below gate", cfg("", "warning"), "x", "bogus", false},
		{"both gates pass", cfg("disk", "error"), "disk full", "critical", true},
		{"rule passes severity fails", cfg("disk", "critical"), "disk full", "error", false},
	}
	for _, c := range cases {
		if got := alertMatches(c.cfg, c.rule, c.severity); got != c.want {
			t.Errorf("%s: alertMatches = %v, want %v", c.name, got, c.want)
		}
	}
}
