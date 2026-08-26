package store

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	biz "github.com/ongridio/ongrid/internal/manager/biz/aiops"
	model "github.com/ongridio/ongrid/internal/manager/model/aiops"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

var _ biz.OperationRepo = (*SessionRepo)(nil)

func (r *SessionRepo) CreateOperation(ctx context.Context, operation *model.Operation) error {
	if operation == nil {
		return errs.ErrInvalid
	}
	if err := r.db.WithContext(ctx).Create(operation).Error; err != nil {
		return fmt.Errorf("aiops operation: create: %w", err)
	}
	return nil
}

func (r *SessionRepo) GetOperation(ctx context.Context, id string) (*model.Operation, error) {
	var operation model.Operation
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&operation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrNotFound
		}
		return nil, fmt.Errorf("aiops operation: get: %w", err)
	}
	return &operation, nil
}

func (r *SessionRepo) UpdateOperation(ctx context.Context, id string, states []string, updates map[string]any) error {
	query := r.db.WithContext(ctx).Model(&model.Operation{}).Where("id = ?", id)
	if len(states) != 0 {
		query = query.Where("state IN ?", states)
	}
	res := query.Updates(updates)
	if res.Error != nil {
		return fmt.Errorf("aiops operation: update: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return errs.ErrConflict
	}
	return nil
}

func (r *SessionRepo) AppendOperationEvent(ctx context.Context, event *model.OperationEvent) (bool, error) {
	if event == nil {
		return false, errs.ErrInvalid
	}
	if err := r.db.WithContext(ctx).Create(event).Error; err != nil {
		if isDuplicate(err) {
			return false, nil
		}
		return false, fmt.Errorf("aiops operation: append event: %w", err)
	}
	return true, nil
}

func (r *SessionRepo) CreateOperationArtifact(ctx context.Context, artifact *model.OperationArtifact) error {
	if artifact == nil {
		return errs.ErrInvalid
	}
	if err := r.db.WithContext(ctx).Create(artifact).Error; err != nil {
		if isDuplicate(err) {
			return errs.ErrConflict
		}
		return fmt.Errorf("aiops operation: create artifact: %w", err)
	}
	return nil
}

func (r *SessionRepo) ListOperationArtifacts(ctx context.Context, operationID string) ([]*model.OperationArtifact, error) {
	var artifacts []*model.OperationArtifact
	if err := r.db.WithContext(ctx).Where("operation_id = ?", operationID).Order("created_at ASC").Find(&artifacts).Error; err != nil {
		return nil, fmt.Errorf("aiops operation: list artifacts: %w", err)
	}
	return artifacts, nil
}
