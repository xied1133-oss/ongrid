package edge

import (
	"context"
	"errors"
	"testing"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/edge"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

type enrollmentRepoStub struct {
	profile             *model.EnrollmentProfile
	edge                *model.Edge
	activeClusterCounts map[uint64]int64
}

func (r *enrollmentRepoStub) CreateProfile(context.Context, *model.EnrollmentProfile) error {
	return nil
}

func (r *enrollmentRepoStub) GetProfileByTokenHash(context.Context, string) (*model.EnrollmentProfile, error) {
	return r.profile, nil
}

func (r *enrollmentRepoStub) ListProfiles(context.Context, EnrollmentProfileListFilter) ([]*model.EnrollmentProfile, int64, error) {
	return nil, 0, nil
}

func (r *enrollmentRepoStub) CountActiveProfilesForCluster(_ context.Context, clusterNodeID uint64, _ time.Time) (int64, error) {
	return r.activeClusterCounts[clusterNodeID], nil
}

func (r *enrollmentRepoStub) RevokeProfile(context.Context, uint64) error { return nil }

func (r *enrollmentRepoStub) DeleteProfile(context.Context, uint64) error { return nil }

func (r *enrollmentRepoStub) Claim(context.Context, string, string, string, *model.Edge, time.Time) (*model.EnrollmentProfile, *model.Enrollment, *model.Edge, bool, error) {
	return r.profile, &model.Enrollment{ID: 1, ProfileID: r.profile.ID, EdgeID: r.edge.ID}, r.edge, true, nil
}

func (r *enrollmentRepoStub) GetEnrollmentByEdgeID(context.Context, uint64) (*model.Enrollment, *model.EnrollmentProfile, error) {
	return nil, nil, nil
}

func (r *enrollmentRepoStub) Complete(context.Context, uint64, uint64, time.Time) error { return nil }

func TestEnrollWithoutPluginSeederStillReturnsCredentials(t *testing.T) {
	profile := &model.EnrollmentProfile{
		ID:             1,
		AssignmentMode: model.EnrollmentModeBatchOnly,
		ExpiresAt:      time.Now().UTC().Add(time.Hour),
		MaxUses:        2,
		Status:         model.EnrollmentStatusActive,
	}
	repo := &enrollmentRepoStub{
		profile: profile,
		edge:    &model.Edge{ID: 42, AccessKeyID: "access-key"},
	}
	edges := NewUsecase(nil, nil, nil, nil)
	uc := NewEnrollmentUsecase(repo, nil, edges, EnrollmentConfig{
		PublicURL:  "https://manager.example.test",
		TunnelAddr: "manager.example.test:40012",
	}, nil)

	result, err := uc.Enroll(context.Background(), EnrollInput{
		Token: "oen_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
		HostInfo: tunnel.HostInfo{
			Hostname:            "edge-a",
			HardwareFingerprint: "hardware-fingerprint-a",
		},
	})
	if err != nil {
		t.Fatalf("Enroll() error = %v", err)
	}
	if result.EdgeID != 42 || result.AccessKey != "access-key" || result.SecretKey == "" {
		t.Fatalf("Enroll() result = %#v", result)
	}
}

func TestValidateClusterDeleteRejectsActiveEnrollmentProfile(t *testing.T) {
	repo := &enrollmentRepoStub{activeClusterCounts: map[uint64]int64{101: 1}}
	uc := NewEnrollmentUsecase(repo, nil, nil, EnrollmentConfig{}, nil)

	if err := uc.ValidateClusterDelete(context.Background(), 101); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("ValidateClusterDelete(active) error = %v, want ErrConflict", err)
	}
	if err := uc.ValidateClusterDelete(context.Background(), 102); err != nil {
		t.Fatalf("ValidateClusterDelete(empty) error = %v", err)
	}
}
