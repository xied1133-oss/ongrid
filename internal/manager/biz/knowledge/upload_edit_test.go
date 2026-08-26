package knowledge

import (
	"context"
	"errors"
	"testing"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/knowledge"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/qdrantx"
)

// fakeVec is a record-only QdrantClient. GetPoints returns whatever head
// point it was seeded with; every write is captured for assertions.
type fakeVec struct {
	head      qdrantx.SearchHit
	upserts   [][]qdrantx.Point
	deletedBy []map[string]any
	deletedID []uint64
}

func (f *fakeVec) EnsureCollection(context.Context, string, int) error          { return nil }
func (f *fakeVec) EnsurePayloadIndex(context.Context, string, string, string) error { return nil }
func (f *fakeVec) Upsert(_ context.Context, _ string, pts []qdrantx.Point) error {
	f.upserts = append(f.upserts, pts)
	return nil
}
func (f *fakeVec) DeleteByFilter(_ context.Context, _ string, m map[string]any) error {
	f.deletedBy = append(f.deletedBy, m)
	return nil
}
func (f *fakeVec) DeleteByID(_ context.Context, _ string, id uint64) error {
	f.deletedID = append(f.deletedID, id)
	return nil
}
func (f *fakeVec) GetPoints(_ context.Context, _ string, ids []uint64) ([]qdrantx.SearchHit, error) {
	if len(ids) > 0 && ids[0] == f.head.ID {
		return []qdrantx.SearchHit{f.head}, nil
	}
	return nil, nil
}
func (f *fakeVec) Search(context.Context, string, []float32, qdrantx.SearchOpts) ([]qdrantx.SearchHit, error) {
	return nil, nil
}
func (f *fakeVec) Scroll(context.Context, string, qdrantx.ScrollOpts) (*qdrantx.ScrollResult, error) {
	return &qdrantx.ScrollResult{}, nil
}

type fakeEmbed struct{}

func (fakeEmbed) Dim() int { return 4 }
func (fakeEmbed) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{0, 0, 0, 0}
	}
	return out, nil
}

func uploadHead(url string) qdrantx.SearchHit {
	id := uploadChunkDocID(url, 0)
	return qdrantx.SearchHit{
		ID: id,
		Payload: map[string]any{
			"source_type": model.SourceUpload,
			"title":       "技术行业为什么总在发明新词",
			"content":     "old body",
			"url":         url,
			"chunk_index": float64(0),
			"id_alias":    float64(id),
			"created_at":  time.Now().UTC().Format(time.RFC3339),
		},
	}
}

// TestUpdateManualDoc_UploadDoc — ADR-028 组织CRUD regression. Editing an
// uploaded file used to 400 ("repo docs are read-only") because the update
// path only accepted source_type=manual. It must now re-ingest the upload:
// sweep the old chunks by (source_type=upload, url), then re-embed + upsert,
// keeping the stable head-chunk id.
func TestUpdateManualDoc_UploadDoc(t *testing.T) {
	url := "技术行业为什么总在发明新词.md"
	fv := &fakeVec{head: uploadHead(url)}
	u := &Usecase{vec: fv, embed: fakeEmbed{}}

	got, err := u.UpdateManualDoc(context.Background(), fv.head.ID, UpdateManualDocInput{
		Title:   "技术行业为什么总在发明新词",
		Content: "# new body\n\n更新后的内容",
		Path:    "foo",
		Tags:    []string{"dns", "resolv"},
	})
	if err != nil {
		t.Fatalf("edit uploaded doc: unexpected error: %v", err)
	}
	if got.ID != uploadChunkDocID(url, 0) {
		t.Fatalf("head id changed: got %d want %d", got.ID, uploadChunkDocID(url, 0))
	}
	if got.SourceType != model.SourceUpload {
		t.Fatalf("source flipped: %s", got.SourceType)
	}
	if len(fv.deletedBy) != 1 {
		t.Fatalf("want exactly one sweep, got %d", len(fv.deletedBy))
	}
	if fv.deletedBy[0]["url"] != url || fv.deletedBy[0]["source_type"] != model.SourceUpload {
		t.Fatalf("sweep not scoped to this upload: %+v", fv.deletedBy[0])
	}
	if len(fv.upserts) == 0 {
		t.Fatalf("re-embed never upserted any chunks")
	}
}

// TestDeleteDoc_UploadDoc — the matching delete-path regression. Deleting an
// uploaded file must drop every chunk by (source_type=upload, url), not 400
// and not single-point DeleteByID (which would orphan chunks 1..N).
func TestDeleteDoc_UploadDoc(t *testing.T) {
	url := "技术行业为什么总在发明新词.md"
	fv := &fakeVec{head: uploadHead(url)}
	u := &Usecase{vec: fv, embed: fakeEmbed{}}

	if err := u.DeleteDoc(context.Background(), fv.head.ID); err != nil {
		t.Fatalf("delete uploaded doc: unexpected error: %v", err)
	}
	if len(fv.deletedBy) != 1 || fv.deletedBy[0]["url"] != url {
		t.Fatalf("upload delete must sweep by url, got %+v", fv.deletedBy)
	}
	if len(fv.deletedID) != 0 {
		t.Fatalf("upload delete must not use single-point DeleteByID")
	}
}

// TestUpdateManualDoc_VaultRejects — vault/repo docs stay read-only; the
// reject must name the actual source type, not the old "repo docs" wording.
func TestUpdateManualDoc_VaultRejects(t *testing.T) {
	head := uploadHead("seed/playbook.md")
	head.Payload["source_type"] = model.SourceVault
	fv := &fakeVec{head: head}
	u := &Usecase{vec: fv, embed: fakeEmbed{}}

	_, err := u.UpdateManualDoc(context.Background(), fv.head.ID, UpdateManualDocInput{
		Title:   "x",
		Content: "y",
	})
	if !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("vault edit should reject with ErrInvalid, got %v", err)
	}
}
