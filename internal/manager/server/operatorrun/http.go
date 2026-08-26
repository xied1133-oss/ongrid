// Package operatorrun exposes short-lived operator tool runs.
package operatorrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	biz "github.com/ongridio/ongrid/internal/manager/biz/operatorrun"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

type Service interface {
	Create(ctx context.Context, caller biz.Caller, in biz.CreateInput) (*biz.Run, error)
	Get(ctx context.Context, caller biz.Caller, id string) (*biz.Run, error)
	Cancel(ctx context.Context, caller biz.Caller, id string) (*biz.Run, error)
	ListNetNS(ctx context.Context, caller biz.Caller, edgeID uint64) (*biz.NetNSList, error)
	Subscribe(ctx context.Context, caller biz.Caller, id string) ([]biz.Event, <-chan biz.Event, func(), error)
}

type Handler struct {
	svc Service
}

func NewHandler(svc Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Register(r chi.Router) {
	r.Post("/v1/operator-runs", h.create)
	r.Get("/v1/operator-runs/netns", h.netns)
	r.Get("/v1/operator-runs/{id}", h.get)
	r.Get("/v1/operator-runs/{id}/events", h.events)
	r.Post("/v1/operator-runs/{id}/cancel", h.cancel)
}

// @Summary Create operator run
// @Router /api/v1/operator-runs [post]
// @Success 201 {object} operatorrun.Run
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	caller, ok := callerFromRequest(r)
	if !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	var in biz.CreateInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	run, err := h.svc.Create(r.Context(), caller, in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, run)
}

// @Summary List edge network namespaces for operator tools
// @Router /api/v1/operator-runs/netns [get]
// @Success 200 {object} operatorrun.NetNSList
func (h *Handler) netns(w http.ResponseWriter, r *http.Request) {
	caller, ok := callerFromRequest(r)
	if !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	edgeID, err := parseUintParam(r.URL.Query().Get("edge_id"))
	if err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	items, err := h.svc.ListNetNS(r.Context(), caller, edgeID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// @Summary Get operator run
// @Router /api/v1/operator-runs/{id} [get]
// @Success 200 {object} operatorrun.Run
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	caller, ok := callerFromRequest(r)
	if !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	run, err := h.svc.Get(r.Context(), caller, runIDFromRequest(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// @Summary Stream operator run events
// @Router /api/v1/operator-runs/{id}/events [get]
// @Success 200 {string} string
func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	caller, ok := callerFromRequest(r)
	if !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, fmt.Errorf("%w: streaming unsupported", errs.ErrInvalid))
		return
	}
	history, ch, unsubscribe, err := h.svc.Subscribe(r.Context(), caller, runIDFromRequest(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	defer unsubscribe()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	historyDone := false
	for _, event := range history {
		writeSSE(w, flusher, event.Type, event)
		if event.Type == biz.EventDone {
			historyDone = true
		}
	}
	if historyDone {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			writeSSE(w, flusher, event.Type, event)
			if event.Type == biz.EventDone {
				return
			}
		}
	}
}

// @Summary Cancel operator run
// @Router /api/v1/operator-runs/{id}/cancel [post]
// @Success 200 {object} operatorrun.Run
func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	caller, ok := callerFromRequest(r)
	if !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	run, err := h.svc.Cancel(r.Context(), caller, runIDFromRequest(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func runIDFromRequest(r *http.Request) string {
	if id := strings.TrimSpace(chi.URLParam(r, "id")); id != "" {
		return id
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api"), "/")
	parts := strings.Split(path, "/")
	for i := 0; i+2 < len(parts); i++ {
		if parts[i] == "v1" && parts[i+1] == "operator-runs" {
			return parts[i+2]
		}
	}
	return ""
}

func parseUintParam(value string) (uint64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("edge_id required")
	}
	var id uint64
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, errors.New("edge_id must be numeric")
		}
		id = id*10 + uint64(r-'0')
	}
	if id == 0 {
		return 0, errors.New("edge_id required")
	}
	return id, nil
}

func callerFromRequest(r *http.Request) (biz.Caller, bool) {
	tenant, ok := tenantctx.From(r.Context())
	if !ok {
		return biz.Caller{}, false
	}
	return biz.Caller{UserID: tenant.UserID, Role: tenant.Role}, true
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, name string, payload any) {
	var b strings.Builder
	b.WriteString("event: ")
	b.WriteString(name)
	b.WriteString("\n")
	encoded, err := json.Marshal(payload)
	if err != nil {
		encoded = []byte(`{"error":"encode event"}`)
	}
	b.WriteString("data: ")
	b.Write(encoded)
	b.WriteString("\n\n")
	_, _ = w.Write([]byte(b.String()))
	flusher.Flush()
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
	case errors.Is(err, errs.ErrUnauthorized):
		return "unauthorized"
	case errors.Is(err, errs.ErrForbidden):
		return "forbidden"
	case errors.Is(err, errs.ErrNotFound):
		return "not-found"
	case errors.Is(err, errs.ErrInvalid):
		return "invalid"
	default:
		return "internal"
	}
}
