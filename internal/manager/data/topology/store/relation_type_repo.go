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

// RelationTypeRepo is the GORM-backed biz/topology.RelationTypeRepo.
type RelationTypeRepo struct{ db *gorm.DB }

// NewRelationTypeRepo constructs the repo.
func NewRelationTypeRepo(db *gorm.DB) *RelationTypeRepo { return &RelationTypeRepo{db: db} }

var _ biz.RelationTypeRepo = (*RelationTypeRepo)(nil)

// Upsert inserts a new RelationType or refreshes its mutable fields by
// primary key (name). Built-in seeding goes through migrate.go; this
// path is the operator-registration entry point.
func (r *RelationTypeRepo) Upsert(ctx context.Context, rt *model.RelationType) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"display_name", "display_name_en", "propagates_failure", "direction",
			"semantics_tag", "description", "updated_at",
		}),
	}).Create(rt).Error
}

func (r *RelationTypeRepo) Get(ctx context.Context, name string) (*model.RelationType, error) {
	var rt model.RelationType
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&rt).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrNotFound
		}
		return nil, err
	}
	return &rt, nil
}

func (r *RelationTypeRepo) List(ctx context.Context) ([]*model.RelationType, error) {
	var out []*model.RelationType
	if err := r.db.WithContext(ctx).Order("name ASC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (r *RelationTypeRepo) Delete(ctx context.Context, name string) error {
	res := r.db.WithContext(ctx).Where("name = ?", name).Delete(&model.RelationType{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// CountRelationsByType reports how many `relations` rows still
// reference the given type name. Used by Usecase.DeleteRelationType as
// a safety guard.
func (r *RelationTypeRepo) CountRelationsByType(ctx context.Context, name string) (int64, error) {
	var n int64
	if err := r.db.WithContext(ctx).Model(&model.Relation{}).Where("type = ?", name).Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}
