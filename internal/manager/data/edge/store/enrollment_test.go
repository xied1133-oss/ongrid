package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/edge"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

func TestEnrollmentClaimIsBoundedAndRetryable(t *testing.T) {
	edgeRepo := newTestRepo(t)
	repo := NewEnrollmentRepo(edgeRepo.db)
	ctx := context.Background()
	now := time.Now().UTC()
	profile := &model.EnrollmentProfile{
		Name:           "rack-a",
		AssignmentMode: model.EnrollmentModeBatchOnly,
		TokenHash:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ExpiresAt:      now.Add(time.Hour),
		MaxUses:        1,
		Status:         model.EnrollmentStatusActive,
	}
	if err := repo.CreateProfile(ctx, profile); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	first := &model.Edge{Name: "host-a", AccessKeyID: "access-a", SecretKeyHash: "hash-a", Status: model.StatusOffline}
	claimedProfile, enrollment, edge, created, err := repo.Claim(ctx, profile.TokenHash, "fp_host_a", "192.0.2.1", first, now)
	if err != nil {
		t.Fatalf("Claim(first): %v", err)
	}
	if !created || edge.ID == 0 || enrollment.EdgeID != edge.ID || claimedProfile.UsedCount != 1 {
		t.Fatalf("first claim = profile=%+v enrollment=%+v edge=%+v created=%v", claimedProfile, enrollment, edge, created)
	}

	retry := &model.Edge{Name: "host-a", AccessKeyID: "unused-new-access", SecretKeyHash: "hash-retry", Status: model.StatusOffline}
	claimedProfile, retryEnrollment, retryEdge, created, err := repo.Claim(ctx, profile.TokenHash, "fp_host_a", "192.0.2.1", retry, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Claim(retry): %v", err)
	}
	if created || retryEdge.ID != edge.ID || retryEdge.AccessKeyID != "access-a" || retryEdge.SecretKeyHash != "hash-retry" {
		t.Fatalf("retry claim = profile=%+v enrollment=%+v edge=%+v created=%v", claimedProfile, retryEnrollment, retryEdge, created)
	}
	if claimedProfile.UsedCount != 1 {
		t.Fatalf("retry used_count = %d, want 1", claimedProfile.UsedCount)
	}

	if err := repo.Complete(ctx, enrollment.ID, 42, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, _, _, _, err := repo.Claim(ctx, profile.TokenHash, "fp_host_a", "192.0.2.1", retry, now.Add(3*time.Minute)); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("completed retry error = %v, want ErrConflict", err)
	}
	second := &model.Edge{Name: "host-b", AccessKeyID: "access-b", SecretKeyHash: "hash-b", Status: model.StatusOffline}
	if _, _, _, _, err := repo.Claim(ctx, profile.TokenHash, "fp_host_b", "192.0.2.2", second, now.Add(3*time.Minute)); !errors.Is(err, errs.ErrBudgetExceeded) {
		t.Fatalf("quota error = %v, want ErrBudgetExceeded", err)
	}
}

func TestEnrollmentProfileRevokeStopsClaims(t *testing.T) {
	edgeRepo := newTestRepo(t)
	repo := NewEnrollmentRepo(edgeRepo.db)
	ctx := context.Background()
	profile := &model.EnrollmentProfile{
		Name:           "rack-b",
		AssignmentMode: model.EnrollmentModeBatchOnly,
		TokenHash:      "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		ExpiresAt:      time.Now().UTC().Add(time.Hour),
		MaxUses:        2,
		Status:         model.EnrollmentStatusActive,
	}
	if err := repo.CreateProfile(ctx, profile); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if err := repo.RevokeProfile(ctx, profile.ID); err != nil {
		t.Fatalf("RevokeProfile: %v", err)
	}
	candidate := &model.Edge{Name: "host", AccessKeyID: "access", SecretKeyHash: "hash", Status: model.StatusOffline}
	if _, _, _, _, err := repo.Claim(ctx, profile.TokenHash, "fp_host", "", candidate, time.Now().UTC()); !errors.Is(err, errs.ErrUnauthorized) {
		t.Fatalf("revoked claim error = %v, want ErrUnauthorized", err)
	}
}

func TestEnrollmentProfileDeleteRemovesClaimsButKeepsEdge(t *testing.T) {
	edgeRepo := newTestRepo(t)
	repo := NewEnrollmentRepo(edgeRepo.db)
	ctx := context.Background()
	now := time.Now().UTC()
	profile := &model.EnrollmentProfile{
		Name:           "rack-delete",
		AssignmentMode: model.EnrollmentModeBatchOnly,
		TokenHash:      "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		ExpiresAt:      now.Add(time.Hour),
		MaxUses:        2,
		Status:         model.EnrollmentStatusActive,
	}
	if err := repo.CreateProfile(ctx, profile); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	candidate := &model.Edge{Name: "host", AccessKeyID: "access-delete", SecretKeyHash: "hash", Status: model.StatusOffline}
	_, _, edge, _, err := repo.Claim(ctx, profile.TokenHash, "fp_delete", "", candidate, now)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := repo.DeleteProfile(ctx, profile.ID); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}

	if _, _, _, _, err := repo.Claim(ctx, profile.TokenHash, "fp_other", "", candidate, now); !errors.Is(err, errs.ErrUnauthorized) {
		t.Fatalf("deleted claim error = %v, want ErrUnauthorized", err)
	}
	var enrollmentCount int64
	if err := edgeRepo.db.Model(&model.Enrollment{}).Where("profile_id = ?", profile.ID).Count(&enrollmentCount).Error; err != nil {
		t.Fatalf("count enrollments: %v", err)
	}
	if enrollmentCount != 0 {
		t.Fatalf("enrollment count = %d, want 0", enrollmentCount)
	}
	var edgeCount int64
	if err := edgeRepo.db.Model(&model.Edge{}).Where("id = ?", edge.ID).Count(&edgeCount).Error; err != nil {
		t.Fatalf("count edge: %v", err)
	}
	if edgeCount != 1 {
		t.Fatalf("edge count = %d, want 1", edgeCount)
	}
}

func TestEnrollmentProfileExpiryStopsClaims(t *testing.T) {
	edgeRepo := newTestRepo(t)
	repo := NewEnrollmentRepo(edgeRepo.db)
	ctx := context.Background()
	now := time.Now().UTC()
	profile := &model.EnrollmentProfile{
		Name:           "expired",
		AssignmentMode: model.EnrollmentModeBatchOnly,
		TokenHash:      "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ExpiresAt:      now.Add(-time.Minute),
		MaxUses:        2,
		Status:         model.EnrollmentStatusActive,
	}
	if err := repo.CreateProfile(ctx, profile); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	candidate := &model.Edge{Name: "host", AccessKeyID: "access", SecretKeyHash: "hash", Status: model.StatusOffline}
	if _, _, _, _, err := repo.Claim(ctx, profile.TokenHash, "fp_host", "", candidate, now); !errors.Is(err, errs.ErrUnauthorized) {
		t.Fatalf("expired claim error = %v, want ErrUnauthorized", err)
	}
}

func TestCountActiveProfilesForClusterUsesEffectiveStatus(t *testing.T) {
	edgeRepo := newTestRepo(t)
	repo := NewEnrollmentRepo(edgeRepo.db)
	ctx := context.Background()
	now := time.Now().UTC()
	clusterID := uint64(501)
	profiles := []*model.EnrollmentProfile{
		{Name: "active", AssignmentMode: model.EnrollmentModeCluster, ClusterNodeID: &clusterID, TokenHash: strings.Repeat("1", 64), ExpiresAt: now.Add(time.Hour), MaxUses: 2, UsedCount: 1, Status: model.EnrollmentStatusActive},
		{Name: "expired", AssignmentMode: model.EnrollmentModeCluster, ClusterNodeID: &clusterID, TokenHash: strings.Repeat("2", 64), ExpiresAt: now.Add(-time.Minute), MaxUses: 2, Status: model.EnrollmentStatusActive},
		{Name: "exhausted", AssignmentMode: model.EnrollmentModeCluster, ClusterNodeID: &clusterID, TokenHash: strings.Repeat("3", 64), ExpiresAt: now.Add(time.Hour), MaxUses: 1, UsedCount: 1, Status: model.EnrollmentStatusActive},
		{Name: "revoked", AssignmentMode: model.EnrollmentModeCluster, ClusterNodeID: &clusterID, TokenHash: strings.Repeat("4", 64), ExpiresAt: now.Add(time.Hour), MaxUses: 2, Status: model.EnrollmentStatusRevoked},
	}
	for _, profile := range profiles {
		if err := repo.CreateProfile(ctx, profile); err != nil {
			t.Fatalf("CreateProfile(%s): %v", profile.Name, err)
		}
	}
	count, err := repo.CountActiveProfilesForCluster(ctx, clusterID, now)
	if err != nil || count != 1 {
		t.Fatalf("CountActiveProfilesForCluster() = %d, %v; want 1", count, err)
	}
}
