// Package collector exposes two interchangeable host-metric sources:
//
//   - embedded:  in-process collection via gopsutil, producing
//     node_exporter-compatible Prometheus metric names. CGO-free.
//   - scrape:    multi-target HTTP scraper that pulls /metrics in
//     Prometheus text format and parses via expfmt.
//
// Both modes feed the same internal pipeline: each tick yields a
// CollectorOutput with (a) the legacy 8-field tunnel.HostMetricPoint
// fast path and (b) a flat slice of tunnel.PromSample for the new
// push_prom_samples wire method.
//
// node_exporter SDK was evaluated and rejected: kingpin-bound flag
// state, Linux-only build constraints, and pulling >100 transitive
// dependencies for what amounts to a thin /proc reader. gopsutil v3
// gives the same five resources (cpu, mem, load, net, disk) with
// pure Go and zero CGO. Process listing also folds in (gopsutil
// process.Process). Metric names emitted from embedded mode follow
// node_exporter convention so the cloud-side mapper / PromQL queries
// can match either source byte-for-byte.
package collector
