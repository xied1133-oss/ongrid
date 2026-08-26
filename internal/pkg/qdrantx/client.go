// Package qdrantx is a thin HTTP wrapper around qdrant's REST API.
// We don't pull in the full upstream go-client because it's gRPC-first
// and we only need 4 ops: ensure-collection, upsert, delete-by-filter,
// and search. The HTTP API is documented at
// https://qdrant.github.io/qdrant/redoc/index.html.
//
// Conventions:
//   - One collection per ongrid deployment, default name "knowledge".
//   - Vectors are float32; cosine distance.
//   - Point IDs are uint64. The knowledge service mints them as
//     md5(payload.url) >> 64 lo bits — stable across sync runs so a
//     re-import overwrites the old point instead of duplicating.
//   - Payload always carries: source_type, title, content, url,
//     repo_id (when source_type=repo). Search returns those verbatim.
package qdrantx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Client is the qdrant HTTP wrapper.
type Client struct {
	base string
	hc   *http.Client
	log  *slog.Logger
}

// New returns a Client. baseURL e.g. "http://qdrant:6333".
func New(baseURL string, log *slog.Logger) *Client {
	if log == nil {
		log = slog.Default()
	}
	return &Client{
		base: strings.TrimRight(baseURL, "/"),
		hc:   &http.Client{Timeout: 30 * time.Second},
		log:  log,
	}
}

// EnsureCollection creates the collection if missing. Idempotent —
// 409 on existing is treated as success. dim is the vector size; the
// caller must keep this in sync with the embedding model.
func (c *Client) EnsureCollection(ctx context.Context, name string, dim int) error {
	// First check existence (cheap GET) AND dim. If the collection
	// exists with a different vector dim, the historical behavior was
	// to leave it alone and let writes fail with cryptic 400s ('Vector
	// dimension error: expected dim X, got Y') hours later. Now we
	// either drop+recreate (safe when no points yet — typical fresh
	// install where we ensured at boot-time default dim before the
	// operator's real embedder was configured) or refuse with a clear
	// error (when there ARE points; auto-destruction would lose data).
	getResp, err := c.do(ctx, http.MethodGet, "/collections/"+name, nil)
	if err == nil && getResp.StatusCode == 200 {
		var info struct {
			Result struct {
				Config struct {
					Params struct {
						Vectors struct {
							Size int `json:"size"`
						} `json:"vectors"`
					} `json:"params"`
				} `json:"config"`
				PointsCount int `json:"points_count"`
			} `json:"result"`
		}
		if decErr := json.NewDecoder(getResp.Body).Decode(&info); decErr == nil {
			_ = getResp.Body.Close()
			existing := info.Result.Config.Params.Vectors.Size
			if existing == dim {
				return nil
			}
			// Dim mismatch.
			if info.Result.PointsCount > 0 {
				return fmt.Errorf("qdrant: collection %s has dim %d but caller wants %d, "+
					"and %d points already exist — refusing to drop. "+
					"Drop manually with `curl -X DELETE qdrant:6333/collections/%s` "+
					"after backing up if needed", name, existing, dim, info.Result.PointsCount, name)
			}
			// Empty — safe to drop + recreate.
			delResp, _ := c.do(ctx, http.MethodDelete, "/collections/"+name, nil)
			if delResp != nil {
				_ = delResp.Body.Close()
			}
			// fall through to recreate at the desired dim
		} else {
			_ = getResp.Body.Close()
			return nil // exists but couldn't decode — assume OK
		}
	} else if getResp != nil {
		_ = getResp.Body.Close()
	}
	body := map[string]any{
		"vectors": map[string]any{
			"size":     dim,
			"distance": "Cosine",
		},
	}
	resp, err := c.do(ctx, http.MethodPut, "/collections/"+name, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 && resp.StatusCode != 409 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant: ensure collection: http %d: %s", resp.StatusCode, truncate(string(raw), 256))
	}
	return nil
}

// EnsurePayloadIndex creates a payload index on `field` if not already
// there. Schema selects the indexer:
//
//   - "" / "keyword"  — exact-match (default; for category, tags,
//     source_type, repo_id-as-string, etc.)
//   - "text"          — full-text + prefix match (use this for path
//     fields filtered with match.text "网络/")
//   - "integer" / "float" / "bool" / "geo" — numeric / typed
//
// Required before server-side filter on that field can run
// efficiently — without it qdrant scans the whole collection.
// Idempotent (HTTP 200 on existing).
func (c *Client) EnsurePayloadIndex(ctx context.Context, collection, field, schema string) error {
	if schema == "" {
		schema = "keyword"
	}
	body := map[string]any{
		"field_name":   field,
		"field_schema": schema,
	}
	resp, err := c.do(ctx, http.MethodPut,
		"/collections/"+collection+"/index?wait=true", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 {
		return nil
	}
	raw, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("qdrant: ensure payload index %s: http %d: %s",
		field, resp.StatusCode, truncate(string(raw), 256))
}

// Point is one record in qdrant.
type Point struct {
	ID      uint64                 `json:"id"`
	Vector  []float32              `json:"vector"`
	Payload map[string]any         `json:"payload,omitempty"`
}

// Upsert writes points. Replaces by id.
func (c *Client) Upsert(ctx context.Context, collection string, points []Point) error {
	if len(points) == 0 {
		return nil
	}
	body := map[string]any{"points": points}
	resp, err := c.do(ctx, http.MethodPut, "/collections/"+collection+"/points?wait=true", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant: upsert: http %d: %s", resp.StatusCode, truncate(string(raw), 256))
	}
	return nil
}

// DeleteByFilter removes every point whose payload matches the filter.
// Used for "drop every doc owned by repo X" before a re-sync.
func (c *Client) DeleteByFilter(ctx context.Context, collection string, mustMatch map[string]any) error {
	if len(mustMatch) == 0 {
		return fmt.Errorf("qdrant: DeleteByFilter requires at least one match clause (refusing to delete all)")
	}
	conds := make([]map[string]any, 0, len(mustMatch))
	for k, v := range mustMatch {
		conds = append(conds, map[string]any{
			"key":   k,
			"match": map[string]any{"value": v},
		})
	}
	body := map[string]any{
		"filter": map[string]any{"must": conds},
	}
	resp, err := c.do(ctx, http.MethodPost, "/collections/"+collection+"/points/delete?wait=true", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant: delete: http %d: %s", resp.StatusCode, truncate(string(raw), 256))
	}
	return nil
}

// GetPoints fetches one or more points by id. Unlike Search/Scroll,
// the point-id API takes uint64 IDs natively (the JSON number is
// scoped to point identity, not filter value), so it works for IDs
// > 2^63 — which Search/Scroll filters do not.
func (c *Client) GetPoints(ctx context.Context, collection string, ids []uint64) ([]SearchHit, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	body := map[string]any{
		"ids":          ids,
		"with_payload": true,
		"with_vector":  false,
	}
	resp, err := c.do(ctx, http.MethodPost, "/collections/"+collection+"/points", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("qdrant: get points: http %d: %s", resp.StatusCode, truncate(string(raw), 256))
	}
	var sr struct {
		Result []SearchHit `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("qdrant: decode get points: %w", err)
	}
	return sr.Result, nil
}

// DeleteByID drops one point.
func (c *Client) DeleteByID(ctx context.Context, collection string, id uint64) error {
	body := map[string]any{"points": []uint64{id}}
	resp, err := c.do(ctx, http.MethodPost, "/collections/"+collection+"/points/delete?wait=true", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant: delete by id: http %d: %s", resp.StatusCode, truncate(string(raw), 256))
	}
	return nil
}

// SearchHit is the result row.
type SearchHit struct {
	ID      uint64                 `json:"id"`
	Score   float64                `json:"score"`
	Payload map[string]any         `json:"payload"`
}

// SearchOpts narrows a vector search via server-side payload filters.
// MustMatch keys map to qdrant `must` clauses; values may be:
//
//   - string / number → exact match
//   - []string        → match.any (doc passes if its field equals any
//     listed value, OR — for array fields like tags — if the array
//     contains any listed value)
//
// Limit defaults to 10.
type SearchOpts struct {
	Limit     int
	MustMatch map[string]any
}

// Search runs a top-K cosine search with optional payload filtering.
func (c *Client) Search(ctx context.Context, collection string, vector []float32, opts SearchOpts) ([]SearchHit, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}
	body := map[string]any{
		"vector":       vector,
		"limit":        limit,
		"with_payload": true,
	}
	if f := buildFilter(opts.MustMatch); f != nil {
		body["filter"] = f
	}
	resp, err := c.do(ctx, http.MethodPost, "/collections/"+collection+"/points/search", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("qdrant: search: http %d: %s", resp.StatusCode, truncate(string(raw), 256))
	}
	var sr struct {
		Result []SearchHit `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("qdrant: decode search: %w", err)
	}
	return sr.Result, nil
}

// Scroll lists points by filter — used by the SPA's /knowledge/docs
// listing endpoint. Returns up to limit; offset advances via the
// returned next_page_offset.
type ScrollOpts struct {
	MustMatch map[string]any
	Limit     int
	Offset    *uint64
}

type ScrollResult struct {
	Points     []SearchHit `json:"points"`
	NextOffset *uint64     `json:"next_page_offset"`
}

// Scroll lists payloads matching MustMatch (or all when empty).
func (c *Client) Scroll(ctx context.Context, collection string, opts ScrollOpts) (*ScrollResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	body := map[string]any{
		"limit":        limit,
		"with_payload": true,
		"with_vector":  false,
	}
	if opts.Offset != nil {
		body["offset"] = *opts.Offset
	}
	if f := buildFilter(opts.MustMatch); f != nil {
		body["filter"] = f
	}
	resp, err := c.do(ctx, http.MethodPost, "/collections/"+collection+"/points/scroll", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("qdrant: scroll: http %d: %s", resp.StatusCode, truncate(string(raw), 256))
	}
	var sr struct {
		Result ScrollResult `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("qdrant: decode scroll: %w", err)
	}
	return &sr.Result, nil
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("qdrant: marshal: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.hc.Do(req)
}

// PrefixMatch is a sentinel value for buildFilter — wrap a string in
// it to request `match.text` (prefix / full-text match against a
// `text`-schema payload index) instead of the default exact-value
// match. Used for path-prefix filtering ("网络/" matches "网络/DNS").
type PrefixMatch struct{ Prefix string }

// buildFilter renders a qdrant filter from a flat must-match map.
// Each entry becomes one `must` clause:
//
//   - PrefixMatch            → {"match": {"text": ...}}   (prefix / fulltext)
//   - []string               → {"match": {"any": [...]}}  (any-of)
//   - string / number / bool → {"match": {"value": ...}}  (exact)
//
// Returns nil when the map is empty (caller should not set filter).
func buildFilter(must map[string]any) map[string]any {
	if len(must) == 0 {
		return nil
	}
	conds := make([]map[string]any, 0, len(must))
	for k, v := range must {
		switch tv := v.(type) {
		case PrefixMatch:
			if tv.Prefix == "" {
				continue
			}
			conds = append(conds, map[string]any{
				"key":   k,
				"match": map[string]any{"text": tv.Prefix},
			})
		case []string:
			if len(tv) == 0 {
				continue
			}
			anyList := make([]any, 0, len(tv))
			for _, s := range tv {
				anyList = append(anyList, s)
			}
			conds = append(conds, map[string]any{
				"key":   k,
				"match": map[string]any{"any": anyList},
			})
		default:
			conds = append(conds, map[string]any{
				"key":   k,
				"match": map[string]any{"value": v},
			})
		}
	}
	if len(conds) == 0 {
		return nil
	}
	return map[string]any{"must": conds}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
