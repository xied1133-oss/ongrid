package aiops

import "time"

// UserAgent is a persona created via the UI by a user — Phase 3 of
// the Agent-Assistant initiative. Mirrors enough of chatruntime.Agent
// for the registry to hydrate one without going back to disk: name,
// description, when_to_use, system_prompt, and the allowed/disallowed
// tool lists serialised as JSON. The registry-side Agent is rebuilt
// per row at boot + on every CRUD via the user-agent service.
//
// Owner: Phase 3 launches single-tenant (UserID is the creator); when
// SaaS lands the same column doubles as org_id. Until then we don't
// enforce cross-user visibility — every authed caller sees every user
// agent in /v1/agents (matches the rest of the platform's "shared
// inside one private deployment" stance and avoids tenant_bind plumbing
// that explicitly parks).
type UserAgent struct {
	ID                uint64 `gorm:"primaryKey;autoIncrement"`
	UserID            uint64 `gorm:"not null;default:0;index"`
	Name              string `gorm:"size:128;not null;uniqueIndex:idx_user_agent_name"`
	Description       string `gorm:"size:512;not null"`
	WhenToUse         string `gorm:"type:text;column:when_to_use"`
	SystemPrompt      string `gorm:"type:text;column:system_prompt"`
	CriticalReminder  string `gorm:"type:text;column:critical_reminder"`
	AllowedToolsJSON  string `gorm:"type:text;column:allowed_tools_json"`
	DisallowedToolsJSON string `gorm:"type:text;column:disallowed_tools_json"`
	PermissionMode    string `gorm:"size:32;column:permission_mode"`
	Model             string `gorm:"size:128;column:model"`
	MaxTurns          int    `gorm:"column:max_turns"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// TableName pins the SQLite table name.
func (UserAgent) TableName() string { return "user_agents" }
