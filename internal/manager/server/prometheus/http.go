package prometheus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	svc "github.com/ongridio/ongrid/internal/manager/service/prometheus"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/promquery"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

const promTicketCookie = "ongrid_prom_ticket"

// maxExprBytes caps the PromQL expression so an authenticated user can't
// pin Prom with a multi-MB query body. 4 KiB matches the existing
// /v1/metrics/query_range handler — a reasonable hand-built expression
// is well under this and Prom's own limits sit at 1 MiB.
const maxExprBytes = 4 * 1024

type Service interface {
	BuildLaunch(caller svc.Caller, in svc.LaunchInput) (string, string, time.Duration, error)
	RefreshTicket(token string) (string, time.Duration, bool)
	VerifyTicket(token string) error
}

// PromQuerier is the narrow PromQL surface this handler depends on.
// *promquery.Client satisfies it; tests stub it.
type PromQuerier interface {
	QueryRange(ctx context.Context, expr string, start, end time.Time, step time.Duration) (*promquery.InstantResult, error)
}

type Handler struct {
	svc   Service
	prom  PromQuerier // may be nil when ONGRID_PROM_ENABLED=false
}

func NewHandler(s Service) *Handler { return &Handler{svc: s} }

// NewHandlerWithProm is the form used when the cloud Prometheus is wired —
// it also exposes the JSON `query_range` endpoint the SPA's PromQLPanel
// renderer leans on. When prom is nil the route still installs but
// returns 503 "prometheus disabled" so the UI can degrade gracefully
// instead of 404'ing.
func NewHandlerWithProm(s Service, prom PromQuerier) *Handler {
	return &Handler{svc: s, prom: prom}
}

func (h *Handler) RegisterProtected(r chi.Router) {
	r.Post("/v1/prometheus/launch", h.launch)
	r.Post("/v1/prometheus/query_range", h.queryRange)
}

func (h *Handler) RegisterPublic(r chi.Router) {
	r.Get("/v1/prometheus/auth", h.auth)
}

type launchReq struct {
	Expr       string `json:"expr"`
	RangeInput string `json:"range_input,omitempty"`
	EndInput   string `json:"end_input,omitempty"`
	StepInput  string `json:"step_input,omitempty"`
}

type launchResp struct {
	URL string `json:"url"`
}

func (h *Handler) launch(w http.ResponseWriter, r *http.Request) {
	caller, ok := callerFromCtx(r.Context())
	if !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	var req launchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	u, ticket, ttl, err := h.svc.BuildLaunch(caller, svc.LaunchInput{
		Expr:       req.Expr,
		RangeInput: req.RangeInput,
		EndInput:   req.EndInput,
		StepInput:  req.StepInput,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     promTicketCookie,
		Value:    ticket,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, launchResp{URL: u})
}

func (h *Handler) auth(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(promTicketCookie)
	if err != nil {
		http.Error(w, "missing prometheus ticket", http.StatusUnauthorized)
		return
	}
	if err := h.svc.VerifyTicket(c.Value); err != nil {
		http.Error(w, "invalid prometheus ticket", http.StatusUnauthorized)
		return
	}
	// Sliding refresh: every successful auth re-mints the cookie so an
	// active session never expires mid-read. The TTL on the original
	// cookie was 30min, but a user reading a Grafana dashboard hits
	// many auth_request subrequests per minute (one per panel + asset),
	// each of which refreshes here. Idle > TTL → next request 401s →
	// SPA pops a fresh launch.
	if fresh, ttl, ok := h.svc.RefreshTicket(c.Value); ok {
		w.Header().Set("Set-Cookie", (&http.Cookie{
			Name:     promTicketCookie,
			Value:    fresh,
			Path:     "/",
			MaxAge:   int(ttl.Seconds()),
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
		}).String())
	}
	w.WriteHeader(http.StatusNoContent)
}

// queryRangeReq is the wire shape the SPA's prom client sends. Times
// are RFC3339 strings, step is a Go duration ("30s" / "1m" / "5m").
type queryRangeReq struct {
	Expr  string `json:"expr"`
	Start string `json:"start"`
	End   string `json:"end"`
	Step  string `json:"step"`
}

// queryRangeResp is the JSON shape returned to the SPA. We pass the
// matrix through verbatim from Prom — each entry is
// `{ "metric": {label:value, ...}, "values": [[ts, "value"], ...] }`.
// `result_type` is hard-set to "matrix"; if Prom ever returns something
// else (shouldn't on query_range) we ship an empty result.
type queryRangeResp struct {
	ResultType string          `json:"result_type"`
	Result     json.RawMessage `json:"result"`
	From       string          `json:"from"`
	To         string          `json:"to"`
}

// queryRange is the auth'd PromQL range-query passthrough used by the
// Monitor page's PromQLPanel renderer. Same auth gate as
// /v1/prometheus/launch (any logged-in user). The handler resolves the
// Prom URL + creds via the resolver wired into the underlying
// promquery.Client so admin edits to system_settings.prom take effect
// without a manager restart.
func (h *Handler) queryRange(w http.ResponseWriter, r *http.Request) {
	if h.prom == nil {
		writeErr(w, fmt.Errorf("%w: prometheus disabled", errs.ErrNotWiredYet))
		return
	}
	if _, ok := tenantctx.From(r.Context()); !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}

	var req queryRangeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, fmt.Errorf("%w: %s", errs.ErrInvalid, err))
		return
	}
	expr := strings.TrimSpace(req.Expr)
	if expr == "" {
		writeErr(w, fmt.Errorf("%w: expr is required", errs.ErrInvalid))
		return
	}
	if len(expr) > maxExprBytes {
		writeErr(w, fmt.Errorf("%w: expr too large (%d > %d bytes)", errs.ErrInvalid, len(expr), maxExprBytes))
		return
	}
	start, err := time.Parse(time.RFC3339, strings.TrimSpace(req.Start))
	if err != nil {
		writeErr(w, fmt.Errorf("%w: start: %s", errs.ErrInvalid, err))
		return
	}
	end, err := time.Parse(time.RFC3339, strings.TrimSpace(req.End))
	if err != nil {
		writeErr(w, fmt.Errorf("%w: end: %s", errs.ErrInvalid, err))
		return
	}
	if !end.After(start) {
		writeErr(w, fmt.Errorf("%w: end must be after start", errs.ErrInvalid))
		return
	}
	stepStr := strings.TrimSpace(req.Step)
	if stepStr == "" {
		writeErr(w, fmt.Errorf("%w: step is required", errs.ErrInvalid))
		return
	}
	step, err := time.ParseDuration(stepStr)
	if err != nil {
		writeErr(w, fmt.Errorf("%w: step: %s", errs.ErrInvalid, err))
		return
	}
	if step <= 0 {
		writeErr(w, fmt.Errorf("%w: step must be > 0", errs.ErrInvalid))
		return
	}

	// Bound the call so a misconfigured Prom can't tie up a goroutine
	// past 30s — same ceiling promquery.Client uses internally.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	res, err := h.prom.QueryRange(ctx, expr, start, end, step)
	if err != nil {
		// Prom parse errors arrive as plain errors out of promquery; we
		// can't reliably tell user-input mistakes from upstream-down,
		// so 400 keeps the UI surface small. Operators see the message.
		writeErr(w, fmt.Errorf("%w: %s", errs.ErrInvalid, err))
		return
	}

	matrix := json.RawMessage("[]")
	if res != nil && res.ResultType == "matrix" && len(res.Result) > 0 {
		matrix = res.Result
	}
	writeJSON(w, http.StatusOK, queryRangeResp{
		ResultType: "matrix",
		Result:     matrix,
		From:       start.UTC().Format(time.RFC3339),
		To:         end.UTC().Format(time.RFC3339),
	})
}

func callerFromCtx(ctx context.Context) (svc.Caller, bool) {
	t, ok := tenantctx.From(ctx)
	if !ok {
		return svc.Caller{}, false
	}
	return svc.Caller{UserID: t.UserID, Role: t.Role}, true
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(errs.HTTPStatus(err))
	_ = json.NewEncoder(w).Encode(errorBody{
		Error: err.Error(),
		Code:  errCode(err),
	})
}

func errCode(err error) string {
	switch {
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
