// Package monitor builds the HTTP routes for user-managed Monitor page
// panels. Thin handler — auth + JSON decode + delegate to biz layer.
package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	biz "github.com/ongridio/ongrid/internal/manager/biz/monitor"
	model "github.com/ongridio/ongrid/internal/manager/model/monitor"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

// PanelService is the narrow biz contract the handler needs.
// *biz/monitor.Service satisfies it.
type PanelService interface {
	List(ctx context.Context) ([]*model.Panel, error)
	Get(ctx context.Context, id uint64) (*model.Panel, error)
	Create(ctx context.Context, in biz.CreateInput) (*model.Panel, error)
	Update(ctx context.Context, id uint64, in biz.UpdateInput) (*model.Panel, error)
	Delete(ctx context.Context, id uint64) error
}

// Handler bundles the routes.
type Handler struct {
	svc PanelService
}

// NewHandler wires the handler.
func NewHandler(svc PanelService) *Handler { return &Handler{svc: svc} }

// Register attaches routes:
//
//	GET    /v1/monitor/panels         (any auth user)
//	POST   /v1/monitor/panels         (admin)
//	PATCH  /v1/monitor/panels/{id}    (admin)
//	DELETE /v1/monitor/panels/{id}    (admin)
//
// Listing is open to any authenticated operator so dashboards render
// for all users; mutations are admin-gated to mirror the rest of the
// settings/integration surface.
func (h *Handler) Register(r chi.Router) {
	r.Get("/v1/monitor/panels", h.list)
	r.Post("/v1/monitor/panels", h.create)
	r.Patch("/v1/monitor/panels/{id}", h.update)
	r.Delete("/v1/monitor/panels/{id}", h.delete)
}

type listResp struct {
	Panels []*model.Panel `json:"panels"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	out, err := h.svc.List(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, listResp{Panels: out})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var in biz.CreateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	saved, err := h.svc.Create(r.Context(), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var in biz.UpdateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	saved, err := h.svc.Update(r.Context(), id, in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.svc.Delete(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- helpers ---

func parseID(r *http.Request) (uint64, error) {
	raw := chi.URLParam(r, "id")
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, errors.Join(errs.ErrInvalid, err)
	}
	if id == 0 {
		return 0, errs.ErrInvalid
	}
	return id, nil
}

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	t, ok := tenantctx.From(r.Context())
	if !ok {
		writeErr(w, errs.ErrUnauthorized)
		return false
	}
	if t.Role != "admin" {
		writeErr(w, errs.ErrForbidden)
		return false
	}
	return true
}

func requireUser(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := tenantctx.From(r.Context()); !ok {
		writeErr(w, errs.ErrUnauthorized)
		return false
	}
	return true
}

type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
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
	status := errs.HTTPStatus(err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
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
	default:
		return "internal"
	}
}
