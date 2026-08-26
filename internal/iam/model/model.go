// Package model holds the iam BC's persistence entities.
//
// Tables owned: users, orgs, org_memberships.
//
// Phase-1 (2026-05-10): re-introduce orgs + memberships as RBAC-with-domains
// scaffolding. Casbin is the policy engine; this package only owns the
// HR-style truth (who exists, who's in which org with which role). The
// casbin_rule table created by gorm-adapter is owned by the biz layer.
//
// Design choices:
//   - Orgs are flat (one level). Hierarchy can be added later via casbin
//     g3 role inheritance if customers ask; not now.
//   - User.IsSuperuser bypasses casbin entirely (middleware short-circuits).
//     The legacy User.Role column is kept for backwards compat with the
//     existing JWT, but is treated as advisory once memberships exist.
//   - OrgMembership.Role is the casbin subject ("org_admin" | "member" |
//     "viewer"). Membership rows are mirrored to casbin_rule via the biz
//     layer's Authorizer on every mutation.
package model

import "time"

// System-level role constants (`users.role` column).
//
//   - admin = 一切权限 + 用户/组织管理 (= 旧 admin ∪ 旧 superuser)
//   - user = 能用所有功能（含 chat 全工具集 / 跑 ClassMutating），
//               但不能改平台配置 / 不能管理用户
//   - viewer = 只读 + 受限 chat（toolbag 过滤成 ClassSafe）
//
// 见 同名 "viewer" 与 MembershipRoleViewer 不冲突（不同列）。
const (
	RoleAdmin  = "admin"
	RoleUser   = "user"
	RoleViewer = "viewer"
)

// IsValidRole returns true when r is one of the canonical system role
// constants. Used by Service.SetRole / Service.Create to refuse junk.
func IsValidRole(r string) bool {
	switch r {
	case RoleAdmin, RoleUser, RoleViewer:
		return true
	default:
		return false
	}
}

// RoleCanMutate is true for roles that can perform write actions on
// their own resources (chat sessions / custom agents / acks). viewer
// is the only role that returns false. admin and user both pass.
func RoleCanMutate(r string) bool {
	return r != RoleViewer
}

// Status constants.
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
)

// MembershipRole values. These also serve as casbin subject names in
// the policy matrix.
const (
	MembershipRoleAdmin  = "org_admin" // can manage org + members
	MembershipRoleMember = "member"    // can read + write resources
	MembershipRoleViewer = "viewer"    // read-only
)

// IsValidMembershipRole returns true when r is one of the canonical
// membership role constants.
func IsValidMembershipRole(r string) bool {
	switch r {
	case MembershipRoleAdmin, MembershipRoleMember, MembershipRoleViewer:
		return true
	default:
		return false
	}
}

// User is the login identity. PassHash is argon2id-encoded.
//
// IsSuperuser is independent of memberships and represents the system
// administrator role — the boot user, the operator who can manage
// users/orgs themselves. Casbin does not enforce against superusers
// (middleware short-circuits) so a corrupt policy table can never lock
// out the operator.
type User struct {
	ID          uint64 `gorm:"primaryKey;autoIncrement"`
	Email       string `gorm:"size:256;uniqueIndex"`
	PassHash    string `gorm:"size:512;column:pass_hash"`
	DisplayName string `gorm:"size:128;not null;default:'';column:display_name"`
	Phone       string `gorm:"size:32;not null;default:''"`
	// Role is the legacy column; new permission decisions go through
	// memberships + casbin. Kept for JWT compat — the boot migration
	// copies Role="admin" rows into IsSuperuser=true.
	Role         string    `gorm:"size:32;default:user;check:role IN ('admin','user','viewer')"`
	IsSuperuser  bool      `gorm:"not null;default:false;column:is_superuser"`
	Status       string    `gorm:"size:32;default:active;check:status IN ('active','disabled')"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// TableName pins the table name to the one created by the SQL migrations.
func (User) TableName() string { return "users" }

// Org is an organizational unit (department / team). May nest via
// ParentID to reflect the company structure; cycle prevention happens
// in biz/org. Permissions DO NOT inherit through the hierarchy —
// memberships stay attached to the specific org_id ().
// Name is unique tenant-wide.
type Org struct {
	ID          uint64 `gorm:"primaryKey;autoIncrement"`
	Name        string `gorm:"size:128;uniqueIndex;not null"`
	Description string `gorm:"size:512;not null;default:''"`
	// ParentID is nil for top-level orgs. References Org.ID; on the
	// data-layer side we don't FK-constrain (gorm migrations + sqlite
	// dialect drift make FKs flaky); biz layer guards integrity.
	ParentID    *uint64 `gorm:"column:parent_id;index"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// TableName pins the table name.
func (Org) TableName() string { return "orgs" }

// OrgMembership is the N:M junction between users and orgs with a role.
// Same user can be in multiple orgs with different roles. The biz
// layer's Authorizer mirrors every row into casbin's grouping table.
type OrgMembership struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement"`
	UserID    uint64    `gorm:"not null;column:user_id;uniqueIndex:idx_user_org,priority:1"`
	OrgID     uint64    `gorm:"not null;column:org_id;uniqueIndex:idx_user_org,priority:2"`
	Role      string    `gorm:"size:32;not null;check:role IN ('org_admin','member','viewer')"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TableName pins the table name.
func (OrgMembership) TableName() string { return "org_memberships" }
