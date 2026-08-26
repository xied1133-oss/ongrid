package knowledge

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	model "github.com/ongridio/ongrid/internal/manager/model/knowledge"
)

// TestE2E_ManualDocLifecycle drives the full create→list→get→search→edit→
// delete loop for a manual (pasted) doc through the real biz layer.
func TestE2E_ManualDocLifecycle(t *testing.T) {
	router, store := newE2E(t)

	// create
	rec := jsonReq(t, router, http.MethodPost, "/v1/knowledge/docs", map[string]any{
		"title":   "nginx 重启 SOP",
		"content": "# 重启\n\nsystemctl restart nginx",
		"path":    "网络/HTTP",
		"tags":    []string{"nginx", "sop"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	created := decodeDoc(t, rec)
	if created.SourceType != model.SourceManual {
		t.Fatalf("create: source=%s want manual", created.SourceType)
	}
	if created.ID == 0 {
		t.Fatal("create: zero id")
	}

	// list — exactly one doc, ours
	rec = req(t, router, http.MethodGet, "/v1/knowledge/docs", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", rec.Code)
	}
	docs := decodeDocs(t, rec)
	if len(docs) != 1 || docs[0].ID != created.ID {
		t.Fatalf("list: want [%d], got %+v", created.ID, docs)
	}

	// get — content present
	rec = req(t, router, http.MethodGet, "/v1/knowledge/docs/"+idStr(created.ID), "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: want 200, got %d", rec.Code)
	}
	if got := decodeDoc(t, rec); !strings.Contains(got.Content, "systemctl restart nginx") {
		t.Fatalf("get: content missing, got %q", got.Content)
	}

	// search — surfaces the doc
	rec = req(t, router, http.MethodGet, "/v1/knowledge/search?q=nginx", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "nginx 重启 SOP") {
		t.Fatalf("search: doc not in results: %s", rec.Body.String())
	}

	// edit — new title + content
	rec = jsonReq(t, router, http.MethodPatch, "/v1/knowledge/docs/"+idStr(created.ID), map[string]any{
		"title":   "nginx 重启 SOP v2",
		"content": "# v2\n\nsystemctl reload nginx",
		"path":    "网络/HTTP",
		"tags":    []string{"nginx"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("edit: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	rec = req(t, router, http.MethodGet, "/v1/knowledge/docs/"+idStr(created.ID), "", nil)
	if got := decodeDoc(t, rec); got.Title != "nginx 重启 SOP v2" || !strings.Contains(got.Content, "reload") {
		t.Fatalf("edit not persisted: %+v", got)
	}

	// delete — 204, then gone
	rec = req(t, router, http.MethodDelete, "/v1/knowledge/docs/"+idStr(created.ID), "", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d (%s)", rec.Code, rec.Body.String())
	}
	if store.count() != 0 {
		t.Fatalf("delete: store still has %d points", store.count())
	}
	rec = req(t, router, http.MethodGet, "/v1/knowledge/docs", "", nil)
	if docs := decodeDocs(t, rec); len(docs) != 0 {
		t.Fatalf("after delete, list should be empty, got %+v", docs)
	}
}

// buildUpload constructs a multipart body for POST /v1/knowledge/upload.
func buildUpload(t *testing.T, filename, content, path, tags string) (string, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if path != "" {
		_ = w.WriteField("path", path)
	}
	if tags != "" {
		_ = w.WriteField("tags", tags)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return w.FormDataContentType(), &buf
}

// TestE2E_UploadDocLifecycle is the regression for the two reported bugs:
// editing AND deleting an uploaded (source_type=upload, multi-chunk) doc
// used to 400. It uploads a long file (forces ≥2 chunks), confirms the
// listing collapses it to one row, edits it to a shorter body (must sweep
// the stale chunks), then deletes it (must drop every chunk).
func TestE2E_UploadDocLifecycle(t *testing.T) {
	router, store := newE2E(t)

	longBody := "# 技术行业为什么总在发明新词\n\n" + strings.Repeat("观测性运维平台工程。", 600) // > chunkChars runes
	ct, body := buildUpload(t, "技术行业为什么总在发明新词.md", longBody, "foo", "dns, resolv")
	rec := req(t, router, http.MethodPost, "/v1/knowledge/upload", ct, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	up := decodeDoc(t, rec)
	if up.SourceType != model.SourceUpload {
		t.Fatalf("upload: source=%s want upload", up.SourceType)
	}
	if store.count() < 2 {
		t.Fatalf("upload: long body should chunk into ≥2 points, got %d", store.count())
	}

	// listing collapses chunks → one row
	rec = req(t, router, http.MethodGet, "/v1/knowledge/docs", "", nil)
	docs := decodeDocs(t, rec)
	if len(docs) != 1 || docs[0].SourceType != model.SourceUpload {
		t.Fatalf("list: want one upload row, got %+v", docs)
	}

	// get returns the full reassembled content
	rec = req(t, router, http.MethodGet, "/v1/knowledge/docs/"+idStr(up.ID), "", nil)
	if got := decodeDoc(t, rec); !strings.Contains(got.Content, "观测性运维平台工程") {
		t.Fatalf("get upload: content missing")
	}

	// edit to a SHORT body — was HTTP 400 before the fix
	rec = jsonReq(t, router, http.MethodPatch, "/v1/knowledge/docs/"+idStr(up.ID), map[string]any{
		"title":   "技术行业为什么总在发明新词",
		"content": "# 短内容\n\n精简后的正文",
		"path":    "foo",
		"tags":    []string{"dns", "resolv"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("edit upload: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if store.count() != 1 {
		t.Fatalf("edit upload: stale chunks not swept, store has %d points", store.count())
	}
	rec = req(t, router, http.MethodGet, "/v1/knowledge/docs/"+idStr(up.ID), "", nil)
	if got := decodeDoc(t, rec); got.Title != "技术行业为什么总在发明新词" || !strings.Contains(got.Content, "精简后的正文") {
		t.Fatalf("edit upload not persisted: %+v", got)
	}

	// delete — was HTTP 400 before the fix
	rec = req(t, router, http.MethodDelete, "/v1/knowledge/docs/"+idStr(up.ID), "", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete upload: want 204, got %d (%s)", rec.Code, rec.Body.String())
	}
	if store.count() != 0 {
		t.Fatalf("delete upload: store still has %d points", store.count())
	}
}
