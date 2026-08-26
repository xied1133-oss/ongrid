package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	biz "github.com/ongridio/ongrid/internal/manager/biz/edge"
	model "github.com/ongridio/ongrid/internal/manager/model/edge"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

var _ biz.UpgradeJobRepo = (*Repo)(nil)
var _ biz.UpgradeEdgeReader = (*Repo)(nil)

func (r *Repo) CreateUpgradeJob(ctx context.Context, job *model.UpgradeJob, items []*model.UpgradeJobItem) error {
	if job == nil || len(items) == 0 {
		return errs.ErrInvalid
	}
	job.Total = len(items)
	assignUpgradeBatches(job, items)
	job.Succeeded, job.Failed, job.Skipped, job.Pending = summarizeUpgradeItems(items)
	job.Status = model.UpgradeJobStatusQueued
	if job.Pending == 0 {
		job.Status = terminalUpgradeJobStatus(job.Succeeded, job.Failed)
		now := time.Now()
		job.FinishedAt = &now
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(job).Error; err != nil {
			return err
		}
		for _, item := range items {
			item.JobID = job.ID
		}
		return tx.Create(&items).Error
	})
}

func assignUpgradeBatches(job *model.UpgradeJob, items []*model.UpgradeJobItem) {
	if job.BatchSize <= 0 {
		job.BatchSize = model.DefaultUpgradeJobBatchSize
	}
	job.CurrentBatch = 0
	job.TotalBatches = 0
	eligible := 0
	for _, item := range items {
		if item.Status != model.UpgradeJobItemStatusQueued {
			item.BatchNumber = 0
			continue
		}
		eligible++
		item.BatchNumber = (eligible-1)/job.BatchSize + 1
		job.TotalBatches = item.BatchNumber
	}
}

func (r *Repo) GetUpgradeJob(ctx context.Context, id uint64) (*model.UpgradeJob, []*model.UpgradeJobItem, error) {
	var job model.UpgradeJob
	if err := r.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", id).First(&job).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, errs.ErrNotFound
		}
		return nil, nil, err
	}
	var items []*model.UpgradeJobItem
	if err := r.db.WithContext(ctx).Where("job_id = ?", id).Order("id ASC").Find(&items).Error; err != nil {
		return nil, nil, err
	}
	return &job, items, nil
}

func (r *Repo) ListUpgradeJobs(ctx context.Context, filter biz.UpgradeJobListFilter) ([]*model.UpgradeJob, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.UpgradeJob{}).Where("deleted_at IS NULL")
	if filter.ClusterNodeID != nil {
		q = q.Where("cluster_node_id = ?", *filter.ClusterNodeID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if filter.Limit > 0 {
		q = q.Limit(filter.Limit)
	}
	if filter.Offset > 0 {
		q = q.Offset(filter.Offset)
	}
	var jobs []*model.UpgradeJob
	if err := q.Order("id DESC").Find(&jobs).Error; err != nil {
		return nil, 0, err
	}
	return jobs, total, nil
}

func (r *Repo) ClaimNextUpgradeJob(ctx context.Context, now time.Time) (*model.UpgradeJob, error) {
	var claimed model.UpgradeJob
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("status = ? AND deleted_at IS NULL", model.UpgradeJobStatusQueued).
			Order("id ASC").First(&claimed).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errs.ErrNotFound
			}
			return err
		}
		updates := map[string]any{
			"status":     model.UpgradeJobStatusRunning,
			"updated_at": now,
		}
		if claimed.StartedAt == nil {
			updates["started_at"] = now
			claimed.StartedAt = &now
		}
		res := tx.Model(&model.UpgradeJob{}).
			Where("id = ? AND status = ?", claimed.ID, model.UpgradeJobStatusQueued).
			Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return errs.ErrConflict
		}
		claimed.Status = model.UpgradeJobStatusRunning
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &claimed, nil
}

func (r *Repo) SetUpgradeJobCurrentBatch(ctx context.Context, jobID uint64, batchNumber int, now time.Time) error {
	if batchNumber <= 0 {
		return errs.ErrInvalid
	}
	res := r.db.WithContext(ctx).Model(&model.UpgradeJob{}).
		Where("id = ? AND status = ? AND deleted_at IS NULL AND total_batches >= ?", jobID, model.UpgradeJobStatusRunning, batchNumber).
		Updates(map[string]any{"current_batch": batchNumber, "updated_at": now})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return errs.ErrConflict
	}
	return nil
}

func (r *Repo) ListUpgradeJobItems(ctx context.Context, jobID uint64, statuses ...string) ([]*model.UpgradeJobItem, error) {
	q := r.db.WithContext(ctx).Where("job_id = ?", jobID)
	if len(statuses) > 0 {
		q = q.Where("status IN ?", statuses)
	}
	var items []*model.UpgradeJobItem
	if err := q.Order("id ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func (r *Repo) MarkUpgradeItemDispatching(ctx context.Context, itemID uint64, baselineRegisteredAt *time.Time, now time.Time) error {
	res := r.db.WithContext(ctx).Model(&model.UpgradeJobItem{}).
		Where("id = ? AND status = ?", itemID, model.UpgradeJobItemStatusQueued).
		Updates(map[string]any{
			"status":                   model.UpgradeJobItemStatusDispatching,
			"attempt":                  gorm.Expr("attempt + 1"),
			"error_code":               "",
			"error_message":            "",
			"baseline_registered_at":   baselineRegisteredAt,
			"verification_deadline_at": nil,
			"started_at":               now,
			"finished_at":              nil,
			"updated_at":               now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return errs.ErrConflict
	}
	return nil
}

func (r *Repo) RefreshUpgradeItemBaseline(ctx context.Context, itemID uint64, baselineRegisteredAt time.Time) error {
	return r.updateUpgradeItem(ctx, itemID, model.UpgradeJobItemStatusDispatching, map[string]any{
		"baseline_registered_at": baselineRegisteredAt,
		"updated_at":             baselineRegisteredAt,
	})
}

func (r *Repo) MarkUpgradeItemWaiting(ctx context.Context, itemID uint64, deadline time.Time) error {
	return r.updateUpgradeItem(ctx, itemID, model.UpgradeJobItemStatusDispatching, map[string]any{
		"status":                   model.UpgradeJobItemStatusWaitingRegistration,
		"verification_deadline_at": deadline,
		"updated_at":               time.Now(),
	})
}

func (r *Repo) MarkUpgradeItemSucceeded(ctx context.Context, itemID uint64, observedVersion string, observedRegisteredAt *time.Time, now time.Time) error {
	return r.updateUpgradeItem(ctx, itemID, model.UpgradeJobItemStatusWaitingRegistration, map[string]any{
		"status":                   model.UpgradeJobItemStatusSucceeded,
		"observed_version":         observedVersion,
		"observed_registered_at":   observedRegisteredAt,
		"verification_deadline_at": nil,
		"finished_at":              now,
		"updated_at":               now,
	})
}

func (r *Repo) MarkUpgradeItemFailed(ctx context.Context, itemID uint64, status, code, message, observedVersion string, observedRegisteredAt *time.Time, now time.Time) error {
	if status != model.UpgradeJobItemStatusFailed && status != model.UpgradeJobItemStatusTimedOut {
		return errs.ErrInvalid
	}
	res := r.db.WithContext(ctx).Model(&model.UpgradeJobItem{}).
		Where("id = ? AND status IN ?", itemID, []string{
			model.UpgradeJobItemStatusQueued,
			model.UpgradeJobItemStatusDispatching,
			model.UpgradeJobItemStatusWaitingRegistration,
		}).Updates(map[string]any{
		"status":                   status,
		"error_code":               code,
		"error_message":            message,
		"observed_version":         observedVersion,
		"observed_registered_at":   observedRegisteredAt,
		"verification_deadline_at": nil,
		"finished_at":              now,
		"updated_at":               now,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return errs.ErrConflict
	}
	return nil
}

func (r *Repo) RefreshUpgradeJob(ctx context.Context, jobID uint64, now time.Time) (*model.UpgradeJob, error) {
	var refreshed model.UpgradeJob
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND deleted_at IS NULL", jobID).First(&refreshed).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errs.ErrNotFound
			}
			return err
		}
		var items []*model.UpgradeJobItem
		if err := tx.Select("status").Where("job_id = ?", jobID).Find(&items).Error; err != nil {
			return err
		}
		succeeded, failed, skipped, pending := summarizeUpgradeItems(items)
		status := model.UpgradeJobStatusRunning
		if refreshed.Status == model.UpgradeJobStatusQueued {
			status = model.UpgradeJobStatusQueued
		}
		var finishedAt *time.Time
		if pending == 0 {
			status = terminalUpgradeJobStatus(succeeded, failed)
			finishedAt = &now
		}
		if err := tx.Model(&model.UpgradeJob{}).Where("id = ?", jobID).Updates(map[string]any{
			"total":       len(items),
			"succeeded":   succeeded,
			"failed":      failed,
			"skipped":     skipped,
			"pending":     pending,
			"status":      status,
			"finished_at": finishedAt,
			"updated_at":  now,
		}).Error; err != nil {
			return err
		}
		refreshed.Total = len(items)
		refreshed.Succeeded = succeeded
		refreshed.Failed = failed
		refreshed.Skipped = skipped
		refreshed.Pending = pending
		refreshed.Status = status
		refreshed.FinishedAt = finishedAt
		refreshed.UpdatedAt = now
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &refreshed, nil
}

func (r *Repo) RequeueUpgradeJob(ctx context.Context, jobID uint64, now time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.UpgradeJobItem{}).
			Where("job_id = ? AND status = ?", jobID, model.UpgradeJobItemStatusDispatching).
			Updates(map[string]any{
				"status":                   model.UpgradeJobItemStatusQueued,
				"verification_deadline_at": nil,
				"updated_at":               now,
			}).Error; err != nil {
			return err
		}
		res := tx.Model(&model.UpgradeJob{}).
			Where("id = ? AND status = ? AND deleted_at IS NULL", jobID, model.UpgradeJobStatusRunning).
			Updates(map[string]any{"status": model.UpgradeJobStatusQueued, "updated_at": now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return errs.ErrConflict
		}
		return nil
	})
}

func (r *Repo) RecoverUpgradeJobs(ctx context.Context) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.UpgradeJobItem{}).
			Where("status = ?", model.UpgradeJobItemStatusDispatching).
			Updates(map[string]any{
				"status":                   model.UpgradeJobItemStatusQueued,
				"verification_deadline_at": nil,
				"updated_at":               time.Now(),
			}).Error; err != nil {
			return err
		}
		return tx.Model(&model.UpgradeJob{}).
			Where("status = ? AND deleted_at IS NULL", model.UpgradeJobStatusRunning).
			Updates(map[string]any{"status": model.UpgradeJobStatusQueued, "updated_at": time.Now()}).Error
	})
}

func (r *Repo) RetryUpgradeJob(ctx context.Context, jobID uint64, snapshots []biz.UpgradeRetrySnapshot, now time.Time) (*model.UpgradeJob, error) {
	if len(snapshots) == 0 {
		return nil, errs.ErrConflict
	}
	byEdge := make(map[uint64]biz.UpgradeRetrySnapshot, len(snapshots))
	for _, snapshot := range snapshots {
		byEdge[snapshot.EdgeID] = snapshot
	}
	var job model.UpgradeJob
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND deleted_at IS NULL", jobID).First(&job).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errs.ErrNotFound
			}
			return err
		}
		if job.Status == model.UpgradeJobStatusQueued || job.Status == model.UpgradeJobStatusRunning {
			return errs.ErrConflict
		}
		var items []*model.UpgradeJobItem
		if err := tx.Where("job_id = ? AND status IN ?", jobID, []string{
			model.UpgradeJobItemStatusFailed,
			model.UpgradeJobItemStatusTimedOut,
		}).Order("id ASC").Find(&items).Error; err != nil {
			return err
		}
		reset := 0
		for _, item := range items {
			snapshot, ok := byEdge[item.EdgeID]
			if !ok {
				continue
			}
			reset++
			batchSize := job.BatchSize
			if batchSize <= 0 {
				batchSize = model.DefaultUpgradeJobBatchSize
			}
			if err := tx.Model(&model.UpgradeJobItem{}).Where("id = ?", item.ID).Updates(map[string]any{
				"status":                   model.UpgradeJobItemStatusQueued,
				"batch_number":             (reset-1)/batchSize + 1,
				"from_version":             snapshot.FromVersion,
				"baseline_registered_at":   snapshot.BaselineRegisteredAt,
				"observed_version":         "",
				"observed_registered_at":   nil,
				"verification_deadline_at": nil,
				"error_code":               "",
				"error_message":            "",
				"started_at":               nil,
				"finished_at":              nil,
				"updated_at":               now,
			}).Error; err != nil {
				return err
			}
		}
		if reset == 0 {
			return errs.ErrConflict
		}
		batchSize := job.BatchSize
		if batchSize <= 0 {
			batchSize = model.DefaultUpgradeJobBatchSize
		}
		return tx.Model(&model.UpgradeJob{}).Where("id = ?", jobID).Updates(map[string]any{
			"status":        model.UpgradeJobStatusQueued,
			"batch_size":    batchSize,
			"current_batch": 0,
			"total_batches": (reset + batchSize - 1) / batchSize,
			"finished_at":   nil,
			"updated_at":    now,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return r.RefreshUpgradeJob(ctx, jobID, now)
}

func (r *Repo) CountActiveUpgradeJobsForCluster(ctx context.Context, clusterNodeID uint64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.UpgradeJob{}).
		Where("cluster_node_id = ? AND deleted_at IS NULL AND status IN ?", clusterNodeID, []string{
			model.UpgradeJobStatusQueued,
			model.UpgradeJobStatusRunning,
		}).
		Count(&count).Error
	return count, err
}

func (r *Repo) DeleteFinishedUpgradeJobsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	var ids []uint64
	if err := r.db.WithContext(ctx).Model(&model.UpgradeJob{}).
		Where("deleted_at IS NULL AND finished_at IS NOT NULL AND finished_at < ?", cutoff).
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("job_id IN ?", ids).Delete(&model.UpgradeJobItem{}).Error; err != nil {
			return err
		}
		return tx.Where("id IN ?", ids).Delete(&model.UpgradeJob{}).Error
	})
	if err != nil {
		return 0, err
	}
	return int64(len(ids)), nil
}

func (r *Repo) updateUpgradeItem(ctx context.Context, itemID uint64, expectedStatus string, updates map[string]any) error {
	res := r.db.WithContext(ctx).Model(&model.UpgradeJobItem{}).
		Where("id = ? AND status = ?", itemID, expectedStatus).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return errs.ErrConflict
	}
	return nil
}

func summarizeUpgradeItems(items []*model.UpgradeJobItem) (succeeded, failed, skipped, pending int) {
	for _, item := range items {
		switch item.Status {
		case model.UpgradeJobItemStatusSucceeded:
			succeeded++
		case model.UpgradeJobItemStatusFailed, model.UpgradeJobItemStatusTimedOut:
			failed++
		case model.UpgradeJobItemStatusSkipped:
			skipped++
		default:
			pending++
		}
	}
	return succeeded, failed, skipped, pending
}

func terminalUpgradeJobStatus(succeeded, failed int) string {
	if failed == 0 {
		return model.UpgradeJobStatusSucceeded
	}
	if succeeded == 0 {
		return model.UpgradeJobStatusFailed
	}
	return model.UpgradeJobStatusPartialFailed
}
