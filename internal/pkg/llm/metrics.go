package llm

import (
	"errors"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
)

// metrics bundles the Prom collectors scoped to the llm package.
//
// Label cardinality red line: never user_id / org_id / session_id.
// Labels here are limited to model / kind / result.
type metrics struct {
	tokensTotal    *prometheus.CounterVec   // {model, kind=prompt|completion}
	requestsTotal  *prometheus.CounterVec   // {model, result=success|error|budget_exceeded}
	requestSeconds *prometheus.HistogramVec // {model}
}

// newMetrics constructs and registers the llm collectors.
//
// If reg is nil we fall back to prometheus.DefaultRegisterer and warn once.
// An "already registered" error downgrades to warn + reuse the existing
// collector so tests can call newMetrics on a shared registry without
// panicking.
func newMetrics(reg *prometheus.Registry, log *slog.Logger) *metrics {
	var registerer prometheus.Registerer = reg
	if reg == nil {
		if log != nil {
			log.Warn("llm metrics: nil registry, falling back to prometheus.DefaultRegisterer")
		}
		registerer = prometheus.DefaultRegisterer
	}

	tokens := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ongrid_llm_tokens_total",
			Help: "Total LLM tokens consumed, split by model and kind (prompt|completion).",
		},
		[]string{"model", "kind"},
	)
	reqs := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ongrid_llm_requests_total",
			Help: "Total LLM chat completion requests, split by model and result.",
		},
		[]string{"model", "result"},
	)
	dur := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ongrid_llm_request_duration_seconds",
			Help:    "LLM chat completion request duration in seconds.",
			Buckets: prometheus.ExponentialBuckets(0.1, 2, 10), // 0.1s .. ~51s
		},
		[]string{"model"},
	)

	m := &metrics{
		tokensTotal:    tokens,
		requestsTotal:  reqs,
		requestSeconds: dur,
	}

	m.tokensTotal = registerOrExisting(registerer, tokens, log).(*prometheus.CounterVec)
	m.requestsTotal = registerOrExisting(registerer, reqs, log).(*prometheus.CounterVec)
	m.requestSeconds = registerOrExisting(registerer, dur, log).(*prometheus.HistogramVec)

	return m
}

// registerOrExisting registers c; on AlreadyRegisteredError it returns the
// existing collector and logs a warn.
func registerOrExisting(reg prometheus.Registerer, c prometheus.Collector, log *slog.Logger) prometheus.Collector {
	err := reg.Register(c)
	if err == nil {
		return c
	}
	var are prometheus.AlreadyRegisteredError
	if errors.As(err, &are) {
		if log != nil {
			log.Warn("llm metrics: collector already registered, reusing existing")
		}
		return are.ExistingCollector
	}
	// Any other registration failure is a programming error; panic as
	// MustRegister would. We cannot continue with a broken collector.
	panic(err)
}
