package edge

import (
	"context"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/edge"
)

type UpgradeJobListFilter struct {
	ClusterNodeID *uint64
	Limit         int
	Offset        int
}

type UpgradeRetrySnapshot struct {
	EdgeID               uint64
	FromVersion          string
	BaselineRegisteredAt *time.Time
}

// UpgradeJobRepo is the durable state machine boundary consumed by the
// upgrade coordinator. Implementations must keep parent counters consistent
// with item transitions.
type UpgradeJobRepo interface {
	CreateUpgradeJob(ctx context.Context, job *model.UpgradeJob, items []*model.UpgradeJobItem) error
	GetUpgradeJob(ctx context.Context, id uint64) (*model.UpgradeJob, []*model.UpgradeJobItem, error)
	ListUpgradeJobs(ctx context.Context, filter UpgradeJobListFilter) ([]*model.UpgradeJob, int64, error)
	ClaimNextUpgradeJob(ctx context.Context, now time.Time) (*model.UpgradeJob, error)
	SetUpgradeJobCurrentBatch(ctx context.Context, jobID uint64, batchNumber int, now time.Time) error
	ListUpgradeJobItems(ctx context.Context, jobID uint64, statuses ...string) ([]*model.UpgradeJobItem, error)
	MarkUpgradeItemDispatching(ctx context.Context, itemID uint64, baselineRegisteredAt *time.Time, now time.Time) error
	RefreshUpgradeItemBaseline(ctx context.Context, itemID uint64, baselineRegisteredAt time.Time) error
	MarkUpgradeItemWaiting(ctx context.Context, itemID uint64, deadline time.Time) error
	MarkUpgradeItemSucceeded(ctx context.Context, itemID uint64, observedVersion string, observedRegisteredAt *time.Time, now time.Time) error
	MarkUpgradeItemFailed(ctx context.Context, itemID uint64, status, code, message, observedVersion string, observedRegisteredAt *time.Time, now time.Time) error
	RefreshUpgradeJob(ctx context.Context, jobID uint64, now time.Time) (*model.UpgradeJob, error)
	RequeueUpgradeJob(ctx context.Context, jobID uint64, now time.Time) error
	RecoverUpgradeJobs(ctx context.Context) error
	RetryUpgradeJob(ctx context.Context, jobID uint64, snapshots []UpgradeRetrySnapshot, now time.Time) (*model.UpgradeJob, error)
	CountActiveUpgradeJobsForCluster(ctx context.Context, clusterNodeID uint64) (int64, error)
	DeleteFinishedUpgradeJobsBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

// UpgradeEdgeReader provides the coordinator with authoritative registration
// state without coupling it to a concrete GORM repository.
type UpgradeEdgeReader interface {
	GetByID(ctx context.Context, id uint64) (*model.Edge, error)
	GetManyByIDs(ctx context.Context, ids []uint64) (map[uint64]*model.Edge, error)
}
