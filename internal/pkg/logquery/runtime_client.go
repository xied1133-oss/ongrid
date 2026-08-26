package logquery

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// LokiEndpoint is the complete Manager-side connection configuration for the
// currently selected Loki-compatible query endpoint.
type LokiEndpoint struct {
	URL           string
	BasicUser     string
	BasicPassword string
	TLSInsecure   bool
}

// LokiEndpointResolver returns the current Loki-compatible query endpoint.
// Implementations may read runtime settings; RuntimeClient resolves it before
// every public operation so configuration changes do not require a restart.
type LokiEndpointResolver interface {
	ResolveLokiEndpoint(ctx context.Context) (LokiEndpoint, error)
}

// RuntimeClient implements the Loki query and backend-neutral search surfaces
// while allowing URL, Basic Auth and TLS settings to change at runtime. The
// underlying HTTP client is reused until the resolved endpoint changes.
type RuntimeClient struct {
	resolver LokiEndpointResolver
	log      *slog.Logger

	mu             sync.Mutex
	cachedEndpoint LokiEndpoint
	cachedClient   *Client
	cachedHTTP     *http.Client
}

// NewRuntimeClient builds a concurrency-safe runtime-resolved Loki client.
func NewRuntimeClient(resolver LokiEndpointResolver, log *slog.Logger) *RuntimeClient {
	if log == nil {
		log = slog.Default()
	}
	return &RuntimeClient{resolver: resolver, log: log}
}

func (c *RuntimeClient) client(ctx context.Context) (*Client, error) {
	if c == nil || c.resolver == nil {
		return nil, errors.New("logquery: Loki endpoint resolver is unavailable")
	}
	endpoint, err := c.resolver.ResolveLokiEndpoint(ctx)
	if err != nil {
		return nil, fmt.Errorf("logquery: resolve Loki endpoint: %w", err)
	}
	endpoint.URL = strings.TrimRight(strings.TrimSpace(endpoint.URL), "/")
	endpoint.BasicUser = strings.TrimSpace(endpoint.BasicUser)
	if err := validateLokiEndpoint(endpoint); err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cachedClient != nil && c.cachedEndpoint == endpoint {
		return c.cachedClient, nil
	}

	httpClient := newLokiHTTPClient(endpoint)
	client := NewWithHTTPClient(endpoint.URL, httpClient, c.log)
	previousHTTP := c.cachedHTTP
	c.cachedEndpoint = endpoint
	c.cachedClient = client
	c.cachedHTTP = httpClient
	if previousHTTP != nil {
		previousHTTP.CloseIdleConnections()
	}
	return client, nil
}

func validateLokiEndpoint(endpoint LokiEndpoint) error {
	if endpoint.URL == "" {
		return errors.New("logquery: Loki endpoint URL is empty")
	}
	parsed, err := url.Parse(endpoint.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("logquery: Loki endpoint URL must be an absolute HTTP(S) URL")
	}
	if endpoint.BasicUser == "" && endpoint.BasicPassword != "" {
		return errors.New("logquery: Loki Basic Auth user is required when password is configured")
	}
	return nil
}

func newLokiHTTPClient(endpoint LokiEndpoint) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if endpoint.TLSInsecure {
		transport.TLSClientConfig = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true, //nolint:gosec // explicit admin-only test-environment switch
		}
	}
	var roundTripper http.RoundTripper = transport
	if endpoint.BasicUser != "" {
		roundTripper = &lokiBasicAuthRoundTripper{
			base:     transport,
			user:     endpoint.BasicUser,
			password: endpoint.BasicPassword,
		}
	}
	return &http.Client{Transport: roundTripper, Timeout: defaultTimeout}
}

type lokiBasicAuthRoundTripper struct {
	base     http.RoundTripper
	user     string
	password string
}

func (t *lokiBasicAuthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.SetBasicAuth(t.user, t.password)
	return t.base.RoundTrip(cloned)
}

func (c *RuntimeClient) QueryRange(ctx context.Context, opts QueryRangeOptions) (*QueryRangeResult, error) {
	client, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	return client.QueryRange(ctx, opts)
}

func (c *RuntimeClient) LabelNames(ctx context.Context, start, end time.Time) ([]string, error) {
	client, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	return client.LabelNames(ctx, start, end)
}

func (c *RuntimeClient) LabelValues(ctx context.Context, name string, start, end time.Time) ([]string, error) {
	client, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	return client.LabelValues(ctx, name, start, end)
}

func (c *RuntimeClient) Search(ctx context.Context, req SearchRequest) (*SearchResult, error) {
	client, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	return client.Search(ctx, req)
}

func (c *RuntimeClient) Count(ctx context.Context, req SearchRequest) (uint64, error) {
	client, err := c.client(ctx)
	if err != nil {
		return 0, err
	}
	return client.Count(ctx, req)
}

func (c *RuntimeClient) CountGrouped(ctx context.Context, req SearchRequest, groupBy []string) ([]CountGroup, error) {
	client, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	return client.CountGrouped(ctx, req, groupBy)
}

func (c *RuntimeClient) Fields(ctx context.Context, start, end time.Time, scope Scope) ([]Field, error) {
	client, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	return client.Fields(ctx, start, end, scope)
}

func (c *RuntimeClient) FieldValues(ctx context.Context, req FieldValuesRequest) ([]string, error) {
	client, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	return client.FieldValues(ctx, req)
}

func (c *RuntimeClient) Histogram(ctx context.Context, req SearchRequest, interval time.Duration) ([]HistogramBucket, error) {
	client, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	return client.Histogram(ctx, req, interval)
}
