package knowledge

import (
	"testing"

	"github.com/ongridio/ongrid/internal/pkg/qdrantx"
)

// TestDedupeByIDAlias_PrechunkingManualDoc — the regression that drove the
// v0.7.78 fix. A manual doc inserted before upsertDoc started writing
// chunk_index ends up with no chunk_index field in payload. The old
// ListDocs MustMatch chunk_index=0 hid it; the new dedupe must treat
// missing as "head chunk" and keep it.
func TestDedupeByIDAlias_PrechunkingManualDoc(t *testing.T) {
	in := []qdrantx.SearchHit{{
		ID: 100,
		Payload: map[string]any{
			"source_type": "manual",
			"title":       "XFS vs ext4",
			"content":     "...",
			"id_alias":    float64(100),
			// chunk_index intentionally absent
		},
	}}
	out := dedupeByIDAlias(in, 0)
	if len(out) != 1 {
		t.Fatalf("pre-chunking doc dropped: got %d, want 1", len(out))
	}
	if out[0].Title != "XFS vs ext4" {
		t.Fatalf("wrong doc: %+v", out[0])
	}
}

// TestDedupeByIDAlias_ChunkedRepoDoc — a chunked repo doc returns one
// row per logical doc (the head chunk), not one per chunk.
func TestDedupeByIDAlias_ChunkedRepoDoc(t *testing.T) {
	chunks := []qdrantx.SearchHit{
		{
			ID: 200,
			Payload: map[string]any{
				"source_type": "repo", "title": "README",
				"id_alias": float64(200), "chunk_index": float64(0),
			},
		},
		{
			ID: 201,
			Payload: map[string]any{
				"source_type": "repo", "title": "README",
				"id_alias": float64(200), "chunk_index": float64(1),
			},
		},
		{
			ID: 202,
			Payload: map[string]any{
				"source_type": "repo", "title": "README",
				"id_alias": float64(200), "chunk_index": float64(2),
			},
		},
	}
	out := dedupeByIDAlias(chunks, 0)
	if len(out) != 1 {
		t.Fatalf("chunks not collapsed: got %d, want 1", len(out))
	}
	if out[0].ID != 200 {
		t.Fatalf("dedupe didn't pick head chunk: got id=%d, want 200", out[0].ID)
	}
}

// TestDedupeByIDAlias_HeadUpgrade — when qdrant returns a non-head chunk
// before the head (scroll ordering isn't guaranteed), dedupe must
// upgrade the slot to the head chunk's payload, not just keep the first.
func TestDedupeByIDAlias_HeadUpgrade(t *testing.T) {
	mixed := []qdrantx.SearchHit{
		// chunk 2 arrives first
		{
			ID: 302,
			Payload: map[string]any{
				"source_type": "repo", "title": "tail",
				"id_alias": float64(300), "chunk_index": float64(2),
			},
		},
		// then chunk 0 — should overwrite the slot
		{
			ID: 300,
			Payload: map[string]any{
				"source_type": "repo", "title": "head",
				"id_alias": float64(300), "chunk_index": float64(0),
			},
		},
	}
	out := dedupeByIDAlias(mixed, 0)
	if len(out) != 1 {
		t.Fatalf("got %d, want 1", len(out))
	}
	if out[0].Title != "head" {
		t.Fatalf("head not upgraded: title=%q", out[0].Title)
	}
}

// TestDedupeByIDAlias_MixedManualAndRepo — list pages mix sources.
// Each logical doc surfaces exactly once.
func TestDedupeByIDAlias_MixedManualAndRepo(t *testing.T) {
	in := []qdrantx.SearchHit{
		{ID: 400, Payload: map[string]any{"source_type": "manual", "title": "playbook A", "id_alias": float64(400)}},
		{ID: 500, Payload: map[string]any{"source_type": "repo", "title": "README", "id_alias": float64(500), "chunk_index": float64(0)}},
		{ID: 501, Payload: map[string]any{"source_type": "repo", "title": "README", "id_alias": float64(500), "chunk_index": float64(1)}},
		{ID: 600, Payload: map[string]any{"source_type": "manual", "title": "playbook B", "id_alias": float64(600), "chunk_index": float64(0)}},
	}
	out := dedupeByIDAlias(in, 0)
	if len(out) != 3 {
		t.Fatalf("got %d, want 3 (2 manual + 1 repo collapsed)", len(out))
	}
}

// TestDedupeByIDAlias_LimitCap — limit truncates the result but the
// dedupe still happens for the truncated slice.
func TestDedupeByIDAlias_LimitCap(t *testing.T) {
	in := []qdrantx.SearchHit{
		{ID: 1, Payload: map[string]any{"id_alias": float64(1), "title": "a"}},
		{ID: 2, Payload: map[string]any{"id_alias": float64(2), "title": "b"}},
		{ID: 3, Payload: map[string]any{"id_alias": float64(3), "title": "c"}},
	}
	out := dedupeByIDAlias(in, 2)
	if len(out) != 2 {
		t.Fatalf("limit=2: got %d, want 2", len(out))
	}
}

// TestChunkIndexFromPayload covers the JSON number unmarshal flavors we
// encounter on the wire (Go's json default is float64; some codepaths
// re-encode via json.Number).
func TestChunkIndexFromPayload(t *testing.T) {
	cases := []struct {
		name     string
		payload  map[string]any
		want     int
		wantPres bool
	}{
		{"absent", map[string]any{}, 0, false},
		{"nil", map[string]any{"chunk_index": nil}, 0, false},
		{"float64-zero", map[string]any{"chunk_index": float64(0)}, 0, true},
		{"float64-three", map[string]any{"chunk_index": float64(3)}, 3, true},
		{"int", map[string]any{"chunk_index": 7}, 7, true},
		{"int64", map[string]any{"chunk_index": int64(9)}, 9, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, pres := chunkIndexFromPayload(tc.payload)
			if got != tc.want || pres != tc.wantPres {
				t.Fatalf("got (%d, %v), want (%d, %v)", got, pres, tc.want, tc.wantPres)
			}
		})
	}
}
