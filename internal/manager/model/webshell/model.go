// Package webshell holds the audit row schema for WebSSH sessions.
// Every session emits one INSERT on open and one UPDATE on close, with
// byte counters and the termination cause. Passwords NEVER land here.
package webshell

import "time"

// Termination reasons. Kept as constants so handlers don't drift.
const (
	TerminatedByUser         = "user"          // browser closed
	TerminatedByIdle         = "idle"          // no input >N minutes
	TerminatedByDisconnect   = "disconnect"    // tunnel / WS dropped
	TerminatedByAdminKill    = "admin_kill"    // admin kicked the session
	TerminatedBySSHAuthFail  = "ssh_auth_fail" // sshd refused creds
	TerminatedBySSHExit      = "ssh_exit"      // user typed exit / shell ended
	TerminatedByDeviceOffline = "device_offline"
)

// Session is one audit row for an opened WebSSH session.
type Session struct {
	ID              string     `gorm:"primaryKey;size:64"`
	OngridUserID    uint64     `gorm:"not null;column:ongrid_user_id;index"`
	SSHUser         string     `gorm:"size:64;not null;column:ssh_user"`
	DeviceID        uint64     `gorm:"not null;column:device_id;index"`
	EdgeID          uint64     `gorm:"not null;column:edge_id"`
	ClientIP        string     `gorm:"size:64;column:client_ip"`
	StartedAt       time.Time  `gorm:"not null;column:started_at"`
	EndedAt         *time.Time `gorm:"column:ended_at"`
	BytesStdin      uint64     `gorm:"not null;default:0;column:bytes_stdin"`
	BytesStdout     uint64     `gorm:"not null;default:0;column:bytes_stdout"`
	ExitCode        int        `gorm:"not null;default:0;column:exit_code"`
	TerminatedBy    string     `gorm:"size:32;column:terminated_by"`
}

// TableName pins the table name.
func (Session) TableName() string { return "webshell_sessions" }
