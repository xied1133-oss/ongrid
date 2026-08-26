package edge

import "time"

const (
	EnrollmentModeBatchOnly = "batch_only"
	EnrollmentModeCluster   = "cluster"

	EnrollmentStatusActive  = "active"
	EnrollmentStatusRevoked = "revoked"
)

// EnrollmentProfile is a reusable, bounded bootstrap capability for a batch
// of non-Kubernetes Edge installations. TokenHash stores only the SHA-256
// digest of the high-entropy token; plaintext is returned once at creation.
type EnrollmentProfile struct {
	ID             uint64    `gorm:"primaryKey;autoIncrement"`
	Name           string    `gorm:"size:128;not null"`
	AssignmentMode string    `gorm:"size:16;not null;index"`
	ClusterNodeID  *uint64   `gorm:"column:cluster_node_id;index"`
	TokenHash      string    `gorm:"size:64;not null;uniqueIndex"`
	ExpiresAt      time.Time `gorm:"not null;index"`
	MaxUses        int       `gorm:"not null"`
	UsedCount      int       `gorm:"not null;default:0"`
	Status         string    `gorm:"size:16;not null;default:active;index"`
	CreatedBy      *uint64   `gorm:"column:created_by"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
}

func (EnrollmentProfile) TableName() string { return "edge_enrollment_profiles" }

// Enrollment records one host identity claimed from a profile. DeviceID is
// filled only after the existing register_edge flow has created or found the
// host Device. The profile+fingerprint uniqueness makes a lost HTTP response
// safely retryable without consuming another slot.
type Enrollment struct {
	ID              uint64     `gorm:"primaryKey;autoIncrement"`
	ProfileID       uint64     `gorm:"not null;index;uniqueIndex:idx_edge_enrollments_profile_fingerprint,priority:1"`
	EdgeID          uint64     `gorm:"not null;uniqueIndex"`
	DeviceID        *uint64    `gorm:"column:device_id;index"`
	HostFingerprint string     `gorm:"size:35;not null;uniqueIndex:idx_edge_enrollments_profile_fingerprint,priority:2"`
	SourceIP        string     `gorm:"size:64;not null;default:''"`
	EnrolledAt      time.Time  `gorm:"not null;column:enrolled_at"`
	CompletedAt     *time.Time `gorm:"column:completed_at"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at"`
}

func (Enrollment) TableName() string { return "edge_enrollments" }
