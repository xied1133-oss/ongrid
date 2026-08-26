package setting

import (
	"context"
	"strings"

	model "github.com/ongridio/ongrid/internal/manager/model/setting"
)

// WebSearchResolver reads the provider selection + per-provider config
// (Tavily / Brave key, SearXNG URL) from system_settings. Implements
// the builtin.WebSearchConfigResolver interface declared in
// internal/skill/builtin/web_search.go so cmd/main.go can wire the
// skill at boot without internal/skill importing biz/setting.
//
// Mirrors LokiResolver / TempoResolver — single dependency on Service,
// nil-safe Get path so the skill returns "not configured" cleanly when
// the row is absent.
type WebSearchResolver struct {
	svc *Service
}

// NewWebSearchResolver builds the resolver. svc must be non-nil; the
// resolver returns "" / defaults otherwise.
func NewWebSearchResolver(svc *Service) *WebSearchResolver {
	return &WebSearchResolver{svc: svc}
}

func (r *WebSearchResolver) get(ctx context.Context, key string) string {
	if r == nil || r.svc == nil {
		return ""
	}
	v, _, err := r.svc.Get(ctx, model.CategoryWebSearch, key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

// Provider returns the configured provider name, lowercased.
//
// Selection rules (in priority order):
//
//  1. Explicit `websearch.provider` row → use it (validated against the
//     known set; unknown values fall through to step 2).
//  2. Tavily key set + nothing else → "tavily" (back-compat with
//     installs that pre-date SearXNG; swapping default would silently
//     reroute traffic).
//  3. Brave key set + nothing else → "brave".
//  4. Default → "searxng".
//
// Note: when both SearXNG and Tavily are configured (provider unset),
// SearXNG wins per spec — it's free / unlimited.
func (r *WebSearchResolver) Provider(ctx context.Context) string {
	if r == nil {
		return model.ProviderSearxng
	}
	switch strings.ToLower(r.get(ctx, model.KeyWebSearchProvider)) {
	case model.ProviderSearxng:
		return model.ProviderSearxng
	case model.ProviderTavily:
		return model.ProviderTavily
	case model.ProviderBrave:
		return model.ProviderBrave
	}
	// Inferred default: SearXNG always wins when no explicit choice has
	// been made — it's the zero-config baseline that always works in
	// the embedded compose stack.
	return model.ProviderSearxng
}

// SearxngURL returns the configured SearXNG base URL. Falls back to the
// docker-internal default (http://searxng:8080).
func (r *WebSearchResolver) SearxngURL(ctx context.Context) string {
	if r == nil {
		return model.DefaultSearxngURL
	}
	if v := r.get(ctx, model.KeySearxngURL); v != "" {
		return strings.TrimRight(v, "/")
	}
	return model.DefaultSearxngURL
}

// TavilyAPIKey implements the Tavily-key half of WebSearchConfigResolver.
// Returns the trimmed Tavily key, or "" when not configured / on error.
func (r *WebSearchResolver) TavilyAPIKey(ctx context.Context) string {
	return r.get(ctx, model.KeyTavilyAPIKey)
}

// BraveAPIKey returns the trimmed Brave Search API key. "" when unset.
func (r *WebSearchResolver) BraveAPIKey(ctx context.Context) string {
	return r.get(ctx, model.KeyBraveAPIKey)
}
