package user

import (
	"context"

	"github.com/ongridio/ongrid/internal/iam/model"
)

// Repo is the iam/user persistence contract. Implemented in
// internal/iam/data/user/sqlite.
type Repo interface {
	Create(ctx context.Context, u *model.User) error
	GetByEmail(ctx context.Context, email string) (*model.User, error)
	GetByID(ctx context.Context, id uint64) (*model.User, error)
	List(ctx context.Context) ([]*model.User, error)
	Count(ctx context.Context) (int64, error)
	Delete(ctx context.Context, id uint64) error
	UpdateRole(ctx context.Context, id uint64, role string) error
	// UpdateProfile sets display_name + phone. ErrNotFound on missing row.
	UpdateProfile(ctx context.Context, id uint64, displayName, phone string) error
	// UpdateStatus toggles active/disabled (= soft delete via status).
	UpdateStatus(ctx context.Context, id uint64, status string) error
	// UpdateSuperuser sets is_superuser. Reserved for migrations + the
	// superuser-promotes-superuser flow.
	UpdateSuperuser(ctx context.Context, id uint64, isSuperuser bool) error
	// UpdatePassHash sets a new argon2id hash. Used by self-service
	// password reset and admin reset.
	UpdatePassHash(ctx context.Context, id uint64, passHash string) error
}
