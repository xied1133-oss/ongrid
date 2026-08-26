package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/ongridio/ongrid/internal/skill"
)

func init() { skill.Register(&ProbePing{}) }

// ProbePing runs a bounded ICMP ping using the host ping binary. Safe: it only
// sends a small fixed number of ICMP probes and never goes through a shell.
type ProbePing struct{}

func (ProbePing) Metadata() skill.Metadata {
	return skill.Metadata{
		Key:         "host_probe_ping",
		Name:        "Ping 探测",
		Description: "对目标 host 发起短时 ICMP ping，返回退出码、耗时和原始输出",
		Class:       skill.ClassSafe,
		Category:    "network",
		Params: skill.ParamSchema{
			{Name: "host", Param: skill.Param{
				Type: "string", Required: true,
				Desc: "目标主机名或 IP，例如 8.8.8.8 或 example.com",
			}},
			{Name: "count", Param: skill.Param{
				Type: "int", Default: 4,
				Desc: "发送包数，默认 4，最大 10",
			}},
			{Name: "timeout_ms", Param: skill.Param{
				Type: "int", Default: 3000,
				Desc: "单包等待超时（毫秒），默认 3000",
			}},
		},
		ResultPreview: "{ok, exit_code, duration_ms, stdout, stderr}",
	}
}

type probePingParams struct {
	Host      string `json:"host"`
	Count     int    `json:"count"`
	TimeoutMS int    `json:"timeout_ms"`
}

type probePingResult struct {
	OK         bool   `json:"ok"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	Error      string `json:"error,omitempty"`
}

func (ProbePing) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	p, err := normalizeProbePingParams(params)
	if err != nil {
		return nil, err
	}
	timeoutSeconds := (p.TimeoutMS + 999) / 1000
	if timeoutSeconds < 1 {
		timeoutSeconds = 1
	}
	outer := time.Duration(p.Count*timeoutSeconds+2) * time.Second
	cctx, cancel := context.WithTimeout(ctx, outer)
	defer cancel()

	cmd := exec.CommandContext(cctx, "ping", "-c", fmt.Sprint(p.Count), "-W", fmt.Sprint(timeoutSeconds), p.Host)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err = cmd.Run()
	res := probePingResult{
		OK:         err == nil,
		ExitCode:   0,
		DurationMS: time.Since(start).Milliseconds(),
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
	}
	if err != nil {
		res.ExitCode = 1
		res.Error = err.Error()
		if ee, ok := err.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
		}
		if cctx.Err() != nil {
			res.Error = cctx.Err().Error()
		}
	}
	return json.Marshal(res)
}

func normalizeProbePingParams(params json.RawMessage) (probePingParams, error) {
	var p probePingParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return p, fmt.Errorf("probe_ping: decode params: %w", err)
		}
	}
	if p.Host == "" {
		return p, fmt.Errorf("probe_ping: host required")
	}
	if p.Count <= 0 {
		p.Count = 4
	}
	if p.Count > 10 {
		p.Count = 10
	}
	if p.TimeoutMS <= 0 {
		p.TimeoutMS = 3000
	}
	if p.TimeoutMS > 10000 {
		p.TimeoutMS = 10000
	}
	return p, nil
}
