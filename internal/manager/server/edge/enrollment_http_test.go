package edge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	biz "github.com/ongridio/ongrid/internal/manager/biz/edge"
	model "github.com/ongridio/ongrid/internal/manager/model/edge"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

type fakeEnrollmentHTTPService struct {
	lastEnroll     biz.EnrollInput
	deletedProfile uint64
}

func (f *fakeEnrollmentHTTPService) CreateProfile(context.Context, biz.CreateEnrollmentProfileInput) (*biz.CreateEnrollmentProfileResult, error) {
	return nil, nil
}

func (f *fakeEnrollmentHTTPService) ListProfiles(context.Context, biz.EnrollmentProfileListFilter) ([]*model.EnrollmentProfile, int64, error) {
	return nil, 0, nil
}

func (f *fakeEnrollmentHTTPService) RevokeProfile(context.Context, uint64) error { return nil }

func (f *fakeEnrollmentHTTPService) DeleteProfile(_ context.Context, id uint64) error {
	f.deletedProfile = id
	return nil
}

func (f *fakeEnrollmentHTTPService) Enroll(_ context.Context, input biz.EnrollInput) (*biz.EnrollResult, error) {
	f.lastEnroll = input
	return &biz.EnrollResult{
		EdgeID:           9,
		AccessKey:        "access-9",
		SecretKey:        "secret-9",
		CloudAddr:        "manager.example.com:40012",
		ManagerPublicURL: "https://manager.example.com",
	}, nil
}

func TestEnrollmentEndpointRequiresBearerAndIssuesIndependentCredential(t *testing.T) {
	svc := &fakeEnrollmentHTTPService{}
	handler := NewEnrollmentHandler(svc)
	router := chi.NewRouter()
	handler.RegisterInternal(router)
	body := `{"host_info":{"hostname":"host-a","fingerprint":"machine-a"},"agent_version":"v1.0.0"}`

	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/internal/edge/enroll", strings.NewReader(body)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("missing bearer status = %d, body=%s", unauthorized.Code, unauthorized.Body.String())
	}

	request := httptest.NewRequest(http.MethodPost, "/internal/edge/enroll", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer oen_bootstrap")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Real-IP", "192.0.2.9")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if svc.lastEnroll.Token != "oen_bootstrap" || svc.lastEnroll.HostInfo.Hostname != "host-a" || svc.lastEnroll.SourceIP != "192.0.2.9" {
		t.Fatalf("Enroll input = %+v", svc.lastEnroll)
	}
	var payload enrollEdgeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.EdgeID != 9 || payload.AccessKey != "access-9" || payload.SecretKey != "secret-9" {
		t.Fatalf("response = %+v", payload)
	}
}

func TestDeleteEnrollmentProfileDeletesByID(t *testing.T) {
	svc := &fakeEnrollmentHTTPService{}
	handler := NewEnrollmentHandler(svc)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tenant := tenantctx.Tenant{UserID: 1, Role: roleAdmin}
			next.ServeHTTP(w, r.WithContext(tenantctx.With(r.Context(), tenant)))
		})
	})
	handler.RegisterProtected(router)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/v1/edge-enrollment-profiles/42", nil))

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if svc.deletedProfile != 42 {
		t.Fatalf("deleted profile = %d, want 42", svc.deletedProfile)
	}
}
