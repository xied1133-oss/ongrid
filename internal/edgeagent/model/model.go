// Package model holds edgeagent's local types.
//
// Note: despite structural similarity to manager/model/metric, these types
// are duplicated here to preserve BC boundaries — the edge is not allowed
// to import manager/**.
package model

import "time"

// HostMetric is the sample pushed to cloud via tunnel push_host_metrics.
type HostMetric struct {
	Ts          time.Time
	CPUPct      float32
	MemPct      float32
	Load1       float32
	Load5       float32
	Load15      float32
	NetRxBps    uint64
	NetTxBps    uint64
	DiskUsedPct float32
}

// ProcessInfo is one entry in a get_process_top response.
type ProcessInfo struct {
	PID     int32
	Name    string
	CPUPct  float32
	MemRSS  uint64
	Command string
}
