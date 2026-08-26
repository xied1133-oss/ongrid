package decorators

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/ongridio/ongrid/internal/manager/biz/aiops/tools/basetool"
)

// metricCollectors bundles the Prom collectors registered by the metric
// decorator. Allocated lazily per Registerer so multiple Wrap() calls
// against the same registerer share collectors (avoiding
// "already registered" panics in tests). — MetricTool
// (记 ongrid_tool_invocations_total).
type metricCollectors struct {
	invocations *prometheus.CounterVec
	duration    *prometheus.HistogramVec
}

var (
	metricRegMu  sync.Mutex
	metricRegMap = map[prometheus.Registerer]*metricCollectors{}
)

// getMetricCollectors returns (and lazily creates / registers) the
// collectors for reg. Identical pattern to internal/pkg/llm/metrics.go's
// registerOrExisting — we treat AlreadyRegisteredError as "reuse the
// existing collector" so multiple decorator chains over the same
// registry don't panic.
func getMetricCollectors(reg prometheus.Registerer) *metricCollectors {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	metricRegMu.Lock()
	defer metricRegMu.Unlock()
	if mc, ok := metricRegMap[reg]; ok {
		return mc
	}
	inv := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ongrid_tool_invocations_total",
			Help: "Total ongrid tool invocations, split by name and result.",
		},
		[]string{"name", "result"},
	)
	dur := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ongrid_tool_duration_seconds",
			Help:    "ongrid tool invocation duration in seconds.",
			Buckets: prometheus.ExponentialBuckets(0.05, 2, 10), // ~50ms .. ~25s
		},
		[]string{"name"},
	)
	mc := &metricCollectors{invocations: regOrExist(reg, inv).(*prometheus.CounterVec),
		duration: regOrExist(reg, dur).(*prometheus.HistogramVec)}
	metricRegMap[reg] = mc
	return mc
}

// regOrExist mirrors llm/metrics.go's registerOrExisting helper. Kept
// local to avoid an import cycle with the llm package (which lives at a
// peer of biz/aiops, not above it).
func regOrExist(reg prometheus.Registerer, c prometheus.Collector) prometheus.Collector {
	err := reg.Register(c)
	if err == nil {
		return c
	}
	var are prometheus.AlreadyRegisteredError
	if errors.As(err, &are) {
		return are.ExistingCollector
	}
	panic(err)
}

// MetricTool wraps inner so each invocation increments a Prom counter
// and observes a duration histogram. — MetricTool 装饰器
// 层。Cardinality red line: labels are {name, result} only —
// never user_id / tenant / device_id.
type MetricTool struct {
	inner      basetool.BaseTool
	collectors *metricCollectors
}

// WithMetric returns inner wrapped to emit Prom counter + histogram on
// every InvokableRun. A nil registerer falls back to
// prometheus.DefaultRegisterer (matching llm/metrics.go).
func WithMetric(inner basetool.BaseTool, reg prometheus.Registerer) basetool.BaseTool {
	return &MetricTool{
		inner:      inner,
		collectors: getMetricCollectors(reg),
	}
}

// Info passes through — collectors don't affect schema.
func (m *MetricTool) Info(ctx context.Context) (*basetool.ToolInfo, error) {
	return m.inner.Info(ctx)
}

// InvokableRun runs the inner tool, then increments
// ongrid_tool_invocations_total{name=..,result=success|error} and
// observes ongrid_tool_duration_seconds{name=..} regardless of outcome.
func (m *MetricTool) InvokableRun(ctx context.Context, argsJSON string, opts ...basetool.InvokeOption) (string, error) {
	name := ""
	if info, err := m.inner.Info(ctx); err == nil && info != nil {
		name = info.Name
	}
	start := time.Now()
	out, err := m.inner.InvokableRun(ctx, argsJSON, opts...)
	dur := time.Since(start)

	result := "success"
	if err != nil {
		result = "error"
	}
	m.collectors.invocations.WithLabelValues(name, result).Inc()
	m.collectors.duration.WithLabelValues(name).Observe(dur.Seconds())
	return out, err
}
