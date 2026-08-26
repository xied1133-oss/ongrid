package edge

import (
	"context"

	biz "github.com/ongridio/ongrid/internal/manager/biz/edge"
	model "github.com/ongridio/ongrid/internal/manager/model/edge"
)

type EnrollmentService struct{ uc *biz.EnrollmentUsecase }

func NewEnrollmentService(uc *biz.EnrollmentUsecase) *EnrollmentService {
	return &EnrollmentService{uc: uc}
}

func (s *EnrollmentService) CreateProfile(ctx context.Context, in biz.CreateEnrollmentProfileInput) (*biz.CreateEnrollmentProfileResult, error) {
	return s.uc.CreateProfile(ctx, in)
}

func (s *EnrollmentService) ListProfiles(ctx context.Context, filter biz.EnrollmentProfileListFilter) ([]*model.EnrollmentProfile, int64, error) {
	return s.uc.ListProfiles(ctx, filter)
}

func (s *EnrollmentService) RevokeProfile(ctx context.Context, id uint64) error {
	return s.uc.RevokeProfile(ctx, id)
}

func (s *EnrollmentService) DeleteProfile(ctx context.Context, id uint64) error {
	return s.uc.DeleteProfile(ctx, id)
}

func (s *EnrollmentService) Enroll(ctx context.Context, in biz.EnrollInput) (*biz.EnrollResult, error) {
	return s.uc.Enroll(ctx, in)
}
