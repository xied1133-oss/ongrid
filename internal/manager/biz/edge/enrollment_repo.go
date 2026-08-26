package edge

import (
	"context"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/edge"
)

type EnrollmentProfileListFilter struct {
	Limit  int
	Offset int
}

// EnrollmentRepo owns the transaction that consumes one profile slot and
// creates an independent Edge identity. Keeping those writes together avoids
// quota overrun and orphan Edge rows under concurrent bootstrap requests.
type EnrollmentRepo interface {
	CreateProfile(ctx context.Context, profile *model.EnrollmentProfile) error
	GetProfileByTokenHash(ctx context.Context, tokenHash string) (*model.EnrollmentProfile, error)
	ListProfiles(ctx context.Context, filter EnrollmentProfileListFilter) ([]*model.EnrollmentProfile, int64, error)
	CountActiveProfilesForCluster(ctx context.Context, clusterNodeID uint64, now time.Time) (int64, error)
	RevokeProfile(ctx context.Context, id uint64) error
	DeleteProfile(ctx context.Context, id uint64) error
	Claim(ctx context.Context, tokenHash, hostFingerprint, sourceIP string, candidate *model.Edge, now time.Time) (
		profile *model.EnrollmentProfile,
		enrollment *model.Enrollment,
		edge *model.Edge,
		created bool,
		err error,
	)
	GetEnrollmentByEdgeID(ctx context.Context, edgeID uint64) (*model.Enrollment, *model.EnrollmentProfile, error)
	Complete(ctx context.Context, enrollmentID, deviceID uint64, completedAt time.Time) error
}
