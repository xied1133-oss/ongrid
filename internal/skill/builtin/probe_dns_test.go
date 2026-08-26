package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ongridio/ongrid/internal/skill"
)

func TestProbeDNS_Metadata(t *testing.T) {
	m := (ProbeDNS{}).Metadata()
	if err := m.Validate(); err != nil {
		t.Fatalf("metadata invalid: %v", err)
	}
	if m.EffectiveClass() != skill.ClassSafe {
		t.Fatalf("want ClassSafe, got %v", m.EffectiveClass())
	}
}

func TestProbeDNS_Execute_HappyPath(t *testing.T) {
	params, _ := json.Marshal(map[string]any{
		"host":       "localhost",
		"timeout_ms": 2000,
	})
	out, err := (ProbeDNS{}).Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var res probeDNSResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected error: %q", res.Error)
	}
	if len(res.Addrs) == 0 {
		t.Fatalf("expected at least one addr for localhost")
	}
	found := false
	for _, a := range res.Addrs {
		if a == "127.0.0.1" || strings.Contains(a, "::1") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 127.0.0.1 or ::1 in %v", res.Addrs)
	}
}

func TestProbeDNS_Execute_InvalidParams(t *testing.T) {
	if _, err := (ProbeDNS{}).Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected error for missing host")
	}
	if _, err := (ProbeDNS{}).Execute(context.Background(), json.RawMessage(`{"host":[]}`)); err == nil {
		t.Fatal("expected error for wrong type")
	}
}

func TestProbeDNS_Execute_Unresolvable(t *testing.T) {
	params, _ := json.Marshal(map[string]any{
		"host":       "this-host-definitely-does-not-exist.invalid",
		"timeout_ms": 2000,
	})
	out, err := (ProbeDNS{}).Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var res probeDNSResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Error == "" {
		t.Fatalf("expected resolution error, got %+v", res)
	}
}
