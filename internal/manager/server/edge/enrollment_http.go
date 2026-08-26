package edge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	biz "github.com/ongridio/ongrid/internal/manager/biz/edge"
	model "github.com/ongridio/ongrid/internal/manager/model/edge"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

const maxEnrollmentBodyBytes = 32 << 10

type EnrollmentHTTPService interface {
	CreateProfile(ctx context.Context, in biz.CreateEnrollmentProfileInput) (*biz.CreateEnrollmentProfileResult, error)
	ListProfiles(ctx context.Context, filter biz.EnrollmentProfileListFilter) ([]*model.EnrollmentProfile, int64, error)
	RevokeProfile(ctx context.Context, id uint64) error
	DeleteProfile(ctx context.Context, id uint64) error
	Enroll(ctx context.Context, in biz.EnrollInput) (*biz.EnrollResult, error)
}

type EnrollmentHandler struct {
	svc   EnrollmentHTTPService
	authz AuthzMW
	slots chan struct{}
}

func NewEnrollmentHandler(svc EnrollmentHTTPService) *EnrollmentHandler {
	return &EnrollmentHandler{svc: svc, slots: make(chan struct{}, 64)}
}

func (h *EnrollmentHandler) SetAuthz(authz AuthzMW) { h.authz = authz }

func (h *EnrollmentHandler) RegisterProtected(r chi.Router) {
	write := h.requireAdmin
	destroy := h.requireAdmin
	if h.authz != nil {
		write = h.authz.Require("edge:*", "write")
		destroy = h.authz.Require("edge:*", "delete")
	}
	r.With(write).Post("/v1/edge-enrollment-profiles", h.createProfile)
	r.With(write).Get("/v1/edge-enrollment-profiles", h.listProfiles)
	r.With(write).Post("/v1/edge-enrollment-profiles/{id}/revoke", h.revokeProfile)
	r.With(destroy).Delete("/v1/edge-enrollment-profiles/{id}", h.deleteProfile)
}

// RegisterInternal exposes only the capability-token bootstrap endpoint. It
// must be wired outside JWT middleware; the Bearer enrollment token is its
// authentication mechanism.
func (h *EnrollmentHandler) RegisterInternal(r chi.Router) {
	r.Post("/internal/edge/enroll", h.enroll)
}

func (h *EnrollmentHandler) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant, ok := tenantctx.From(r.Context())
		if !ok {
			writeErr(w, errs.ErrUnauthorized)
			return
		}
		if tenant.Role != roleAdmin {
			writeErr(w, errs.ErrForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type createEnrollmentProfileRequest struct {
	Name           string  `json:"name"`
	AssignmentMode string  `json:"assignment_mode"`
	ClusterNodeID  *uint64 `json:"cluster_node_id,omitempty"`
	ExpiresInHours int     `json:"expires_in_hours"`
	MaxUses        int     `json:"max_uses"`
}

type enrollmentProfileResponse struct {
	ID             uint64    `json:"id"`
	Name           string    `json:"name"`
	AssignmentMode string    `json:"assignment_mode"`
	ClusterNodeID  *uint64   `json:"cluster_node_id,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
	MaxUses        int       `json:"max_uses"`
	UsedCount      int       `json:"used_count"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}

type createEnrollmentProfileResponse struct {
	Profile         enrollmentProfileResponse `json:"profile"`
	EnrollmentToken string                    `json:"enrollment_token"`
}

type listEnrollmentProfilesResponse struct {
	Items    []enrollmentProfileResponse `json:"items"`
	Total    int64                       `json:"total"`
	Page     int                         `json:"page"`
	PageSize int                         `json:"page_size"`
}

// createProfile godoc
// @Summary 创建非 Kubernetes Edge 批量安装配置
// @Success 201 {object} createEnrollmentProfileResponse
// @Router /api/v1/edge-enrollment-profiles [post]
func (h *EnrollmentHandler) createProfile(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantctx.From(r.Context())
	if !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	var req createEnrollmentProfileRequest
	if err := decodeEnrollmentJSON(w, r, &req); err != nil {
		writeErr(w, err)
		return
	}
	uid := tenant.UserID
	result, err := h.svc.CreateProfile(r.Context(), biz.CreateEnrollmentProfileInput{
		Name:           req.Name,
		AssignmentMode: req.AssignmentMode,
		ClusterNodeID:  req.ClusterNodeID,
		ExpiresInHours: req.ExpiresInHours,
		MaxUses:        req.MaxUses,
		CreatedBy:      &uid,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, createEnrollmentProfileResponse{
		Profile:         enrollmentProfileDTO(result.Profile, time.Now().UTC()),
		EnrollmentToken: result.Token,
	})
}

// listProfiles godoc
// @Summary 列出非 Kubernetes Edge 批量安装配置
// @Success 200 {object} listEnrollmentProfilesResponse
// @Router /api/v1/edge-enrollment-profiles [get]
func (h *EnrollmentHandler) listProfiles(w http.ResponseWriter, r *http.Request) {
	page := positiveQueryInt(r, "page", 1)
	pageSize := positiveQueryInt(r, "page_size", 20)
	if pageSize > 100 {
		pageSize = 100
	}
	profiles, total, err := h.svc.ListProfiles(r.Context(), biz.EnrollmentProfileListFilter{
		Limit:  pageSize,
		Offset: (page - 1) * pageSize,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	now := time.Now().UTC()
	items := make([]enrollmentProfileResponse, 0, len(profiles))
	for _, profile := range profiles {
		items = append(items, enrollmentProfileDTO(profile, now))
	}
	writeJSON(w, http.StatusOK, listEnrollmentProfilesResponse{Items: items, Total: total, Page: page, PageSize: pageSize})
}

// revokeProfile godoc
// @Summary 撤销非 Kubernetes Edge 批量安装配置
// @Success 204
// @Router /api/v1/edge-enrollment-profiles/{id}/revoke [post]
func (h *EnrollmentHandler) revokeProfile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id == 0 {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	if err := h.svc.RevokeProfile(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteProfile godoc
// @Summary 删除非 Kubernetes Edge 批量安装配置
// @Success 204
// @Router /api/v1/edge-enrollment-profiles/{id} [delete]
func (h *EnrollmentHandler) deleteProfile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id == 0 {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	if err := h.svc.DeleteProfile(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type enrollEdgeRequest struct {
	HostInfo     tunnel.HostInfo `json:"host_info"`
	AgentVersion string          `json:"agent_version,omitempty"`
}

type enrollEdgeResponse struct {
	EdgeID           uint64 `json:"edge_id"`
	AccessKey        string `json:"access_key"`
	SecretKey        string `json:"secret_key"`
	CloudAddr        string `json:"cloud_addr"`
	ManagerPublicURL string `json:"manager_public_url"`
}

// enroll godoc
// @Summary 使用安装令牌领取独立 Edge 凭证
// @Success 201 {object} enrollEdgeResponse
// @Router /internal/edge/enroll [post]
func (h *EnrollmentHandler) enroll(w http.ResponseWriter, r *http.Request) {
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		writeErr(w, errs.ErrTooManyAttempts)
		return
	}
	token, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	var req enrollEdgeRequest
	if err := decodeEnrollmentJSON(w, r, &req); err != nil {
		writeErr(w, err)
		return
	}
	result, err := h.svc.Enroll(r.Context(), biz.EnrollInput{
		Token:        token,
		HostInfo:     req.HostInfo,
		AgentVersion: req.AgentVersion,
		SourceIP:     requestSourceIP(r),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, enrollEdgeResponse{
		EdgeID:           result.EdgeID,
		AccessKey:        result.AccessKey,
		SecretKey:        result.SecretKey,
		CloudAddr:        result.CloudAddr,
		ManagerPublicURL: result.ManagerPublicURL,
	})
}

func enrollmentProfileDTO(profile *model.EnrollmentProfile, now time.Time) enrollmentProfileResponse {
	return enrollmentProfileResponse{
		ID:             profile.ID,
		Name:           profile.Name,
		AssignmentMode: profile.AssignmentMode,
		ClusterNodeID:  profile.ClusterNodeID,
		ExpiresAt:      profile.ExpiresAt,
		MaxUses:        profile.MaxUses,
		UsedCount:      profile.UsedCount,
		Status:         biz.EnrollmentProfileEffectiveStatus(profile, now),
		CreatedAt:      profile.CreatedAt,
	}
}

func decodeEnrollmentJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxEnrollmentBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return errors.Join(errs.ErrInvalid, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.Join(errs.ErrInvalid, err)
	}
	return nil
}

func positiveQueryInt(r *http.Request, key string, fallback int) int {
	value, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func bearerToken(raw string) (string, bool) {
	parts := strings.Fields(raw)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
		return "", false
	}
	return parts[1], true
}

func requestSourceIP(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(value) != nil {
		return value
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && net.ParseIP(host) != nil {
		return host
	}
	return ""
}
