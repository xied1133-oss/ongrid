package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	biz "github.com/ongridio/ongrid/internal/manager/biz/edge"
	model "github.com/ongridio/ongrid/internal/manager/model/edge"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

type EnrollmentRepo struct{ db *gorm.DB }

func NewEnrollmentRepo(db *gorm.DB) *EnrollmentRepo { return &EnrollmentRepo{db: db} }

var _ biz.EnrollmentRepo = (*EnrollmentRepo)(nil)

func (r *EnrollmentRepo) CreateProfile(ctx context.Context, profile *model.EnrollmentProfile) error {
	if profile == nil {
		return errs.ErrInvalid
	}
	return r.db.WithContext(ctx).Create(profile).Error
}

func (r *EnrollmentRepo) GetProfileByTokenHash(ctx context.Context, tokenHash string) (*model.EnrollmentProfile, error) {
	var profile model.EnrollmentProfile
	if err := r.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&profile).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrUnauthorized
		}
		return nil, err
	}
	return &profile, nil
}

func (r *EnrollmentRepo) ListProfiles(ctx context.Context, filter biz.EnrollmentProfileListFilter) ([]*model.EnrollmentProfile, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.EnrollmentProfile{})
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if filter.Limit > 0 {
		q = q.Limit(filter.Limit)
	}
	if filter.Offset > 0 {
		q = q.Offset(filter.Offset)
	}
	var profiles []*model.EnrollmentProfile
	if err := q.Order("id DESC").Find(&profiles).Error; err != nil {
		return nil, 0, err
	}
	return profiles, total, nil
}

func (r *EnrollmentRepo) CountActiveProfilesForCluster(ctx context.Context, clusterNodeID uint64, now time.Time) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.EnrollmentProfile{}).
		Where("cluster_node_id = ? AND assignment_mode = ? AND status = ? AND expires_at > ? AND used_count < max_uses",
			clusterNodeID, model.EnrollmentModeCluster, model.EnrollmentStatusActive, now).
		Count(&count).Error
	return count, err
}

func (r *EnrollmentRepo) RevokeProfile(ctx context.Context, id uint64) error {
	res := r.db.WithContext(ctx).Model(&model.EnrollmentProfile{}).
		Where("id = ? AND status = ?", id, model.EnrollmentStatusActive).
		Update("status", model.EnrollmentStatusRevoked)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		var count int64
		if err := r.db.WithContext(ctx).Model(&model.EnrollmentProfile{}).Where("id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return errs.ErrNotFound
		}
	}
	return nil
}

// DeleteProfile removes the profile and its enrollment claim records in one
// transaction. The independently issued Edge identities are not deleted.
func (r *EnrollmentRepo) DeleteProfile(ctx context.Context, id uint64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var profile model.EnrollmentProfile
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id").
			Where("id = ?", id).
			First(&profile).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errs.ErrNotFound
			}
			return err
		}
		if err := tx.Where("profile_id = ?", id).Delete(&model.Enrollment{}).Error; err != nil {
			return err
		}
		return tx.Delete(&profile).Error
	})
}

func (r *EnrollmentRepo) Claim(
	ctx context.Context,
	tokenHash, hostFingerprint, sourceIP string,
	candidate *model.Edge,
	now time.Time,
) (*model.EnrollmentProfile, *model.Enrollment, *model.Edge, bool, error) {
	if candidate == nil {
		return nil, nil, nil, false, errs.ErrInvalid
	}
	var (
		claimedProfile    model.EnrollmentProfile
		claimedEnrollment model.Enrollment
		claimedEdge       model.Edge
		created           bool
	)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("token_hash = ?", tokenHash).
			First(&claimedProfile).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errs.ErrUnauthorized
			}
			return err
		}
		if claimedProfile.Status != model.EnrollmentStatusActive || !now.Before(claimedProfile.ExpiresAt) {
			return errs.ErrUnauthorized
		}

		err := tx.Where("profile_id = ? AND host_fingerprint = ?", claimedProfile.ID, hostFingerprint).
			First(&claimedEnrollment).Error
		switch {
		case err == nil:
			if claimedEnrollment.DeviceID != nil {
				return fmt.Errorf("%w: host already completed this enrollment", errs.ErrConflict)
			}
			if err := tx.First(&claimedEdge, claimedEnrollment.EdgeID).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.Edge{}).Where("id = ?", claimedEdge.ID).Updates(map[string]any{
				"secret_key_hash": candidate.SecretKeyHash,
				"name":            candidate.Name,
			}).Error; err != nil {
				return err
			}
			claimedEdge.SecretKeyHash = candidate.SecretKeyHash
			claimedEdge.Name = candidate.Name
			return nil
		case !errors.Is(err, gorm.ErrRecordNotFound):
			return err
		}

		if claimedProfile.UsedCount >= claimedProfile.MaxUses {
			return fmt.Errorf("%w: enrollment profile usage limit reached", errs.ErrBudgetExceeded)
		}
		if err := tx.Create(candidate).Error; err != nil {
			return err
		}
		claimedEdge = *candidate
		claimedEnrollment = model.Enrollment{
			ProfileID:       claimedProfile.ID,
			EdgeID:          candidate.ID,
			HostFingerprint: hostFingerprint,
			SourceIP:        sourceIP,
			EnrolledAt:      now,
		}
		if err := tx.Create(&claimedEnrollment).Error; err != nil {
			return err
		}
		res := tx.Model(&model.EnrollmentProfile{}).
			Where("id = ? AND used_count < max_uses", claimedProfile.ID).
			UpdateColumn("used_count", gorm.Expr("used_count + 1"))
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return fmt.Errorf("%w: enrollment profile usage limit reached", errs.ErrBudgetExceeded)
		}
		claimedProfile.UsedCount++
		created = true
		return nil
	})
	if err != nil {
		return nil, nil, nil, false, err
	}
	return &claimedProfile, &claimedEnrollment, &claimedEdge, created, nil
}

func (r *EnrollmentRepo) GetEnrollmentByEdgeID(ctx context.Context, edgeID uint64) (*model.Enrollment, *model.EnrollmentProfile, error) {
	var enrollment model.Enrollment
	if err := r.db.WithContext(ctx).Where("edge_id = ?", edgeID).First(&enrollment).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, errs.ErrNotFound
		}
		return nil, nil, err
	}
	var profile model.EnrollmentProfile
	if err := r.db.WithContext(ctx).First(&profile, enrollment.ProfileID).Error; err != nil {
		return nil, nil, err
	}
	return &enrollment, &profile, nil
}

func (r *EnrollmentRepo) Complete(ctx context.Context, enrollmentID, deviceID uint64, completedAt time.Time) error {
	res := r.db.WithContext(ctx).Model(&model.Enrollment{}).
		Where("id = ? AND (device_id IS NULL OR device_id = ?)", enrollmentID, deviceID).
		Updates(map[string]any{"device_id": deviceID, "completed_at": completedAt})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: enrollment already belongs to another device", errs.ErrConflict)
	}
	return nil
}
