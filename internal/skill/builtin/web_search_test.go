package builtin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ongridio/ongrid/internal/skill"
)

// stubKeyResolver is the legacy test double for the deprecated
// builtin.TavilyKeyResolver. Kept so the back-compat shim
// (SetWebSearchKeyResolver → legacyTavilyResolver) is exercised.
type stubKeyResolver struct{ key string }

func (s stubKeyResolver) TavilyAPIKey(_ context.Context) string { return s.key }

// stubConfigResolver is the test double for the new multi-provider
// WebSearchConfigResolver. Each field maps to one resolver method;
// providerOverride lets tests pin a specific backend.
type stubConfigResolver struct {
	provider    string
	searxngURL  string
	tavilyKey   string
	braveKey    string
}

func (s stubConfigResolver) Provider(_ context.Context) string {
	if s.provider == "" {
		return providerSearxng
	}
	return s.provider
}
func (s stubConfigResolver) SearxngURL(_ context.Context) string {
	if s.searxngURL == "" {
		return defaultSearxngURL
	}
	return s.searxngURL
}
func (s stubConfigResolver) TavilyAPIKey(_ context.Context) string { return s.tavilyKey }
func (s stubConfigResolver) BraveAPIKey(_ context.Context) string  { return s.braveKey }

// resetWebSearch puts the package singleton back into a known state
// after a test mutates it. Subtests that override the resolver /
// endpoints / http client must defer this so they don't leak state
// into other tests.
func resetWebSearch() {
	SetWebSearchConfigResolver(nil)
	SetWebSearchHTTPClient(nil)
	SetWebSearchTavilyEndpoint("https://api.tavily.com/search")
	SetWebSearchBraveEndpoint("https://api.search.brave.com/res/v1/web/search")
}

func TestWebSearch_Metadata(t *testing.T) {
	defer resetWebSearch()
	m := WebSearch.Metadata()
	if err := m.Validate(); err != nil {
		t.Fatalf("metadata invalid: %v", err)
	}
	if m.Key != "web_search" {
		t.Fatalf("key=%q", m.Key)
	}
	if m.EffectiveScope() != skill.ScopeManager {
		t.Fatalf("scope=%v, want manager", m.EffectiveScope())
	}
	if m.EffectiveClass() != skill.ClassSafe {
		t.Fatalf("class=%v, want safe", m.EffectiveClass())
	}
}

// TestWebSearch_DefaultProvider_IsSearxng verifies the spec-mandated
// default: when no provider is explicitly set in params, the resolver
// returns "searxng" (zero-config baseline) and dispatch lands on the
// searxng sub-fn.
func TestWebSearch_DefaultProvider_IsSearxng(t *testing.T) {
	defer resetWebSearch()

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/search" {
			t.Errorf("path=%q, want /search", r.URL.Path)
		}
		if r.URL.Query().Get("format") != "json" {
			t.Errorf("format=%q", r.URL.Query().Get("format"))
		}
		_, _ = w.Write([]byte(`{"query":"hi","results":[
			{"title":"K8s docs","url":"https://k8s.io/","content":"snip","publishedDate":"2024-01-01"}
		]}`))
	}))
	defer srv.Close()

	SetWebSearchConfigResolver(stubConfigResolver{searxngURL: srv.URL})
	SetWebSearchHTTPClient(srv.Client())

	out, err := WebSearch.Execute(context.Background(), json.RawMessage(`{"query":"k8s"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if hits != 1 {
		t.Fatalf("expected 1 searxng hit, got %d", hits)
	}
	var resp webSearchResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Provider != providerSearxng {
		t.Errorf("provider=%q, want searxng", resp.Provider)
	}
	if len(resp.Results) != 1 || resp.Results[0].URL != "https://k8s.io/" {
		t.Errorf("results=%+v", resp.Results)
	}
}

// TestWebSearch_FallbackToSearxng_WhenTavilyKeyEmpty asserts that a
// resolver with no provider preference and no Tavily key falls
// through to SearXNG, NOT to "skipped_reason: Tavily not configured".
// SearXNG is the zero-config baseline, Tavily is explicit-opt-in.
func TestWebSearch_FallbackToSearxng_WhenTavilyKeyEmpty(t *testing.T) {
	defer resetWebSearch()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"title":"x","url":"https://x.com","content":"y"}]}`))
	}))
	defer srv.Close()

	SetWebSearchConfigResolver(stubConfigResolver{
		provider:   "", // not set → defaults
		searxngURL: srv.URL,
		tavilyKey:  "", // explicitly empty
	})
	SetWebSearchHTTPClient(srv.Client())

	out, err := WebSearch.Execute(context.Background(), json.RawMessage(`{"query":"hi"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var resp webSearchResponse
	_ = json.Unmarshal(out, &resp)
	if resp.Provider != providerSearxng {
		t.Errorf("provider=%q, want searxng (fallback)", resp.Provider)
	}
	if resp.SkippedReason != "" {
		t.Errorf("expected fallthrough to searxng, got skipped_reason: %q", resp.SkippedReason)
	}
}

// TestWebSearch_ExplicitProviderTavily_NoKey_ReturnsSkippedReason
// covers the Tavily-without-key case. Provider is explicitly forced to
// "tavily" so the fallback to SearXNG doesn't kick in.
func TestWebSearch_ExplicitProviderTavily_NoKey_ReturnsSkippedReason(t *testing.T) {
	defer resetWebSearch()
	SetWebSearchConfigResolver(stubConfigResolver{provider: providerTavily, tavilyKey: ""})

	out, err := WebSearch.Execute(context.Background(), json.RawMessage(`{"query":"hello"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var resp webSearchResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.SkippedReason == "" {
		t.Fatalf("expected skipped_reason, got %s", string(out))
	}
	if !strings.Contains(resp.SkippedReason, "Tavily") {
		t.Fatalf("skipped reason should mention Tavily, got %q", resp.SkippedReason)
	}
	if resp.Provider != providerTavily {
		t.Errorf("provider=%q, want tavily", resp.Provider)
	}
}

// TestWebSearch_LegacyShim_NoKey_ReturnsSkippedReason verifies the
// back-compat path: callers still using SetWebSearchKeyResolver get a
// resolver that pins provider=tavily and behaves identically to the
// pre-SearXNG codebase.
func TestWebSearch_LegacyShim_NoKey_ReturnsSkippedReason(t *testing.T) {
	defer resetWebSearch()
	SetWebSearchKeyResolver(stubKeyResolver{key: ""})

	out, err := WebSearch.Execute(context.Background(), json.RawMessage(`{"query":"hello"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var resp webSearchResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.SkippedReason == "" || !strings.Contains(resp.SkippedReason, "Tavily") {
		t.Fatalf("expected Tavily skipped_reason, got %q", resp.SkippedReason)
	}
}

func TestWebSearch_Tavily_HappyPath_HitsAPI(t *testing.T) {
	defer resetWebSearch()

	var captured tavilyRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("want POST, got %s", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type=%q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{
			"answer": "Kubernetes OOM kill 是因为容器超过内存上限。",
			"results": [
				{"title":"K8s OOM 排查","url":"https://example.com/k8s-oom","content":"看 dmesg 和 cgroup memory 统计","published_date":"2024-09-01"},
				{"title":"Pod evicted","url":"https://example.com/pod-evicted","content":"OOMKilled status","published_date":"2024-08-15"}
			]
		}`))
	}))
	defer srv.Close()

	SetWebSearchTavilyEndpoint(srv.URL)
	SetWebSearchConfigResolver(stubConfigResolver{provider: providerTavily, tavilyKey: "test-key-abc"})
	SetWebSearchHTTPClient(srv.Client())

	args := json.RawMessage(`{"query":"kubernetes oom kill 排查","max_results":3,"include_domains":"example.com, kubernetes.io"}`)
	out, err := WebSearch.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if captured.APIKey != "test-key-abc" {
		t.Errorf("api_key forwarded=%q, want test-key-abc", captured.APIKey)
	}
	if captured.Query != "kubernetes oom kill 排查" {
		t.Errorf("query=%q", captured.Query)
	}
	if captured.MaxResults != 3 {
		t.Errorf("max_results=%d", captured.MaxResults)
	}
	if !captured.IncludeAnswer {
		t.Errorf("include_answer should be true")
	}
	if len(captured.IncludeDomains) != 2 || captured.IncludeDomains[0] != "example.com" || captured.IncludeDomains[1] != "kubernetes.io" {
		t.Errorf("include_domains=%v", captured.IncludeDomains)
	}

	var resp webSearchResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Provider != providerTavily {
		t.Errorf("provider=%q, want tavily", resp.Provider)
	}
	if !strings.Contains(resp.Answer, "OOM") {
		t.Errorf("answer not propagated: %q", resp.Answer)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("results=%d, want 2", len(resp.Results))
	}
	if resp.Results[0].Snippet != "看 dmesg 和 cgroup memory 统计" {
		t.Errorf("snippet=%q", resp.Results[0].Snippet)
	}
	if resp.Results[0].URL != "https://example.com/k8s-oom" {
		t.Errorf("url=%q", resp.Results[0].URL)
	}
}

func TestWebSearch_Tavily_Error_PropagatesAsGoError(t *testing.T) {
	defer resetWebSearch()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limit", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	SetWebSearchTavilyEndpoint(srv.URL)
	SetWebSearchConfigResolver(stubConfigResolver{provider: providerTavily, tavilyKey: "k"})
	SetWebSearchHTTPClient(srv.Client())

	_, err := WebSearch.Execute(context.Background(), json.RawMessage(`{"query":"x"}`))
	if err == nil {
		t.Fatal("expected error on Tavily 429")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error should include status: %v", err)
	}
}

func TestWebSearch_EmptyQuery(t *testing.T) {
	defer resetWebSearch()
	SetWebSearchConfigResolver(stubConfigResolver{provider: providerTavily, tavilyKey: "k"})

	if _, err := WebSearch.Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestWebSearch_Tavily_MaxResultsClamped(t *testing.T) {
	defer resetWebSearch()

	var captured tavilyRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()
	SetWebSearchTavilyEndpoint(srv.URL)
	SetWebSearchConfigResolver(stubConfigResolver{provider: providerTavily, tavilyKey: "k"})
	SetWebSearchHTTPClient(srv.Client())

	if _, err := WebSearch.Execute(context.Background(),
		json.RawMessage(`{"query":"x","max_results":99}`)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if captured.MaxResults != 10 {
		t.Errorf("max_results clamp failed, got %d", captured.MaxResults)
	}
}

// TestWebSearch_Searxng_HappyPath verifies the SearXNG dispatch path:
// GET /search?q=...&format=json, parsing of results[].title/url/content
// → normalised WebSearchResult, and that the provider field is populated.
func TestWebSearch_Searxng_HappyPath(t *testing.T) {
	defer resetWebSearch()

	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method=%s, want GET", r.Method)
		}
		if r.URL.Path != "/search" {
			t.Errorf("path=%q", r.URL.Path)
		}
		capturedQuery = r.URL.Query().Get("q")
		if got := r.URL.Query().Get("format"); got != "json" {
			t.Errorf("format=%q", got)
		}
		_, _ = w.Write([]byte(`{
			"query": "kubernetes oom",
			"results": [
				{"title":"K8s OOM","url":"https://k8s.io/oom","content":"explanation","publishedDate":"2024-09-01"},
				{"title":"Pod evicted","url":"https://example.com/evict","content":"OOMKilled","publishedDate":""}
			]
		}`))
	}))
	defer srv.Close()

	SetWebSearchConfigResolver(stubConfigResolver{
		provider:   providerSearxng,
		searxngURL: srv.URL,
	})
	SetWebSearchHTTPClient(srv.Client())

	out, err := WebSearch.Execute(context.Background(),
		json.RawMessage(`{"query":"kubernetes oom","max_results":5}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if capturedQuery != "kubernetes oom" {
		t.Errorf("upstream q=%q", capturedQuery)
	}
	var resp webSearchResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Provider != providerSearxng {
		t.Errorf("provider=%q, want searxng", resp.Provider)
	}
	if resp.Answer != "" {
		t.Errorf("searxng has no answer field; got %q", resp.Answer)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("results=%d, want 2", len(resp.Results))
	}
	if resp.Results[0].Title != "K8s OOM" || resp.Results[0].URL != "https://k8s.io/oom" {
		t.Errorf("first result mis-mapped: %+v", resp.Results[0])
	}
	if resp.Results[0].Snippet != "explanation" {
		t.Errorf("content→snippet mapping broken: %q", resp.Results[0].Snippet)
	}
	if resp.Results[0].PublishedDate != "2024-09-01" {
		t.Errorf("publishedDate not propagated: %q", resp.Results[0].PublishedDate)
	}
}

// TestWebSearch_Searxng_Unreachable_ReturnsSkippedReason verifies the
// "docker compose hasn't started searxng yet" case is a soft failure
// (skipped_reason envelope, not Go error). The LLM should be able to
// tell the operator how to recover.
func TestWebSearch_Searxng_Unreachable_ReturnsSkippedReason(t *testing.T) {
	defer resetWebSearch()
	// Deliberate dead address — TCP dial should fail fast.
	SetWebSearchConfigResolver(stubConfigResolver{
		provider:   providerSearxng,
		searxngURL: "http://127.0.0.1:1", // port 1 — connection refused
	})
	SetWebSearchHTTPClient(&http.Client{})

	out, err := WebSearch.Execute(context.Background(),
		json.RawMessage(`{"query":"hi"}`))
	if err != nil {
		t.Fatalf("execute should not error on unreachable searxng: %v", err)
	}
	var resp webSearchResponse
	_ = json.Unmarshal(out, &resp)
	if resp.SkippedReason == "" {
		t.Fatalf("expected skipped_reason for unreachable searxng, got %s", string(out))
	}
	if !strings.Contains(resp.SkippedReason, "SearXNG") {
		t.Errorf("skipped_reason should mention SearXNG: %q", resp.SkippedReason)
	}
}

// TestWebSearch_Searxng_MaxResultsClamped_ServerSide verifies that
// max_results truncation happens after parse (SearXNG itself doesn't
// honour count; we cap on our side).
func TestWebSearch_Searxng_MaxResultsClamped_ServerSide(t *testing.T) {
	defer resetWebSearch()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[
			{"title":"a","url":"https://a","content":""},
			{"title":"b","url":"https://b","content":""},
			{"title":"c","url":"https://c","content":""},
			{"title":"d","url":"https://d","content":""},
			{"title":"e","url":"https://e","content":""}
		]}`))
	}))
	defer srv.Close()

	SetWebSearchConfigResolver(stubConfigResolver{
		provider:   providerSearxng,
		searxngURL: srv.URL,
	})
	SetWebSearchHTTPClient(srv.Client())

	out, err := WebSearch.Execute(context.Background(),
		json.RawMessage(`{"query":"x","max_results":2}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var resp webSearchResponse
	_ = json.Unmarshal(out, &resp)
	if len(resp.Results) != 2 {
		t.Errorf("results=%d, want 2 after clamp", len(resp.Results))
	}
}

// TestWebSearch_ProviderParamOverride checks that the params.provider
// field overrides the resolver's choice. Uses a SearXNG-default
// resolver but pins provider=tavily in the call args.
func TestWebSearch_ProviderParamOverride(t *testing.T) {
	defer resetWebSearch()

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"answer":"","results":[]}`))
	}))
	defer srv.Close()

	SetWebSearchTavilyEndpoint(srv.URL)
	SetWebSearchConfigResolver(stubConfigResolver{
		// resolver default = searxng, but param should override
		provider:  providerSearxng,
		tavilyKey: "tk",
	})
	SetWebSearchHTTPClient(srv.Client())

	out, err := WebSearch.Execute(context.Background(),
		json.RawMessage(`{"query":"x","provider":"tavily"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if hits != 1 {
		t.Errorf("expected 1 tavily hit (override), got %d", hits)
	}
	var resp webSearchResponse
	_ = json.Unmarshal(out, &resp)
	if resp.Provider != providerTavily {
		t.Errorf("provider=%q, want tavily (override)", resp.Provider)
	}
}

// TestWebSearch_Brave_HappyPath verifies Brave dispatch:
// X-Subscription-Token header, GET /web/search?q=...&count=N, and the
// web.results[] → WebSearchResult mapping.
func TestWebSearch_Brave_HappyPath(t *testing.T) {
	defer resetWebSearch()

	var capturedAuth, capturedQuery, capturedCount string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method=%s", r.Method)
		}
		capturedAuth = r.Header.Get("X-Subscription-Token")
		capturedQuery = r.URL.Query().Get("q")
		capturedCount = r.URL.Query().Get("count")
		_, _ = w.Write([]byte(`{
			"web": {
				"results": [
					{"title":"Brave R1","url":"https://b1.example","description":"d1","age":"3 days ago"},
					{"title":"Brave R2","url":"https://b2.example","description":"d2","age":""}
				]
			}
		}`))
	}))
	defer srv.Close()

	SetWebSearchBraveEndpoint(srv.URL)
	SetWebSearchConfigResolver(stubConfigResolver{provider: providerBrave, braveKey: "bk-1"})
	SetWebSearchHTTPClient(srv.Client())

	out, err := WebSearch.Execute(context.Background(),
		json.RawMessage(`{"query":"foo","max_results":5}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if capturedAuth != "bk-1" {
		t.Errorf("X-Subscription-Token=%q, want bk-1", capturedAuth)
	}
	if capturedQuery != "foo" {
		t.Errorf("q=%q", capturedQuery)
	}
	if capturedCount != "5" {
		t.Errorf("count=%q", capturedCount)
	}

	var resp webSearchResponse
	_ = json.Unmarshal(out, &resp)
	if resp.Provider != providerBrave {
		t.Errorf("provider=%q, want brave", resp.Provider)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("results=%d, want 2", len(resp.Results))
	}
	if resp.Results[0].Title != "Brave R1" || resp.Results[0].URL != "https://b1.example" {
		t.Errorf("first result mis-mapped: %+v", resp.Results[0])
	}
	if resp.Results[0].Snippet != "d1" {
		t.Errorf("description→snippet broken: %q", resp.Results[0].Snippet)
	}
	if resp.Results[0].PublishedDate != "3 days ago" {
		t.Errorf("age→published_date broken: %q", resp.Results[0].PublishedDate)
	}
}

// TestWebSearch_Brave_NoKey_ReturnsSkippedReason — provider explicit,
// no key → skipped_reason envelope mentioning Brave.
func TestWebSearch_Brave_NoKey_ReturnsSkippedReason(t *testing.T) {
	defer resetWebSearch()
	SetWebSearchConfigResolver(stubConfigResolver{provider: providerBrave, braveKey: ""})

	out, err := WebSearch.Execute(context.Background(), json.RawMessage(`{"query":"x"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var resp webSearchResponse
	_ = json.Unmarshal(out, &resp)
	if !strings.Contains(resp.SkippedReason, "Brave") {
		t.Fatalf("skipped_reason should mention Brave, got %q", resp.SkippedReason)
	}
	if resp.Provider != providerBrave {
		t.Errorf("provider=%q, want brave", resp.Provider)
	}
}
