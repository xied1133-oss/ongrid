package report

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	bizreport "github.com/ongridio/ongrid/internal/manager/biz/report"
	reportstore "github.com/ongridio/ongrid/internal/manager/data/report/store"
	reportmodel "github.com/ongridio/ongrid/internal/manager/model/report"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

func newTestHandler(t *testing.T) (*Handler, *gorm.DB) {
	return newTestHandlerWithGenerator(t, nil)
}

func newTestHandlerWithGenerator(t *testing.T, gen bizreport.Generator) (*Handler, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := reportstore.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := reportstore.NewRepo(db)
	uc := bizreport.NewUsecase(repo, gen, func() string { return "rpt-" + time.Now().Format("150405.000000000") }).
		WithReadRepo(repo)
	return NewHandler(uc), db
}

func req(method, path, body, role string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	ctx := tenantctx.With(r.Context(), tenantctx.Tenant{UserID: 42, Role: role})
	return r.WithContext(ctx)
}

func serve(h *Handler, r *http.Request) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	h.Register(router)
	h.RegisterPublic(router)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func TestCreateSchedule_AsUser(t *testing.T) {
	h, _ := newTestHandler(t)
	body := `{"name":"Weekly Ops","kind":"weekly","timezone":"Asia/Shanghai"}`
	w := serve(h, req("POST", "/v1/report-schedules", body, "user"))
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", w.Code, w.Body.String())
	}
	var got scheduleView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.CronSpec != "0 9 * * 1" {
		t.Errorf("cron spec = %q, want weekly default", got.CronSpec)
	}
	if got.NextFireAt == nil {
		t.Error("next_fire_at not armed")
	}
	if !got.Enabled {
		t.Error("schedule should be enabled by default")
	}
}

func TestCreateSchedule_ViewerForbidden(t *testing.T) {
	h, _ := newTestHandler(t)
	w := serve(h, req("POST", "/v1/report-schedules", `{"kind":"weekly"}`, "viewer"))
	if w.Code != http.StatusForbidden {
		t.Errorf("viewer create status = %d, want 403", w.Code)
	}
}

func TestListSchedules_ViewerReadOnly(t *testing.T) {
	h, db := newTestHandler(t)
	// Seed a schedule directly through the usecase.
	repo := reportstore.NewRepo(db)
	uc := bizreport.NewUsecase(repo, nil, func() string { return "x" }).WithReadRepo(repo)
	s := &reportmodel.ReportSchedule{CreatedBy: 42, Name: "seed", Kind: reportmodel.KindWeekly, Timezone: "UTC"}
	if err := uc.CreateSchedule(context.Background(), s, time.Now()); err != nil {
		t.Fatal(err)
	}

	w := serve(h, req("GET", "/v1/report-schedules", "", "viewer"))
	if w.Code != http.StatusOK {
		t.Fatalf("viewer list status = %d", w.Code)
	}
	var resp struct {
		Schedules []scheduleView `json:"schedules"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Schedules) != 1 {
		t.Errorf("viewer should see schedules, got %d", len(resp.Schedules))
	}
}

func TestGenerateNow_ViewerForbidden(t *testing.T) {
	h, _ := newTestHandler(t)
	w := serve(h, req("POST", "/v1/reports", `{"kind":"weekly"}`, "viewer"))
	if w.Code != http.StatusForbidden {
		t.Errorf("viewer generate status = %d, want 403", w.Code)
	}
}

func TestGenerateNow_AsUser_Accepted(t *testing.T) {
	h, _ := newTestHandler(t)
	w := serve(h, req("POST", "/v1/reports", `{"kind":"weekly","timezone":"UTC"}`, "user"))
	if w.Code != http.StatusAccepted {
		t.Fatalf("generate status = %d, body=%s", w.Code, w.Body.String())
	}
	var d reportDetail
	_ = json.Unmarshal(w.Body.Bytes(), &d)
	if d.Status != "pending" {
		t.Errorf("manual report status = %q, want pending", d.Status)
	}
}

func TestGenerateNow_WhenLLMProviderMissing_ReturnsNotWired(t *testing.T) {
	h, _ := newTestHandlerWithGenerator(t, bizreport.NewUnavailableGenerator("LLM provider not configured"))
	w := serve(h, req("POST", "/v1/reports", `{"kind":"weekly","timezone":"UTC"}`, "user"))
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("generate status = %d, body=%s", w.Code, w.Body.String())
	}
	var body errorBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "not-wired-yet" {
		t.Fatalf("error code = %q, want not-wired-yet", body.Code)
	}
	if !strings.Contains(body.Error, "LLM provider not configured") {
		t.Fatalf("error body = %q, want LLM provider hint", body.Error)
	}
}

func TestShareAndPublicRead(t *testing.T) {
	h, _ := newTestHandler(t)
	// Create a manual report.
	w := serve(h, req("POST", "/v1/reports", `{"kind":"weekly"}`, "user"))
	var d reportDetail
	_ = json.Unmarshal(w.Body.Bytes(), &d)

	// Share it.
	w = serve(h, req("POST", "/v1/reports/"+d.ID+"/share", "", "user"))
	if w.Code != http.StatusOK {
		t.Fatalf("share status = %d", w.Code)
	}
	var sh struct {
		Token string `json:"share_token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &sh)
	if sh.Token == "" {
		t.Fatal("no share token returned")
	}

	// Public read with NO auth context.
	pub := httptest.NewRequest("GET", "/r/"+sh.Token, nil)
	w = serve(h, pub)
	if w.Code != http.StatusOK {
		t.Errorf("public share read status = %d, want 200", w.Code)
	}
}

func TestUnauthenticated401(t *testing.T) {
	h, _ := newTestHandler(t)
	r := httptest.NewRequest("GET", "/v1/reports", nil) // no tenant ctx
	w := serve(h, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated status = %d, want 401", w.Code)
	}
}
