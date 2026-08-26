package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestHostNetnsInspect_Metadata(t *testing.T) {
	m := HostNetnsInspect{}.Metadata()
	if m.Key != "host_netns_inspect" {
		t.Fatalf("Key = %q", m.Key)
	}
	if m.Class != "safe" {
		t.Fatalf("Class = %q, want safe", m.Class)
	}
	if m.Scope != "host" {
		t.Fatalf("Scope = %q, want host", m.Scope)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("metadata invalid: %v", err)
	}
}

func TestValidateNetnsName(t *testing.T) {
	good := []string{"foo", "ns-0", "ns_test.1", "abcXYZ123", strings.Repeat("a", 64)}
	for _, n := range good {
		if err := validateNetnsName(n); err != nil {
			t.Errorf("validateNetnsName(%q) = %v, want nil", n, err)
		}
	}
	bad := []string{
		"",
		"; rm -rf /",
		"`whoami`",
		"$(id)",
		"foo bar",
		"foo/bar",
		"foo;bar",
		"foo|bar",
		"foo\nbar",
		strings.Repeat("a", 65),
	}
	for _, n := range bad {
		if err := validateNetnsName(n); err == nil {
			t.Errorf("validateNetnsName(%q) = nil, want error", n)
		}
	}
}

// TestHostNetnsInspect_RejectsBadName ensures the skill refuses
// shell-metacharacter strings up front (no exec attempt).
func TestHostNetnsInspect_RejectsBadName(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"namespace": "; rm -rf /"})
	_, err := HostNetnsInspect{}.Execute(context.Background(), body)
	if err == nil {
		t.Fatalf("expected error on bad namespace name")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("error %q should mention 'invalid'", err.Error())
	}
}
