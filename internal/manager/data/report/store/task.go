package store

import (
	"context"

	model "github.com/ongridio/ongrid/internal/manager/model/report"
)

// task.go — persistence for the unified-task spine (HLD-022 Phase 2). Only
// non-recurring (oneoff) tasks are stored; recurring tasks are a view over
// report_schedules, unioned at the biz layer.

// CreateTask inserts a task row.
func (r *Repo) CreateTask(ctx context.Context, t *model.Task) error {
	return r.db.WithContext(ctx).Create(t).Error
}

// GetTask returns one task by id (soft-deleted excluded by gorm).
func (r *Repo) GetTask(ctx context.Context, id string) (*model.Task, error) {
	var t model.Task
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&t).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// ListTasks returns stored (oneoff) tasks newest-first.
func (r *Repo) ListTasks(ctx context.Context) ([]*model.Task, error) {
	var rows []*model.Task
	err := r.db.WithContext(ctx).Order("created_at DESC").Find(&rows).Error
	return rows, err
}

// UpdateTaskStatus sets a task's status (mirrors the latest run outcome).
func (r *Repo) UpdateTaskStatus(ctx context.Context, id, status string) error {
	return r.db.WithContext(ctx).Model(&model.Task{}).Where("id = ?", id).Update("status", status).Error
}

// DeleteTask soft-deletes a task row.
func (r *Repo) DeleteTask(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&model.Task{}).Error
}
