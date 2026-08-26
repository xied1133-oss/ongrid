// noop_push.go — Collector that suppresses the periodic push path while
// keeping the on-demand RPC paths working.
//
// Why this exists: once the `hostmetrics` plugin (subprocess
// node_exporter) is running on an edge, the manager-side Prometheus
// scrapes host metrics directly through the docker bridge. The legacy
// embedded path (gopsutil-derived samples pushed via push_prom_samples)
// produces duplicate `node_*` series labelled `ongrid_source=embedded`
// — pure noise that shows up as extra legend rows in Monitor panels.
//
// We still want the on-demand snapshot RPCs (`get_host_load`,
// `get_host_processes`, `host_info`) to keep working because the
// AIOps tools and the EdgeDetail "current load" card depend on them.
// They take a fresh gopsutil sample per call, so they don't generate
// duplicate Prom series — only the periodic push does.
//
// NoopPush wraps an EmbeddedCollector and only stubs CollectAll. The
// rest delegates to the embedded collector so the RPCs behave
// identically to "auto" / "embedded" mode.

package collector

import (
	"context"

	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

// NoopPush returns a Collector that produces no periodic samples while
// keeping HostInfo / GetHostLoad / GetProcessList available for the
// on-demand RPCs. Caller passes a live EmbeddedCollector for those
// snapshots; nil triggers fully-empty RPC responses (test mode).
type NoopPushCollector struct {
	emb *EmbeddedCollector
}

// NewNoopPush wraps emb so its on-demand snapshot methods stay live
// while CollectAll returns nothing.
func NewNoopPush(emb *EmbeddedCollector) *NoopPushCollector {
	return &NoopPushCollector{emb: emb}
}

// CollectAll returns no samples — the agent's periodic push goroutine
// sees an empty result and skips the push_prom_samples RPC.
func (n *NoopPushCollector) CollectAll(_ context.Context) ([]CollectorOutput, error) {
	return nil, nil
}

func (n *NoopPushCollector) HostInfo(ctx context.Context) (tunnel.HostInfo, error) {
	if n.emb == nil {
		return tunnel.HostInfo{}, nil
	}
	return n.emb.HostInfo(ctx)
}

func (n *NoopPushCollector) GetHostLoad(ctx context.Context) (tunnel.GetHostLoadResponse, error) {
	if n.emb == nil {
		return tunnel.GetHostLoadResponse{}, nil
	}
	return n.emb.GetHostLoad(ctx)
}

func (n *NoopPushCollector) GetProcessList(ctx context.Context, topN int, sortBy string) (tunnel.GetProcessListResponse, error) {
	if n.emb == nil {
		return tunnel.GetProcessListResponse{}, nil
	}
	return n.emb.GetProcessList(ctx, topN, sortBy)
}
