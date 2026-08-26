package edge

import (
	"context"

	biz "github.com/ongridio/ongrid/internal/manager/biz/edge"
	model "github.com/ongridio/ongrid/internal/manager/model/edge"
)

// UpgradeJobs is the HTTP-facing service facade for durable package rollout
// records. The background Run loop remains owned by the biz usecase.
type UpgradeJobs struct {
	uc *biz.UpgradeJobUsecase
}

func NewUpgradeJobs(uc *biz.UpgradeJobUsecase) *UpgradeJobs {
	return &UpgradeJobs{uc: uc}
}

func (s *UpgradeJobs) Create(ctx context.Context, in biz.CreateUpgradeJobInput) (*model.UpgradeJob, error) {
	return s.uc.Create(ctx, in)
}

func (s *UpgradeJobs) List(ctx context.Context, filter biz.UpgradeJobListFilter) ([]*model.UpgradeJob, int64, error) {
	return s.uc.List(ctx, filter)
}

func (s *UpgradeJobs) Get(ctx context.Context, id uint64) (*model.UpgradeJob, []*model.UpgradeJobItem, error) {
	return s.uc.Get(ctx, id)
}

func (s *UpgradeJobs) Retry(ctx context.Context, id uint64) (*model.UpgradeJob, error) {
	return s.uc.Retry(ctx, id)
}
