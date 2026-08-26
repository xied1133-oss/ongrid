// Package metric is the manager/metric service layer. It is a thin shim
// over biz.IngestService + biz.QueryUsecase: HTTP / tunnel handlers call
// into Service, Service validates + delegates, business logic stays in
// biz.
//
// This package MUST NOT import manager/data/** — the layering rule
// is enforced by spec (gospec §architecture/layering).
package metric

import (
	"context"
	"log/slog"

	biz "github.com/ongridio/ongrid/internal/manager/biz/metric"
	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

// Service delegates to biz.IngestService and biz.QueryUsecase.
type Service struct {
	ingester biz.IngestService
	query    *biz.QueryUsecase
	log      *slog.Logger
}

// New builds the Service.
//
// ingester is an interface (not the concrete *Ingester) so tests can
// swap in a fake and the tunnel-side handler composition agent sees
// the same type it expects.
func New(ingester biz.IngestService, q *biz.QueryUsecase, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{ingester: ingester, query: q, log: log}
}

// Push is the tunnel-side entrypoint for host metrics. Points arrive in
// their on-wire tunnel.HostMetricPoint shape; the ingester converts to
// domain Points internally so the service API never leaks model types.
func (s *Service) Push(ctx context.Context, edgeID uint64, points []tunnel.HostMetricPoint) error {
	return s.ingester.Push(ctx, edgeID, points)
}

// Query is the HTTP-side entrypoint for time-range metric queries.
// Validation (window size, from<to, edge_id>0) lives in QueryUsecase.
func (s *Service) Query(ctx context.Context, q biz.RangeQuery) (*biz.Series, error) {
	return s.query.Query(ctx, q)
}
