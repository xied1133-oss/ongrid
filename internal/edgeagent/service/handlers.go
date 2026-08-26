// Package service registers the tool handlers that cloud invokes on the
// edge (get_host_load / get_process_list / ...). It's the only edgeagent
// package that speaks the tunnel body wire format.
//
// The Agent's Run loop normally registers these automatically; this
// package is kept as a standalone helper for tests and for callers that
// wire the tunnel client without using the full Agent.
package service

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/ongridio/ongrid/internal/edgeagent/biz"
	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

// Register installs the cloud->edge handler set on the given tunnel
// client. A set of zero-value stub handlers is installed that always
// returns the empty response. Kept for wiring backwards-compatibility
// with the Phase 1 main.go; prefer RegisterWithCollector in new code.
func Register(client tunnel.Client, log *slog.Logger) {
	RegisterWithCollector(client, nil, log)
}

// RegisterWithCollector installs the cloud->edge handler set backed by
// the provided Collector. If collector is nil, stub handlers are used
// (same behaviour as Register).
func RegisterWithCollector(client tunnel.Client, collector biz.Collector, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	if collector == nil {
		registerStubs(client, log)
		return
	}

	client.RegisterHandler(tunnel.MethodGetHostLoad,
		func(ctx context.Context, _ tunnel.Session, _ string, _ []byte) ([]byte, error) {
			v, err := collector.GetHostLoad(ctx)
			if err != nil {
				return nil, err
			}
			return json.Marshal(v)
		})

	client.RegisterHandler(tunnel.MethodGetProcessList,
		func(ctx context.Context, _ tunnel.Session, _ string, body []byte) ([]byte, error) {
			var req tunnel.GetProcessListRequest
			if len(body) > 0 {
				if err := json.Unmarshal(body, &req); err != nil {
					return nil, err
				}
			}
			if req.TopN == 0 {
				req.TopN = 20
			}
			if req.SortBy == "" {
				req.SortBy = tunnel.ProcessSortByCPU
			}
			v, err := collector.GetProcessList(ctx, int(req.TopN), req.SortBy)
			if err != nil {
				return nil, err
			}
			return json.Marshal(v)
		})
}

// registerStubs installs handlers that always return the zero-value
// response. Handy for dev boxes without a real collector (e.g.
// mid-rollout of a new BC).
func registerStubs(client tunnel.Client, log *slog.Logger) {
	client.RegisterHandler(tunnel.MethodGetHostLoad,
		func(ctx context.Context, _ tunnel.Session, _ string, _ []byte) ([]byte, error) {
			log.Debug("get_host_load invoked (stub)")
			return json.Marshal(tunnel.GetHostLoadResponse{})
		})
	client.RegisterHandler(tunnel.MethodGetProcessList,
		func(ctx context.Context, _ tunnel.Session, _ string, _ []byte) ([]byte, error) {
			log.Debug("get_process_list invoked (stub)")
			return json.Marshal(tunnel.GetProcessListResponse{Processes: []tunnel.ProcessInfo{}})
		})
}
