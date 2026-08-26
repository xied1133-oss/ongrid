// Package report is the HTTP surface for scheduled reports (HLD-014).
// Mirrors the device handler's lean pattern — the chi-mounted Handler
// talks straight to the biz Usecase, pulls the caller from tenantctx,
// and gates writes on role via requireWriter. RBAC (ADR-022):
// admin/user may CRUD schedules + trigger generation; viewer is
// read-only. The public /r/{token} share route mounts separately
// without auth.
package report

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	bizreport "github.com/ongridio/ongrid/internal/manager/biz/report"
	model "github.com/ongridio/ongrid/internal/manager/model/report"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

const roleViewer = "viewer"

// Handler serves the report API. uc is required; a nil uc makes every
// route 503 (binary built without the report stack wired).
type Handler struct {
	uc  *bizreport.Usecase
	now func() time.Time // injectable clock for tests
}

func NewHandler(uc *bizreport.Usecase) *Handler {
	return &Handler{uc: uc, now: func() time.Time { return time.Now().UTC() }}
}

// Register mounts the authenticated routes.
//
//	GET    /v1/reports                       list
//	POST   /v1/reports                       manual generate            (writer)
//	GET    /v1/reports/{id}                  detail
//	DELETE /v1/reports/{id}                  delete                     (writer)
//	POST   /v1/reports/{id}/share            mint share token           (writer)
//	GET    /v1/report-schedules              list
//	POST   /v1/report-schedules              create                     (writer)
//	GET    /v1/report-schedules/{id}         detail
//	PUT    /v1/report-schedules/{id}         update                     (writer)
//	DELETE /v1/report-schedules/{id}         delete                     (writer)
//	POST   /v1/report-schedules/{id}/toggle  enable/disable             (writer)
//	POST   /v1/report-schedules/{id}/run-now generate immediately       (writer)
func (h *Handler) Register(r chi.Router) {
	r.Get("/v1/reports", h.listReports)
	r.With(h.requireWriter).Post("/v1/reports", h.generateNow)
	r.Get("/v1/reports/{id}", h.getReport)
	r.With(h.requireWriter).Delete("/v1/reports/{id}", h.deleteReport)
	r.With(h.requireWriter).Post("/v1/reports/{id}/share", h.shareReport)

	r.Get("/v1/report-schedules", h.listSchedules)
	r.With(h.requireWriter).Post("/v1/report-schedules", h.createSchedule)
	r.Get("/v1/report-schedules/{id}", h.getSchedule)
	r.With(h.requireWriter).Put("/v1/report-schedules/{id}", h.updateSchedule)
	r.With(h.requireWriter).Delete("/v1/report-schedules/{id}", h.deleteSchedule)
	r.With(h.requireWriter).Post("/v1/report-schedules/{id}/toggle", h.toggleSchedule)
	r.With(h.requireWriter).Post("/v1/report-schedules/{id}/run-now", h.runNow)

	// HLD-022 Phase 2 — unified 任务 surface (recurring schedules ∪ oneoff tasks).
	r.Get("/v1/tasks", h.listTasks)
	r.With(h.requireWriter).Post("/v1/tasks/oneoff", h.createOneoffTask)
	r.Get("/v1/tasks/{id}", h.getTask)
	r.With(h.requireWriter).Post("/v1/tasks/{id}/run", h.rerunTask)
	r.With(h.requireWriter).Delete("/v1/tasks/{id}", h.deleteTask)
}

// RegisterPublic mounts the unauthenticated share route.
//
//	GET /r/{token}  shared report (read-only, 30d TTL)
func (h *Handler) RegisterPublic(r chi.Router) {
	r.Get("/r/{token}", h.sharedReport)
}

// requireWriter rejects viewer-role callers (ADR-022 read-only tier).
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

// --- reports ---

func (h *Handler) listReports(w http.ResponseWriter, r *http.Request) {
	if !h.authed(w, r) {
		return
	}
	q := r.URL.Query()
	f := bizreport.ReportFilter{
		Status: q.Get("status"),
		Kind:   q.Get("kind"),
		Limit:  atoiDefault(q.Get("limit"), 50),
		Offset: atoiDefault(q.Get("offset"), 0),
	}
	// schedule_id scopes the list to one schedule's reports.
	if sid := q.Get("schedule_id"); sid != "" {
		if v, err := strconv.ParseUint(sid, 10, 64); err == nil {
			f.ScheduleID = &v
		}
	}
	// task_id scopes to one task's reports (HLD-022) — the canonical 任务 detail
	// key, covering scheduled + run-now reports.
	f.TaskID = q.Get("task_id")
	rows, err := h.uc.ListReports(r.Context(), f)
	if err != nil {
		writeErr(w, err)
		return
	}
	total, err := h.uc.CountReports(r.Context(), f)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reports": toReportList(rows), "total": total})
}

func (h *Handler) getReport(w http.ResponseWriter, r *http.Request) {
	if !h.authed(w, r) {
		return
	}
	rpt, err := h.uc.GetReport(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toReportDetail(rpt))
}

func (h *Handler) deleteReport(w http.ResponseWriter, r *http.Request) {
	if err := h.uc.DeleteReport(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

type generateNowReq struct {
	Kind      string `json:"kind"`
	Timezone  string `json:"timezone"`
	ScopeJSON string `json:"scope_json"`
}

func (h *Handler) generateNow(w http.ResponseWriter, r *http.Request) {
	t, ok := tenantctx.From(r.Context())
	if !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	var req generateNowReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	if req.Kind == "" {
		req.Kind = model.KindWeekly
	}
	if req.Timezone == "" {
		req.Timezone = "UTC"
	}
	loc, err := time.LoadLocation(req.Timezone)
	if err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	period, err := bizreport.PeriodFor(req.Kind, h.now(), loc, time.Time{})
	if err != nil {
		writeErr(w, err)
		return
	}
	scope := req.ScopeJSON
	if scope == "" {
		scope = "{}"
	}
	rpt, err := h.uc.GenerateNow(r.Context(), t.UserID, req.Kind, req.Timezone, scope, localeFromRequest(r), "", period)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, toReportDetail(rpt))
}

func (h *Handler) shareReport(w http.ResponseWriter, r *http.Request) {
	token, err := h.uc.ShareReport(r.Context(), chi.URLParam(r, "id"), h.now())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"share_token": token, "path": "/r/" + token})
}

func (h *Handler) sharedReport(w http.ResponseWriter, r *http.Request) {
	rpt, err := h.uc.GetSharedReport(r.Context(), chi.URLParam(r, "token"), h.now())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toReportDetail(rpt))
}

// --- schedules ---

func (h *Handler) listSchedules(w http.ResponseWriter, r *http.Request) {
	t, ok := tenantctx.From(r.Context())
	if !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	rows, err := h.uc.ListSchedules(r.Context(), t.UserID, t.Role != roleViewer)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedules": toScheduleList(rows)})
}

func (h *Handler) getSchedule(w http.ResponseWriter, r *http.Request) {
	if !h.authed(w, r) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	s, err := h.uc.GetSchedule(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toScheduleView(s))
}

type scheduleReq struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Kind           string   `json:"kind"`
	CronSpec       string   `json:"cron_spec"`
	Timezone       string   `json:"timezone"`
	ScopeJSON      string   `json:"scope_json"`
	ChannelIDs     []uint64 `json:"channel_ids"`
	InAppVisible   *bool    `json:"in_app_visible"`
	PromptOverride string   `json:"prompt_override"`
}

func (h *Handler) createSchedule(w http.ResponseWriter, r *http.Request) {
	t, ok := tenantctx.From(r.Context())
	if !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	var req scheduleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	s := req.toModel(t.UserID)
	if err := h.uc.CreateSchedule(r.Context(), s, h.now()); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toScheduleView(s))
}

func (h *Handler) updateSchedule(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	existing, err := h.uc.GetSchedule(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	var req scheduleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	req.applyTo(existing)
	if err := h.uc.UpdateSchedule(r.Context(), existing, h.now()); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toScheduleView(existing))
}

func (h *Handler) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.uc.DeleteSchedule(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

type toggleReq struct {
	Enabled bool `json:"enabled"`
}

func (h *Handler) toggleSchedule(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var req toggleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	s, err := h.uc.SetScheduleEnabled(r.Context(), id, req.Enabled, h.now())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toScheduleView(s))
}

func (h *Handler) runNow(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	rpt, err := h.uc.RunNow(r.Context(), id, localeFromRequest(r), h.now())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, toReportDetail(rpt))
}

// --- helpers ---

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

// localeFromRequest picks the operator's UI language from Accept-Language
// (sent by the SPA from web/src/i18n/locale.ts). Returns "en" / "zh" or
// "" when unset/unknown — the generator's DefaultLocale catches "" for
// scheduled fires. Mirrors alert/http.go's helper. See
// feedback_ai_output_locale.
func localeFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	raw := strings.TrimSpace(r.Header.Get("Accept-Language"))
	if raw == "" {
		return ""
	}
	first := strings.SplitN(raw, ",", 2)[0]
	primary := strings.ToLower(strings.SplitN(strings.TrimSpace(first), "-", 2)[0])
	switch primary {
	case "en", "zh":
		return primary
	default:
		return ""
	}
}

// compile-time guard keeps context import lint-clean across edits.
var _ = context.Background
