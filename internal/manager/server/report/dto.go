package report

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/report"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// reportListItem is the compact shape for the list view.
type reportListItem struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"`
	Summary     string     `json:"summary"`
	PeriodStart time.Time  `json:"period_start"`
	PeriodEnd   time.Time  `json:"period_end"`
	GeneratedAt *time.Time `json:"generated_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	// ScheduleID links a report to the 任务 (schedule) that generated it; nil
	// for manually-generated reports. Surfaced in the list so the 任务 detail
	// can group its reports and the 产物 report cards can show provenance.
	ScheduleID *uint64 `json:"schedule_id,omitempty"`
	// TaskID is the owning-task back-ref (HLD-022), e.g. "report-schedule:42".
	// Set on scheduled AND run-now reports (unlike ScheduleID), so it's the
	// canonical key for "this task's artifacts" + the report card's task badge.
	TaskID string `json:"task_id,omitempty"`
	// RunID is the trigger/run back-ref (task → run → artifact). Artifacts from
	// the same trigger share it, so a task detail can group by 触发.
	RunID string `json:"run_id,omitempty"`
}

// reportDetail is the full shape for the detail view. ContentJSON is
// emitted as raw JSON so the SPA renders the structured cards directly.
type reportDetail struct {
	reportListItem
	Content    json.RawMessage `json:"content"`
	ContentMD  string          `json:"content_md"`
	Timezone   string          `json:"timezone"`
	ErrorMsg   string          `json:"error_msg,omitempty"`
	ShareToken *string         `json:"share_token,omitempty"`
	Delivery   json.RawMessage `json:"delivery,omitempty"`
}

func toReportListItem(r *model.Report) reportListItem {
	return reportListItem{
		ID:          r.ID,
		Title:       r.Title,
		Kind:        r.Kind,
		Status:      r.Status,
		Summary:     r.SummaryText,
		PeriodStart: r.PeriodStart,
		PeriodEnd:   r.PeriodEnd,
		GeneratedAt: r.GeneratedAt,
		CreatedAt:   r.CreatedAt,
		ScheduleID:  r.ScheduleID,
		TaskID:      r.TaskID,
		RunID:       r.RunID,
	}
}

func toReportList(rows []*model.Report) []reportListItem {
	out := make([]reportListItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, toReportListItem(r))
	}
	return out
}

func toReportDetail(r *model.Report) reportDetail {
	d := reportDetail{
		reportListItem: toReportListItem(r),
		ContentMD:      r.ContentMD,
		Timezone:       r.Timezone,
		ErrorMsg:       r.ErrorMsg,
		ShareToken:     r.ShareToken,
	}
	if r.ContentJSON != "" {
		d.Content = json.RawMessage(r.ContentJSON)
	}
	if r.DeliveryJSON != "" {
		d.Delivery = json.RawMessage(r.DeliveryJSON)
	}
	return d
}

// scheduleView is the schedule shape returned to the SPA. ChannelIDs is
// decoded from the stored JSON for convenience.
type scheduleView struct {
	ID             uint64     `json:"id"`
	Name           string     `json:"name"`
	Description    string     `json:"description"`
	Kind           string     `json:"kind"`
	CronSpec       string     `json:"cron_spec"`
	Timezone       string     `json:"timezone"`
	ScopeJSON      string     `json:"scope_json"`
	ChannelIDs     []uint64   `json:"channel_ids"`
	InAppVisible   bool       `json:"in_app_visible"`
	AgentPersona   string     `json:"agent_persona"`
	PromptOverride string     `json:"prompt_override,omitempty"`
	Enabled        bool       `json:"enabled"`
	NextFireAt     *time.Time `json:"next_fire_at,omitempty"`
	LastFireAt     *time.Time `json:"last_fire_at,omitempty"`
	LastReportID   *string    `json:"last_report_id,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

func toScheduleView(s *model.ReportSchedule) scheduleView {
	v := scheduleView{
		ID:           s.ID,
		Name:         s.Name,
		Description:  s.Description,
		Kind:         s.Kind,
		CronSpec:     s.CronSpec,
		Timezone:     s.Timezone,
		ScopeJSON:    s.ScopeJSON,
		ChannelIDs:   decodeChannelIDs(s.ChannelIDsJSON),
		InAppVisible: s.InAppVisible,
		AgentPersona: s.AgentPersona,
		Enabled:      s.Enabled,
		NextFireAt:   s.NextFireAt,
		LastFireAt:   s.LastFireAt,
		LastReportID: s.LastReportID,
		CreatedAt:    s.CreatedAt,
	}
	if s.PromptOverride != nil {
		v.PromptOverride = *s.PromptOverride
	}
	return v
}

func toScheduleList(rows []*model.ReportSchedule) []scheduleView {
	out := make([]scheduleView, 0, len(rows))
	for _, s := range rows {
		out = append(out, toScheduleView(s))
	}
	return out
}

// toModel builds a fresh ReportSchedule from a create request.
func (req scheduleReq) toModel(ownerID uint64) *model.ReportSchedule {
	s := &model.ReportSchedule{
		CreatedBy:      ownerID,
		Name:           req.Name,
		Description:    req.Description,
		Kind:           req.Kind,
		CronSpec:       req.CronSpec,
		Timezone:       firstNonEmpty(req.Timezone, "UTC"),
		ScopeJSON:      firstNonEmpty(req.ScopeJSON, "{}"),
		ChannelIDsJSON: encodeChannelIDs(req.ChannelIDs),
		Enabled:        true,
		InAppVisible:   true,
	}
	if req.InAppVisible != nil {
		s.InAppVisible = *req.InAppVisible
	}
	if req.PromptOverride != "" {
		po := req.PromptOverride
		s.PromptOverride = &po
	}
	return s
}

// applyTo mutates an existing schedule with the request's fields.
func (req scheduleReq) applyTo(s *model.ReportSchedule) {
	if req.Name != "" {
		s.Name = req.Name
	}
	s.Description = req.Description
	if req.Kind != "" {
		s.Kind = req.Kind
	}
	s.CronSpec = req.CronSpec // may be cleared to re-derive from kind
	if req.Timezone != "" {
		s.Timezone = req.Timezone
	}
	if req.ScopeJSON != "" {
		s.ScopeJSON = req.ScopeJSON
	}
	s.ChannelIDsJSON = encodeChannelIDs(req.ChannelIDs)
	if req.InAppVisible != nil {
		s.InAppVisible = *req.InAppVisible
	}
	if req.PromptOverride != "" {
		po := req.PromptOverride
		s.PromptOverride = &po
	} else {
		s.PromptOverride = nil
	}
}

func decodeChannelIDs(raw string) []uint64 {
	if raw == "" {
		return []uint64{}
	}
	var ids []uint64
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return []uint64{}
	}
	return ids
}

func encodeChannelIDs(ids []uint64) string {
	if len(ids) == 0 {
		return "[]"
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// --- response helpers (mirror device/http.go) ---

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if body == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func writeErr(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(errs.HTTPStatus(err))
	_ = json.NewEncoder(w).Encode(errorBody{Error: err.Error(), Code: errCode(err)})
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
	default:
		return "internal"
	}
}
