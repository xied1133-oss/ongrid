package alert

import (
	"context"
	"strings"
	"testing"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/alert"
)

type silenceTestClock struct {
	now time.Time
}

func (c silenceTestClock) Now() time.Time { return c.now }

func TestSilenceIncidentBoundsLongUnicodeName(t *testing.T) {
	repo := newFakeRepo()
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	title := "cpu_high: " + strings.Repeat("节点负载过高", 60)
	repo.incidents[39] = &model.Incident{
		ID:        39,
		Title:     title,
		Rule:      "cpu_high",
		RuleName:  "CPU high",
		ScopeType: model.RuleScopeGlobal,
		Severity:  "critical",
		Status:    model.IncidentStatusOpen,
	}

	uc := NewUsecase(repo, nil)
	uc.clock = silenceTestClock{now: now}
	if err := uc.SilenceIncident(context.Background(), 39, 7, "30m", "investigating"); err != nil {
		t.Fatalf("SilenceIncident() error = %v", err)
	}
	if len(repo.silences) != 1 {
		t.Fatalf("silences = %d, want 1", len(repo.silences))
	}
	got := repo.silences[0].Name
	if n := len([]rune(got)); n != maxSilenceNameRunes {
		t.Fatalf("silence name runes = %d, want %d", n, maxSilenceNameRunes)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("silence name %q does not end with ellipsis", got)
	}
	if repo.incidents[39].Status != model.IncidentStatusSilenced {
		t.Fatalf("incident status = %q, want %q", repo.incidents[39].Status, model.IncidentStatusSilenced)
	}
}

func TestBuildSilenceNameFallsBackToRuleIdentity(t *testing.T) {
	incident := &model.Incident{RuleName: "  CPU high  ", Rule: "cpu_high"}
	if got := buildSilenceName(incident); got != "CPU high" {
		t.Fatalf("buildSilenceName() = %q, want %q", got, "CPU high")
	}
}

func TestIncidentMutationsAllowEmptyAndLongNotes(t *testing.T) {
	mutations := []struct {
		name   string
		status string
		apply  func(*Usecase, context.Context, uint64, uint64, string) error
	}{
		{name: "acknowledge", status: model.IncidentStatusAcknowledged, apply: (*Usecase).AckIncident},
		{name: "resolve", status: model.IncidentStatusResolved, apply: (*Usecase).ResolveIncident},
	}
	notes := []struct {
		name string
		note string
	}{
		{name: "empty"},
		{name: "long unicode", note: strings.Repeat("处理记录", 2000)},
	}

	for _, mutation := range mutations {
		for _, note := range notes {
			t.Run(mutation.name+"/"+note.name, func(t *testing.T) {
				repo := newFakeRepo()
				repo.incidents[1] = &model.Incident{ID: 1, Title: "CPU high", Rule: "cpu_high", Status: model.IncidentStatusOpen}
				uc := NewUsecase(repo, nil)
				if err := mutation.apply(uc, context.Background(), 1, 7, note.note); err != nil {
					t.Fatalf("%s incident: %v", mutation.name, err)
				}
				if len(repo.events) != 1 {
					t.Fatalf("events = %d, want 1", len(repo.events))
				}
				if repo.incidents[1].Status != mutation.status {
					t.Fatalf("incident status = %q, want %q", repo.incidents[1].Status, mutation.status)
				}
				if note.note == "" {
					if repo.events[0].Message != nil {
						t.Fatalf("empty note message = %q, want nil", *repo.events[0].Message)
					}
				} else if repo.events[0].Message == nil || *repo.events[0].Message != note.note {
					t.Fatal("long note was not preserved in the incident event")
				}
			})
		}
	}
}
