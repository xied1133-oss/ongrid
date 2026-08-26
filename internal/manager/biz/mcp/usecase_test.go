package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	model "github.com/ongridio/ongrid/internal/manager/model/mcp"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/mcpclient"
)

// fakeRepo is an in-memory Repo for tests (no DB, no network).
type fakeRepo struct {
	rows   map[uint64]*model.Server
	nextID uint64
}

func newFakeRepo() *fakeRepo { return &fakeRepo{rows: map[uint64]*model.Server{}} }

func (f *fakeRepo) Create(_ context.Context, s *model.Server) error {
	for _, r := range f.rows {
		if r.Name == s.Name {
			return errs.ErrConflict
		}
	}
	f.nextID++
	s.ID = f.nextID
	cp := *s
	f.rows[s.ID] = &cp
	return nil
}

func (f *fakeRepo) Get(_ context.Context, id uint64) (*model.Server, error) {
	if s, ok := f.rows[id]; ok {
		cp := *s
		return &cp, nil
	}
	return nil, errs.ErrNotFound
}

func (f *fakeRepo) GetByName(_ context.Context, name string) (*model.Server, error) {
	for _, s := range f.rows {
		if s.Name == name {
			cp := *s
			return &cp, nil
		}
	}
	return nil, errs.ErrNotFound
}

func (f *fakeRepo) List(_ context.Context) ([]*model.Server, error) {
	out := make([]*model.Server, 0, len(f.rows))
	for _, s := range f.rows {
		cp := *s
		out = append(out, &cp)
	}
	return out, nil
}

func (f *fakeRepo) Update(_ context.Context, id uint64, patch *model.Server) error {
	s, ok := f.rows[id]
	if !ok {
		return errs.ErrNotFound
	}
	s.Transport = patch.Transport
	s.Endpoint = patch.Endpoint
	s.Credential = patch.Credential
	s.HeaderTemplateJSON = patch.HeaderTemplateJSON
	s.Trusted = patch.Trusted
	s.Enabled = patch.Enabled
	return nil
}

func (f *fakeRepo) Delete(_ context.Context, id uint64) error {
	if _, ok := f.rows[id]; !ok {
		return errs.ErrNotFound
	}
	delete(f.rows, id)
	return nil
}

func (f *fakeRepo) SetStatus(_ context.Context, id uint64, status, lastErr string) error {
	if s, ok := f.rows[id]; ok {
		s.Status = status
		s.LastError = lastErr
		return nil
	}
	return errs.ErrNotFound
}

func (f *fakeRepo) SetToolsCache(_ context.Context, id uint64, toolsJSON string) error {
	if s, ok := f.rows[id]; ok {
		s.ToolsCacheJSON = toolsJSON
		return nil
	}
	return errs.ErrNotFound
}

// fakeSecrets returns a fixed field map.
type fakeSecrets struct {
	fields map[string]string
	err    error
}

func (f fakeSecrets) ResolveFields(_ context.Context, _ string) (map[string]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.fields, nil
}

func TestBuildClient_HeaderTemplateExpansion(t *testing.T) {
	u := NewUsecase(newFakeRepo(), fakeSecrets{fields: map[string]string{"token": "abc"}}, nil)
	s := &model.Server{
		Transport:          "http",
		Endpoint:           "https://example.com/mcp",
		Credential:         "github-bot",
		HeaderTemplateJSON: `{"Authorization":"Bearer {{token}}"}`,
	}
	cli, err := u.BuildClient(context.Background(), s)
	if err != nil {
		t.Fatalf("BuildClient: %v", err)
	}
	if cli == nil {
		t.Fatal("nil client")
	}

	// Verify expansion directly via the helper (client headers are unexported).
	headers, err := expandHeaders(s.HeaderTemplateJSON, map[string]string{"token": "abc"})
	if err != nil {
		t.Fatalf("expandHeaders: %v", err)
	}
	if got := headers["Authorization"]; got != "Bearer abc" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer abc")
	}
}

func TestUsecase_UpdateAndDeleteNotifyRuntimeCatalog(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	u := NewUsecase(repo, fakeSecrets{}, nil)
	server, err := u.Create(ctx, &model.Server{
		Name:     "github",
		Endpoint: "https://example.test/mcp",
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cache, err := json.Marshal([]mcpclient.Tool{{Name: "search", InputSchema: json.RawMessage(`{"type":"object"}`)}})
	if err != nil {
		t.Fatalf("marshal cache: %v", err)
	}
	if err := repo.SetToolsCache(ctx, server.ID, string(cache)); err != nil {
		t.Fatalf("SetToolsCache: %v", err)
	}

	type event struct {
		name    string
		server  *model.Server
		toolLen int
	}
	var events []event
	u.SetToolChangeHook(func(_ context.Context, name string, current *model.Server, tools []mcpclient.Tool) {
		events = append(events, event{name: name, server: current, toolLen: len(tools)})
	})

	if err := u.Update(ctx, server.ID, &model.Server{Name: server.Name, Endpoint: server.Endpoint, Enabled: true}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(events) != 2 || events[0].server != nil || events[1].server == nil || events[1].toolLen != 1 {
		t.Fatalf("update events = %#v, want remove then cached replacement", events)
	}

	if err := u.Delete(ctx, server.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(events) != 3 || events[2].name != "github" || events[2].server != nil {
		t.Fatalf("delete event = %#v, want removal", events)
	}
}

func TestUsecase_UpdateChangedConnectionClearsUnverifiedToolCache(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	u := NewUsecase(repo, fakeSecrets{}, nil)
	server, err := u.Create(ctx, &model.Server{
		Name:     "github",
		Endpoint: "https://old.example.test/mcp",
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.SetToolsCache(ctx, server.ID, `[{"name":"old_search"}]`); err != nil {
		t.Fatalf("SetToolsCache: %v", err)
	}
	if err := repo.SetStatus(ctx, server.ID, "ok", ""); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	var events int
	u.SetToolChangeHook(func(_ context.Context, _ string, _ *model.Server, _ []mcpclient.Tool) {
		events++
	})
	if err := u.Update(ctx, server.ID, &model.Server{
		Name:     server.Name,
		Endpoint: "https://new.example.test/mcp",
		Enabled:  true,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	updated, err := repo.Get(ctx, server.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if updated.ToolsCacheJSON != "" || updated.Status != "" || updated.LastError != "" {
		t.Fatalf("changed connection retained probe state: %#v", updated)
	}
	if events != 1 {
		t.Fatalf("events = %d, want only stale-prefix removal", events)
	}
}

func TestUsecase_TestConnectionPublishesVerifiedTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": mcpclient.ProtocolVersion}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name": "search", "description": "search docs", "inputSchema": map[string]any{"type": "object"},
			}}}
		default:
			http.Error(w, "unexpected method", http.StatusBadRequest)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	repo := newFakeRepo()
	u := NewUsecase(repo, fakeSecrets{}, nil)
	registered, err := u.Create(ctx, &model.Server{Name: "docs", Endpoint: server.URL, Enabled: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var published []mcpclient.Tool
	u.SetToolChangeHook(func(_ context.Context, name string, current *model.Server, tools []mcpclient.Tool) {
		if name != "docs" || current == nil {
			t.Fatalf("unexpected hook target: name=%q server=%#v", name, current)
		}
		published = tools
	})

	tools, err := u.TestConnection(ctx, registered.ID)
	if err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
	if len(tools) != 1 || len(published) != 1 || published[0].Name != "search" {
		t.Fatalf("verified=%v published=%v, want one search tool", tools, published)
	}
}

func TestBuildClient_StdioUnsupported(t *testing.T) {
	u := NewUsecase(newFakeRepo(), fakeSecrets{}, nil)
	_, err := u.BuildClient(context.Background(), &model.Server{Transport: "stdio"})
	if err == nil {
		t.Fatal("expected stdio to be unsupported")
	}
}

func TestCreate_Validation(t *testing.T) {
	cases := []struct {
		name    string
		in      *model.Server
		wantErr bool
	}{
		{
			name:    "http missing endpoint",
			in:      &model.Server{Name: "x", Transport: "http"},
			wantErr: true,
		},
		{
			name:    "empty name",
			in:      &model.Server{Transport: "http", Endpoint: "https://e"},
			wantErr: true,
		},
		{
			name:    "bad transport",
			in:      &model.Server{Name: "x", Transport: "grpc", Endpoint: "https://e"},
			wantErr: true,
		},
		{
			name:    "valid http",
			in:      &model.Server{Name: "ok", Transport: "http", Endpoint: "https://e"},
			wantErr: false,
		},
		{
			name:    "transport defaults to http",
			in:      &model.Server{Name: "deflt", Endpoint: "https://e"},
			wantErr: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := NewUsecase(newFakeRepo(), fakeSecrets{}, nil)
			_, err := u.Create(context.Background(), tc.in)
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr && err != nil && !errors.Is(err, errs.ErrInvalid) {
				t.Errorf("expected ErrInvalid, got %v", err)
			}
		})
	}
}
