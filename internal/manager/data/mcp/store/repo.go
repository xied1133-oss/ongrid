// Package store is the GORM-backed persistence for external MCP server
// registrations (HLD-018). It is dumb storage — credential resolution and
// header-template expansion live in biz/mcp, never here.
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	model "github.com/ongridio/ongrid/internal/manager/model/mcp"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// Repo is the GORM-backed MCP server store. Concurrency-safe.
type Repo struct{ db *gorm.DB }

// NewRepo builds the repo around an opened *gorm.DB.
func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// Create inserts a new server registration. Name must be unique.
func (r *Repo) Create(ctx context.Context, s *model.Server) error {
	if s == nil || strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("%w: name required", errs.ErrInvalid)
	}
	if err := r.db.WithContext(ctx).Create(s).Error; err != nil {
		if isDup(err) {
			return fmt.Errorf("%w: mcp server %q already exists", errs.ErrConflict, s.Name)
		}
		return err
	}
	return nil
}

// Get returns one server by id.
func (r *Repo) Get(ctx context.Context, id uint64) (*model.Server, error) {
	var s model.Server
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&s).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrNotFound
		}
		return nil, err
	}
	return &s, nil
}

// GetByName returns one server by its unique name.
func (r *Repo) GetByName(ctx context.Context, name string) (*model.Server, error) {
	var s model.Server
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&s).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrNotFound
		}
		return nil, err
	}
	return &s, nil
}

// List returns every registered server ordered by name.
func (r *Repo) List(ctx context.Context) ([]*model.Server, error) {
	var out []*model.Server
	if err := r.db.WithContext(ctx).Order("name ASC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// Update writes the editable fields from patch by id. Status / LastError /
// ToolsCache are owned by the probe path (SetStatus / SetToolsCache), not by
// the generic edit.
func (r *Repo) Update(ctx context.Context, id uint64, patch *model.Server) error {
	if patch == nil {
		return fmt.Errorf("%w: nil patch", errs.ErrInvalid)
	}
	fields := map[string]any{
		"transport":            patch.Transport,
		"endpoint":             patch.Endpoint,
		"command":              patch.Command,
		"args_json":            patch.ArgsJSON,
		"credential":           patch.Credential,
		"header_template_json": patch.HeaderTemplateJSON,
		"trusted":              patch.Trusted,
		"enabled":              patch.Enabled,
	}
	res := r.db.WithContext(ctx).Model(&model.Server{}).Where("id = ?", id).Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// Delete removes a server by id.
func (r *Repo) Delete(ctx context.Context, id uint64) error {
	res := r.db.WithContext(ctx).Where("id = ?", id).Delete(&model.Server{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// SetStatus records the outcome of a connection probe.
func (r *Repo) SetStatus(ctx context.Context, id uint64, status, lastErr string) error {
	res := r.db.WithContext(ctx).Model(&model.Server{}).Where("id = ?", id).
		Updates(map[string]any{"status": status, "last_error": lastErr})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// SetToolsCache stores the JSON-encoded tools snapshot from a successful probe.
func (r *Repo) SetToolsCache(ctx context.Context, id uint64, toolsJSON string) error {
	res := r.db.WithContext(ctx).Model(&model.Server{}).Where("id = ?", id).
		Update("tools_cache_json", toolsJSON)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
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
