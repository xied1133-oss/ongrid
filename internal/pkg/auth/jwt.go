// Package auth signs and verifies JWTs and exposes an HTTP middleware that
// writes tenantctx.Tenant onto the request context.
//
// Red line: Verify does signature/claims validation ONLY; it does NOT look up
// the user in the iam database. User identity is baked into the access token
// at login time and trusted for the token's lifetime.
package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the JWT payload used by ongrid. It embeds RegisteredClaims so
// standard fields (exp, iat, sub, ...) are signed too.
//
// IsSuperuser is the system-administrator flag (independent of Role and
// org memberships). When true, the manager's authz middleware short-
// circuits casbin entirely. Old tokens without this field decode with
// IsSuperuser=false; they keep working through the legacy Role=="admin"
// fallback in the middleware.
type Claims struct {
	UserID      uint64 `json:"user_id"`
	Email       string `json:"email,omitempty"`
	Role        string `json:"role"`
	IsSuperuser bool   `json:"is_superuser,omitempty"`
	jwt.RegisteredClaims
}

// Signer issues and verifies ongrid JWTs.
type Signer struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
}

// NewSigner builds a Signer. secret must be non-empty at runtime; MVP allows
// a dev default from config for local use.
func NewSigner(secret string, accessTTL, refreshTTL time.Duration) *Signer {
	return &Signer{
		secret:     []byte(secret),
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
	}
}

// AccessTTL exposes the configured access-token lifetime so service handlers
// can report expires_in to clients without guessing.
func (s *Signer) AccessTTL() time.Duration { return s.accessTTL }

// RefreshTTL exposes the configured refresh-token lifetime.
func (s *Signer) RefreshTTL() time.Duration { return s.refreshTTL }

// SignAccess issues a short-lived access token. exp/iat are overwritten from
// TTL; the caller supplies UserID/Role/Sub.
func (s *Signer) SignAccess(c Claims) (string, error) {
	return s.sign(c, s.accessTTL)
}

// SignRefresh issues a long-lived refresh token.
func (s *Signer) SignRefresh(c Claims) (string, error) {
	return s.sign(c, s.refreshTTL)
}

// SignWithTTL issues a token with a caller-supplied ttl. This is used for
// short-lived internal tickets such as authenticated reverse-proxy hops.
func (s *Signer) SignWithTTL(c Claims, ttl time.Duration) (string, error) {
	return s.sign(c, ttl)
}

func (s *Signer) sign(c Claims, ttl time.Duration) (string, error) {
	now := time.Now()
	c.RegisteredClaims.IssuedAt = jwt.NewNumericDate(now)
	c.RegisteredClaims.ExpiresAt = jwt.NewNumericDate(now.Add(ttl))
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	return tok.SignedString(s.secret)
}

// Verify parses and validates the token. On success returns the claims.
// Signature mismatch, expiry and malformed tokens all map to an error; the
// caller (middleware) surfaces 401.
func (s *Signer) Verify(token string) (*Claims, error) {
	var c Claims
	t, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return s.secret, nil
	})
	if err != nil {
		return nil, err
	}
	if !t.Valid {
		return nil, errors.New("invalid token")
	}
	return &c, nil
}
