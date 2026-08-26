package store

import (
	"context"
	"errors"

	"gorm.io/gorm"

	bizreport "github.com/ongridio/ongrid/internal/manager/biz/report"
	model "github.com/ongridio/ongrid/internal/manager/model/report"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// Compile-time check that Repo satisfies the API read surface too.
var _ bizreport.ReadRepo = (*Repo)(nil)

// ListReports returns reports matching the filter, newest first.
func (r *Repo) ListReports(ctx context.Context, f bizreport.ReportFilter) ([]*model.Report, error) {
	q := r.reportListQuery(ctx, f)
	limit := f.Limit
	if limit <= 0 {
		limit = bizreport.DefaultListLimit
	}
	var rows []*model.Report
	err := q.Order("created_at DESC").Limit(limit).Offset(f.Offset).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *Repo) CountReports(ctx context.Context, f bizreport.ReportFilter) (int64, error) {
	var total int64
	if err := r.reportListQuery(ctx, f).Count(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func (r *Repo) reportListQuery(ctx context.Context, f bizreport.ReportFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.Report{})
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.Kind != "" {
		q = q.Where("kind = ?", f.Kind)
	}
	if f.ScheduleID != nil {
		q = q.Where("schedule_id = ?", *f.ScheduleID)
	}
	if f.TaskID != "" {
		q = q.Where("task_id = ?", f.TaskID)
	}
	return q
}

func (r *Repo) DeleteReport(ctx context.Context, id string) error {
	res := r.db.WithContext(ctx).Where("id = ?", id).Delete(&model.Report{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// ListSchedules returns schedules. When all is false only ownerID's
// rows are returned (non-admin scoping).
func (r *Repo) ListSchedules(ctx context.Context, ownerID uint64, all bool) ([]*model.ReportSchedule, error) {
	q := r.db.WithContext(ctx).Model(&model.ReportSchedule{})
	if !all {
		q = q.Where("created_by = ?", ownerID)
	}
	var rows []*model.ReportSchedule
	if err := q.Order("created_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *Repo) DeleteSchedule(ctx context.Context, id uint64) error {
	res := r.db.WithContext(ctx).Where("id = ?", id).Delete(&model.ReportSchedule{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// GetReportByShareToken resolves a report by its share token (TTL is
// enforced in the biz layer).
func (r *Repo) GetReportByShareToken(ctx context.Context, token string) (*model.Report, error) {
	var rpt model.Report
	err := r.db.WithContext(ctx).First(&rpt, "share_token = ?", token).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrNotFound
		}
		return nil, err
	}
	return &rpt, nil
}
