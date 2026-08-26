// Package logquery is the manager-side Loki query client.
//
// Mirrors internal/pkg/promquery and internal/pkg/tracequery — same
// shape, separate package so the three signal types stay independently
// swappable. Backend-decoupled name (logquery, not lokiquery) per
// — when Loki gets swapped for VictoriaLogs the
// package name and all import paths stay valid.
//
// Used by:
//   - alert evaluators (log_match / log_volume — PR-C4)
//   - the Logs UI page's manager-proxied query endpoint
//   - the AI tool query_logs (PR-C4)
package logquery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// QueryRangeResult is the unmarshalled `data` field from
// /loki/api/v1/query_range. Result holds either a streams response (log
// lines) or a matrix response (metric_over_time aggregations) — kept as
// raw JSON so the SPA can switch on `ResultType` ("streams" | "matrix")
// itself, mirroring promquery's pattern.
type QueryRangeResult struct {
	ResultType string          `json:"resultType"`
	Result     json.RawMessage `json:"result"`
}

// LabelValuesResult is the data field from /loki/api/v1/label/<name>/values.
// The wire shape is `{"status":"success","data":["v1","v2",...]}` — we
// expose the slice directly.
type LabelValuesResult struct {
	Values []string
}

// BaseURLResolver returns the current Loki API root. Invoked once per
// call so admin-side URL changes propagate without a manager restart
// (mirrors promquery / tracequery patterns).
type BaseURLResolver interface {
	ResolveBaseURL(ctx context.Context) (string, error)
}

// Client wraps Loki's /loki/api/v1/query_range + /label/<name>/values.
// Safe for concurrent use.
type Client struct {
	base       BaseURLResolver
	httpClient *http.Client
	log        *slog.Logger
}

// defaultTimeout caps a single Loki round-trip. Loki query_range can be
// slow for wide windows / large limits; 30s matches promquery /
// tracequery for symmetry.
const defaultTimeout = 30 * time.Second

type staticBase struct{ url string }

func (s staticBase) ResolveBaseURL(_ context.Context) (string, error) {
	if s.url == "" {
		return "", errors.New("logquery: baseURL is empty")
	}
	return s.url, nil
}

// New builds a Client with a static baseURL. Use this when the URL
// won't change at runtime (e.g. internal Loki at http://loki:3100).
func New(baseURL string, log *slog.Logger) *Client {
	return NewWithHTTPClient(baseURL, &http.Client{Timeout: defaultTimeout}, log)
}

// NewWithHTTPClient is the test seam.
func NewWithHTTPClient(baseURL string, hc *http.Client, log *slog.Logger) *Client {
	return newClient(staticBase{url: strings.TrimRight(baseURL, "/")}, hc, log)
}

// NewWithResolverAndHTTPClient resolves the URL for each call while keeping a
// fixed HTTP transport. Use RuntimeClient when auth or TLS can also change.
func NewWithResolverAndHTTPClient(r BaseURLResolver, hc *http.Client, log *slog.Logger) *Client {
	return newClient(r, hc, log)
}

func newClient(r BaseURLResolver, hc *http.Client, log *slog.Logger) *Client {
	if log == nil {
		log = slog.Default()
	}
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{base: r, httpClient: hc, log: log}
}

// QueryRangeOptions narrows a /loki/api/v1/query_range call.
type QueryRangeOptions struct {
	// Query is the LogQL expression. Required.
	Query string
	// Start / End define the search window. Required.
	Start time.Time
	End   time.Time
	// Limit caps result rows. Loki defaults to 100; we send 1000 so the
	// UI table has enough material to scroll.
	Limit int
	// Step is for metric queries (count_over_time, rate, etc). When 0
	// Loki picks an auto step. Ignored for stream queries.
	Step time.Duration
	// Direction: "forward" or "backward". Default backward (newest first
	// — matches what operators expect from a log search).
	Direction string
}

// QueryRange runs a /loki/api/v1/query_range. Returns the data block;
// the X-Scope-OrgID header is hardcoded to "ongrid" (matches what nginx
// injects on the data plane ingest path) so single-tenant installs work
// without admin tweaks.
func (c *Client) QueryRange(ctx context.Context, opts QueryRangeOptions) (*QueryRangeResult, error) {
	if strings.TrimSpace(opts.Query) == "" {
		return nil, errors.New("logquery: query is empty")
	}
	if opts.Start.IsZero() || opts.End.IsZero() {
		return nil, errors.New("logquery: start/end required")
	}
	if !opts.End.After(opts.Start) {
		return nil, errors.New("logquery: end must be after start")
	}

	q := url.Values{}
	q.Set("query", opts.Query)
	// Loki wants nanosecond unix timestamps as strings.
	q.Set("start", strconv.FormatInt(opts.Start.UnixNano(), 10))
	q.Set("end", strconv.FormatInt(opts.End.UnixNano(), 10))
	limit := opts.Limit
	if limit <= 0 {
		limit = 1000
	}
	q.Set("limit", strconv.Itoa(limit))
	if opts.Step > 0 {
		q.Set("step", opts.Step.String())
	}
	dir := opts.Direction
	if dir == "" {
		dir = "backward"
	}
	q.Set("direction", dir)

	body, err := c.do(ctx, "/loki/api/v1/query_range", q)
	if err != nil {
		return nil, err
	}
	// Loki wraps in {"status":"success","data":{"resultType":...,"result":...}}.
	var env struct {
		Status string           `json:"status"`
		Data   QueryRangeResult `json:"data"`
		Error  string           `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("logquery: decode: %w", err)
	}
	if env.Status != "success" {
		return nil, fmt.Errorf("logquery: %s", env.Error)
	}
	return &env.Data, nil
}

// LabelNames lists every label key Loki has indexed in the window.
// Used by the SPA's Logs page to populate the label selector autocomplete.
func (c *Client) LabelNames(ctx context.Context, start, end time.Time) ([]string, error) {
	q := url.Values{}
	if !start.IsZero() {
		q.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	}
	if !end.IsZero() {
		q.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	}
	body, err := c.do(ctx, "/loki/api/v1/labels", q)
	if err != nil {
		return nil, err
	}
	var env struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
		Error  string   `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("logquery: decode: %w", err)
	}
	if env.Status != "success" {
		return nil, fmt.Errorf("logquery: %s", env.Error)
	}
	return env.Data, nil
}

// LabelValues lists the known values for one label.
func (c *Client) LabelValues(ctx context.Context, name string, start, end time.Time) ([]string, error) {
	if name == "" {
		return nil, errors.New("logquery: label name is empty")
	}
	q := url.Values{}
	if !start.IsZero() {
		q.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	}
	if !end.IsZero() {
		q.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	}
	path := "/loki/api/v1/label/" + url.PathEscape(name) + "/values"
	body, err := c.do(ctx, path, q)
	if err != nil {
		return nil, err
	}
	var env struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
		Error  string   `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("logquery: decode: %w", err)
	}
	if env.Status != "success" {
		return nil, fmt.Errorf("logquery: %s", env.Error)
	}
	return env.Data, nil
}

func (c *Client) do(ctx context.Context, path string, q url.Values) ([]byte, error) {
	base, rerr := c.base.ResolveBaseURL(ctx)
	if rerr != nil {
		return nil, fmt.Errorf("logquery: resolve baseURL: %w", rerr)
	}
	full := base + path
	if q != nil && len(q) > 0 {
		full += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, fmt.Errorf("logquery: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ongrid-logquery/0.1")
	// Single-tenant install — nginx injects X-Scope-OrgID: ongrid on
	// the ingest side, so use the same on read.
	req.Header.Set("X-Scope-OrgID", "ongrid")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("logquery: %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 8 MiB cap — well above typical query_range payload, well below
	// "OOM the manager process". If a query consistently exceeds this
	// the operator should narrow the window or add a limit.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("logquery: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		c.log.Warn("logquery: non-200",
			slog.Int("status", resp.StatusCode),
			slog.String("path", path),
		)
		return nil, fmt.Errorf("logquery: %s returned %d: %s", path, resp.StatusCode, truncate(string(body), 512))
	}
	return body, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
