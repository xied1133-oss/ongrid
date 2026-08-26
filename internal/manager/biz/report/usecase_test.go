package report

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/report"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// fakeRepo is an in-memory Repo. CreateReport enforces the
// (schedule_id, period_start) unique constraint so dedup behaviour can
// be exercised without a real DB.
type fakeRepo struct {
	mu         sync.Mutex
	reports    map[string]*model.Report
	schedules  map[uint64]*model.ReportSchedule
	seenWindow map[string]bool // "schedID|periodStart" → exists

	failCreate error
	failUpdate error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		reports:    map[string]*model.Report{},
		schedules:  map[uint64]*model.ReportSchedule{},
		seenWindow: map[string]bool{},
	}
}

func windowKey(schedID *uint64, start time.Time) string {
	if schedID == nil {
		return "" // manual reports never dedup
	}
	return strconv.FormatUint(*schedID, 10) + "|" + start.UTC().Format(time.RFC3339)
}

func (r *fakeRepo) CreateReport(_ context.Context, rpt *model.Report) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failCreate != nil {
		return r.failCreate
	}
	if rpt.ScheduleID != nil {
		k := windowKey(rpt.ScheduleID, rpt.PeriodStart)
		if r.seenWindow[k] {
			return errs.ErrConflict
		}
		r.seenWindow[k] = true
	}
	cp := *rpt
	r.reports[rpt.ID] = &cp
	return nil
}

func (r *fakeRepo) GetReport(_ context.Context, id string) (*model.Report, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rpt, ok := r.reports[id]
	if !ok {
		return nil, errs.ErrNotFound
	}
	return rpt, nil
}

func (r *fakeRepo) UpdateReport(_ context.Context, rpt *model.Report) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reports[rpt.ID] = rpt
	return nil
}

func (r *fakeRepo) CreateSchedule(_ context.Context, s *model.ReportSchedule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.schedules[s.ID] = s
	return nil
}

func (r *fakeRepo) GetSchedule(_ context.Context, id uint64) (*model.ReportSchedule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.schedules[id]
	if !ok {
		return nil, errs.ErrNotFound
	}
	return s, nil
}

func (r *fakeRepo) UpdateSchedule(_ context.Context, s *model.ReportSchedule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failUpdate != nil {
		return r.failUpdate
	}
	r.schedules[s.ID] = s
	return nil
}

func (r *fakeRepo) DueSchedules(_ context.Context, now time.Time) ([]*model.ReportSchedule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*model.ReportSchedule
	for _, s := range r.schedules {
		if s.Enabled && s.NextFireAt != nil && !s.NextFireAt.After(now) {
			out = append(out, s)
		}
	}
	return out, nil
}

// seqIDGen returns deterministic ids r1, r2, ...
func seqIDGen() IDGen {
	var n int
	var mu sync.Mutex
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		n++
		return "r" + strconv.Itoa(n)
	}
}

func weeklySchedule() *model.ReportSchedule {
	return &model.ReportSchedule{
		ID:        1,
		CreatedBy: 42,
		Kind:      model.KindWeekly,
		CronSpec:  "0 9 * * 1",
		Timezone:  "Asia/Shanghai",
		ScopeJSON: "{}",
		Enabled:   true,
	}
}

func TestFireSchedule_CreatesPendingAndRearms(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, nopGenerator{}, seqIDGen())
	s := weeklySchedule()
	_ = repo.CreateSchedule(context.Background(), s)

	loc := mustLoc(t, "Asia/Shanghai")
	fire := time.Date(2026, 6, 8, 9, 0, 0, 0, loc)
	next := fire.AddDate(0, 0, 7)

	rpt, err := uc.FireSchedule(context.Background(), s, fire, next)
	if err != nil {
		t.Fatal(err)
	}
	if rpt == nil || rpt.Status != model.StatusPending {
		t.Fatalf("want pending report, got %+v", rpt)
	}
	if rpt.ScheduleID == nil || *rpt.ScheduleID != 1 {
		t.Errorf("schedule_id not stamped: %+v", rpt.ScheduleID)
	}
	if rpt.CreatedBy != 42 {
		t.Errorf("created_by = %d, want 42", rpt.CreatedBy)
	}
	// Re-arm
	if s.NextFireAt == nil || !s.NextFireAt.Equal(next) {
		t.Errorf("next_fire_at not advanced: %+v", s.NextFireAt)
	}
	if s.LastFireAt == nil || !s.LastFireAt.Equal(fire) {
		t.Errorf("last_fire_at not set: %+v", s.LastFireAt)
	}
	if s.LastReportID == nil || *s.LastReportID != rpt.ID {
		t.Errorf("last_report_id not set")
	}
}

func TestFireSchedule_DuplicateWindowIsNoOp(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, nopGenerator{}, seqIDGen())
	s := weeklySchedule()
	_ = repo.CreateSchedule(context.Background(), s)

	loc := mustLoc(t, "Asia/Shanghai")
	fire := time.Date(2026, 6, 8, 9, 0, 0, 0, loc)
	next := fire.AddDate(0, 0, 7)

	// First fire creates the report.
	if _, err := uc.FireSchedule(context.Background(), s, fire, next); err != nil {
		t.Fatal(err)
	}
	// Second fire for the SAME window (same period_start) — must no-op,
	// not error, and still re-arm.
	s.LastFireAt = nil // reset so PeriodFor recomputes the same window
	rpt, err := uc.FireSchedule(context.Background(), s, fire, next)
	if err != nil {
		t.Fatalf("duplicate window should be no-op, got err %v", err)
	}
	if rpt != nil {
		t.Errorf("duplicate window should return nil report, got %+v", rpt)
	}
	if len(repo.reports) != 1 {
		t.Errorf("expected exactly 1 report after duplicate fire, got %d", len(repo.reports))
	}
	if s.NextFireAt == nil || !s.NextFireAt.Equal(next) {
		t.Errorf("schedule should still re-arm on duplicate window")
	}
}

func TestFireSchedule_RearmFailureSurfaces(t *testing.T) {
	repo := newFakeRepo()
	repo.failUpdate = errs.ErrNotWiredYet
	uc := NewUsecase(repo, nopGenerator{}, seqIDGen())
	s := weeklySchedule()

	loc := mustLoc(t, "Asia/Shanghai")
	fire := time.Date(2026, 6, 8, 9, 0, 0, 0, loc)
	if _, err := uc.FireSchedule(context.Background(), s, fire, fire.AddDate(0, 0, 7)); err == nil {
		t.Error("expected re-arm failure to surface")
	}
}

func TestGenerateNow_NoScheduleNoDedup(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, nopGenerator{}, seqIDGen())
	loc := mustLoc(t, "Asia/Shanghai")
	p := Period{
		Start: time.Date(2026, 6, 1, 0, 0, 0, 0, loc),
		End:   time.Date(2026, 6, 8, 0, 0, 0, 0, loc),
	}
	// Two manual generations for the same window both succeed (no dedup).
	r1, err := uc.GenerateNow(context.Background(), 42, model.KindWeekly, "Asia/Shanghai", "{}", "zh", "", p)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := uc.GenerateNow(context.Background(), 42, model.KindWeekly, "Asia/Shanghai", "{}", "zh", "", p)
	if err != nil {
		t.Fatal(err)
	}
	if r1.ID == r2.ID {
		t.Error("manual reports should get distinct ids")
	}
	if r1.ScheduleID != nil || r2.ScheduleID != nil {
		t.Error("manual reports must have nil schedule_id")
	}
	if len(repo.reports) != 2 {
		t.Errorf("expected 2 manual reports, got %d", len(repo.reports))
	}
}

func TestGenerateNow_GeneratorUnavailableDoesNotCreateReport(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, NewUnavailableGenerator("LLM provider not configured"), seqIDGen())
	loc := mustLoc(t, "Asia/Shanghai")
	p := Period{
		Start: time.Date(2026, 6, 1, 0, 0, 0, 0, loc),
		End:   time.Date(2026, 6, 8, 0, 0, 0, 0, loc),
	}

	rpt, err := uc.GenerateNow(context.Background(), 42, model.KindWeekly, "Asia/Shanghai", "{}", "zh", "", p)
	if !errors.Is(err, errs.ErrNotWiredYet) {
		t.Fatalf("GenerateNow err = %v, want ErrNotWiredYet", err)
	}
	if rpt != nil {
		t.Fatalf("GenerateNow report = %+v, want nil", rpt)
	}
	if len(repo.reports) != 0 {
		t.Fatalf("unavailable generator should not create report rows, got %d", len(repo.reports))
	}
}

func TestNewUsecase_NilGuards(t *testing.T) {
	assertPanics(t, "nil repo", func() { NewUsecase(nil, nopGenerator{}, seqIDGen()) })
	assertPanics(t, "nil idgen", func() { NewUsecase(newFakeRepo(), nopGenerator{}, nil) })
	// nil generator is allowed (falls back to no-op).
	if uc := NewUsecase(newFakeRepo(), nil, seqIDGen()); uc == nil {
		t.Error("nil generator should be allowed")
	}
}

func assertPanics(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: expected panic", name)
		}
	}()
	fn()
}
