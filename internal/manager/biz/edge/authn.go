package edge

import (
	"context"
	"log/slog"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/edge"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/passwd"
	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

// AccessKeyAuthenticator implements tunnel.AuthFunc for edge handshakes.
// Injected into internal/pkg/tunnel at wiring time by cmd/ongrid.
type AccessKeyAuthenticator struct {
	repo  Repo
	log   *slog.Logger
	cache *authCache
}

// NewAccessKeyAuthenticator builds the authenticator. log may be nil.
func NewAccessKeyAuthenticator(repo Repo, log *slog.Logger) *AccessKeyAuthenticator {
	return &AccessKeyAuthenticator{repo: repo, log: log, cache: newAuthCache()}
}

// Authenticate looks up the edge by AccessKeyID, verifies the argon2id
// SecretKeyHash against the presented secretKey in constant time, and
// returns the Session on success. All failure paths collapse to
// errs.ErrUnauthorized so the tunnel layer never leaks enumeration signals.
//
// Successful verifications are cached for authCacheTTL (60s) to avoid the
// expensive argon2id computation on every high-frequency data-plane push.
//
// On success it fires a goroutine that bumps status='online' + last_seen_at
// via repo.UpdateStatus; errors are logged and swallowed so a flaky DB does
// not block the handshake. The goroutine uses a 5-second timeout so it
// cannot leak indefinitely if the database is stalled.
func (a *AccessKeyAuthenticator) Authenticate(ctx context.Context, accessKey, secretKey string) (tunnel.Session, error) {
	if a.repo == nil {
		return tunnel.Session{}, errs.ErrNotWiredYet
	}

	// Fast path: check the credential cache before hitting argon2id.
	if edgeID, ok := a.cache.lookup(accessKey, secretKey); ok {
		return tunnel.Session{EdgeID: edgeID}, nil
	}

	e, err := a.repo.GetByAccessKey(ctx, accessKey)
	if err != nil || e == nil {
		return tunnel.Session{}, errs.ErrUnauthorized
	}
	if !passwd.Verify(secretKey, e.SecretKeyHash) {
		return tunnel.Session{}, errs.ErrUnauthorized
	}

	edgeID := e.ID
	a.cache.store(accessKey, secretKey, edgeID)

	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.repo.UpdateStatus(bgCtx, edgeID, model.StatusOnline, time.Now().UTC()); err != nil && a.log != nil {
			a.log.Warn("authn: UpdateStatus(online) failed", "edge_id", edgeID, "err", err)
		}
	}()

	return tunnel.Session{EdgeID: edgeID}, nil
}

// AsAuthFunc adapts Authenticate to tunnel.AuthFunc.
func (a *AccessKeyAuthenticator) AsAuthFunc() tunnel.AuthFunc {
	return a.Authenticate
}
