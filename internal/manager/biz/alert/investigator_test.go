package alert

import (
	"context"
	"testing"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/alert"
)

// fakeInvestigator records the incidents passed to InvestigateAsync.
type fakeInvestigator struct {
	incidents []*model.Incident
}

func (f *fakeInvestigator) InvestigateAsync(incident *model.Incident) {
	f.incidents = append(f.incidents, incident)
}

// TestRecordFiringDispatchesInvestigatorOnNew verifies that a firing
// that creates a fresh incident (IsNew=true) hands the incident to the
// investigator.
func TestRecordFiringDispatchesInvestigatorOnNew(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, nil)
	inv := &fakeInvestigator{}
	uc.SetInvestigator(inv)

	deviceID := uint64(7)
	res, err := uc.RecordFiring(context.Background(), FiringInput{
		ScopeType:  model.RuleScopeHost,
		Rule:       "cpu_high",
		Severity:   "warning",
		DeviceID:   &deviceID,
		OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("RecordFiring: %v", err)
	}
	if !res.IsNew {
		t.Fatalf("expected IsNew=true on first firing")
	}
	if len(inv.incidents) != 1 {
		t.Fatalf("expected 1 investigation dispatched, got %d", len(inv.incidents))
	}
	if inv.incidents[0].ID == 0 {
		t.Errorf("dispatched incident has zero ID")
	}
}

// TestRecordFiringSkipsInvestigatorOnRefire verifies that subsequent
// firings of the same dedupe_key do NOT re-dispatch the investigator —
// a noisy rule storm must not generate redundant LLM round-trips.
func TestRecordFiringSkipsInvestigatorOnRefire(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, nil)
	inv := &fakeInvestigator{}
	uc.SetInvestigator(inv)

	deviceID := uint64(9)
	in := FiringInput{
		ScopeType:  model.RuleScopeHost,
		Rule:       "mem_high",
		Severity:   "warning",
		DeviceID:   &deviceID,
		OccurredAt: time.Now().UTC(),
	}
	for i := 0; i < 3; i++ {
		if _, err := uc.RecordFiring(context.Background(), in); err != nil {
			t.Fatalf("RecordFiring[%d]: %v", i, err)
		}
	}
	if len(inv.incidents) != 1 {
		t.Errorf("expected exactly 1 dispatch across 3 firings, got %d", len(inv.incidents))
	}
}

// TestRecordFiringNoInvestigatorIsSafe verifies that the firing path
// works unchanged when no investigator is wired (the LLM-disabled
// deployment shape).
func TestRecordFiringNoInvestigatorIsSafe(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, nil)
	// Note: SetInvestigator NOT called.

	deviceID := uint64(11)
	res, err := uc.RecordFiring(context.Background(), FiringInput{
		ScopeType:  model.RuleScopeHost,
		Rule:       "disk_high",
		Severity:   "critical",
		DeviceID:   &deviceID,
		OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("RecordFiring without investigator: %v", err)
	}
	if !res.IsNew {
		t.Fatalf("expected IsNew=true")
	}
	// Sanity: a firing event was still written.
	if !hasEventType(repo.events, model.EventTypeFiring) {
		t.Errorf("expected firing event")
	}
}
