package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/ongridio/ongrid/internal/skill"
)

func init() { skill.Register(&ProbeDNS{}) }

// ProbeDNS resolves a hostname to A/AAAA addresses via the system
// resolver. Safe: pure read.
type ProbeDNS struct{}

// Metadata returns the framework-visible spec for probe_dns.
func (ProbeDNS) Metadata() skill.Metadata {
	return skill.Metadata{
		Key:         "host_probe_dns",
		Name:        "DNS 解析",
		Description: "DNS 解析目标 host，返回 A/AAAA 记录",
		Class:       skill.ClassSafe,
		Category:    "network",
		Params: skill.ParamSchema{
			{Name: "host", Param: skill.Param{
				Type: "string", Required: true,
				Desc: "要解析的主机名，例如 example.com",
			}},
			{Name: "timeout_ms", Param: skill.Param{
				Type: "int", Default: 3000,
				Desc: "解析超时（毫秒），默认 3000",
			}},
		},
		ResultPreview: "{addrs, latency_ms, error?}",
	}
}

type probeDNSParams struct {
	Host      string `json:"host"`
	TimeoutMS int    `json:"timeout_ms"`
}

type probeDNSResult struct {
	Addrs     []string `json:"addrs"`
	LatencyMS int64    `json:"latency_ms"`
	Error     string   `json:"error,omitempty"`
}

// Execute calls net.DefaultResolver.LookupIPAddr. Timeout is enforced via
// a child context so a slow resolver doesn't pin the dispatcher
// goroutine.
func (ProbeDNS) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p probeDNSParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("probe_dns: decode params: %w", err)
		}
	}
	if p.Host == "" {
		return nil, fmt.Errorf("probe_dns: host required")
	}
	if p.TimeoutMS <= 0 {
		p.TimeoutMS = 3000
	}

	res := probeDNSResult{Addrs: []string{}}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(p.TimeoutMS)*time.Millisecond)
	defer cancel()

	start := time.Now()
	ips, err := net.DefaultResolver.LookupIPAddr(cctx, p.Host)
	res.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		res.Error = err.Error()
		return json.Marshal(res)
	}
	for _, ip := range ips {
		res.Addrs = append(res.Addrs, ip.IP.String())
	}
	return json.Marshal(res)
}
