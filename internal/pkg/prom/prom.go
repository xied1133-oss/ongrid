// Package prom builds the process-wide Prometheus registry and /metrics
// handler used by ongrid cloud and ongrid-edge.
//
// Label cardinality red line: NEVER use org_id / user_id / edge_id
// / URL full-path as labels. Allowed labels: method, code, status, model,
// direction, result, plan_bucket (free|pro|enterprise).
package prom

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
)

// NewRegistry returns a fresh registry with Go runtime and process
// collectors registered. Each BC/sub-domain then registers its own metrics
// via its constructor.
func NewRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return reg
}

// Handler returns the /metrics HTTP handler for the given registry.
func Handler(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg})
}
