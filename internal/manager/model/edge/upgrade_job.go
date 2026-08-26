package edge

import "time"

const (
	DefaultUpgradeJobBatchSize = 10

	UpgradeJobStatusQueued        = "queued"
	UpgradeJobStatusRunning       = "running"
	UpgradeJobStatusSucceeded     = "succeeded"
	UpgradeJobStatusPartialFailed = "partial_failed"
	UpgradeJobStatusFailed        = "failed"

	UpgradeJobItemStatusQueued              = "queued"
	UpgradeJobItemStatusDispatching         = "dispatching"
	UpgradeJobItemStatusWaitingRegistration = "waiting_registration"
	UpgradeJobItemStatusSucceeded           = "succeeded"
	UpgradeJobItemStatusFailed              = "failed"
	UpgradeJobItemStatusTimedOut            = "timed_out"
	UpgradeJobItemStatusSkipped             = "skipped"
)

// UpgradeJob is the durable parent record for one fleet package rollout.
// ClusterNodeID is optional so the same orchestration can later serve a
// manually selected device set without inventing a synthetic cluster.
type UpgradeJob struct {
	ID             uint64     `gorm:"column:id;primaryKey;autoIncrement"`
	ClusterNodeID  *uint64    `gorm:"column:cluster_node_id;index"`
	TargetVersion  string     `gorm:"column:target_version;size:32;not null;default:''"`
	Status         string     `gorm:"column:status;size:24;not null;default:queued;index"`
	ForceReinstall bool       `gorm:"column:force_reinstall;not null;default:false"`
	BatchSize      int        `gorm:"column:batch_size;not null;default:10"`
	CurrentBatch   int        `gorm:"column:current_batch;not null;default:0"`
	TotalBatches   int        `gorm:"column:total_batches;not null;default:0"`
	Total          int        `gorm:"column:total;not null;default:0"`
	Succeeded      int        `gorm:"column:succeeded;not null;default:0"`
	Failed         int        `gorm:"column:failed;not null;default:0"`
	Skipped        int        `gorm:"column:skipped;not null;default:0"`
	Pending        int        `gorm:"column:pending;not null;default:0"`
	CreatedBy      *uint64    `gorm:"column:created_by"`
	StartedAt      *time.Time `gorm:"column:started_at"`
	FinishedAt     *time.Time `gorm:"column:finished_at"`
	CreatedAt      time.Time  `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;autoUpdateTime"`
	DeletedAt      *time.Time `gorm:"column:deleted_at;index"`
}

func (UpgradeJob) TableName() string { return "edge_upgrade_jobs" }

// UpgradeJobItem snapshots the target identity so history remains readable
// even if the Edge or Device is later renamed or removed.
type UpgradeJobItem struct {
	ID                     uint64     `gorm:"column:id;primaryKey;autoIncrement"`
	JobID                  uint64     `gorm:"column:job_id;not null;uniqueIndex:idx_edge_upgrade_job_edge,priority:1"`
	EdgeID                 uint64     `gorm:"column:edge_id;not null;uniqueIndex:idx_edge_upgrade_job_edge,priority:2;index"`
	DeviceID               *uint64    `gorm:"column:device_id"`
	EdgeName               string     `gorm:"column:edge_name;size:128;not null;default:''"`
	DeviceName             string     `gorm:"column:device_name;size:255;not null;default:''"`
	Arch                   string     `gorm:"column:arch;size:32;not null;default:''"`
	FromVersion            string     `gorm:"column:from_version;size:32;not null;default:''"`
	TargetVersion          string     `gorm:"column:target_version;size:32;not null;default:''"`
	BatchNumber            int        `gorm:"column:batch_number;not null;default:0;index"`
	Status                 string     `gorm:"column:status;size:32;not null;default:queued"`
	Attempt                int        `gorm:"column:attempt;not null;default:0"`
	ErrorCode              string     `gorm:"column:error_code;size:64;not null;default:''"`
	ErrorMessage           string     `gorm:"column:error_message;size:1024;not null;default:''"`
	ObservedVersion        string     `gorm:"column:observed_version;size:32;not null;default:''"`
	BaselineRegisteredAt   *time.Time `gorm:"column:baseline_registered_at"`
	ObservedRegisteredAt   *time.Time `gorm:"column:observed_registered_at"`
	VerificationDeadlineAt *time.Time `gorm:"column:verification_deadline_at"`
	StartedAt              *time.Time `gorm:"column:started_at"`
	FinishedAt             *time.Time `gorm:"column:finished_at"`
	CreatedAt              time.Time  `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt              time.Time  `gorm:"column:updated_at;autoUpdateTime"`
}

func (UpgradeJobItem) TableName() string { return "edge_upgrade_job_items" }
