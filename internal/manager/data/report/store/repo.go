package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	bizreport "github.com/ongridio/ongrid/internal/manager/biz/report"
	model "github.com/ongridio/ongrid/internal/manager/model/report"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// Repo is the gorm-backed storage for report_schedules + reports.
// Implements bizreport.Repo.
type Repo struct {
	db *gorm.DB
}

func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// Compile-time check that Repo satisfies the biz interface.
var _ bizreport.Repo = (*Repo)(nil)

// CreateReport inserts a report row. A unique-index violation on
// (schedule_id, period_start) is translated to errs.ErrConflict so the
// Usecase can treat a duplicate scheduled fire as a no-op.
func (r *Repo) CreateReport(ctx context.Context, rpt *model.Report) error {
	if err := r.db.WithContext(ctx).Create(rpt).Error; err != nil {
		if isDuplicateKey(err) {
			return errs.ErrConflict
		}
		return err
	}
	return nil
}

func (r *Repo) GetReport(ctx context.Context, id string) (*model.Report, error) {
	var rpt model.Report
	if err := r.db.WithContext(ctx).First(&rpt, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrNotFound
		}
		return nil, err
	}
	return &rpt, nil
}

func (r *Repo) UpdateReport(ctx context.Context, rpt *model.Report) error {
	return r.db.WithContext(ctx).Save(rpt).Error
}

func (r *Repo) CreateSchedule(ctx context.Context, s *model.ReportSchedule) error {
	return r.db.WithContext(ctx).Create(s).Error
}

func (r *Repo) GetSchedule(ctx context.Context, id uint64) (*model.ReportSchedule, error) {
	var s model.ReportSchedule
	if err := r.db.WithContext(ctx).First(&s, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrNotFound
		}
		return nil, err
	}
	return &s, nil
}

func (r *Repo) UpdateSchedule(ctx context.Context, s *model.ReportSchedule) error {
	return r.db.WithContext(ctx).Save(s).Error
}

// DueSchedules returns enabled schedules whose next_fire_at is non-NULL
// and <= now. Ordered by next_fire_at so the most-overdue fire first.
func (r *Repo) DueSchedules(ctx context.Context, now time.Time) ([]*model.ReportSchedule, error) {
	var rows []*model.ReportSchedule
	err := r.db.WithContext(ctx).
		Where("enabled = ? AND next_fire_at IS NOT NULL AND next_fire_at <= ?", true, now).
		Order("next_fire_at ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// isDuplicateKey detects the MySQL ER_DUP_ENTRY (1062) and SQLite
// "UNIQUE constraint failed" markers. Mirrors the alert store helper —
// duplicated to avoid a cross-domain import.
func isDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "Error 1062") ||
		strings.Contains(s, "UNIQUE constraint failed") ||
		strings.Contains(s, "duplicate key")
}
