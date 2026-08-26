package prometheus

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	svc "github.com/ongridio/ongrid/internal/manager/service/prometheus"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/promquery"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

type stubService struct {
	buildLaunch func(caller svc.Caller, in svc.LaunchInput) (string, string, time.Duration, error)
	verify      func(token string) error
}

func (s stubService) BuildLaunch(caller svc.Caller, in svc.LaunchInput) (string, string, time.Duration, error) {
	return s.buildLaunch(caller, in)
}

func (s stubService) VerifyTicket(token string) error {
	return s.verify(token)
}

func (s stubService) RefreshTicket(token string) (string, time.Duration, bool) {
	if s.verify != nil && s.verify(token) == nil {
		return token, 30 * time.Minute, true
	}
	return "", 0, false
}

func TestLaunchSetsCookieAndReturnsURL(t *testing.T) {
	t.Parallel()

	h := NewHandler(stubService{
		buildLaunch: func(caller svc.Caller, in svc.LaunchInput) (string, string, time.Duration, error) {
			if caller.UserID != 7 || caller.Role != "admin" {
				t.Fatalf("caller = %+v", caller)
			}
			if in.Expr != "up" || in.RangeInput != "1h" || in.StepInput != "30s" {
				t.Fatalf("input = %+v", in)
			}
			return "/prometheus/graph?g0.expr=up", "ticket-123", 2 * time.Minute, nil
		},
		verify: func(token string) error { return nil },
	})

	body := bytes.NewBufferString(`{"expr":"up","range_input":"1h","step_input":"30s"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/prometheus/launch", body)
	req = req.WithContext(tenantctx.With(context.Background(), tenantctx.Tenant{
		UserID: 7,
		Role:   "admin",
	}))
	rec := httptest.NewRecorder()

	h.launch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("launch() status = %d", rec.Code)
	}
	var resp launchResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if resp.URL != "/prometheus/graph?g0.expr=up" {
		t.Fatalf("launch() url = %q", resp.URL)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("launch() cookies = %d", len(cookies))
	}
	if cookies[0].Name != promTicketCookie || cookies[0].Value != "ticket-123" {
		t.Fatalf("launch() cookie = %+v", cookies[0])
	}
	if cookies[0].Path != "/" || !cookies[0].HttpOnly || !cookies[0].Secure {
		t.Fatalf("launch() cookie attrs = %+v", cookies[0])
	}
}

func TestLaunchRequiresCaller(t *testing.T) {
	t.Parallel()

	h := NewHandler(stubService{
		buildLaunch: func(caller svc.Caller, in svc.LaunchInput) (string, string, time.Duration, error) {
			t.Fatal("BuildLaunch should not be called")
			return "", "", 0, nil
		},
		verify: func(token string) error { return nil },
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/prometheus/launch", bytes.NewBufferString(`{"expr":"up"}`))
	rec := httptest.NewRecorder()

	h.launch(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("launch() status = %d", rec.Code)
	}
}

func TestAuthRejectsMissingCookie(t *testing.T) {
	t.Parallel()

	h := NewHandler(stubService{
		buildLaunch: func(caller svc.Caller, in svc.LaunchInput) (string, string, time.Duration, error) {
			return "", "", 0, nil
		},
		verify: func(token string) error { return nil },
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/prometheus/auth", nil)
	rec := httptest.NewRecorder()

	h.auth(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("auth() status = %d", rec.Code)
	}
}

func TestAuthPassesCookieToVerifier(t *testing.T) {
	t.Parallel()

	h := NewHandler(stubService{
		buildLaunch: func(caller svc.Caller, in svc.LaunchInput) (string, string, time.Duration, error) {
			return "", "", 0, nil
		},
		verify: func(token string) error {
			if token != "ticket-123" {
				t.Fatalf("VerifyTicket() token = %q", token)
			}
			return nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/prometheus/auth", nil)
	req.AddCookie(&http.Cookie{Name: promTicketCookie, Value: "ticket-123"})
	rec := httptest.NewRecorder()

	h.auth(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("auth() status = %d", rec.Code)
	}
}

// fakeProm satisfies PromQuerier for handler tests. Records the last
// QueryRange invocation so test bodies can assert wire-shape mapping.
type fakeProm struct {
	gotExpr  string
	gotStart time.Time
	gotEnd   time.Time
	gotStep  time.Duration

	resp *promquery.InstantResult
	err  error
}

func (f *fakeProm) QueryRange(_ context.Context, expr string, start, end time.Time, step time.Duration) (*promquery.InstantResult, error) {
	f.gotExpr = expr
	f.gotStart = start
	f.gotEnd = end
	f.gotStep = step
	return f.resp, f.err
}

func TestQueryRangeHappyPath(t *testing.T) {
	t.Parallel()
	matrix := json.RawMessage(`[{"metric":{"edge_id":"7"},"values":[[1714572000,"12.3"]]}]`)
	prom := &fakeProm{resp: &promquery.InstantResult{ResultType: "matrix", Result: matrix}}
	h := NewHandlerWithProm(stubService{
		buildLaunch: func(_ svc.Caller, _ svc.LaunchInput) (string, string, time.Duration, error) {
			return "", "", 0, nil
		},
		verify: func(_ string) error { return nil },
	}, prom)

	body := bytes.NewBufferString(`{"expr":"up","start":"2026-05-01T12:00:00Z","end":"2026-05-01T13:00:00Z","step":"30s"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/prometheus/query_range", body)
	req = req.WithContext(tenantctx.With(context.Background(), tenantctx.Tenant{UserID: 7, Role: "admin"}))
	rec := httptest.NewRecorder()

	h.queryRange(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp queryRangeResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if resp.ResultType != "matrix" {
		t.Errorf("result_type = %q, want matrix", resp.ResultType)
	}
	if string(resp.Result) != string(matrix) {
		t.Errorf("result not passed through:\n got %s\nwant %s", resp.Result, matrix)
	}
	if prom.gotExpr != "up" {
		t.Errorf("expr propagated = %q", prom.gotExpr)
	}
	if prom.gotStep != 30*time.Second {
		t.Errorf("step = %v, want 30s", prom.gotStep)
	}
}

func TestQueryRangeRejectsMissingTenant(t *testing.T) {
	t.Parallel()
	h := NewHandlerWithProm(stubService{
		buildLaunch: func(_ svc.Caller, _ svc.LaunchInput) (string, string, time.Duration, error) {
			return "", "", 0, nil
		},
		verify: func(_ string) error { return nil },
	}, &fakeProm{})

	body := bytes.NewBufferString(`{"expr":"up","start":"2026-05-01T12:00:00Z","end":"2026-05-01T13:00:00Z","step":"30s"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/prometheus/query_range", body)
	rec := httptest.NewRecorder()
	h.queryRange(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestQueryRangeRejectsBadInput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
	}{
		{"missing expr", `{"start":"2026-05-01T12:00:00Z","end":"2026-05-01T13:00:00Z","step":"30s"}`},
		{"bad start", `{"expr":"up","start":"yesterday","end":"2026-05-01T13:00:00Z","step":"30s"}`},
		{"end<start", `{"expr":"up","start":"2026-05-01T13:00:00Z","end":"2026-05-01T12:00:00Z","step":"30s"}`},
		{"bad step", `{"expr":"up","start":"2026-05-01T12:00:00Z","end":"2026-05-01T13:00:00Z","step":"banana"}`},
		{"missing step", `{"expr":"up","start":"2026-05-01T12:00:00Z","end":"2026-05-01T13:00:00Z"}`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := NewHandlerWithProm(stubService{
				buildLaunch: func(_ svc.Caller, _ svc.LaunchInput) (string, string, time.Duration, error) {
					return "", "", 0, nil
				},
				verify: func(_ string) error { return nil },
			}, &fakeProm{})
			req := httptest.NewRequest(http.MethodPost, "/v1/prometheus/query_range", bytes.NewBufferString(tc.body))
			req = req.WithContext(tenantctx.With(context.Background(), tenantctx.Tenant{UserID: 7, Role: "admin"}))
			rec := httptest.NewRecorder()
			h.queryRange(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestQueryRangeRejectsOversizeExpr(t *testing.T) {
	t.Parallel()
	h := NewHandlerWithProm(stubService{
		buildLaunch: func(_ svc.Caller, _ svc.LaunchInput) (string, string, time.Duration, error) {
			return "", "", 0, nil
		},
		verify: func(_ string) error { return nil },
	}, &fakeProm{})
	huge := strings.Repeat("a", maxExprBytes+1)
	body := bytes.NewBufferString(`{"expr":"` + huge + `","start":"2026-05-01T12:00:00Z","end":"2026-05-01T13:00:00Z","step":"30s"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/prometheus/query_range", body)
	req = req.WithContext(tenantctx.With(context.Background(), tenantctx.Tenant{UserID: 7, Role: "admin"}))
	rec := httptest.NewRecorder()
	h.queryRange(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestQueryRangeWithoutPromReturnsNotWired(t *testing.T) {
	t.Parallel()
	h := NewHandler(stubService{
		buildLaunch: func(_ svc.Caller, _ svc.LaunchInput) (string, string, time.Duration, error) {
			return "", "", 0, nil
		},
		verify: func(_ string) error { return nil },
	})
	body := bytes.NewBufferString(`{"expr":"up","start":"2026-05-01T12:00:00Z","end":"2026-05-01T13:00:00Z","step":"30s"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/prometheus/query_range", body)
	req = req.WithContext(tenantctx.With(context.Background(), tenantctx.Tenant{UserID: 7, Role: "admin"}))
	rec := httptest.NewRecorder()
	h.queryRange(rec, req)
	// errs.ErrNotWiredYet maps to 501 in errs.HTTPStatus.
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuthRejectsInvalidCookie(t *testing.T) {
	t.Parallel()

	h := NewHandler(stubService{
		buildLaunch: func(caller svc.Caller, in svc.LaunchInput) (string, string, time.Duration, error) {
			return "", "", 0, nil
		},
		verify: func(token string) error { return errs.ErrUnauthorized },
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/prometheus/auth", nil)
	req.AddCookie(&http.Cookie{Name: promTicketCookie, Value: "bad-ticket"})
	rec := httptest.NewRecorder()

	h.auth(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("auth() status = %d", rec.Code)
	}
}
