// Package packetcapture exposes packet-capture task management and authenticated
// raw PCAP download for the operations UI.
package packetcapture

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	bizpacketcapture "github.com/ongridio/ongrid/internal/manager/biz/packetcapture"
	model "github.com/ongridio/ongrid/internal/manager/model/packetcapture"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

const roleViewer = "viewer"

type Handler struct {
	uc *bizpacketcapture.Usecase
}

func NewHandler(uc *bizpacketcapture.Usecase) *Handler {
	return &Handler{uc: uc}
}

func (h *Handler) Register(r chi.Router) {
	r.Get("/v1/packet-capture-sessions", h.listSessions)
	r.Get("/v1/packet-capture-sessions/{publicID}", h.getSession)
	r.Get("/v1/packet-captures", h.list)
	r.Get("/v1/packet-captures/artifacts/{artifactID}", h.getArtifact)
	r.Get("/v1/packet-captures/{id}", h.get)
	r.Get("/v1/packet-captures/{id}/download", h.download)
	r.With(h.requireWriter).Post("/v1/packet-captures", h.create)
	r.With(h.requireWriter).Post("/v1/packet-captures/{id}/refresh", h.refresh)
	r.With(h.requireWriter).Post("/v1/packet-captures/{id}/cancel", h.cancel)
	r.With(h.requireWriter).Post("/v1/packet-captures/{id}/stop", h.stop)
	r.With(h.requireWriter).Post("/v1/packet-capture-sessions", h.createSession)
	r.With(h.requireWriter).Post("/v1/packet-capture-sessions/{publicID}/refresh", h.refreshSession)
	r.With(h.requireWriter).Post("/v1/packet-capture-sessions/{publicID}/cancel", h.cancelSession)
	r.With(h.requireWriter).Post("/v1/packet-capture-sessions/{publicID}/stop", h.stopSession)
}

// @Summary Get packet capture artifact
// @Router /api/v1/packet-captures/artifacts/{artifactID} [get]
// @Success 200 {object} captureDTO
func (h *Handler) getArtifact(w http.ResponseWriter, r *http.Request) {
	if !h.authed(w, r) {
		return
	}
	capture, err := h.uc.GetArtifact(r.Context(), chi.URLParam(r, "artifactID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDTO(capture))
}

func (h *Handler) RegisterInternal(r chi.Router) {
	r.Get("/internal/packet-captures/{id}/download", h.downloadWithArtifactToken)
}

func (h *Handler) requireWriter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t, ok := tenantctx.From(r.Context())
		if !ok {
			writeErr(w, errs.ErrUnauthorized)
			return
		}
		if t.Role == roleViewer {
			writeErr(w, errs.ErrForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type captureDTO struct {
	ID               uint64     `json:"id"`
	CreatedBy        uint64     `json:"created_by"`
	Source           string     `json:"source"`
	State            string     `json:"state"`
	EdgeID           uint64     `json:"edge_id"`
	DeviceID         uint64     `json:"device_id"`
	SessionID        uint64     `json:"session_id,omitempty"`
	InterfaceName    string     `json:"interface_name"`
	NetworkNamespace string     `json:"network_namespace,omitempty"`
	CanonicalFilter  string     `json:"canonical_filter"`
	Direction        string     `json:"direction"`
	Format           string     `json:"format"`
	Promiscuous      bool       `json:"promiscuous"`
	DurationSeconds  uint32     `json:"duration_seconds"`
	MaxBytes         uint64     `json:"max_bytes"`
	MaxPackets       uint64     `json:"max_packets"`
	Snaplen          uint32     `json:"snaplen"`
	Title            string     `json:"title"`
	Description      string     `json:"description"`
	CapturedBytes    uint64     `json:"captured_bytes"`
	CapturedPackets  uint64     `json:"captured_packets"`
	LivePreview      []string   `json:"live_preview,omitempty"`
	ArtifactID       string     `json:"artifact_id,omitempty"`
	RawAvailable     bool       `json:"raw_available"`
	Analysis         any        `json:"analysis,omitempty"`
	ErrorCode        string     `json:"error_code,omitempty"`
	ErrorDetail      string     `json:"error_detail,omitempty"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type createReq struct {
	DeviceID              uint64 `json:"device_id"`
	Interface             string `json:"interface"`
	NetworkNamespace      string `json:"network_namespace"`
	Filter                string `json:"filter"`
	DurationSeconds       int    `json:"duration_seconds"`
	MaxBytes              int64  `json:"max_bytes"`
	MaxPackets            int    `json:"max_packets"`
	Snaplen               int    `json:"snaplen"`
	Promiscuous           bool   `json:"promiscuous"`
	Title                 string `json:"title"`
	Description           string `json:"description"`
	RequestIdempotencyKey string `json:"request_idempotency_key"`
}

type sessionTargetReq struct {
	DeviceID         uint64 `json:"device_id"`
	Interface        string `json:"interface"`
	NetworkNamespace string `json:"network_namespace"`
}
type createSessionReq struct {
	Targets         []sessionTargetReq `json:"targets"`
	Filter          string             `json:"filter"`
	DurationSeconds int                `json:"duration_seconds"`
	MaxBytes        int64              `json:"max_bytes"`
	MaxPackets      int                `json:"max_packets"`
	Snaplen         int                `json:"snaplen"`
	Promiscuous     bool               `json:"promiscuous"`
	Title           string             `json:"title"`
	Description     string             `json:"description"`
}

type sessionDTO struct {
	ID              string    `json:"id"`
	Source          string    `json:"source"`
	PCAPCount       int       `json:"pcap_count"`
	State           string    `json:"state"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	CanonicalFilter string    `json:"canonical_filter"`
	DurationSeconds uint32    `json:"duration_seconds"`
	PlannedStartAt  time.Time `json:"planned_start_at"`
	ClockQuality    string    `json:"clock_quality"`
	Analysis        any       `json:"analysis,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type sessionAnalysisDTO struct {
	Summary bizpacketcapture.SessionSummary `json:"summary"`
	Flows   []bizpacketcapture.SessionFlow  `json:"flows"`
}

func toSessionDTO(s *model.Session) sessionDTO {
	if s == nil {
		return sessionDTO{}
	}
	dto := sessionDTO{ID: s.PublicID, Source: s.Source, State: s.State, Title: s.Title, Description: s.Description, CanonicalFilter: s.CanonicalFilter, DurationSeconds: s.DurationSecs, PlannedStartAt: s.PlannedStartAt, ClockQuality: s.ClockQuality, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt}
	return dto
}

func toSessionAnalysisDTO(analysis bizpacketcapture.SessionAnalysis) sessionAnalysisDTO {
	return sessionAnalysisDTO{Summary: analysis.Summary, Flows: analysis.Flows}
}

func toSessionListDTO(s *model.Session) sessionDTO {
	dto := toSessionDTO(s)
	if s == nil {
		return dto
	}
	if s.AnalysisJSON != "" {
		var analysis struct {
			Summary bizpacketcapture.SessionSummary `json:"summary"`
		}
		// A malformed stored analysis must not make the session list unavailable.
		if err := json.Unmarshal([]byte(s.AnalysisJSON), &analysis); err == nil && analysis.Summary != (bizpacketcapture.SessionSummary{}) {
			dto.Analysis = analysis
		}
	}
	return dto
}

func toDTO(c *model.Capture) captureDTO {
	return toDTOWithAnalysis(c, true)
}

func toSessionMemberDTO(c *model.Capture) captureDTO {
	return toDTOWithAnalysis(c, false)
}

func toDTOWithAnalysis(c *model.Capture, includeAnalysis bool) captureDTO {
	if c == nil {
		return captureDTO{}
	}
	dto := captureDTO{
		ID:               c.ID,
		CreatedBy:        c.CreatedBy,
		Source:           c.Source,
		State:            c.State,
		EdgeID:           c.EdgeID,
		DeviceID:         c.DeviceID,
		SessionID:        c.SessionID,
		InterfaceName:    c.InterfaceName,
		NetworkNamespace: c.NetworkNamespace,
		CanonicalFilter:  c.CanonicalFilter,
		Direction:        c.Direction,
		Format:           c.Format,
		Promiscuous:      c.Promiscuous,
		DurationSeconds:  c.DurationSecs,
		MaxBytes:         c.MaxBytes,
		MaxPackets:       c.MaxPackets,
		Snaplen:          c.Snaplen,
		Title:            c.Title,
		Description:      c.Description,
		CapturedBytes:    c.CapturedBytes,
		CapturedPackets:  c.CapturedPackets,
		LivePreview:      livePreview(c.LivePreviewJSON),
		ArtifactID:       c.ArtifactID,
		RawAvailable:     c.RawObjectKey != "",
		ErrorCode:        c.ErrorCode,
		ErrorDetail:      c.ErrorDetail,
		StartedAt:        c.StartedAt,
		FinishedAt:       c.FinishedAt,
		CreatedAt:        c.CreatedAt,
		UpdatedAt:        c.UpdatedAt,
	}
	if includeAnalysis && c.ParsedJSON != "" {
		var analysis any
		if err := json.Unmarshal([]byte(c.ParsedJSON), &analysis); err == nil {
			dto.Analysis = analysis
		}
	}
	return dto
}

func livePreview(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var preview []string
	if err := json.Unmarshal([]byte(raw), &preview); err != nil {
		return nil
	}
	return preview
}

// @Summary List packet capture sessions
// @Router /api/v1/packet-capture-sessions [get]
// @Success 200 {object} map[string]interface{}
func (h *Handler) listSessions(w http.ResponseWriter, r *http.Request) {
	if !h.authed(w, r) {
		return
	}
	items, total, err := h.uc.ListSessions(r.Context(), atoiDefault(r.URL.Query().Get("limit"), 50), atoiDefault(r.URL.Query().Get("offset"), 0))
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]sessionDTO, 0, len(items))
	sessionIDs := make([]uint64, 0, len(items))
	for _, item := range items {
		sessionIDs = append(sessionIDs, item.ID)
	}
	counts, err := h.uc.CountSessionCaptures(r.Context(), sessionIDs)
	if err != nil {
		writeErr(w, err)
		return
	}
	for _, item := range items {
		dto := toSessionListDTO(item)
		dto.PCAPCount = counts[item.ID]
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "total": total})
}

// @Summary Get packet capture session
// @Router /api/v1/packet-capture-sessions/{publicID} [get]
// @Success 200 {object} map[string]interface{}
func (h *Handler) getSession(w http.ResponseWriter, r *http.Request) {
	if !h.authed(w, r) {
		return
	}
	detail, err := h.uc.GetSession(r.Context(), chi.URLParam(r, "publicID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	members := make([]captureDTO, 0, len(detail.Captures))
	for _, capture := range detail.Captures {
		members = append(members, toSessionMemberDTO(capture))
	}
	dto := toSessionDTO(detail.Session)
	dto.PCAPCount = len(detail.Captures)
	dto.Analysis = toSessionAnalysisDTO(detail.Analysis)
	writeJSON(w, http.StatusOK, map[string]any{"session": dto, "captures": members})
}

// @Summary List packet captures
// @Router /api/v1/packet-captures [get]
// @Success 200 {object} map[string]interface{}
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	if !h.authed(w, r) {
		return
	}
	q := r.URL.Query()
	items, total, err := h.uc.List(r.Context(), bizpacketcapture.ListFilter{
		DeviceID: uint64Param(q.Get("device_id")),
		EdgeID:   uint64Param(q.Get("edge_id")),
		State:    q.Get("state"),
		Limit:    atoiDefault(q.Get("limit"), 50),
		Offset:   atoiDefault(q.Get("offset"), 0),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]captureDTO, 0, len(items))
	for _, item := range items {
		out = append(out, toDTO(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "total": total})
}

// @Summary Get packet capture
// @Router /api/v1/packet-captures/{id} [get]
// @Success 200 {object} captureDTO
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	if !h.authed(w, r) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	capture, err := h.uc.Get(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDTO(capture))
}

// @Summary Download raw packet capture
// @Router /api/v1/packet-captures/{id}/download [get]
// @Success 200 {file} file
func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	if !h.authed(w, r) {
		return
	}
	h.writeRawObject(w, r)
}

func (h *Handler) downloadWithArtifactToken(w http.ResponseWriter, r *http.Request) {
	authz := h.uc.RawDownloadAuthorizer()
	if authz == nil {
		writeErr(w, errs.ErrNotWiredYet)
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	capture, raw, err := h.uc.RawObject(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if err := authz.VerifyDownloadToken(token, id, raw.SHA256Hex, time.Now().UTC()); err != nil {
		writeErr(w, err)
		return
	}
	h.writeRaw(w, capture, raw)
}

func (h *Handler) writeRawObject(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	capture, raw, err := h.uc.RawObject(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	h.writeRaw(w, capture, raw)
}

func (h *Handler) writeRaw(w http.ResponseWriter, capture *model.Capture, raw bizpacketcapture.RawObject) {
	filename := fmt.Sprintf("%s.pcap", safeArtifactFilename(capture.ArtifactID, capture.ID))
	w.Header().Set("Content-Type", "application/vnd.tcpdump.pcap")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.FormatUint(raw.SizeBytes, 10))
	w.WriteHeader(http.StatusOK)
	if _, writeErr := w.Write(raw.Data); writeErr != nil {
		return
	}
}

// @Summary Create packet capture
// @Router /api/v1/packet-captures [post]
// @Success 201 {object} captureDTO
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	t, _ := tenantctx.From(r.Context())
	var in createReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	out, err := h.uc.CreateSession(r.Context(), bizpacketcapture.CreateSessionInput{
		Targets:         []bizpacketcapture.SessionTarget{{DeviceID: in.DeviceID, Interface: in.Interface, NetworkNamespace: in.NetworkNamespace}},
		Filter:          in.Filter,
		DurationSeconds: in.DurationSeconds,
		MaxBytes:        in.MaxBytes,
		MaxPackets:      in.MaxPackets,
		Snaplen:         in.Snaplen,
		Promiscuous:     in.Promiscuous,
		Title:           in.Title,
		Description:     in.Description,
		Source:          bizpacketcapture.SourceAPI,
		CreatedBy:       t.UserID,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if len(out.Captures) == 0 {
		writeErr(w, noCreatedMembersError(out.MemberErrors))
		return
	}
	writeJSON(w, http.StatusCreated, toDTO(out.Captures[0]))
}

func noCreatedMembersError(memberErrors []string) error {
	if len(memberErrors) == 0 {
		return fmt.Errorf("packet capture session has no created members")
	}
	return fmt.Errorf("packet capture session has no created members: %s", strings.Join(memberErrors, "; "))
}

// @Summary Create multi-edge packet capture session
// @Router /api/v1/packet-capture-sessions [post]
// @Success 201 {object} map[string]interface{}
func (h *Handler) createSession(w http.ResponseWriter, r *http.Request) {
	t, _ := tenantctx.From(r.Context())
	var in createSessionReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	targets := make([]bizpacketcapture.SessionTarget, 0, len(in.Targets))
	for _, target := range in.Targets {
		targets = append(targets, bizpacketcapture.SessionTarget{DeviceID: target.DeviceID, Interface: target.Interface, NetworkNamespace: target.NetworkNamespace})
	}
	out, err := h.uc.CreateSession(r.Context(), bizpacketcapture.CreateSessionInput{Targets: targets, Filter: in.Filter, DurationSeconds: in.DurationSeconds, MaxBytes: in.MaxBytes, MaxPackets: in.MaxPackets, Snaplen: in.Snaplen, Promiscuous: in.Promiscuous, Title: in.Title, Description: in.Description, Source: bizpacketcapture.SourceAPI, CreatedBy: t.UserID})
	if err != nil {
		writeErr(w, err)
		return
	}
	members := make([]captureDTO, 0, len(out.Captures))
	for _, capture := range out.Captures {
		members = append(members, toDTO(capture))
	}
	writeJSON(w, http.StatusCreated, map[string]any{"session": toSessionDTO(out.Session), "captures": members, "member_errors": out.MemberErrors})
}

// @Summary Refresh packet capture state
// @Router /api/v1/packet-captures/{id}/refresh [post]
// @Success 200 {object} captureDTO
func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	capture, err := h.uc.Refresh(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDTO(capture))
}

// @Summary Cancel packet capture
// @Router /api/v1/packet-captures/{id}/cancel [post]
// @Success 200 {object} captureDTO
func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	capture, err := h.uc.Cancel(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDTO(capture))
}

// @Summary Stop packet capture and retain collected packets
// @Router /api/v1/packet-captures/{id}/stop [post]
// @Success 200 {object} captureDTO
func (h *Handler) stop(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	capture, err := h.uc.Stop(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDTO(capture))
}

// @Summary Refresh packet capture session
// @Router /api/v1/packet-capture-sessions/{publicID}/refresh [post]
// @Success 200 {object} map[string]interface{}
func (h *Handler) refreshSession(w http.ResponseWriter, r *http.Request) {
	detail, err := h.uc.RefreshSession(r.Context(), chi.URLParam(r, "publicID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	members := make([]captureDTO, 0, len(detail.Captures))
	for _, capture := range detail.Captures {
		members = append(members, toSessionMemberDTO(capture))
	}
	dto := toSessionDTO(detail.Session)
	dto.PCAPCount = len(detail.Captures)
	dto.Analysis = toSessionAnalysisDTO(detail.Analysis)
	writeJSON(w, http.StatusOK, map[string]any{"session": dto, "captures": members})
}

// @Summary Cancel packet capture session
// @Router /api/v1/packet-capture-sessions/{publicID}/cancel [post]
// @Success 200 {object} sessionDTO
func (h *Handler) cancelSession(w http.ResponseWriter, r *http.Request) {
	if !h.authed(w, r) {
		return
	}
	detail, err := h.uc.CancelSession(r.Context(), chi.URLParam(r, "publicID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	dto := toSessionDTO(detail.Session)
	dto.PCAPCount = len(detail.Captures)
	dto.Analysis = toSessionAnalysisDTO(detail.Analysis)
	writeJSON(w, http.StatusOK, dto)
}

// @Summary Stop packet capture session and retain collected packets
// @Router /api/v1/packet-capture-sessions/{publicID}/stop [post]
// @Success 200 {object} sessionDTO
func (h *Handler) stopSession(w http.ResponseWriter, r *http.Request) {
	if !h.authed(w, r) {
		return
	}
	detail, err := h.uc.StopSession(r.Context(), chi.URLParam(r, "publicID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	dto := toSessionDTO(detail.Session)
	dto.PCAPCount = len(detail.Captures)
	dto.Analysis = toSessionAnalysisDTO(detail.Analysis)
	writeJSON(w, http.StatusOK, dto)
}

func (h *Handler) authed(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := tenantctx.From(r.Context()); !ok {
		writeErr(w, errs.ErrUnauthorized)
		return false
	}
	return true
}

func pathID(r *http.Request) (uint64, error) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return 0, errors.Join(errs.ErrInvalid, err)
	}
	return id, nil
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func uint64Param(s string) uint64 {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func safeArtifactFilename(artifactID string, id uint64) string {
	name := artifactID
	if name == "" {
		name = fmt.Sprintf("pcap-%d", id)
	}
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return fmt.Sprintf("pcap-%d", id)
	}
	return string(out)
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func writeErr(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(errs.HTTPStatus(err))
	if encErr := json.NewEncoder(w).Encode(errorBody{Error: err.Error(), Code: errCode(err)}); encErr != nil {
		http.Error(w, encErr.Error(), http.StatusInternalServerError)
	}
}

func errCode(err error) string {
	switch {
	case errors.Is(err, errs.ErrNotFound):
		return "not-found"
	case errors.Is(err, errs.ErrUnauthorized):
		return "unauthorized"
	case errors.Is(err, errs.ErrForbidden):
		return "forbidden"
	case errors.Is(err, errs.ErrInvalid):
		return "invalid"
	case errors.Is(err, errs.ErrNotWiredYet):
		return "not-wired-yet"
	case errors.Is(err, errs.ErrConflict):
		return "conflict"
	default:
		return "internal"
	}
}
