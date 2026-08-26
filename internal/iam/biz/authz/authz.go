// Package authz wraps casbin to provide a narrow, typed authorization
// surface for the iam BC. It owns the casbin Enforcer instance, hides
// the policy file/loader gunk, and exposes:
//
//	Enforcer.Allow(ctx, userID, orgID, obj, act) bool
//
// Invariants:
//   - Subjects are stringified user IDs ("42") in casbin g/p rows;
//     domains are stringified org IDs ("7"). The model uses a "*"
//     wildcard for domain-agnostic policies (today: only the legacy
//     superuser-bypass route, which does NOT go through casbin at all).
//   - Membership truth lives in iam.OrgMembership rows. Authorizer
//     mirrors the truth into casbin g policies via SyncMembership /
//     RevokeMembership / SyncMembershipsForOrg.
//   - Role policies (p rows) are hardcoded in this package — three
//     roles (org_admin / member / viewer), boot-time injected, never
//     edited at runtime.
//
// The middleware that calls Enforcer.Allow lives in the manager server
// layer; the iam BC stays HTTP-agnostic.
package authz

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"

	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
	gormadapter "github.com/casbin/gorm-adapter/v3"
	"gorm.io/gorm"

	iammodel "github.com/ongridio/ongrid/internal/iam/model"
)

//go:embed model.conf
var modelConf []byte

// Wildcard domain used by superuser policies.
const DomainAny = "*"

// Enforcer wraps casbin.SyncedEnforcer with our typed accessors.
type Enforcer struct {
	mu sync.Mutex
	e  *casbin.SyncedEnforcer
	l  *slog.Logger
}

// New builds an Enforcer backed by gorm-adapter on db. The adapter
// creates table `casbin_rule` automatically; it lives alongside the
// app's other tables and uses the same connection.
//
// Boot order (cmd/ongrid):
//  1. iam Migrate(db) — creates users, orgs, org_memberships
//  2. authz.New(db, log) — creates casbin_rule, loads policies
//  3. authz.Enforcer.SeedRolePolicies() — idempotent inject of the
//     hardcoded role/policy matrix
//  4. authz.Enforcer.HydrateMemberships(memberships) — reflect every
//     OrgMembership row as a casbin g rule
func New(db *gorm.DB, log *slog.Logger) (*Enforcer, error) {
	if log == nil {
		log = slog.Default()
	}
	a, err := gormadapter.NewAdapterByDB(db)
	if err != nil {
		return nil, fmt.Errorf("authz: gorm adapter: %w", err)
	}
	m, err := model.NewModelFromString(string(modelConf))
	if err != nil {
		return nil, fmt.Errorf("authz: parse model: %w", err)
	}
	e, err := casbin.NewSyncedEnforcer(m, a)
	if err != nil {
		return nil, fmt.Errorf("authz: new enforcer: %w", err)
	}
	if err := e.LoadPolicy(); err != nil {
		return nil, fmt.Errorf("authz: load policy: %w", err)
	}
	return &Enforcer{e: e, l: log}, nil
}

// rolePolicies is the hardcoded matrix injected at boot. Domains are
// templated as "{dom}" — actually we use "*" so a single row covers
// every domain. Per-domain policies are unnecessary because membership
// already pins the user to a specific domain via g rules.
var rolePolicies = [][]string{
	// org_admin: full inside their domain, plus member management.
	{iammodel.MembershipRoleAdmin, "*", "org:*", "*"},
	{iammodel.MembershipRoleAdmin, "*", "member:*", "*"},
	{iammodel.MembershipRoleAdmin, "*", "*", "*"},

	// member: read + write + exec on resources, no member management.
	{iammodel.MembershipRoleMember, "*", "*", "read"},
	{iammodel.MembershipRoleMember, "*", "*", "write"},
	{iammodel.MembershipRoleMember, "*", "device:shell", "exec"},

	// viewer: read only. No device:shell access — viewers see audit
	// logs, not live shells.
	{iammodel.MembershipRoleViewer, "*", "*", "read"},

	// superuser is enforced via middleware short-circuit; we keep a
	// fallback policy here so an in-policy decision still resolves
	// allow if a future call routes through casbin without the
	// short-circuit (defense in depth).
	{"superuser", "*", "*", "*"},
}

// SeedRolePolicies idempotently inserts every row in rolePolicies.
// Safe to call on every boot; AddPolicies is a no-op for duplicates.
func (a *Enforcer) SeedRolePolicies(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range rolePolicies {
		ok, err := a.e.AddPolicy(p[0], p[1], p[2], p[3])
		if err != nil {
			return fmt.Errorf("authz: add policy %v: %w", p, err)
		}
		_ = ok // false = already present
	}
	return nil
}

// HydrateMemberships reflects every OrgMembership row as a casbin g
// rule (subject=user_id, role, domain=org_id). Idempotent.
//
// We don't bother diffing with what's already in casbin_rule because
// the cost of re-AddGroupingPolicy on duplicates is one no-op insert
// per row. For larger fleets this is the right trade-off (memberships
// are ≪10k for any realistic deployment).
func (a *Enforcer) HydrateMemberships(ctx context.Context, ms []iammodel.OrgMembership) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, m := range ms {
		if _, err := a.e.AddGroupingPolicy(uidStr(m.UserID), m.Role, oidStr(m.OrgID)); err != nil {
			return fmt.Errorf("authz: hydrate membership %d: %w", m.ID, err)
		}
	}
	return nil
}

// SyncMembership upserts a single g rule. Called from biz/membership on
// AddMember / ChangeRole. role is the new role; if a previous role exists
// for (user, org) we strip it before adding the new one — casbin doesn't
// implicitly replace.
func (a *Enforcer) SyncMembership(ctx context.Context, userID, orgID uint64, role string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	uid, oid := uidStr(userID), oidStr(orgID)
	// Strip any existing g (user, *, org) row first.
	groupings, err := a.e.GetFilteredGroupingPolicy(0, uid, "", oid)
	if err != nil {
		return fmt.Errorf("authz: get filtered grouping: %w", err)
	}
	for _, g := range groupings {
		if len(g) >= 3 && g[2] == oid {
			if _, err := a.e.RemoveGroupingPolicy(g[0], g[1], g[2]); err != nil {
				return fmt.Errorf("authz: remove old grouping: %w", err)
			}
		}
	}
	if _, err := a.e.AddGroupingPolicy(uid, role, oid); err != nil {
		return fmt.Errorf("authz: add grouping: %w", err)
	}
	return nil
}

// RevokeMembership removes every g rule for (user, org). Called from
// biz/membership on RemoveMember.
func (a *Enforcer) RevokeMembership(ctx context.Context, userID, orgID uint64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	uid, oid := uidStr(userID), oidStr(orgID)
	groupings, err := a.e.GetFilteredGroupingPolicy(0, uid, "", oid)
	if err != nil {
		return fmt.Errorf("authz: get filtered grouping: %w", err)
	}
	for _, g := range groupings {
		if len(g) >= 3 && g[2] == oid {
			if _, err := a.e.RemoveGroupingPolicy(g[0], g[1], g[2]); err != nil {
				return fmt.Errorf("authz: remove grouping: %w", err)
			}
		}
	}
	return nil
}

// RevokeAllForOrg removes every g rule referencing the given org.
// Called when the org itself is deleted.
func (a *Enforcer) RevokeAllForOrg(ctx context.Context, orgID uint64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	oid := oidStr(orgID)
	groupings, err := a.e.GetFilteredGroupingPolicy(2, oid)
	if err != nil {
		return fmt.Errorf("authz: get filtered grouping for org: %w", err)
	}
	for _, g := range groupings {
		if len(g) < 3 {
			continue
		}
		if _, err := a.e.RemoveGroupingPolicy(g[0], g[1], g[2]); err != nil {
			return fmt.Errorf("authz: remove grouping: %w", err)
		}
	}
	return nil
}

// RevokeAllForUser removes every g rule referencing the given user.
// Called when the user is deleted.
func (a *Enforcer) RevokeAllForUser(ctx context.Context, userID uint64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	uid := uidStr(userID)
	groupings, err := a.e.GetFilteredGroupingPolicy(0, uid)
	if err != nil {
		return fmt.Errorf("authz: get filtered grouping for user: %w", err)
	}
	for _, g := range groupings {
		if len(g) < 3 {
			continue
		}
		if _, err := a.e.RemoveGroupingPolicy(g[0], g[1], g[2]); err != nil {
			return fmt.Errorf("authz: remove grouping: %w", err)
		}
	}
	return nil
}

// Allow runs the enforcer; returns true when the user is permitted
// (obj, act) in the given org. Errors are logged + returned as deny.
func (a *Enforcer) Allow(ctx context.Context, userID, orgID uint64, obj, act string) bool {
	ok, err := a.e.Enforce(uidStr(userID), oidStr(orgID), obj, act)
	if err != nil {
		a.l.Warn("authz: enforce error",
			slog.Uint64("user", userID),
			slog.Uint64("org", orgID),
			slog.String("obj", obj),
			slog.String("act", act),
			slog.Any("err", err))
		return false
	}
	return ok
}

// AllowAnyOrg runs Enforce against every org the user belongs to and
// returns true on the first allow. Used by middleware when the request
// doesn't carry an X-Active-Org header — Phase 1 default.
//
// Implementation note: we walk casbin's grouping policy filtered by the
// user, extract the unique domains, and short-circuit on first allow.
func (a *Enforcer) AllowAnyOrg(ctx context.Context, userID uint64, obj, act string) bool {
	orgs, err := a.userDomains(userID)
	if err != nil {
		a.l.Warn("authz: list user domains", slog.Uint64("user", userID), slog.Any("err", err))
		return false
	}
	for _, oidStr := range orgs {
		ok, err := a.e.Enforce(uidStr(userID), oidStr, obj, act)
		if err != nil {
			continue
		}
		if ok {
			return true
		}
	}
	return false
}

// UserOrgs returns every org id the user is a member of (resolved
// from casbin g policies). Used by middleware default-domain logic.
func (a *Enforcer) UserOrgs(ctx context.Context, userID uint64) ([]uint64, error) {
	doms, err := a.userDomains(userID)
	if err != nil {
		return nil, err
	}
	out := make([]uint64, 0, len(doms))
	for _, d := range doms {
		oid, err := strconv.ParseUint(d, 10, 64)
		if err != nil {
			continue
		}
		out = append(out, oid)
	}
	return out, nil
}

func (a *Enforcer) userDomains(userID uint64) ([]string, error) {
	groupings, err := a.e.GetFilteredGroupingPolicy(0, uidStr(userID))
	if err != nil {
		return nil, errors.Join(err, errors.New("authz: list groupings for user"))
	}
	seen := make(map[string]struct{}, len(groupings))
	out := make([]string, 0, len(groupings))
	for _, g := range groupings {
		if len(g) < 3 {
			continue
		}
		dom := g[2]
		if _, ok := seen[dom]; ok {
			continue
		}
		seen[dom] = struct{}{}
		out = append(out, dom)
	}
	return out, nil
}

// uidStr / oidStr keep the stringification convention central so we
// don't get casbin rules with mixed shapes ("42" vs "0042" vs uint).
func uidStr(id uint64) string { return strconv.FormatUint(id, 10) }
func oidStr(id uint64) string { return strconv.FormatUint(id, 10) }
