package store

import (
	"context"
	"errors"

	"gorm.io/gorm"

	model "github.com/ongridio/ongrid/internal/manager/model/aiops"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// UserAgentRepo is the persistence surface for user-defined personas
// (Phase 3). Backed by gorm; the same Repo wraps it alongside the
// session repo to share the connection pool.
type UserAgentRepo struct {
	db *gorm.DB
}

// NewUserAgentRepo wires the gorm DB.
func NewUserAgentRepo(db *gorm.DB) *UserAgentRepo {
	return &UserAgentRepo{db: db}
}

// List returns every user-defined persona in name-asc order. Used at
// boot to hydrate the chatruntime AgentRegistry and by the /v1/agents
// listing endpoint (merged with disk-loaded personas at the API
// layer).
func (r *UserAgentRepo) List(ctx context.Context) ([]*model.UserAgent, error) {
	var out []*model.UserAgent
	if err := r.db.WithContext(ctx).Order("name ASC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// GetByName fetches a single persona by name. Returns ErrNotFound when
// no row matches.
func (r *UserAgentRepo) GetByName(ctx context.Context, name string) (*model.UserAgent, error) {
	var ua model.UserAgent
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&ua).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrNotFound
		}
		return nil, err
	}
	return &ua, nil
}

// Create inserts a new user persona. The caller has already validated
// that the name doesn't collide with a disk-loaded one. Unique-index
// on `name` returns gorm's UniqueViolation when two users race the
// same name; the service layer maps that to ErrInvalid (or surfaces
// "name already exists").
func (r *UserAgentRepo) Create(ctx context.Context, ua *model.UserAgent) error {
	return r.db.WithContext(ctx).Create(ua).Error
}

// Update overwrites every editable column on the persona row. Returns
// ErrNotFound when no row matches the name.
func (r *UserAgentRepo) Update(ctx context.Context, name string, ua *model.UserAgent) error {
	res := r.db.WithContext(ctx).Model(&model.UserAgent{}).Where("name = ?", name).
		Updates(map[string]any{
			"description":           ua.Description,
			"when_to_use":           ua.WhenToUse,
			"system_prompt":         ua.SystemPrompt,
			"critical_reminder":     ua.CriticalReminder,
			"allowed_tools_json":    ua.AllowedToolsJSON,
			"disallowed_tools_json": ua.DisallowedToolsJSON,
			"permission_mode":       ua.PermissionMode,
			"model":                 ua.Model,
			"max_turns":             ua.MaxTurns,
			"updated_at":            ua.UpdatedAt,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// Delete removes the persona row by name. Returns ErrNotFound when no
// row matches. Sessions linked to a deleted agent (Session.AgentID)
// fall back to the global default at run time — see runtime.go::Handle.
func (r *UserAgentRepo) Delete(ctx context.Context, name string) error {
	res := r.db.WithContext(ctx).Where("name = ?", name).Delete(&model.UserAgent{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}
