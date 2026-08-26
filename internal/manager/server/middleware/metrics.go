// Package middleware contains cross-cutting HTTP middleware for the
// manager's chi router. The metrics middleware emits ADR-026 self-obs
// histograms (ongrid_http_requests_total + ongrid_http_request_duration_seconds)
// labelled by chi's compiled RoutePattern so cardinality is bounded by
// the route table at compile time.
package middleware

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/ongridio/ongrid/internal/pkg/prom"
)

// MetricsMiddleware wraps a chi.Router request and records duration +
// status against prom.ObserveHTTP. Routes that aren't in chi's tree
// (404s) are bucketed under route="unknown" to keep cardinality bounded
// — without this any random scanner URL would create a new series.
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)

		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			route = "unknown"
		}
		prom.ObserveHTTP(r.Method, route, ww.Status(), time.Since(start).Seconds())
	})
}
