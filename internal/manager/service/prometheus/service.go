package prometheus

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/ongridio/ongrid/internal/pkg/auth"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

const (
	promTicketSubject = "prometheus-proxy"
	// promTicketTTL is the lifetime of one ticket. We refresh sliding
	// inside the nginx auth_request handler (every successful auth
	// re-mints the cookie), so this is effectively the *idle* timeout —
	// users who leave a Grafana tab idle longer than this get prompted.
	// 30 min comfortably covers reading a dashboard / drilling into a
	// trace; previously this was 2 min and users hit 401 mid-read.
	promTicketTTL = 30 * time.Minute
)

type Caller struct {
	UserID uint64
	Role   string
}

type LaunchInput struct {
	Expr       string
	RangeInput string
	EndInput   string
	StepInput  string
}

type Service struct {
	signer *auth.Signer
}

func New(signer *auth.Signer) *Service {
	return &Service{signer: signer}
}

func (s *Service) BuildLaunch(caller Caller, in LaunchInput) (string, string, time.Duration, error) {
	if s.signer == nil {
		return "", "", 0, fmt.Errorf("%w: prometheus signer missing", errs.ErrNotWiredYet)
	}
	expr := strings.TrimSpace(in.Expr)
	if expr == "" {
		return "", "", 0, fmt.Errorf("%w: expr required", errs.ErrInvalid)
	}
	if len(expr) > 2048 {
		return "", "", 0, fmt.Errorf("%w: expr too long", errs.ErrInvalid)
	}

	ticket, err := s.signer.SignWithTTL(auth.Claims{
		UserID: caller.UserID,
		Role:   caller.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: promTicketSubject,
		},
	}, promTicketTTL)
	if err != nil {
		return "", "", 0, err
	}

	q := url.Values{}
	q.Set("g0.expr", expr)
	q.Set("g0.tab", "0")
	if v := strings.TrimSpace(in.RangeInput); v != "" {
		q.Set("g0.range_input", v)
	}
	if v := strings.TrimSpace(in.EndInput); v != "" {
		q.Set("g0.end_input", v)
	}
	if v := strings.TrimSpace(in.StepInput); v != "" {
		q.Set("g0.step_input", v)
	}

	return "/prometheus/graph?" + q.Encode(), ticket, promTicketTTL, nil
}

// RefreshTicket re-mints a ticket from a still-valid one. Used by the
// nginx auth_request handler to slide the session: every successful
// /grafana auth subrequest hands the browser a fresh cookie. Returns
// (newToken, newTTL, true) on success, ("", 0, false) on any error
// (signer missing, token invalid/expired, wrong subject).
func (s *Service) RefreshTicket(token string) (string, time.Duration, bool) {
	if s.signer == nil {
		return "", 0, false
	}
	claims, err := s.signer.Verify(strings.TrimSpace(token))
	if err != nil || claims.Subject != promTicketSubject {
		return "", 0, false
	}
	fresh, err := s.signer.SignWithTTL(auth.Claims{
		UserID: claims.UserID,
		Role:   claims.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: promTicketSubject,
		},
	}, promTicketTTL)
	if err != nil {
		return "", 0, false
	}
	return fresh, promTicketTTL, true
}

func (s *Service) VerifyTicket(token string) error {
	if s.signer == nil {
		return errs.ErrNotWiredYet
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return errs.ErrUnauthorized
	}
	claims, err := s.signer.Verify(token)
	if err != nil {
		return errs.ErrUnauthorized
	}
	if claims.Subject != promTicketSubject {
		return errs.ErrUnauthorized
	}
	return nil
}
