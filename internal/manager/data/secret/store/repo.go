// Package store is the GORM-backed persistence for the credential vault.
// The repo is dumb storage — it never sees plaintext: Data is already the
// sealed blob by the time it arrives (biz/secret does encrypt/decrypt).
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	model "github.com/ongridio/ongrid/internal/manager/model/secret"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// Repo is the GORM-backed credentials store. Concurrency-safe.
type Repo struct{ db *gorm.DB }

// NewRepo builds the repo around an opened *gorm.DB.
func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// Create inserts a new credential. Name must be unique.
func (r *Repo) Create(ctx context.Context, s *model.Secret) error {
	if s == nil || strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("%w: name required", errs.ErrInvalid)
	}
	if err := r.db.WithContext(ctx).Create(s).Error; err != nil {
		if isDup(err) {
			return fmt.Errorf("%w: credential %q already exists", errs.ErrConflict, s.Name)
		}
		return err
	}
	return nil
}

// Update sets the sealed Data (when non-empty) and/or description by id.
// An empty data leaves the stored blob untouched (edit description only).
func (r *Repo) Update(ctx context.Context, id uint64, data, description string) error {
	fields := map[string]any{"description": description}
	if data != "" {
		fields["value"] = data
	}
	res := r.db.WithContext(ctx).Model(&model.Secret{}).Where("id = ?", id).Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// Delete removes a credential by id.
func (r *Repo) Delete(ctx context.Context, id uint64) error {
	res := r.db.WithContext(ctx).Where("id = ?", id).Delete(&model.Secret{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// List returns every credential (sealed Data included — biz redacts before
// any API response).
func (r *Repo) List(ctx context.Context) ([]*model.Secret, error) {
	var out []*model.Secret
	if err := r.db.WithContext(ctx).Order("name ASC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// GetByName returns one credential by name (sealed) — the injection path
// fetches by the bound name, then biz decrypts.
func (r *Repo) GetByName(ctx context.Context, name string) (*model.Secret, error) {
	var s model.Secret
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&s).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrNotFound
		}
		return nil, err
	}
	return &s, nil
}

func isDup(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	m := err.Error()
	return strings.Contains(m, "UNIQUE") || strings.Contains(m, "Duplicate") || strings.Contains(m, "duplicate")
}
