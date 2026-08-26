package store

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	biz "github.com/ongridio/ongrid/internal/manager/biz/topology"
	model "github.com/ongridio/ongrid/internal/manager/model/topology"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// NodeTypeRepo is the GORM-backed biz/topology.NodeTypeRepo.
type NodeTypeRepo struct{ db *gorm.DB }

// NewNodeTypeRepo constructs the repo.
func NewNodeTypeRepo(db *gorm.DB) *NodeTypeRepo { return &NodeTypeRepo{db: db} }

var _ biz.NodeTypeRepo = (*NodeTypeRepo)(nil)

// Upsert inserts or refreshes mutable columns by primary key (name).
// Used by both builtin seeding (Migrate) and operator registration.
func (r *NodeTypeRepo) Upsert(ctx context.Context, nt *model.NodeType) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"display_name", "display_name_en", "tier", "description", "updated_at",
		}),
	}).Create(nt).Error
}

func (r *NodeTypeRepo) Get(ctx context.Context, name string) (*model.NodeType, error) {
	var nt model.NodeType
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&nt).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrNotFound
		}
		return nil, err
	}
	return &nt, nil
}

// List returns rows sorted by tier (top-down layer order), then by
// name within a tier for stable display.
func (r *NodeTypeRepo) List(ctx context.Context) ([]*model.NodeType, error) {
	var out []*model.NodeType
	if err := r.db.WithContext(ctx).Order("tier ASC, name ASC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (r *NodeTypeRepo) Delete(ctx context.Context, name string) error {
	res := r.db.WithContext(ctx).Where("name = ?", name).Delete(&model.NodeType{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// CountNodesByType reports how many `nodes` rows still reference the
// given type. Used by Usecase.DeleteNodeType as a safety guard.
func (r *NodeTypeRepo) CountNodesByType(ctx context.Context, name string) (int64, error) {
	var n int64
	if err := r.db.WithContext(ctx).Model(&model.Node{}).Where("type = ?", name).Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}
