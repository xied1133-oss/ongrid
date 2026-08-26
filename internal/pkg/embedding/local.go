// local.go — offline embedder via fastembed-go (ONNX Runtime).
//
// Triggered when ONGRID_EMBEDDING_PROVIDER=local (or "fastembed" /
// "onnx"). Loads a BAAI BGE model from CacheDir + runs inference
// in-process via the bundled onnxruntime shared library. No external
// HTTP call, no API key, no internet required at runtime (operator
// downloads the model once during install via fetch-embedding-model.sh,
// or fastembed-go downloads on first use if CacheDir is writable +
// upstream is reachable).
//
// Why this exists: ADR-027 — air-gapped / regulated installs can't
// hit OpenAI/GLM/Qwen and previously had RAG entirely disabled.
// Phase-2 of the embedder seam (see the package doc) — the OpenAI
// HTTP implementation stays the primary path for users who already
// have a key; this is the fallback for the "no LLM key, no internet"
// scenario.

package embedding

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	fastembed "github.com/anush008/fastembed-go"
)

// localEmbedder wraps a *fastembed.FlagEmbedding behind our Embedder
// interface. ONNX inference is CPU-bound and not thread-safe in
// fastembed's wrapper; we serialize Embed calls with a mutex so
// concurrent /knowledge/search + Sync upserts don't crash the
// underlying tokenizer. Throughput on a single CPU core is the
// bottleneck regardless — the mutex doesn't make it slower.
type localEmbedder struct {
	model *fastembed.FlagEmbedding
	dim   int
	name  string
	mu    sync.Mutex
	log   *slog.Logger
}

// modelDims maps the fastembed-go model constants to their output
// embedding dimension. Kept here rather than discovering at runtime so
// EnsureCollection (called BEFORE the first Embed) can size the qdrant
// collection correctly. Values mirror the BAAI cards on HuggingFace.
var modelDims = map[fastembed.EmbeddingModel]int{
	fastembed.AllMiniLML6V2: 384,
	fastembed.BGEBaseEN:     768,
	fastembed.BGEBaseENV15:  768,
	fastembed.BGESmallEN:    384,
	fastembed.BGESmallENV15: 384,
	fastembed.BGESmallZH:    512,
}

// resolveModel maps the operator's friendly string to a fastembed
// constant. Defaults to BGE-small-zh-v1.5 — best 中英混合 quality at
// the smallest size (~30MB quantized ONNX) and dim=512 fits comfortably
// in qdrant's HNSW defaults.
func resolveModel(name string) (fastembed.EmbeddingModel, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "", "bge-small-zh-v1.5", "bge-small-zh", "zh", "default":
		return fastembed.BGESmallZH, nil
	case "bge-small-en", "bge-small-en-v1.5", "en":
		return fastembed.BGESmallENV15, nil
	case "bge-base-en", "bge-base-en-v1.5":
		return fastembed.BGEBaseENV15, nil
	case "all-minilm-l6-v2", "minilm":
		return fastembed.AllMiniLML6V2, nil
	}
	return "", fmt.Errorf("embedding: unknown local model %q (try bge-small-zh-v1.5 / bge-small-en-v1.5 / all-minilm-l6-v2)", name)
}

// newLocal builds the ONNX-backed embedder. cfg.Model is the friendly
// model string (resolveModel above). cfg.BaseURL / cfg.APIKey are
// ignored — local inference doesn't talk to any endpoint. Cache
// location comes from ONGRID_EMBEDDING_CACHE_DIR (default
// /var/lib/ongrid/embeddings).
func newLocal(cfg Config) (*localEmbedder, error) {
	model, err := resolveModel(cfg.Model)
	if err != nil {
		return nil, err
	}
	cacheDir := strings.TrimSpace(os.Getenv("ONGRID_EMBEDDING_CACHE_DIR"))
	if cacheDir == "" {
		cacheDir = "/var/lib/ongrid/embeddings"
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("embedding: mkdir cache: %w", err)
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	dim, ok := modelDims[model]
	if !ok {
		return nil, fmt.Errorf("embedding: no known dim for model %s", model)
	}
	// If the operator set ONGRID_EMBEDDING_DIM explicitly, sanity check
	// it matches; mismatch is a config bug (qdrant collection sized for
	// one dim, model produces another) and should fail early.
	if cfg.Dim > 0 && cfg.Dim != dim {
		return nil, fmt.Errorf("embedding: ONGRID_EMBEDDING_DIM=%d doesn't match model %s dim %d",
			cfg.Dim, model, dim)
	}
	showProgress := false
	// MaxLength intentionally left zero — sugarme/tokenizer @ v0.2.3
	// has a nil-pair NPE in TruncateEncodings (PostProcess passes a
	// nil pair when called for a single text, then TruncateEncodings
	// dereferences pair.GetIds() and SIGSEGVs). Setting MaxLength=0
	// disables the lib's truncation altogether; we cap input size
	// ourselves in clampForLocalEmbed() before calling Embed.
	emb, err := fastembed.NewFlagEmbedding(&fastembed.InitOptions{
		Model:                model,
		CacheDir:             cacheDir,
		ShowDownloadProgress: &showProgress,
	})
	if err != nil {
		// Two common modes: missing ONNX Runtime shared library, or no
		// network to download the model on first run. Surface both with
		// actionable hints so the install-deps.sh helper can flag the
		// right fix.
		hint := ""
		switch {
		case strings.Contains(err.Error(), "onnxruntime"), strings.Contains(err.Error(), "ONNX_PATH"):
			hint = " — install libonnxruntime; on debian: apt install -y libonnxruntime-dev"
		case strings.Contains(err.Error(), "download"), strings.Contains(err.Error(), "no such host"):
			hint = " — pre-download the model into " + cacheDir + " (see fetch-embedding-model.sh)"
		}
		return nil, fmt.Errorf("embedding: init local model %s: %w%s", model, err, hint)
	}
	log.Info("embedding: local model loaded",
		slog.String("model", string(model)),
		slog.Int("dim", dim),
		slog.String("cache_dir", cacheDir))
	return &localEmbedder{model: emb, dim: dim, name: string(model), log: log}, nil
}

// Dim implements Embedder.
func (e *localEmbedder) Dim() int { return e.dim }

// Embed implements Embedder. Serialized via mu — see the type doc.
//
// Defensive pre-processing:
//
//  1. Drop empty/whitespace-only inputs (and substitute a single space
//     so the output vector slice still aligns with the caller's input
//     slice by index — knowledge.upsertDoc indexes the result that way).
//  2. Hard-cap each input to maxLocalEmbedChars runes before tokenizing.
//     The fastembed-go MaxLength=512 truncates *tokens*, but the
//     upstream sugarme/tokenizer SIGSEGVs on certain very long CJK
//     inputs in TruncateEncodings (panic seen during Sync of a 7KB
//     md file). Capping at ~2KB chars dodges the bug entirely.
//  3. Small batch size (8) — gives the tokenizer fewer goroutines to
//     juggle and shrinks the blast radius when a single text trips a
//     bug; we still get most of the throughput win vs. one-at-a-time.
func (e *localEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	// Surface ctx cancellation as a fast-fail without enqueueing into
	// the ONNX call; fastembed-go's Embed has no ctx hook so we can't
	// abort mid-batch.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	safe := make([]string, len(texts))
	for i, t := range texts {
		safe[i] = clampForLocalEmbed(t)
	}
	out, err := e.model.Embed(safe, 8)
	if err != nil {
		return nil, fmt.Errorf("embedding: local inference: %w", err)
	}
	if len(out) != len(safe) {
		return nil, errors.New("embedding: local returned wrong vector count")
	}
	for i, v := range out {
		if len(v) != e.dim {
			return nil, fmt.Errorf("embedding: local vector %d has %d dims, want %d", i, len(v), e.dim)
		}
	}
	return out, nil
}

// maxLocalEmbedChars caps input length before tokenizing.
//
// Lib bug forcing this cap: sugarme/tokenizer @ v0.2.3 (the version
// fastembed-go v1.0.0 pins to) has an unchecked pairEncoding.GetIds()
// dereference in TruncateEncodings' LongestFirst switch case (util.go
// line 108). When pairEncoding is nil (which is normal for single-text
// embedding) AND totalLength >= MaxLength (which fastembed pins at
// 512 tokens), the truncation path runs the switch and SIGSEGVs.
//
// fastembed forces MaxLength=512 even when caller passes 0 (its own
// "default" override), so the only way to avoid the dereference is
// to keep totalLength < 512 tokens. For BGE-small-zh tokenizer:
//   - 1 Chinese char ≈ 1 token
//   - 1 English word ≈ 1-2 tokens (5 chars/word avg)
// 350 chars covers both worlds safely: 350 zh chars → ~350 tokens;
// 350 en chars → ~70 tokens; both well under 512.
//
// Trade-off: long documents only get their first 350 chars embedded,
// but the qdrant payload still carries full content for the LLM to
// read after retrieval. Operators with long-form docs should use the
// 'openai' embedder (any compatible API key + dim=1536 or 2048)
// where the upstream tokenizer doesn't have this bug.
const maxLocalEmbedChars = 350

func clampForLocalEmbed(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		// Empty input crashes the tokenizer in some paths; pass a
		// single space so the output slot still gets a (low-quality
		// but valid) vector and the caller's index alignment holds.
		return " "
	}
	r := []rune(s)
	if len(r) > maxLocalEmbedChars {
		return string(r[:maxLocalEmbedChars])
	}
	return s
}
