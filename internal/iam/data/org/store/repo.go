package store

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/ongridio/ongrid/internal/iam/model"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// Repo is the GORM-backed org repository.
type Repo struct {
	db *gorm.DB
}

// NewRepo wraps a *gorm.DB.
func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// Create inserts the org and writes back ID + timestamps.
func (r *Repo) Create(ctx context.Context, o *model.Org) error {
	return r.db.WithContext(ctx).Create(o).Error
}

// GetByID returns the org or ErrNotFound.
func (r *Repo) GetByID(ctx context.Context, id uint64) (*model.Org, error) {
	var o model.Org
	if err := r.db.WithContext(ctx).First(&o, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrNotFound
		}
		return nil, err
	}
	return &o, nil
}

// GetByName returns the org or ErrNotFound. Used by the boot path to
// look up the seed "默认组织" without hardcoding an ID.
func (r *Repo) GetByName(ctx context.Context, name string) (*model.Org, error) {
	var o model.Org
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&o).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrNotFound
		}
		return nil, err
	}
	return &o, nil
}

// List returns every org ordered by id asc. Caller is expected to be
// a superuser; non-superusers should call MembershipRepo.ListByUser
// (which joins through to orgs).
func (r *Repo) List(ctx context.Context) ([]*model.Org, error) {
	var out []*model.Org
	if err := r.db.WithContext(ctx).Order("id asc").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// Update overwrites name + description + parent_id on id. ErrNotFound
// when no row. parent_id == nil → set to NULL (root org); pointing to
// a non-existent or self-referential id is the biz layer's concern.
func (r *Repo) Update(ctx context.Context, id uint64, name, description string, parentID *uint64) error {
	res := r.db.WithContext(ctx).Model(&model.Org{}).Where("id = ?", id).Updates(map[string]any{
		"name":        name,
		"description": description,
		"parent_id":   parentID,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// CountChildren returns how many orgs have ParentID == id. Biz layer
// uses this to refuse delete on non-empty parents ().
func (r *Repo) CountChildren(ctx context.Context, id uint64) (int64, error) {
	var n int64
	if err := r.db.WithContext(ctx).Model(&model.Org{}).Where("parent_id = ?", id).Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// Delete soft-removes by id (we don't add gorm.DeletedAt today; this
// is a hard delete). Memberships pointing to this org are NOT deleted
// here — that's the biz layer's responsibility because it also has to
// strip casbin g policies.
func (r *Repo) Delete(ctx context.Context, id uint64) error {
	res := r.db.WithContext(ctx).Delete(&model.Org{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// Count returns how many org rows exist.
func (r *Repo) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.WithContext(ctx).Model(&model.Org{}).Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}
