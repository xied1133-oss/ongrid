// Package approval is the HTTP surface for the propose-confirm inbox
// (HLD-017). Read + decide routes are admin-only. Strictly additive.
package approval

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	bizapproval "github.com/ongridio/ongrid/internal/manager/biz/approval"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

// Handler serves /v1/approvals.
type Handler struct{ uc *bizapproval.Usecase }

// NewHandler wires the usecase.
func NewHandler(uc *bizapproval.Usecase) *Handler { return &Handler{uc: uc} }

// Register mounts the routes under an auth'd chi.Router.
func (h *Handler) Register(r chi.Router) {
	r.Get("/v1/approvals", h.list)
	r.Get("/v1/approvals/count", h.count)
	r.Get("/v1/approvals/{id}", h.get)
	r.Post("/v1/approvals/{id}/approve", h.approve)
	r.Post("/v1/approvals/{id}/reject", h.reject)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	status := r.URL.Query().Get("status")
	items, err := h.uc.List(r.Context(), status, 0)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) count(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	n, err := h.uc.CountPending(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pending": n})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	a, err := h.uc.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (h *Handler) approve(w http.ResponseWriter, r *http.Request) {
	c, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	a, err := h.uc.Approve(r.Context(), c.UserID, chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (h *Handler) reject(w http.ResponseWriter, r *http.Request) {
	c, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in)
	if err := h.uc.Reject(r.Context(), c.UserID, chi.URLParam(r, "id"), in.Reason); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- auth + json helpers (mirrors server/secret) ---

type caller struct {
	UserID uint64
	Role   string
}

func requireAdmin(w http.ResponseWriter, r *http.Request) (caller, bool) {
	t, ok := tenantctx.From(r.Context())
	if !ok {
		writeErr(w, errs.ErrUnauthorized)
		return caller{}, false
	}
	if t.Role != "admin" {
		writeErr(w, errs.ErrForbidden)
		return caller{}, false
	}
	return caller{UserID: t.UserID, Role: t.Role}, true
}

type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func writeErr(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	slug := "internal"
	switch {
	case errors.Is(err, errs.ErrUnauthorized):
		code, slug = http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, errs.ErrForbidden):
		code, slug = http.StatusForbidden, "forbidden"
	case errors.Is(err, errs.ErrNotFound):
		code, slug = http.StatusNotFound, "not_found"
	case errors.Is(err, errs.ErrInvalid):
		code, slug = http.StatusBadRequest, "invalid"
	}
	writeJSON(w, code, errorBody{Error: err.Error(), Code: slug})
}
