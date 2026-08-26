package report

import (
	"testing"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/report"
)

func TestCronNext_WeeklyMonday9am(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	// After Mon 2026-06-08 10:00 → next Monday 9am = 2026-06-15 09:00.
	after := time.Date(2026, 6, 8, 10, 0, 0, 0, loc)
	next, err := CronNext("0 9 * * 1", loc, after)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 6, 15, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Errorf("next = %s, want %s", next, want)
	}
}

func TestCronNext_DailyStrictlyAfter(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	// Exactly at 9am → next is TOMORROW 9am (strictly after).
	after := time.Date(2026, 6, 8, 9, 0, 0, 0, loc)
	next, _ := CronNext("0 9 * * *", loc, after)
	want := time.Date(2026, 6, 9, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Errorf("daily next = %s, want %s", next, want)
	}
}

func TestCronNext_RespectsTimezone(t *testing.T) {
	sh := mustLoc(t, "Asia/Shanghai")
	ny := mustLoc(t, "America/New_York")
	after := time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)
	nextSH, _ := CronNext("0 9 * * *", sh, after)
	nextNY, _ := CronNext("0 9 * * *", ny, after)
	// 9am Shanghai and 9am New York are different absolute instants.
	if nextSH.Equal(nextNY) {
		t.Errorf("tz ignored: SH=%s NY=%s", nextSH, nextNY)
	}
	if nextSH.In(sh).Hour() != 9 || nextNY.In(ny).Hour() != 9 {
		t.Errorf("local hour wrong: SH=%s NY=%s", nextSH.In(sh), nextNY.In(ny))
	}
}

func TestCronNext_BadSpec(t *testing.T) {
	if _, err := CronNext("not a cron", time.UTC, time.Now()); err == nil {
		t.Error("expected error on bad spec")
	}
}

func TestCronSpecForKind(t *testing.T) {
	for _, kind := range []string{model.KindDaily, model.KindWeekly, model.KindMonthly} {
		spec, err := CronSpecForKind(kind)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if err := ValidateCronSpec(spec); err != nil {
			t.Errorf("%s generated invalid spec %q: %v", kind, spec, err)
		}
	}
	if _, err := CronSpecForKind(model.KindCustom); err == nil {
		t.Error("custom should require explicit spec")
	}
	if _, err := CronSpecForKind("weekly-ish"); err == nil {
		t.Error("unknown kind should error")
	}
}

func TestValidateCronSpec(t *testing.T) {
	if err := ValidateCronSpec("0 9 * * 1"); err != nil {
		t.Errorf("valid spec rejected: %v", err)
	}
	if err := ValidateCronSpec("99 99 * * *"); err == nil {
		t.Error("out-of-range spec accepted")
	}
}
