// Package mcp exposes /v1/mcp/servers — the external MCP server
// registration CRUD + connection-probe surface (HLD-018). Every route is
// admin-only.
//
// Permissions (all requireAdmin):
//   - POST   /v1/mcp/servers            create
//   - GET    /v1/mcp/servers            list
//   - GET    /v1/mcp/servers/{id}       get
//   - PUT    /v1/mcp/servers/{id}       update
//   - DELETE /v1/mcp/servers/{id}       delete
//   - POST   /v1/mcp/servers/{id}/test  probe (initialize → tools/list)
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	model "github.com/ongridio/ongrid/internal/manager/model/mcp"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/mcpclient"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

// Service is the narrow surface the handler depends on. *bizmcp.Usecase
// satisfies it structurally.
type Service interface {
	Create(ctx context.Context, s *model.Server) (*model.Server, error)
	Update(ctx context.Context, id uint64, patch *model.Server) error
	Delete(ctx context.Context, id uint64) error
	Get(ctx context.Context, id uint64) (*model.Server, error)
	List(ctx context.Context) ([]*model.Server, error)
	TestConnection(ctx context.Context, id uint64) ([]mcpclient.Tool, error)
}

// Handler bundles the MCP server routes.
type Handler struct {
	svc Service
}

// NewHandler builds the handler.
func NewHandler(svc Service) *Handler { return &Handler{svc: svc} }

// Register attaches the routes under a chi.Router that already has the auth
// middleware in front of it (see cmd/ongrid).
func (h *Handler) Register(r chi.Router) {
	r.Post("/v1/mcp/servers", h.create)
	r.Get("/v1/mcp/servers", h.list)
	r.Get("/v1/mcp/servers/{id}", h.get)
	r.Put("/v1/mcp/servers/{id}", h.update)
	r.Delete("/v1/mcp/servers/{id}", h.delete)
	r.Post("/v1/mcp/servers/{id}/test", h.test)
}

// serverInput is the editable subset of model.Server accepted on create /
// update. Status / tools cache / timestamps are server-owned.
type serverInput struct {
	Name               string `json:"name"`
	Transport          string `json:"transport"`
	Endpoint           string `json:"endpoint"`
	Command            string `json:"command"`
	ArgsJSON           string `json:"args_json"`
	Credential         string `json:"credential"`
	HeaderTemplateJSON string `json:"header_template_json"`
	Trusted            bool   `json:"trusted"`
	Enabled            bool   `json:"enabled"`
}

func (in serverInput) toModel() *model.Server {
	return &model.Server{
		Name:               in.Name,
		Transport:          in.Transport,
		Endpoint:           in.Endpoint,
		Command:            in.Command,
		ArgsJSON:           in.ArgsJSON,
		Credential:         in.Credential,
		HeaderTemplateJSON: in.HeaderTemplateJSON,
		Trusted:            in.Trusted,
		Enabled:            in.Enabled,
	}
}

type listResp struct {
	Items []*model.Server `json:"items"`
	Total int             `json:"total"`
}

type testResp struct {
	Tools []mcpclient.Tool `json:"tools"`
	Count int              `json:"count"`
}

type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	caller, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	var in serverInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	s := in.toModel()
	s.CreatedBy = caller.UserID
	out, err := h.svc.Create(r.Context(), s)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	items, err := h.svc.List(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, listResp{Items: items, Total: len(items)})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	s, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var in serverInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	if err := h.svc.Update(r.Context(), id, in.toModel()); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
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
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) test(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	tools, err := h.svc.TestConnection(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, testResp{Tools: tools, Count: len(tools)})
}

// --- helpers ----------------------------------------------------------

func parseID(r *http.Request) (uint64, error) {
	raw := chi.URLParam(r, "id")
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		return 0, errs.ErrInvalid
	}
	return id, nil
}

func callerFromRequest(r *http.Request) (tenantctx.Tenant, bool) {
	return tenantctx.From(r.Context())
}

func requireAdmin(w http.ResponseWriter, r *http.Request) (tenantctx.Tenant, bool) {
	c, ok := callerFromRequest(r)
	if !ok {
		writeErr(w, errs.ErrUnauthorized)
		return tenantctx.Tenant{}, false
	}
	if c.Role != "admin" {
		writeErr(w, errs.ErrForbidden)
		return tenantctx.Tenant{}, false
	}
	return c, true
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
	code, slug := mapErr(err)
	writeJSON(w, code, errorBody{Error: err.Error(), Code: slug})
}

func mapErr(err error) (int, string) {
	switch {
	case errors.Is(err, errs.ErrUnauthorized):
		return http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, errs.ErrForbidden):
		return http.StatusForbidden, "forbidden"
	case errors.Is(err, errs.ErrNotFound):
		return http.StatusNotFound, "not-found"
	case errors.Is(err, errs.ErrConflict):
		return http.StatusConflict, "conflict"
	case errors.Is(err, errs.ErrInvalid):
		return http.StatusBadRequest, "invalid-argument"
	default:
		return http.StatusInternalServerError, "internal"
	}
}
