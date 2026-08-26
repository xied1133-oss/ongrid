package operatorrun

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	biz "github.com/ongridio/ongrid/internal/manager/biz/operatorrun"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

type fakeService struct {
	in    biz.CreateInput
	gotID string
}

func (f *fakeService) Create(_ context.Context, _ biz.Caller, in biz.CreateInput) (*biz.Run, error) {
	f.in = in
	return &biz.Run{ID: "run-1", Command: in.Command, Status: biz.StatusRunning, EdgeIDs: in.EdgeIDs}, nil
}
func (f *fakeService) Get(_ context.Context, _ biz.Caller, id string) (*biz.Run, error) {
	f.gotID = id
	return &biz.Run{ID: "run-1"}, nil
}
func (f *fakeService) Cancel(context.Context, biz.Caller, string) (*biz.Run, error) {
	return &biz.Run{ID: "run-1", Status: biz.StatusCancelled}, nil
}
func (f *fakeService) ListNetNS(_ context.Context, _ biz.Caller, edgeID uint64) (*biz.NetNSList, error) {
	return &biz.NetNSList{EdgeID: edgeID, Namespaces: []string{"blue"}}, nil
}

func TestRunIDFromRequestFallsBackToPath(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/operator-runs/run-123/events", nil)
	if got := runIDFromRequest(req); got != "run-123" {
		t.Fatalf("runIDFromRequest = %q", got)
	}
}
func (f *fakeService) Subscribe(context.Context, biz.Caller, string) ([]biz.Event, <-chan biz.Event, func(), error) {
	ch := make(chan biz.Event)
	close(ch)
	return nil, ch, func() {}, nil
}

func TestCreate(t *testing.T) {
	svc := &fakeService{}
	r := chi.NewRouter()
	NewHandler(svc).Register(r)
	body := bytes.NewBufferString(`{"edge_ids":[1,2],"command":"ping","args":{"host":"127.0.0.1"},"timeout_ms":3000}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/operator-runs", body)
	req = req.WithContext(tenantctx.With(req.Context(), tenantctx.Tenant{UserID: 7, Role: "admin"}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if svc.in.Command != "ping" || len(svc.in.EdgeIDs) != 2 {
		t.Fatalf("input = %+v", svc.in)
	}
	var out biz.Run
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.ID != "run-1" {
		t.Fatalf("id = %q", out.ID)
	}
}

func TestListNetNS(t *testing.T) {
	svc := &fakeService{}
	r := chi.NewRouter()
	NewHandler(svc).Register(r)
	req := httptest.NewRequest(http.MethodGet, "/v1/operator-runs/netns?edge_id=7", nil)
	req = req.WithContext(tenantctx.With(req.Context(), tenantctx.Tenant{UserID: 7, Role: "admin"}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out biz.NetNSList
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.EdgeID != 7 || len(out.Namespaces) != 1 || out.Namespaces[0] != "blue" {
		t.Fatalf("out = %+v", out)
	}
}
