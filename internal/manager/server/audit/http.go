// Package audit serves /v1/admin/audit-logs — admin-only paginated
// read of the HLD-010 audit trail. Reads no longer self-audit
// (2026-05-21: operator dropped audit_view because per-refresh rows
// drowned out the create/update/delete signal).
package audit

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	bizaudit "github.com/ongridio/ongrid/internal/manager/biz/audit"
	auditmodel "github.com/ongridio/ongrid/internal/manager/model/audit"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

// Handler exposes the admin-facing audit endpoints.
type Handler struct {
	uc *bizaudit.Usecase
}

// NewHandler wires the audit usecase to the HTTP surface.
func NewHandler(uc *bizaudit.Usecase) *Handler { return &Handler{uc: uc} }

// Register attaches /v1/admin/audit-logs under the protected (auth)
// group. The handler enforces admin role on every call.
func (h *Handler) Register(r chi.Router) {
	r.Get("/v1/admin/audit-logs", h.list)
}

type wireLog struct {
	ID            uint64     `json:"id"`
	OccurredAt    time.Time  `json:"occurred_at"`
	UserID        *uint64    `json:"user_id,omitempty"`
	UserEmail     string     `json:"user_email"`
	Role          string     `json:"role"`
	IP            string     `json:"ip"`
	UserAgent     string     `json:"user_agent"`
	Action        string     `json:"action"`
	ResourceType  string     `json:"resource_type"`
	ResourceID    string     `json:"resource_id"`
	ResourceName  string     `json:"resource_name"`
	Status        string     `json:"status"`
	ErrorCode     string     `json:"error_code,omitempty"`
	ErrorMessage  string     `json:"error_message,omitempty"`
	PayloadJSON   string     `json:"payload_json,omitempty"`
	RequestID     string     `json:"request_id,omitempty"`
}

type listResp struct {
	Items []wireLog `json:"items"`
	Total int64     `json:"total"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	t, ok := tenantctx.From(r.Context())
	if !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	if t.Role != "admin" && !t.IsSuperuser {
		// Denied access stays unaudited — the read-only audit_view
		// action class was dropped 2026-05-21 (operator: 'read-only
		// actions are too noisy'). The 403 itself is visible in
		// nginx/manager request logs, which is enough trail.
		writeErr(w, errs.ErrForbidden)
		return
	}

	q := r.URL.Query()
	f := bizaudit.ListFilters{
		UserEmail:    q.Get("user_email"),
		Action:       q.Get("action"),
		ResourceType: q.Get("resource_type"),
		Status:       q.Get("status"),
		Limit:        parseInt(q.Get("limit"), 50),
		Offset:       parseInt(q.Get("offset"), 0),
	}
	if from := q.Get("from"); from != "" {
		if ts, err := time.Parse(time.RFC3339, from); err == nil {
			f.From = ts
		}
	}
	if to := q.Get("to"); to != "" {
		if ts, err := time.Parse(time.RFC3339, to); err == nil {
			f.To = ts
		}
	}

	rows, total, err := h.uc.List(r.Context(), f)
	if err != nil {
		writeErr(w, err)
		return
	}

	// Read paths are not audited — operator flagged the resulting
	// audit_view spam as drowning out create/update/delete signal
	// (every refresh on /settings/audit posts another row).

	out := listResp{Items: make([]wireLog, 0, len(rows)), Total: total}
	for _, row := range rows {
		out.Items = append(out.Items, toWire(row))
	}
	writeJSON(w, http.StatusOK, out)
}

func toWire(r auditmodel.Log) wireLog {
	return wireLog{
		ID:           r.ID,
		OccurredAt:   r.OccurredAt,
		UserID:       r.UserID,
		UserEmail:    r.UserEmail,
		Role:         r.Role,
		IP:           r.IP,
		UserAgent:    r.UserAgent,
		Action:       r.Action,
		ResourceType: r.ResourceType,
		ResourceID:   r.ResourceID,
		ResourceName: r.ResourceName,
		Status:       r.Status,
		ErrorCode:    r.ErrorCode,
		ErrorMessage: r.ErrorMessage,
		PayloadJSON:  r.PayloadJSON,
		RequestID:    r.RequestID,
	}
}

func parseInt(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if body == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, err error) {
	type errBody struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	code := http.StatusInternalServerError
	slug := "internal"
	switch {
	case errors.Is(err, errs.ErrUnauthorized):
		code, slug = http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, errs.ErrForbidden):
		code, slug = http.StatusForbidden, "forbidden"
	case errors.Is(err, errs.ErrInvalid):
		code, slug = http.StatusBadRequest, "invalid"
	case errors.Is(err, errs.ErrNotFound):
		code, slug = http.StatusNotFound, "not_found"
	}
	writeJSON(w, code, errBody{Error: err.Error(), Code: slug})
}
