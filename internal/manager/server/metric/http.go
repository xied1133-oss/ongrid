// Package metric builds the HTTP routes for the manager/metric sub-domain.
package metric

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	biz "github.com/ongridio/ongrid/internal/manager/biz/metric"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// MetricService is the narrow service contract the handler depends on.
// *service/metric.Service satisfies it by structural typing; tests can
// swap in a fake.
type MetricService interface {
	Query(ctx context.Context, q biz.RangeQuery) (*biz.Series, error)
}

// Handler bundles the metric service with HTTP-layer state.
type Handler struct {
	svc MetricService
}

// NewHandler builds the handler.
func NewHandler(s MetricService) *Handler { return &Handler{svc: s} }

// Register attaches the metric routes on r. Post-pivot paths are flat
// (no org_id). The caller is expected to wrap r in the auth middleware
// before calling Register — any authenticated user may read metrics
//
//	GET /v1/edges/{id}/metrics?from=RFC3339&to=RFC3339&resolution=auto|raw|5m|1h
func (h *Handler) Register(r chi.Router) {
	r.Get("/v1/edges/{id}/metrics", h.queryMetrics)
}

// --------- DTOs ---------

// pointDTO is the UI-facing per-sample shape. A raw sample sets
// avg == max for every gauge and reports one net_rx/net_tx value; a 5m
// or 1h bucket carries the averaged + maxed gauges and summed counters.
// This single tagged-union shape avoids forking the response by
// resolution.
type pointDTO struct {
	Ts          time.Time `json:"ts"`
	CPU         minMax    `json:"cpu"`
	Mem         minMax    `json:"mem"`
	Load1       minMax    `json:"load1"`
	Load5       minMax    `json:"load5"`
	Load15      minMax    `json:"load15"`
	NetRxBps    *uint64   `json:"net_rx_bps,omitempty"`
	NetTxBps    *uint64   `json:"net_tx_bps,omitempty"`
	DiskUsedPct minMax    `json:"disk_used_pct"`
}

// minMax carries avg + max for a gauge. For raw samples avg == max.
//
// Pointers, not values: the Prom-backed handler returns null for buckets
// where the underlying matrix had no data, so the SPA can break the line
// at outage gaps (recharts connectNulls=false). Without nullability a
// missing bucket would render as 0 and silently bridge gaps — exactly
// the bug operators noticed when comparing our chart to Grafana for the
// same range.
type minMax struct {
	Avg *float64 `json:"avg,omitempty"`
	Max *float64 `json:"max,omitempty"`
}

// queryResp is the JSON body for GET /v1/edges/{id}/metrics.
type queryResp struct {
	Resolution string     `json:"resolution"`
	From       time.Time  `json:"from"`
	To         time.Time  `json:"to"`
	Points     []pointDTO `json:"points"`
}

// --------- handlers ---------

func (h *Handler) queryMetrics(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}

	q := r.URL.Query()
	from, err := parseTime(q.Get("from"))
	if err != nil {
		writeErr(w, fmt.Errorf("%w: from: %s", errs.ErrInvalid, err))
		return
	}
	to, err := parseTime(q.Get("to"))
	if err != nil {
		writeErr(w, fmt.Errorf("%w: to: %s", errs.ErrInvalid, err))
		return
	}

	res := biz.Resolution(q.Get("resolution"))
	if res == "" {
		res = biz.ResAuto
	}

	series, err := h.svc.Query(r.Context(), biz.RangeQuery{
		EdgeID:     id,
		From:       from,
		To:         to,
		Resolution: res,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, queryResp{
		Resolution: string(series.Resolution),
		From:       from,
		To:         to,
		Points:     toPointDTOs(series),
	})
}

// toPointDTOs normalises the three backing Series variants into the
// tagged-union pointDTO shape.
func toPointDTOs(s *biz.Series) []pointDTO {
	switch s.Resolution {
	case biz.ResRaw:
		out := make([]pointDTO, len(s.RawPoints))
		for i, p := range s.RawPoints {
			out[i] = pointDTO{
				Ts:          p.Ts,
				CPU:         minMax{Avg: ptrF(p.CPUPct), Max: ptrF(p.CPUPct)},
				Mem:         minMax{Avg: ptrF(p.MemPct), Max: ptrF(p.MemPct)},
				Load1:       minMax{Avg: ptrF(p.Load1), Max: ptrF(p.Load1)},
				Load5:       minMax{Avg: ptrF(p.Load5), Max: ptrF(p.Load5)},
				Load15:      minMax{Avg: ptrF(p.Load15), Max: ptrF(p.Load15)},
				NetRxBps:    ptrU(p.NetRxBps),
				NetTxBps:    ptrU(p.NetTxBps),
				DiskUsedPct: minMax{Avg: ptrF(p.DiskUsedPct), Max: ptrF(p.DiskUsedPct)},
			}
		}
		return out
	case biz.Res5m:
		out := make([]pointDTO, len(s.Buckets5m))
		for i, b := range s.Buckets5m {
			out[i] = pointDTO{
				Ts:          b.Ts,
				CPU:         minMax{Avg: ptrF(b.CPUAvg), Max: ptrF(b.CPUMax)},
				Mem:         minMax{Avg: ptrF(b.MemAvg), Max: ptrF(b.MemMax)},
				Load1:       minMax{Avg: ptrF(b.Load1Avg), Max: ptrF(b.Load1Max)},
				Load5:       minMax{Avg: ptrF(b.Load5Avg), Max: ptrF(b.Load5Max)},
				Load15:      minMax{Avg: ptrF(b.Load15Avg), Max: ptrF(b.Load15Max)},
				NetRxBps:    ptrU(b.NetRxSum),
				NetTxBps:    ptrU(b.NetTxSum),
				DiskUsedPct: minMax{Avg: ptrF(b.DiskUsedAvg), Max: ptrF(b.DiskUsedMax)},
			}
		}
		return out
	case biz.Res1h:
		out := make([]pointDTO, len(s.Buckets1h))
		for i, b := range s.Buckets1h {
			out[i] = pointDTO{
				Ts:          b.Ts,
				CPU:         minMax{Avg: ptrF(b.CPUAvg), Max: ptrF(b.CPUMax)},
				Mem:         minMax{Avg: ptrF(b.MemAvg), Max: ptrF(b.MemMax)},
				Load1:       minMax{Avg: ptrF(b.Load1Avg), Max: ptrF(b.Load1Max)},
				Load5:       minMax{Avg: ptrF(b.Load5Avg), Max: ptrF(b.Load5Max)},
				Load15:      minMax{Avg: ptrF(b.Load15Avg), Max: ptrF(b.Load15Max)},
				NetRxBps:    ptrU(b.NetRxSum),
				NetTxBps:    ptrU(b.NetTxSum),
				DiskUsedPct: minMax{Avg: ptrF(b.DiskUsedAvg), Max: ptrF(b.DiskUsedMax)},
			}
		}
		return out
	default:
		return nil
	}
}

// --------- helpers ---------

func parseID(r *http.Request) (uint64, error) {
	raw := chi.URLParam(r, "id")
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, errors.Join(errs.ErrInvalid, fmt.Errorf("id: %w", err))
	}
	if id == 0 {
		return 0, fmt.Errorf("%w: id must be positive", errs.ErrInvalid)
	}
	return id, nil
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, errors.New("missing timestamp")
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}

// ptrF / ptrU box scalars to pointers so the JSON encoder can serialise
// them as null on the wire when missing (PromHandler.queryMetrics uses
// nil for series gaps; the legacy MySQL handler always has data so it
// always boxes a real value, but keeps the wire shape uniform).
func ptrF(v float64) *float64 { return &v }
func ptrU(v uint64) *uint64   { return &v }

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if body == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

// errorBody is the shape we ship for any error response. Kept in sync
// with server/edge for a uniform cross-BC contract.
type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
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
	case errors.Is(err, errs.ErrEdgeOffline):
		return "edge-offline"
	case errors.Is(err, errs.ErrNotWiredYet):
		return "not-wired-yet"
	default:
		return "internal"
	}
}
