package marketplace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	bizmp "github.com/ongridio/ongrid/internal/manager/biz/marketplace"
	model "github.com/ongridio/ongrid/internal/manager/model/marketplace"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

type stubSvc struct {
	install    func(ctx context.Context, c bizmp.Caller, s bizmp.Source) (*bizmp.InstallResult, error)
	list       func(ctx context.Context, c bizmp.Caller) ([]*model.InstalledPack, error)
	uninstall  func(ctx context.Context, c bizmp.Caller, packID string) error
	registries func(ctx context.Context, c bizmp.Caller) bizmp.AllowedRegistries
}

func (s stubSvc) Install(ctx context.Context, c bizmp.Caller, src bizmp.Source) (*bizmp.InstallResult, error) {
	return s.install(ctx, c, src)
}
func (s stubSvc) List(ctx context.Context, c bizmp.Caller) ([]*model.InstalledPack, error) {
	return s.list(ctx, c)
}
func (s stubSvc) Uninstall(ctx context.Context, c bizmp.Caller, packID string) error {
	return s.uninstall(ctx, c, packID)
}
func (s stubSvc) Registries(ctx context.Context, c bizmp.Caller) bizmp.AllowedRegistries {
	return s.registries(ctx, c)
}
func (s stubSvc) SetBindings(ctx context.Context, c bizmp.Caller, packID string, bindings map[string]string) error {
	return nil
}

func newRouter(h *Handler) http.Handler {
	r := chi.NewRouter()
	h.Register(r)
	return r
}

func adminCtx() context.Context {
	return tenantctx.With(context.Background(), tenantctx.Tenant{UserID: 1, Role: "admin"})
}
func userCtx() context.Context {
	return tenantctx.With(context.Background(), tenantctx.Tenant{UserID: 2, Role: "user"})
}

func TestInstall_AdminGate(t *testing.T) {
	gotSrc := bizmp.Source{}
	svc := stubSvc{
		install: func(_ context.Context, _ bizmp.Caller, s bizmp.Source) (*bizmp.InstallResult, error) {
			gotSrc = s
			return &bizmp.InstallResult{
				Pack: &model.InstalledPack{PackID: "etcd-tools", Version: "0.1.0", InstalledAt: time.Now()},
			}, nil
		},
	}
	router := newRouter(NewHandler(svc))

	body := `{"type":"local","path":"/tmp/etcd-tools"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/marketplace/install", strings.NewReader(body))
	req = req.WithContext(adminCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin install: status = %d body=%s", rec.Code, rec.Body.String())
	}
	if gotSrc.Type != bizmp.SourceTypeLocal {
		t.Fatalf("svc got type = %q", gotSrc.Type)
	}

	// Non-admin → 403.
	req2 := httptest.NewRequest(http.MethodPost, "/v1/marketplace/install", strings.NewReader(body))
	req2 = req2.WithContext(userCtx())
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("user install: status = %d", rec2.Code)
	}

	// No auth → 401.
	req3 := httptest.NewRequest(http.MethodPost, "/v1/marketplace/install", strings.NewReader(body))
	rec3 := httptest.NewRecorder()
	router.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusUnauthorized {
		t.Fatalf("anon install: status = %d", rec3.Code)
	}
}

func TestInstall_BadRequestMaps400(t *testing.T) {
	svc := stubSvc{
		install: func(_ context.Context, _ bizmp.Caller, _ bizmp.Source) (*bizmp.InstallResult, error) {
			return nil, errors.Join(errs.ErrInvalid, errors.New("path must be absolute"))
		},
	}
	router := newRouter(NewHandler(svc))
	req := httptest.NewRequest(http.MethodPost, "/v1/marketplace/install",
		bytes.NewBufferString(`{"type":"local"}`))
	req = req.WithContext(adminCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestInstall_ConflictMaps409(t *testing.T) {
	svc := stubSvc{
		install: func(_ context.Context, _ bizmp.Caller, _ bizmp.Source) (*bizmp.InstallResult, error) {
			return nil, errs.ErrConflict
		},
	}
	router := newRouter(NewHandler(svc))
	req := httptest.NewRequest(http.MethodPost, "/v1/marketplace/install",
		bytes.NewBufferString(`{"type":"local","path":"/tmp/x"}`))
	req = req.WithContext(adminCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestListInstalled_OpenToAuthUser(t *testing.T) {
	svc := stubSvc{
		list: func(_ context.Context, _ bizmp.Caller) ([]*model.InstalledPack, error) {
			return []*model.InstalledPack{
				{PackID: "a", Version: "1.0.0"},
				{PackID: "b", Version: "1.0.0"},
			}, nil
		},
	}
	router := newRouter(NewHandler(svc))
	req := httptest.NewRequest(http.MethodGet, "/v1/marketplace/installed", nil)
	req = req.WithContext(userCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp listResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 2 || len(resp.Items) != 2 {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestUninstall_AdminAndIdempotent(t *testing.T) {
	called := 0
	svc := stubSvc{
		uninstall: func(_ context.Context, _ bizmp.Caller, packID string) error {
			called++
			if packID != "etcd-tools" {
				t.Fatalf("packID = %q", packID)
			}
			return nil
		},
	}
	router := newRouter(NewHandler(svc))
	req := httptest.NewRequest(http.MethodDelete, "/v1/marketplace/installed/etcd-tools", nil)
	req = req.WithContext(adminCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if called != 1 {
		t.Fatalf("svc.uninstall called %d times", called)
	}

	// Non-admin → 403.
	req2 := httptest.NewRequest(http.MethodDelete, "/v1/marketplace/installed/etcd-tools", nil)
	req2 = req2.WithContext(userCtx())
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("user uninstall: status = %d", rec2.Code)
	}
}

func TestRegistries_OpenToAuthUser(t *testing.T) {
	svc := stubSvc{
		registries: func(_ context.Context, _ bizmp.Caller) bizmp.AllowedRegistries {
			return bizmp.AllowedRegistries{
				Items: []bizmp.RegistryEntry{{Name: "ongrid-official", Allowed: true}},
			}
		},
	}
	router := newRouter(NewHandler(svc))
	req := httptest.NewRequest(http.MethodGet, "/v1/marketplace/registries", nil)
	req = req.WithContext(userCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp bizmp.AllowedRegistries
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].Name != "ongrid-official" {
		t.Fatalf("resp = %+v", resp)
	}
}
