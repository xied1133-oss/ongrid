// Package embedding wraps the embedding-model HTTP API. The provider
// abstraction lets the knowledge service swap between any vendor that
// speaks the OpenAI /v1/embeddings shape (OpenAI proper, Azure OpenAI,
// GLM/智谱, Qwen/通义, DeepSeek — all of these expose a compatible
// endpoint) without touching call sites.
//
// Phase-1 ships only the OpenAI-compatible HTTP client. Phase-2 can
// add a fastembed-go (BGE / E5 ONNX) implementation under the same
// interface for fully-offline deployments — the seam is the Embedder
// interface.
//
// Configuration is one of the following triples (env-driven, see
// cmd/main.go for wiring):
//
//	ONGRID_EMBEDDING_PROVIDER  "openai" | "" (default openai)
//	ONGRID_EMBEDDING_MODEL     model id, e.g. "text-embedding-3-small"
//	ONGRID_EMBEDDING_BASE_URL  HTTP base; empty = api.openai.com
//	ONGRID_EMBEDDING_API_KEY   bearer token
//	ONGRID_EMBEDDING_DIM       expected dimensions (must match qdrant
//	                            collection)
package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ongridio/ongrid/internal/pkg/zhipuauth"
)

// Embedder is the narrow interface the knowledge service consumes.
// Embed returns one float32 vector per input text, in the same order.
type Embedder interface {
	// Dim is the vector dimensionality the provider returns. The
	// knowledge service uses this to size the qdrant collection.
	Dim() int
	// Embed embeds each text. Implementations should batch when the
	// provider supports it (the OpenAI shape does, up to ~2048 inputs
	// per call) but the contract requires order preservation only.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Config bundles the env-driven inputs.
type Config struct {
	Provider string // "openai" (default) — only one for now
	Model    string
	BaseURL  string
	APIKey   string
	Dim      int
	Log      *slog.Logger
}

// New builds an Embedder from cfg. Unknown provider returns an error.
//
// Providers:
//
//	"openai" (default) — HTTP /v1/embeddings; works for any OpenAI-
//	  compatible vendor (OpenAI, Azure OpenAI, GLM/智谱, Qwen/通义,
//	  DeepSeek). Requires APIKey + (usually) BaseURL.
//	"local" / "fastembed" / "onnx" — in-process ONNX inference via
//	  fastembed-go. No network, no API key. Model file lives at
//	  cfg.BaseURL (interpreted as CacheDir) or ONGRID_EMBEDDING_CACHE_DIR
//	  (default /var/lib/ongrid/embeddings). cfg.Model picks the variant
//	  (default bge-small-zh-v1.5 — 中英混合 best @ 30MB, dim=512).
func New(cfg Config) (Embedder, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider == "" {
		provider = "openai"
	}
	switch provider {
	case "openai":
		return newOpenAI(cfg)
	case "local", "fastembed", "onnx":
		return newLocal(cfg)
	default:
		return nil, fmt.Errorf("embedding: unknown provider %q", provider)
	}
}

// ---- openai-compatible HTTP impl ----

const defaultOpenAIBase = "https://api.openai.com"
const defaultModel = "text-embedding-3-small"
const defaultDim = 1536

type openAIEmbedder struct {
	base   string
	model  string
	apiKey string
	dim    int
	hc     *http.Client
	log    *slog.Logger
}

func newOpenAI(cfg Config) (*openAIEmbedder, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = defaultOpenAIBase
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = defaultModel
	}
	dim := cfg.Dim
	if dim <= 0 {
		dim = defaultDim
	}
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil, errors.New("embedding: api_key required (set ONGRID_EMBEDDING_API_KEY)")
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	return &openAIEmbedder{
		base:   base,
		model:  model,
		apiKey: apiKey,
		dim:    dim,
		hc:     &http.Client{Timeout: 30 * time.Second},
		log:    log,
	}, nil
}

func (e *openAIEmbedder) Dim() int { return e.dim }

type embedReq struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResp struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Embed POSTs /v1/embeddings.
func (e *openAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(embedReq{Model: e.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("embedding: marshal req: %w", err)
	}
	// Smart join: if base already ends with /v<digit> (e.g. GLM's
	// .../api/paas/v4) just append /embeddings; otherwise append the
	// canonical OpenAI /v1/embeddings. Saves operators from having to
	// remember the right tail per provider.
	url := embedURL(e.base)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embedding: new req: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Zhipu (open.bigmodel.cn) rejects raw `<id>.<secret>` as Bearer on
	// the v4 endpoints with 401 "令牌已过期或验证不正确" — it requires a
	// JWT signed with the secret half of the key. We detect zhipu by
	// URL + key shape and sign on every request (TTL=1h is plenty). All
	// other OpenAI-compatible endpoints continue to use raw Bearer.
	authToken := e.apiKey
	if zhipuauth.LooksLikeZhipuURL(e.base) && zhipuauth.LooksLikeZhipuKey(e.apiKey) {
		signed, sErr := zhipuauth.SignJWT(e.apiKey, time.Hour)
		if sErr != nil {
			return nil, fmt.Errorf("embedding: sign zhipu jwt: %w", sErr)
		}
		authToken = signed
	}
	req.Header.Set("Authorization", "Bearer "+authToken)
	resp, err := e.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding: http: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("embedding: http %d: %s", resp.StatusCode, truncate(string(raw), 256))
	}
	var er embedResp
	if err := json.Unmarshal(raw, &er); err != nil {
		return nil, fmt.Errorf("embedding: decode: %w (body=%s)", err, truncate(string(raw), 256))
	}
	if er.Error != nil && er.Error.Message != "" {
		return nil, fmt.Errorf("embedding: provider err: %s", er.Error.Message)
	}
	if len(er.Data) != len(texts) {
		return nil, fmt.Errorf("embedding: provider returned %d vectors, want %d", len(er.Data), len(texts))
	}
	out := make([][]float32, len(texts))
	for _, d := range er.Data {
		if d.Index < 0 || d.Index >= len(texts) {
			return nil, fmt.Errorf("embedding: out-of-range index %d", d.Index)
		}
		if e.dim > 0 && len(d.Embedding) != e.dim {
			return nil, fmt.Errorf("embedding: provider returned %d-dim vector, want %d (model %q)",
				len(d.Embedding), e.dim, e.model)
		}
		out[d.Index] = d.Embedding
	}
	return out, nil
}

// embedURL appends the right embeddings path. Examples:
//
//	https://api.openai.com               → https://api.openai.com/v1/embeddings
//	https://api.openai.com/v1            → https://api.openai.com/v1/embeddings
//	https://open.bigmodel.cn/api/paas/v4 → https://open.bigmodel.cn/api/paas/v4/embeddings
func embedURL(base string) string {
	base = strings.TrimRight(base, "/")
	if hasVersionSuffix(base) {
		return base + "/embeddings"
	}
	return base + "/v1/embeddings"
}

// hasVersionSuffix returns true when base ends with /v<digit+>.
func hasVersionSuffix(base string) bool {
	idx := strings.LastIndex(base, "/v")
	if idx < 0 || idx >= len(base)-2 {
		return false
	}
	tail := base[idx+2:]
	if tail == "" {
		return false
	}
	for _, r := range tail {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
