package prometheus

import (
	"testing"
	"time"

	"github.com/ongridio/ongrid/internal/pkg/auth"
)

func TestBuildLaunchAndVerifyTicket(t *testing.T) {
	t.Parallel()

	svc := New(auth.NewSigner("test-secret", time.Hour, 24*time.Hour))
	url, ticket, ttl, err := svc.BuildLaunch(Caller{
		UserID: 42,
		Role:   "admin",
	}, LaunchInput{
		Expr:       `up{job="node"}`,
		RangeInput: "1h",
		StepInput:  "30s",
	})
	if err != nil {
		t.Fatalf("BuildLaunch() error = %v", err)
	}
	if url != `/prometheus/graph?g0.expr=up%7Bjob%3D%22node%22%7D&g0.range_input=1h&g0.step_input=30s&g0.tab=0` {
		t.Fatalf("BuildLaunch() url = %q", url)
	}
	if ticket == "" {
		t.Fatal("BuildLaunch() returned empty ticket")
	}
	if ttl != promTicketTTL {
		t.Fatalf("BuildLaunch() ttl = %v, want %v", ttl, promTicketTTL)
	}
	if err := svc.VerifyTicket(ticket); err != nil {
		t.Fatalf("VerifyTicket() error = %v", err)
	}
}

func TestVerifyTicketRejectsWrongSubject(t *testing.T) {
	t.Parallel()

	signer := auth.NewSigner("test-secret", time.Hour, 24*time.Hour)
	token, err := signer.SignWithTTL(auth.Claims{
		UserID: 1,
		Role:   "admin",
	}, time.Minute)
	if err != nil {
		t.Fatalf("SignWithTTL() error = %v", err)
	}

	svc := New(signer)
	if err := svc.VerifyTicket(token); err == nil {
		t.Fatal("VerifyTicket() expected unauthorized error")
	}
}

func TestBuildLaunchRejectsEmptyExpr(t *testing.T) {
	t.Parallel()

	svc := New(auth.NewSigner("test-secret", time.Hour, 24*time.Hour))
	if _, _, _, err := svc.BuildLaunch(Caller{}, LaunchInput{}); err == nil {
		t.Fatal("BuildLaunch() expected invalid error")
	}
}
