package auth

import (
	"net/http"
	"strings"

	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

// Middleware returns an HTTP middleware that:
//  1. reads Authorization: Bearer <token>
//     OR ?token=<jwt> query string (WebSocket fallback — browsers
//     can't set headers on the native WebSocket constructor)
//  2. verifies the JWT via signer
//  3. writes tenantctx.Tenant onto the request context
//
// On any failure it responds 401 and does NOT invoke next.
//
// No DB lookup is performed. Per-route role checks live in the
// iam / manager HTTP handlers which have access to tenantctx.From.
func Middleware(signer *Signer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok := extractBearer(r)
			if tok == "" {
				http.Error(w, "missing bearer token", http.StatusUnauthorized)
				return
			}
			claims, err := signer.Verify(tok)
			if err != nil {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}
			// IsSuperuser comes from the JWT claim when present; old
			// tokens (pre-claim) fall back to Role=="admin" so an
			// existing session still has full system privileges after
			// an upgrade. Renewals will fill the explicit field.
			isSuper := claims.IsSuperuser || claims.Role == "admin"
			t := tenantctx.Tenant{
				UserID:      claims.UserID,
				Email:       claims.Email,
				Role:        claims.Role,
				IsSuperuser: isSuper,
			}
			// Mirror onto the OUTER mutable slot (installed by audit
			// middleware) so outer middlewares can see the tenant even
			// though their `r` doesn't carry our deeper WithContext.
			tenantctx.SetOnSlot(r.Context(), t)
			ctx := tenantctx.With(r.Context(), t)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// extractBearer pulls the JWT from the Authorization: Bearer <tok>
// header, falling back to ?token=<tok> for WebSocket upgrades (where
// browsers can't set request headers on the native WebSocket).
func extractBearer(r *http.Request) string {
	const prefix = "Bearer "
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, prefix) {
		return strings.TrimPrefix(h, prefix)
	}
	if q := r.URL.Query().Get("token"); q != "" {
		return q
	}
	return ""
}
