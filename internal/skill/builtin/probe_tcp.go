package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/ongridio/ongrid/internal/skill"
)

func init() { skill.Register(&ProbeTCP{}) }

// ProbeTCP dials a TCP target and reports connectivity + latency. Safe:
// makes a single outbound connection, immediately closes, no payload sent.
type ProbeTCP struct{}

// Metadata returns the framework-visible spec for probe_tcp.
func (ProbeTCP) Metadata() skill.Metadata {
	return skill.Metadata{
		Key:         "host_probe_tcp",
		Name:        "TCP 连通性探测",
		Description: "对目标 host:port 发起 TCP 连接，返回连通状态 + 延迟",
		Class:       skill.ClassSafe,
		Category:    "network",
		Params: skill.ParamSchema{
			{Name: "target", Param: skill.Param{
				Type: "string", Required: true,
				Desc: "目标地址，host:port 形式，例如 google.com:443",
			}},
			{Name: "timeout_ms", Param: skill.Param{
				Type: "int", Default: 3000,
				Desc: "拨号超时（毫秒），默认 3000",
			}},
		},
		ResultPreview: "{ok, latency_ms, error?}",
	}
}

type probeTCPParams struct {
	Target    string `json:"target"`
	TimeoutMS int    `json:"timeout_ms"`
}

type probeTCPResult struct {
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

// Execute opens a TCP connection to target with the configured timeout
// and returns OK + latency. Errors land in the result Error field rather
// than as a Go error so the audit trail stays consistent.
func (ProbeTCP) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p probeTCPParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("probe_tcp: decode params: %w", err)
		}
	}
	if p.Target == "" {
		return nil, fmt.Errorf("probe_tcp: target required")
	}
	if p.TimeoutMS <= 0 {
		p.TimeoutMS = 3000
	}
	timeout := time.Duration(p.TimeoutMS) * time.Millisecond

	res := probeTCPResult{}
	start := time.Now()
	conn, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", p.Target)
	res.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		res.OK = false
		res.Error = err.Error()
	} else {
		res.OK = true
		_ = conn.Close()
	}
	return json.Marshal(res)
}
