// Package tracing wires OpenTelemetry into the manager + edge agent so
// Tempo's spanmetrics generator has data to derive
// traces_spanmetrics_latency_bucket / traces_spanmetrics_calls_total.
// Without an active OTel exporter Tempo's receiver gets nothing and the
// trace_latency / trace_error_rate evaluators query
// empty matrices forever.
//
// Usage at process boot:
//
//	shutdown, err := tracing.Init(ctx, tracing.Config{
//	    ServiceName: "ongrid-manager",
//	    Endpoint: "tempo:4318", // OTLP HTTP receiver
//	})
//	defer shutdown(context.Background())
//
// All callers then use the global tracer via otel.Tracer("ongrid").
// HTTP middleware: wrap the chi router with otelhttp.NewMiddleware
// (see cmd/ongrid/main.go for the wiring).
package tracing

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
)

// Config bundles what Init needs.
type Config struct {
	// ServiceName goes into resource.service.name; the spanmetrics
	// generator splits series by this so manager + edge stay separate.
	ServiceName string
	// Endpoint is the OTLP HTTP collector. host:port — the SDK
	// derives http://host:port/v1/traces. Empty disables exporting
	// (Init returns a no-op shutdown so callers can defer it
	// unconditionally).
	Endpoint string
	// Insecure flips http vs https. Tempo on the docker network is
	// plain http; production behind a TLS proxy can flip this off.
	Insecure bool
	// SamplingRatio is 0..1 — fraction of root spans to keep.
	// Defaults to 1.0 (sample everything) at our current scale; flip
	// down to 0.1 if span volume becomes a problem.
	SamplingRatio float64
}

// Shutdown gracefully drains the span buffer to the collector.
type Shutdown func(context.Context) error

// Init builds and registers the global TracerProvider. When endpoint
// is empty, returns a no-op shutdown so boot logic can defer
// unconditionally.
func Init(ctx context.Context, cfg Config) (Shutdown, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return func(context.Context) error { return nil }, nil
	}
	if cfg.SamplingRatio <= 0 || cfg.SamplingRatio > 1 {
		cfg.SamplingRatio = 1.0
	}

	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(cfg.Endpoint),
	}
	if cfg.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("tracing: build exporter: %w", err)
	}

	// Build a fresh resource (skip resource.Default() merge to avoid
	// schema-URL version conflicts between different otel module
	// versions in the dep tree). Service name is the load-bearing
	// attribute spanmetrics generator splits by; everything else
	// (host, process pid) is nice-to-have.
	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(cfg.ServiceName),
	)
	_ = err // legacy alias kept; resource.NewWithAttributes can't error

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			// Fast batch flush so an incident's spans aren't held
			// for a minute before showing up in Tempo.
			sdktrace.WithBatchTimeout(2*time.Second),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SamplingRatio))),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return tp.Shutdown, nil
}
