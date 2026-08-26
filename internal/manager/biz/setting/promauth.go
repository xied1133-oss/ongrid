package setting

import (
	"context"
	"strings"

	model "github.com/ongridio/ongrid/internal/manager/model/setting"
	"github.com/ongridio/ongrid/internal/pkg/promauth"
)

// PromResolver implements three resolver interfaces against the
// system_settings table:
//
//   - promauth.Resolver       (Bearer / Basic)
//   - promwrite.EndpointResolver (full remote_write URL with fallback)
//   - promquery.BaseURLResolver  (PromQL API root with fallback)
//
// All three reads route through Service.Get, which has its own internal
// cache; the round-tripper layers a 5s TTL on top of that, so an admin
// edit in the UI propagates within ~5s without restarting the manager.
//
// fallbackQueryURL / fallbackWriteURL are the env-derived bootstrap
// values from cfg.Prom — used when the corresponding system_settings
// row is missing or empty. This way a fresh install with nothing in the
// DB still talks to the embedded Prometheus.
type PromResolver struct {
	svc               *Service
	fallbackQueryURL  string
	fallbackWriteURL  string
}

// NewPromResolver wires the resolver. svc must be non-nil. fallback*URL
// are taken from cfg.Prom.URL and cfg.Prom.RemoteWriteURL respectively.
func NewPromResolver(svc *Service, fallbackQueryURL, fallbackWriteURL string) *PromResolver {
	return &PromResolver{
		svc:              svc,
		fallbackQueryURL: strings.TrimRight(fallbackQueryURL, "/"),
		fallbackWriteURL: fallbackWriteURL,
	}
}

func (r *PromResolver) get(ctx context.Context, key string) string {
	if r.svc == nil {
		return ""
	}
	v, _, err := r.svc.Get(ctx, model.CategoryProm, key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

// Resolve implements promauth.Resolver. Missing rows collapse to empty
// strings (= no auth header).
func (r *PromResolver) Resolve(ctx context.Context) (promauth.Config, error) {
	return promauth.Config{
		BearerToken:   r.get(ctx, model.KeyPromBearerToken),
		BasicUser:     r.get(ctx, model.KeyPromBasicUser),
		BasicPassword: r.get(ctx, model.KeyPromBasicPassword),
	}, nil
}

// ResolveBaseURL implements promquery.BaseURLResolver. Falls back to
// the env-seeded URL when system_settings has no value.
func (r *PromResolver) ResolveBaseURL(ctx context.Context) (string, error) {
	if v := r.get(ctx, model.KeyPromQueryURL); v != "" {
		return strings.TrimRight(v, "/"), nil
	}
	return r.fallbackQueryURL, nil
}

// ResolveWriteURL implements promwrite.EndpointResolver. Returns the
// admin-configured remote_write_url if set; otherwise composes
// query_url + /api/v1/write to mirror the original New() semantics.
func (r *PromResolver) ResolveWriteURL(ctx context.Context) (string, error) {
	if v := r.get(ctx, model.KeyPromRemoteWriteURL); v != "" {
		return v, nil
	}
	if r.fallbackWriteURL != "" {
		return r.fallbackWriteURL, nil
	}
	base, err := r.ResolveBaseURL(ctx)
	if err != nil {
		return "", err
	}
	if base == "" {
		return "", nil
	}
	return base + "/api/v1/write", nil
}
