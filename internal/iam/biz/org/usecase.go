// Package org is the iam Org usecase. CRUD on flat orgs (1 level) plus
// the seed of the boot "默认组织" so non-superuser flows always have
// somewhere to attach.
//
// Permission checks live in the manager server middleware (calls into
// authz.Enforcer); this package just guards inputs and persists.
package org

import (
	"context"
	"fmt"
	"strings"

	"github.com/ongridio/ongrid/internal/iam/model"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// defaultSeedName is the canonical name of the platform's single
// top-level org. Service.Create auto-reparents new orgs under it when
// no explicit parent is supplied; cmd/ongrid/main.go calls EnsureSeed
// with this name on boot. Kept as a const so the value can't drift
// between the seed-side and the auto-reparent-side.
const defaultSeedName = "默认组织"

// Repo is the narrow data surface. *iam/data/org/store.Repo satisfies it.
type Repo interface {
	Create(ctx context.Context, o *model.Org) error
	GetByID(ctx context.Context, id uint64) (*model.Org, error)
	GetByName(ctx context.Context, name string) (*model.Org, error)
	List(ctx context.Context) ([]*model.Org, error)
	Update(ctx context.Context, id uint64, name, description string, parentID *uint64) error
	Delete(ctx context.Context, id uint64) error
	Count(ctx context.Context) (int64, error)
	CountChildren(ctx context.Context, id uint64) (int64, error)
}

// MembershipCleaner is the narrow contract used to clean memberships
// when an org is deleted.
type MembershipCleaner interface {
	DeleteByOrg(ctx context.Context, orgID uint64) error
}

// CasbinHook is the narrow authz contract — strip every g rule for
// the org. Optional (nil = skip).
type CasbinHook interface {
	RevokeAllForOrg(ctx context.Context, orgID uint64) error
}

// Service is the public usecase.
type Service struct {
	repo        Repo
	memberships MembershipCleaner
	authz       CasbinHook
}

// New wires the service.
func New(repo Repo, memberships MembershipCleaner, authz CasbinHook) *Service {
	return &Service{repo: repo, memberships: memberships, authz: authz}
}

// CreateInput is the form-shaped create input.
type CreateInput struct {
	Name        string
	Description string
	// ParentID nil → 顶级组织。非 nil 时必须指向已存在的 org。
	ParentID    *uint64
}

// Create validates + persists.
//
// Top-level uniqueness invariant: the seed org ("默认组织") is the ONE
// AND ONLY top-level org. Any new org without an explicit parent is
// auto-reparented under the seed so the org tree always has a single
// root. Without this, the UI's "create org" form which omits parent
// produced rogue top-level orgs (observed in May 2026 — ongridio
// appeared as a sibling of 默认组织 instead of a child).
func (s *Service) Create(ctx context.Context, in CreateInput) (*model.Org, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: name required", errs.ErrInvalid)
	}
	if len(name) > 128 {
		return nil, fmt.Errorf("%w: name too long (max 128)", errs.ErrInvalid)
	}
	parent := in.ParentID
	if parent != nil {
		if _, err := s.repo.GetByID(ctx, *parent); err != nil {
			return nil, fmt.Errorf("%w: parent org not found", errs.ErrInvalid)
		}
	} else {
		// No parent supplied → pin under the seed org (the platform
		// root). Locating the seed by GetByName avoids hard-coding the
		// id, which can drift across test fixtures.
		seed, serr := s.repo.GetByName(ctx, defaultSeedName)
		if serr == nil && seed != nil {
			seedID := seed.ID
			parent = &seedID
		}
		// If the seed itself doesn't exist yet (first-ever Create
		// before EnsureSeed has been called — only happens in tests
		// or a fresh DB without the boot path), allow top-level so
		// the seed can be created. EnsureSeed is the one legitimate
		// top-level Create caller.
	}
	o := &model.Org{
		Name:        name,
		Description: strings.TrimSpace(in.Description),
		ParentID:    parent,
	}
	if err := s.repo.Create(ctx, o); err != nil {
		return nil, err
	}
	return o, nil
}

// EnsureSeed creates "默认组织" if it doesn't exist; returns either the
// existing or the freshly-created row. Idempotent.
func (s *Service) EnsureSeed(ctx context.Context, name, description string) (*model.Org, error) {
	if existing, err := s.repo.GetByName(ctx, name); err == nil {
		return existing, nil
	}
	o := &model.Org{Name: name, Description: description}
	if err := s.repo.Create(ctx, o); err != nil {
		// race-safe re-fetch
		if again, err2 := s.repo.GetByName(ctx, name); err2 == nil {
			return again, nil
		}
		return nil, err
	}
	return o, nil
}

// Get returns by id.
func (s *Service) Get(ctx context.Context, id uint64) (*model.Org, error) {
	return s.repo.GetByID(ctx, id)
}

// List returns every org.
func (s *Service) List(ctx context.Context) ([]*model.Org, error) {
	return s.repo.List(ctx)
}

// UpdateInput captures editable fields. ParentID 语义：
//   - SetParent=false → 不动 parent_id 列
//   - SetParent=true + ParentID=nil → 提升为顶级组织
//   - SetParent=true + ParentID=&X → 移动到 X 之下（X 不能是当前 org 或其后代）
type UpdateInput struct {
	Name        string
	Description string
	SetParent   bool
	ParentID    *uint64
}

// Update validates + persists.
func (s *Service) Update(ctx context.Context, id uint64, in UpdateInput) (*model.Org, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: name required", errs.ErrInvalid)
	}
	current, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	parent := current.ParentID
	if in.SetParent {
		if in.ParentID != nil {
			if *in.ParentID == id {
				return nil, fmt.Errorf("%w: org cannot be its own parent", errs.ErrInvalid)
			}
			if _, err := s.repo.GetByID(ctx, *in.ParentID); err != nil {
				return nil, fmt.Errorf("%w: parent org not found", errs.ErrInvalid)
			}
			// Cycle check: walk the candidate parent's ancestors back to
			// root; if id appears anywhere, the move would create a loop.
			cur := *in.ParentID
			for hop := 0; hop < 1024; hop++ {
				ancestor, err := s.repo.GetByID(ctx, cur)
				if err != nil {
					break
				}
				if ancestor.ID == id {
					return nil, fmt.Errorf("%w: cycle detected; cannot reparent under a descendant", errs.ErrInvalid)
				}
				if ancestor.ParentID == nil {
					break
				}
				cur = *ancestor.ParentID
			}
		}
		parent = in.ParentID
	}
	if err := s.repo.Update(ctx, id, name, strings.TrimSpace(in.Description), parent); err != nil {
		return nil, err
	}
	return s.repo.GetByID(ctx, id)
}

// Delete removes the org + cascades into memberships + casbin g rules.
// Refuses if the org still has children — caller must move them first.
// This is the conservative choice; cascading delete
// risks data loss in a tree refactor and recursive re-parent surprises
// the operator.
func (s *Service) Delete(ctx context.Context, id uint64) error {
	if n, err := s.repo.CountChildren(ctx, id); err != nil {
		return fmt.Errorf("count children: %w", err)
	} else if n > 0 {
		return fmt.Errorf("%w: org has %d sub-org(s); move them first", errs.ErrInvalid, n)
	}
	if s.memberships != nil {
		if err := s.memberships.DeleteByOrg(ctx, id); err != nil {
			return fmt.Errorf("clean memberships: %w", err)
		}
	}
	if s.authz != nil {
		if err := s.authz.RevokeAllForOrg(ctx, id); err != nil {
			return fmt.Errorf("revoke casbin: %w", err)
		}
	}
	return s.repo.Delete(ctx, id)
}

// Count returns the number of orgs.
func (s *Service) Count(ctx context.Context) (int64, error) {
	return s.repo.Count(ctx)
}
