package edge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/edge"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

const (
	enrollmentTokenEntropyBytes = 32
	minEnrollmentHours          = 1
	maxEnrollmentHours          = 24 * 7
	maxEnrollmentUses           = 10000
)

type EnrollmentClusterAssigner interface {
	ValidateEnrollmentCluster(ctx context.Context, clusterNodeID uint64) error
	AssignEnrollmentDevice(ctx context.Context, clusterNodeID, deviceNodeID, profileID, deviceID uint64) error
}

type EnrollmentConfig struct {
	PublicURL  string
	TunnelAddr string
}

type EnrollmentUsecase struct {
	repo     EnrollmentRepo
	clusters EnrollmentClusterAssigner
	edges    *Usecase
	config   EnrollmentConfig
	log      *slog.Logger
}

func NewEnrollmentUsecase(repo EnrollmentRepo, clusters EnrollmentClusterAssigner, edges *Usecase, cfg EnrollmentConfig, log *slog.Logger) *EnrollmentUsecase {
	return &EnrollmentUsecase{repo: repo, clusters: clusters, edges: edges, config: cfg, log: log}
}

type CreateEnrollmentProfileInput struct {
	Name           string
	AssignmentMode string
	ClusterNodeID  *uint64
	ExpiresInHours int
	MaxUses        int
	CreatedBy      *uint64
}

type CreateEnrollmentProfileResult struct {
	Profile *model.EnrollmentProfile
	Token   string
}

func (u *EnrollmentUsecase) CreateProfile(ctx context.Context, in CreateEnrollmentProfileInput) (*CreateEnrollmentProfileResult, error) {
	if u.repo == nil {
		return nil, errs.ErrNotWiredYet
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 128 {
		return nil, fmt.Errorf("%w: profile name is required and must not exceed 128 characters", errs.ErrInvalid)
	}
	if in.ExpiresInHours < minEnrollmentHours || in.ExpiresInHours > maxEnrollmentHours {
		return nil, fmt.Errorf("%w: expires_in_hours must be between %d and %d", errs.ErrInvalid, minEnrollmentHours, maxEnrollmentHours)
	}
	if in.MaxUses < 1 || in.MaxUses > maxEnrollmentUses {
		return nil, fmt.Errorf("%w: max_uses must be between 1 and %d", errs.ErrInvalid, maxEnrollmentUses)
	}
	switch in.AssignmentMode {
	case model.EnrollmentModeBatchOnly:
		if in.ClusterNodeID != nil {
			return nil, fmt.Errorf("%w: batch_only profile cannot set cluster_node_id", errs.ErrInvalid)
		}
	case model.EnrollmentModeCluster:
		if in.ClusterNodeID == nil || *in.ClusterNodeID == 0 {
			return nil, fmt.Errorf("%w: cluster profile requires cluster_node_id", errs.ErrInvalid)
		}
		if u.clusters == nil {
			return nil, errs.ErrNotWiredYet
		}
		if err := u.clusters.ValidateEnrollmentCluster(ctx, *in.ClusterNodeID); err != nil {
			return nil, fmt.Errorf("validate enrollment cluster: %w", err)
		}
	default:
		return nil, fmt.Errorf("%w: assignment_mode must be batch_only or cluster", errs.ErrInvalid)
	}

	rawToken, err := randomURLSafe(enrollmentTokenEntropyBytes)
	if err != nil {
		return nil, fmt.Errorf("generate enrollment token: %w", err)
	}
	token := "oen_" + rawToken
	profile := &model.EnrollmentProfile{
		Name:           in.Name,
		AssignmentMode: in.AssignmentMode,
		ClusterNodeID:  in.ClusterNodeID,
		TokenHash:      enrollmentTokenHash(token),
		ExpiresAt:      time.Now().UTC().Add(time.Duration(in.ExpiresInHours) * time.Hour),
		MaxUses:        in.MaxUses,
		Status:         model.EnrollmentStatusActive,
		CreatedBy:      in.CreatedBy,
	}
	if err := u.repo.CreateProfile(ctx, profile); err != nil {
		return nil, fmt.Errorf("create enrollment profile: %w", err)
	}
	if u.log != nil {
		u.log.Info("edge enrollment profile created",
			slog.Uint64("profile_id", profile.ID),
			slog.String("assignment_mode", profile.AssignmentMode),
			slog.Int("max_uses", profile.MaxUses),
			slog.Time("expires_at", profile.ExpiresAt))
	}
	return &CreateEnrollmentProfileResult{Profile: profile, Token: token}, nil
}

func (u *EnrollmentUsecase) ListProfiles(ctx context.Context, filter EnrollmentProfileListFilter) ([]*model.EnrollmentProfile, int64, error) {
	if u.repo == nil {
		return nil, 0, errs.ErrNotWiredYet
	}
	return u.repo.ListProfiles(ctx, filter)
}

// ValidateClusterDelete refuses to orphan a still-usable installation command.
func (u *EnrollmentUsecase) ValidateClusterDelete(ctx context.Context, clusterNodeID uint64) error {
	if u == nil || u.repo == nil {
		return errs.ErrNotWiredYet
	}
	if clusterNodeID == 0 {
		return errs.ErrInvalid
	}
	count, err := u.repo.CountActiveProfilesForCluster(ctx, clusterNodeID, time.Now())
	if err != nil {
		return fmt.Errorf("count active enrollment profiles for cluster: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("%w: cluster %d still has %d active enrollment profile(s)", errs.ErrConflict, clusterNodeID, count)
	}
	return nil
}

func (u *EnrollmentUsecase) RevokeProfile(ctx context.Context, id uint64) error {
	if u.repo == nil {
		return errs.ErrNotWiredYet
	}
	if id == 0 {
		return errs.ErrInvalid
	}
	if err := u.repo.RevokeProfile(ctx, id); err != nil {
		return fmt.Errorf("revoke enrollment profile: %w", err)
	}
	if u.log != nil {
		u.log.Info("edge enrollment profile revoked", slog.Uint64("profile_id", id))
	}
	return nil
}

// DeleteProfile permanently removes an installation profile and its claim
// records. Edge identities and devices already created from the profile are
// intentionally retained.
func (u *EnrollmentUsecase) DeleteProfile(ctx context.Context, id uint64) error {
	if u.repo == nil {
		return errs.ErrNotWiredYet
	}
	if id == 0 {
		return errs.ErrInvalid
	}
	if err := u.repo.DeleteProfile(ctx, id); err != nil {
		return fmt.Errorf("delete enrollment profile: %w", err)
	}
	if u.log != nil {
		u.log.Info("edge enrollment profile deleted", slog.Uint64("profile_id", id))
	}
	return nil
}

type EnrollInput struct {
	Token        string
	HostInfo     tunnel.HostInfo
	AgentVersion string
	SourceIP     string
}

type EnrollResult struct {
	EdgeID           uint64
	AccessKey        string
	SecretKey        string
	CloudAddr        string
	ManagerPublicURL string
}

func (u *EnrollmentUsecase) Enroll(ctx context.Context, in EnrollInput) (*EnrollResult, error) {
	if u.repo == nil || u.edges == nil {
		return nil, errs.ErrNotWiredYet
	}
	in.Token = strings.TrimSpace(in.Token)
	if !strings.HasPrefix(in.Token, "oen_") || len(in.Token) < 40 {
		return nil, errs.ErrUnauthorized
	}
	if strings.TrimSpace(in.HostInfo.Hostname) == "" {
		return nil, fmt.Errorf("%w: host_info.hostname is required", errs.ErrInvalid)
	}
	if strings.TrimSpace(in.HostInfo.HardwareFingerprint) == "" && strings.TrimSpace(in.HostInfo.Fingerprint) == "" {
		return nil, fmt.Errorf("%w: a stable host fingerprint is required", errs.ErrInvalid)
	}

	tokenHash := enrollmentTokenHash(in.Token)
	profile, err := u.repo.GetProfileByTokenHash(ctx, tokenHash)
	if err != nil {
		return nil, err
	}
	if profile.Status != model.EnrollmentStatusActive || !time.Now().UTC().Before(profile.ExpiresAt) {
		return nil, errs.ErrUnauthorized
	}
	if profile.AssignmentMode == model.EnrollmentModeCluster {
		if profile.ClusterNodeID == nil || u.clusters == nil {
			return nil, errs.ErrNotWiredYet
		}
		if err := u.clusters.ValidateEnrollmentCluster(ctx, *profile.ClusterNodeID); err != nil {
			return nil, fmt.Errorf("validate enrollment cluster: %w", err)
		}
	}

	candidate, _, secretKey, err := newEdgeIdentity(strings.TrimSpace(in.HostInfo.Hostname), profile.CreatedBy)
	if err != nil {
		return nil, err
	}
	_, _, claimedEdge, created, err := u.repo.Claim(
		ctx,
		tokenHash,
		deviceFingerprint(in.HostInfo),
		strings.TrimSpace(in.SourceIP),
		candidate,
		time.Now().UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("claim enrollment profile: %w", err)
	}
	if created {
		u.edges.seedDefaultPlugins(ctx, claimedEdge.ID)
	}
	if u.log != nil {
		u.log.Info("edge enrollment claimed",
			slog.Uint64("profile_id", profile.ID),
			slog.Uint64("edge_id", claimedEdge.ID),
			slog.Bool("new_edge", created))
	}
	return &EnrollResult{
		EdgeID:           claimedEdge.ID,
		AccessKey:        claimedEdge.AccessKeyID,
		SecretKey:        secretKey,
		CloudAddr:        strings.TrimSpace(u.config.TunnelAddr),
		ManagerPublicURL: strings.TrimRight(strings.TrimSpace(u.config.PublicURL), "/"),
	}, nil
}

// Finalize is called by the existing register_edge flow after it has resolved
// the host Device and its topology node. Manual Edge identities have no
// Enrollment row and remain a no-op.
func (u *EnrollmentUsecase) Finalize(ctx context.Context, edgeID, deviceID, deviceNodeID uint64) error {
	if u.repo == nil {
		return errs.ErrNotWiredYet
	}
	enrollment, profile, err := u.repo.GetEnrollmentByEdgeID(ctx, edgeID)
	if errors.Is(err, errs.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get edge enrollment: %w", err)
	}
	if enrollment.DeviceID != nil {
		if *enrollment.DeviceID == deviceID {
			return nil
		}
		return fmt.Errorf("%w: enrollment already completed by another device", errs.ErrConflict)
	}
	if profile.AssignmentMode == model.EnrollmentModeCluster {
		if profile.ClusterNodeID == nil || deviceNodeID == 0 || u.clusters == nil {
			return fmt.Errorf("%w: cluster enrollment topology is not ready", errs.ErrNotWiredYet)
		}
		if err := u.clusters.AssignEnrollmentDevice(ctx, *profile.ClusterNodeID, deviceNodeID, profile.ID, deviceID); err != nil {
			return fmt.Errorf("assign enrolled device to cluster: %w", err)
		}
	}
	if err := u.repo.Complete(ctx, enrollment.ID, deviceID, time.Now().UTC()); err != nil {
		return fmt.Errorf("complete edge enrollment: %w", err)
	}
	return nil
}

func EnrollmentProfileEffectiveStatus(profile *model.EnrollmentProfile, now time.Time) string {
	if profile == nil {
		return ""
	}
	if profile.Status == model.EnrollmentStatusRevoked {
		return model.EnrollmentStatusRevoked
	}
	if !now.Before(profile.ExpiresAt) {
		return "expired"
	}
	if profile.UsedCount >= profile.MaxUses {
		return "exhausted"
	}
	return model.EnrollmentStatusActive
}

func enrollmentTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
