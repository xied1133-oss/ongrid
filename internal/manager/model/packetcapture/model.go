// Package packetcapture holds durable packet-capture task metadata. Raw PCAP
// objects are intentionally not modelled as public artifacts: their storage
// key and hash remain internal fields and are served only through a dedicated
// authenticated viewer after parsing.
package packetcapture

import (
	"time"

	"gorm.io/gorm"
)

const (
	StatePendingApproval = "pending_approval"
	StateQueued          = "queued"
	StateDispatching     = "dispatching"
	StateCapturing       = "capturing"
	StateUploading       = "uploading"
	StateParsing         = "parsing"
	StateReady           = "ready"
	StateCancelled       = "cancelled"
	StateFailed          = "failed"
	StateRawExpired      = "raw_expired"
	StateExpired         = "expired"
	StateDeleted         = "deleted"
)

const (
	SessionStateCollecting = "collecting"
	SessionStateReady      = "ready"
	SessionStatePartial    = "partial"
	SessionStateCancelled  = "cancelled"
	SessionStateFailed     = "failed"
)

// Capture is one requested packet collection. JSON snapshots preserve the
// exact request and resolved target even if the device later changes.
type Capture struct {
	ID uint64 `gorm:"column:id;primaryKey;autoIncrement" json:"id"`

	RequestIdempotencyKey string `gorm:"column:request_idempotency_key;type:varchar(128);not null;default:'';index:idx_packet_capture_idempotency" json:"request_idempotency_key"`
	CreatedBy             uint64 `gorm:"column:created_by;not null;index" json:"created_by"`
	Source                string `gorm:"column:source;type:varchar(32);not null;default:'';index" json:"source"`
	State                 string `gorm:"column:state;type:varchar(32);not null;default:'queued';index" json:"state"`

	EdgeID    uint64 `gorm:"column:edge_id;not null;index" json:"edge_id"`
	DeviceID  uint64 `gorm:"column:device_id;not null;default:0;index" json:"device_id"`
	SessionID uint64 `gorm:"column:session_id;not null;default:0;index" json:"session_id,omitempty"`

	TargetKind          string `gorm:"column:target_kind;type:varchar(48);not null;default:''" json:"target_kind"`
	RequestedTargetJSON string `gorm:"column:requested_target_json;type:text;not null" json:"-"`
	ResolvedTargetJSON  string `gorm:"column:resolved_target_json;type:text;not null" json:"-"`
	FilterJSON          string `gorm:"column:filter_json;type:text;not null" json:"-"`
	CanonicalFilter     string `gorm:"column:canonical_filter;type:text;not null" json:"canonical_filter"`

	InterfaceName    string `gorm:"column:interface_name;type:varchar(64);not null;default:''" json:"interface_name"`
	NetworkNamespace string `gorm:"column:network_namespace;type:varchar(128);not null;default:''" json:"network_namespace,omitempty"`
	Direction        string `gorm:"column:direction;type:varchar(16);not null;default:'inout'" json:"direction"`
	Format           string `gorm:"column:format;type:varchar(16);not null;default:'pcap'" json:"format"`
	Promiscuous      bool   `gorm:"column:promiscuous;not null;default:false" json:"promiscuous"`
	Immediate        bool   `gorm:"column:immediate;not null;default:false" json:"immediate"`
	DurationSecs     uint32 `gorm:"column:duration_seconds;not null;default:30" json:"duration_seconds"`
	MaxBytes         uint64 `gorm:"column:max_bytes;not null;default:0" json:"max_bytes"`
	MaxPackets       uint64 `gorm:"column:max_packets;not null;default:0" json:"max_packets"`
	Snaplen          uint32 `gorm:"column:snaplen;not null;default:1514" json:"snaplen"`

	Title         string `gorm:"column:title;type:varchar(255);not null;default:''" json:"title"`
	Description   string `gorm:"column:description;type:text;not null" json:"description"`
	LabelsJSON    string `gorm:"column:labels_json;type:text;not null" json:"-"`
	ApprovalID    string `gorm:"column:approval_id;type:varchar(64);not null;default:'';index" json:"approval_id"`
	WorkflowRunID string `gorm:"column:workflow_run_id;type:varchar(64);not null;default:'';index" json:"workflow_run_id"`
	IncidentID    string `gorm:"column:incident_id;type:varchar(64);not null;default:'';index" json:"incident_id"`

	CapturedBytes   uint64 `gorm:"column:captured_bytes;not null;default:0" json:"captured_bytes"`
	CapturedPackets uint64 `gorm:"column:captured_packets;not null;default:0" json:"captured_packets"`
	LivePreviewJSON string `gorm:"column:live_preview_json;type:text;not null" json:"-"`
	RawObjectKey    string `gorm:"column:raw_object_key;type:varchar(512);not null;default:''" json:"-"`
	RawSHA256       string `gorm:"column:raw_sha256;type:char(64);not null;default:''" json:"-"`
	ArtifactID      string `gorm:"column:artifact_id;type:varchar(64);not null;default:'';index" json:"artifact_id"`
	ParsedJSON      string `gorm:"column:parsed_json;type:longtext;not null" json:"-"`
	ErrorCode       string `gorm:"column:error_code;type:varchar(64);not null;default:''" json:"error_code"`
	ErrorDetail     string `gorm:"column:error_detail;type:text;not null" json:"error_detail"`

	StartedAt       *time.Time     `gorm:"column:started_at;index" json:"started_at,omitempty"`
	FinishedAt      *time.Time     `gorm:"column:finished_at;index" json:"finished_at,omitempty"`
	RawExpiresAt    *time.Time     `gorm:"column:raw_expires_at;index" json:"raw_expires_at,omitempty"`
	ParsedExpiresAt *time.Time     `gorm:"column:parsed_expires_at;index" json:"parsed_expires_at,omitempty"`
	CreatedAt       time.Time      `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt       time.Time      `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
	DeletedAt       gorm.DeletedAt `gorm:"column:deleted_at;index" json:"-"`
}

func (Capture) TableName() string { return "packet_captures" }

// Session groups captures requested against several edges for one diagnosis.
// AnalysisJSON stores only normalized packet metadata, never raw PCAP bytes.
type Session struct {
	ID        uint64 `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	PublicID  string `gorm:"column:public_id;type:varchar(64);not null;uniqueIndex" json:"public_id"`
	CreatedBy uint64 `gorm:"column:created_by;not null;index" json:"created_by"`
	Source    string `gorm:"column:source;type:varchar(32);not null;default:'';index" json:"source"`
	State     string `gorm:"column:state;type:varchar(32);not null;default:'collecting';index" json:"state"`
	// ChatSessionID binds a chat-created capture to its originating
	// conversation so terminal capture state can be reported after restart.
	ChatSessionID string `gorm:"column:chat_session_id;type:char(36);not null;default:'';index" json:"-"`
	// OperationID links this domain session to the generic user-visible task.
	// Empty values preserve readability for sessions created before Operation.
	OperationID string `gorm:"column:operation_id;type:char(36);not null;default:'';index" json:"-"`
	// CompletionNotifiedAt is a durable idempotency marker for the chat
	// continuation worker. NULL means the terminal event has not been emitted.
	CompletionNotifiedAt *time.Time `gorm:"column:completion_notified_at;index" json:"-"`

	Title           string    `gorm:"column:title;type:varchar(255);not null;default:''" json:"title"`
	Description     string    `gorm:"column:description;type:text;not null" json:"description"`
	CanonicalFilter string    `gorm:"column:canonical_filter;type:text;not null" json:"canonical_filter"`
	DurationSecs    uint32    `gorm:"column:duration_seconds;not null;default:30" json:"duration_seconds"`
	PlannedStartAt  time.Time `gorm:"column:planned_start_at;index" json:"planned_start_at"`
	ClockQuality    string    `gorm:"column:clock_quality;type:varchar(32);not null;default:'uncalibrated'" json:"clock_quality"`
	AnalysisJSON    string    `gorm:"column:analysis_json;type:longtext;not null" json:"-"`

	CreatedAt time.Time      `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt time.Time      `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;index" json:"-"`
}

func (Session) TableName() string { return "packet_capture_sessions" }
