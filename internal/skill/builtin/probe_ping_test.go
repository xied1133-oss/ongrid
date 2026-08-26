package builtin

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ongridio/ongrid/internal/skill"
)

func TestProbePing_Metadata(t *testing.T) {
	m := (ProbePing{}).Metadata()
	if err := m.Validate(); err != nil {
		t.Fatalf("metadata invalid: %v", err)
	}
	if m.EffectiveClass() != skill.ClassSafe {
		t.Fatalf("want ClassSafe, got %v", m.EffectiveClass())
	}
	if m.Key != "host_probe_ping" {
		t.Fatalf("unexpected Key %q", m.Key)
	}
}

func TestNormalizeProbePingParams_DefaultsAndCaps(t *testing.T) {
	params, _ := json.Marshal(map[string]any{
		"host":       "example.com",
		"count":      999,
		"timeout_ms": 999999,
	})
	got, err := normalizeProbePingParams(params)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got.Count != 10 {
		t.Fatalf("Count = %d, want cap 10", got.Count)
	}
	if got.TimeoutMS != 10000 {
		t.Fatalf("TimeoutMS = %d, want cap 10000", got.TimeoutMS)
	}
}

func TestProbePing_Execute_InvalidParams(t *testing.T) {
	if _, err := (ProbePing{}).Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected error for missing host")
	}
	if _, err := (ProbePing{}).Execute(context.Background(), json.RawMessage(`{"host":123}`)); err == nil {
		t.Fatal("expected error for wrong type")
	}
}
