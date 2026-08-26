package promquery

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

// InstantResult is the unmarshalled `data` field from a Prom query response,
// i.e. the bit the LLM cares about. The full Prom response wraps this in
// `{"status":"success","data":<here>}`. We expose `Result` as raw JSON so
// the AI tool can hand it straight back to the model without losing any
// shape (matrix vs vector vs scalar).
type InstantResult struct {
	ResultType string          `json:"resultType"`
	Result     json.RawMessage `json:"result"`
}

// BaseURLResolver returns the current Prometheus API root. It's invoked
// once per Query / QueryRange call. The wiring layer (cmd/ongrid +
// biz/setting) caches with ~5s TTL so admin UI edits propagate without
// a restart but don't hammer the DB on every PromQL invocation.
type BaseURLResolver interface {
	ResolveBaseURL(ctx context.Context) (string, error)
}

// Client wraps /api/v1/query and /api/v1/query_range. It is safe for
// concurrent use.
type Client struct {
	base       BaseURLResolver
	httpClient *http.Client
	log        *slog.Logger
}

// defaultTimeout caps a single query round-trip. Range queries can be
// expensive on the Prom side; 30s is the standard ceiling.
const defaultTimeout = 30 * time.Second

type staticBase struct{ url string }

func (s staticBase) ResolveBaseURL(_ context.Context) (string, error) {
	if s.url == "" {
		return "", errors.New("promquery: baseURL is empty")
	}
	return s.url, nil
}

// New builds a Client with the default http.Client and a derived logger.
// baseURL is the Prom server root (e.g. "http://prometheus:9090"); the
// /api/v1/* suffix is appended on each call.
func New(baseURL string, log *slog.Logger) *Client {
	return NewWithHTTPClient(baseURL, &http.Client{Timeout: defaultTimeout}, log)
}

// NewWithHTTPClient is the test seam for the static-baseURL form.
func NewWithHTTPClient(baseURL string, hc *http.Client, log *slog.Logger) *Client {
	return newClient(staticBase{url: strings.TrimRight(baseURL, "/")}, hc, log)
}

// NewWithResolverAndHTTPClient is the dynamic form. The resolver is asked
// for the current base URL on each Query / QueryRange call.
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

// promResponse is the wire envelope; status="success" means data is valid.
// Errors are surfaced via errorType / error fields and a non-2xx status.
type promResponse struct {
	Status    string          `json:"status"`
	Data      json.RawMessage `json:"data,omitempty"`
	ErrorType string          `json:"errorType,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// Query runs an instant PromQL query at time ts.
func (c *Client) Query(ctx context.Context, expr string, ts time.Time) (*InstantResult, error) {
	q := url.Values{}
	q.Set("query", expr)
	if !ts.IsZero() {
		q.Set("time", strconv.FormatFloat(float64(ts.UnixNano())/1e9, 'f', -1, 64))
	}
	return c.do(ctx, "/api/v1/query", q)
}

// QueryRange runs a range PromQL query over [start, end] with the given step.
func (c *Client) QueryRange(ctx context.Context, expr string, start, end time.Time, step time.Duration) (*InstantResult, error) {
	if step <= 0 {
		return nil, fmt.Errorf("promquery: step must be > 0, got %v", step)
	}
	if !end.After(start) {
		return nil, fmt.Errorf("promquery: end (%v) must be after start (%v)", end, start)
	}
	q := url.Values{}
	q.Set("query", expr)
	q.Set("start", strconv.FormatFloat(float64(start.UnixNano())/1e9, 'f', -1, 64))
	q.Set("end", strconv.FormatFloat(float64(end.UnixNano())/1e9, 'f', -1, 64))
	q.Set("step", strconv.FormatFloat(step.Seconds(), 'f', -1, 64))
	return c.do(ctx, "/api/v1/query_range", q)
}

// do builds the GET, decodes the wire envelope, and returns the InstantResult.
func (c *Client) do(ctx context.Context, path string, q url.Values) (*InstantResult, error) {
	base, rerr := c.base.ResolveBaseURL(ctx)
	if rerr != nil {
		return nil, fmt.Errorf("promquery: resolve baseURL: %w", rerr)
	}
	full := base + path + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, fmt.Errorf("promquery: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ongrid-promquery/0.1")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("promquery: %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024)) // 8 MiB cap
	if err != nil {
		return nil, fmt.Errorf("promquery: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		// Prom uses 400 for query parse errors; we want to decode that JSON
		// so the caller can see the errorType. Anything else is an outright
		// transport / server failure.
		c.log.Warn("promquery: non-200",
			slog.Int("status", resp.StatusCode),
			slog.String("path", path),
		)
		return nil, fmt.Errorf("promquery: %s returned %d: %s", path, resp.StatusCode, string(body))
	}

	var env promResponse
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("promquery: decode envelope: %w", err)
	}
	if env.Status != "success" {
		return nil, fmt.Errorf("promquery: %s status=%s errorType=%s error=%s",
			path, env.Status, env.ErrorType, env.Error)
	}
	var ir InstantResult
	if err := json.Unmarshal(env.Data, &ir); err != nil {
		return nil, fmt.Errorf("promquery: decode data: %w", err)
	}
	return &ir, nil
}
