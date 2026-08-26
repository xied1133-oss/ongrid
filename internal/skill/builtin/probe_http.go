package builtin

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ongridio/ongrid/internal/skill"
)

func init() { skill.Register(&ProbeHTTP{}) }

// ProbeHTTP issues a HEAD or GET request and reports status + latency +
// content length. Safe: read-only HTTP method only (no PUT/POST). TLS
// verification is intentionally skipped — edge devices commonly probe
// self-signed internal services.
type ProbeHTTP struct{}

// Metadata returns the framework-visible spec for probe_http.
func (ProbeHTTP) Metadata() skill.Metadata {
	return skill.Metadata{
		Key:         "host_probe_http",
		Name:        "HTTP 探测",
		Description: "对 URL 发起 HEAD/GET 请求，返回状态码 + 延迟 + 内容长度",
		Class:       skill.ClassSafe,
		Category:    "network",
		Params: skill.ParamSchema{
			{Name: "url", Param: skill.Param{
				Type: "string", Required: true,
				Desc: "完整 URL，例如 https://example.com/health",
			}},
			{Name: "method", Param: skill.Param{
				Type: "enum", Default: "HEAD", Enum: []string{"GET", "HEAD"},
				Desc: "HTTP 方法，默认 HEAD",
			}},
			{Name: "timeout_ms", Param: skill.Param{
				Type: "int", Default: 5000,
				Desc: "请求超时（毫秒），默认 5000",
			}},
		},
		ResultPreview: "{status_code, latency_ms, content_length, error?}",
	}
}

type probeHTTPParams struct {
	URL       string `json:"url"`
	Method    string `json:"method"`
	TimeoutMS int    `json:"timeout_ms"`
}

type probeHTTPResult struct {
	StatusCode    int    `json:"status_code"`
	LatencyMS     int64  `json:"latency_ms"`
	ContentLength int64  `json:"content_length"`
	Error         string `json:"error,omitempty"`
}

// Execute makes a single HTTP request with the configured timeout. For
// GET it counts the actual response body bytes (Content-Length header may
// be missing or wrong); for HEAD it relies on the header.
func (ProbeHTTP) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p probeHTTPParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("probe_http: decode params: %w", err)
		}
	}
	if p.URL == "" {
		return nil, fmt.Errorf("probe_http: url required")
	}
	switch p.Method {
	case "":
		p.Method = "HEAD"
	case "GET", "HEAD":
	default:
		return nil, fmt.Errorf("probe_http: method must be GET or HEAD, got %q", p.Method)
	}
	if p.TimeoutMS <= 0 {
		p.TimeoutMS = 5000
	}
	timeout := time.Duration(p.TimeoutMS) * time.Millisecond

	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	res := probeHTTPResult{}
	req, err := http.NewRequestWithContext(ctx, p.Method, p.URL, nil)
	if err != nil {
		res.Error = err.Error()
		return json.Marshal(res)
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		res.LatencyMS = time.Since(start).Milliseconds()
		res.Error = err.Error()
		return json.Marshal(res)
	}
	defer resp.Body.Close()

	res.StatusCode = resp.StatusCode
	if p.Method == "GET" {
		n, _ := io.Copy(io.Discard, resp.Body)
		res.ContentLength = n
	} else {
		res.ContentLength = resp.ContentLength
	}
	res.LatencyMS = time.Since(start).Milliseconds()
	return json.Marshal(res)
}
