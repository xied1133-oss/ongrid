package report

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	model "github.com/ongridio/ongrid/internal/manager/model/report"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

// taskIDParam reads the {id} path param and percent-decodes it. The unified task
// id carries a colon ("oneoff:<uuid>" / "report-schedule:<n>") which the client
// sends URL-encoded (%3A); chi leaves it encoded, so decode here before prefix
// matching.
func taskIDParam(r *http.Request) string {
	raw := chi.URLParam(r, "id")
	if dec, err := url.PathUnescape(raw); err == nil {
		return dec
	}
	return raw
}

// task.go — the unified 任务 surface (HLD-022 Phase 2). One list over two
// sources: recurring tasks (report_schedules, id="report-schedule:<id>") +
// stored oneoff tasks (tasks table, id="oneoff:<uuid>"). Recurring CRUD stays on
// /v1/report-schedules; oneoff create/run/delete live here.

// taskDTO is the unified task shape the 任务 page consumes.
type taskDTO struct {
	ID         string     `json:"id"`   // "report-schedule:<n>" | "oneoff:<uuid>"
	Kind       string     `json:"kind"` // "recurring_report" | "oneoff"
	Title      string     `json:"title"`
	ReportKind string     `json:"report_kind"`          // daily/weekly/monthly
	Trigger    string     `json:"trigger"`              // "cron · tz" | "一次性"
	Enabled    bool       `json:"enabled"`              // recurring on/off; oneoff always true
	Status     string     `json:"status"`               // oneoff status; recurring derived
	NextFireAt *time.Time `json:"next_fire_at,omitempty"`
	ScheduleID *uint64    `json:"schedule_id,omitempty"` // recurring: numeric id for CRUD
	CreatedAt  time.Time  `json:"created_at"`
}

func taskFromSchedule(s *model.ReportSchedule) taskDTO {
	id := s.ID
	return taskDTO{
		ID:         "report-schedule:" + strconv.FormatUint(s.ID, 10),
		Kind:       model.TaskKindRecurring,
		Title:      s.Name,
		ReportKind: s.Kind,
		Trigger:    s.CronSpec + " · " + s.Timezone,
		Enabled:    s.Enabled,
		Status:     map[bool]string{true: "active", false: "disabled"}[s.Enabled],
		NextFireAt: s.NextFireAt,
		ScheduleID: &id,
		CreatedAt:  s.CreatedAt,
	}
}

func taskFromOneoff(t *model.Task) taskDTO {
	return taskDTO{
		ID:         model.OneoffTaskRef(t.ID),
		Kind:       model.TaskKindOneoff,
		Title:      t.Title,
		ReportKind: t.ReportKind,
		Trigger:    "oneoff",
		Enabled:    true,
		Status:     t.Status,
		CreatedAt:  t.CreatedAt,
	}
}

// listTasks unions recurring (schedules) + oneoff tasks, newest first.
func (h *Handler) listTasks(w http.ResponseWriter, r *http.Request) {
	t, ok := tenantctx.From(r.Context())
	if !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	out := []taskDTO{}
	scheds, err := h.uc.ListSchedules(r.Context(), t.UserID, t.Role != roleViewer)
	if err != nil {
		writeErr(w, err)
		return
	}
	for _, s := range scheds {
		out = append(out, taskFromSchedule(s))
	}
	oneoffs, err := h.uc.ListOneoffTasks(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	for _, o := range oneoffs {
		out = append(out, taskFromOneoff(o))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	writeJSON(w, http.StatusOK, map[string]any{"tasks": out})
}

// createOneoffTask creates a one-shot task and immediately generates its report
// — the task-side replacement for the removed products-side "立即生成".
func (h *Handler) createOneoffTask(w http.ResponseWriter, r *http.Request) {
	t, ok := tenantctx.From(r.Context())
	if !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	var in struct {
		Title     string `json:"title"`
		Kind      string `json:"kind"` // daily/weekly/monthly
		Timezone  string `json:"timezone"`
		ScopeJSON string `json:"scope_json"`
	}
	// Best-effort decode: an empty body is fine (defaults below apply).
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&in)
	if strings.TrimSpace(in.Kind) == "" {
		in.Kind = "weekly"
	}
	scope := in.ScopeJSON
	if strings.TrimSpace(scope) == "" {
		scope = "{}"
	}
	task, err := h.uc.CreateOneoffTaskAndRun(r.Context(), t.UserID, in.Title, in.Kind, in.Timezone, scope, localeFromRequest(r), h.now())
	if err != nil && task == nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, taskFromOneoff(task))
}

// getTask resolves a unified task id (recurring or oneoff) to its DTO.
func (h *Handler) getTask(w http.ResponseWriter, r *http.Request) {
	if !h.authed(w, r) {
		return
	}
	id := taskIDParam(r)
	if n, ok := strings.CutPrefix(id, "report-schedule:"); ok {
		sid, err := strconv.ParseUint(n, 10, 64)
		if err != nil {
			writeErr(w, errs.ErrInvalid)
			return
		}
		s, err := h.uc.GetSchedule(r.Context(), sid)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, taskFromSchedule(s))
		return
	}
	if u, ok := strings.CutPrefix(id, "oneoff:"); ok {
		task, err := h.uc.GetTask(r.Context(), u)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, taskFromOneoff(task))
		return
	}
	writeErr(w, errs.ErrInvalid)
}

// rerunTask re-generates a oneoff task's report.
func (h *Handler) rerunTask(w http.ResponseWriter, r *http.Request) {
	id := taskIDParam(r)
	u, ok := strings.CutPrefix(id, "oneoff:")
	if !ok {
		writeErr(w, errs.ErrInvalid) // recurring run-now stays on /report-schedules
		return
	}
	task, err := h.uc.RerunOneoffTask(r.Context(), u, localeFromRequest(r), h.now())
	if err != nil && task == nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, taskFromOneoff(task))
}

// deleteTask removes a oneoff task.
func (h *Handler) deleteTask(w http.ResponseWriter, r *http.Request) {
	id := taskIDParam(r)
	u, ok := strings.CutPrefix(id, "oneoff:")
	if !ok {
		writeErr(w, errs.ErrInvalid)
		return
	}
	if err := h.uc.DeleteTask(r.Context(), u); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}
