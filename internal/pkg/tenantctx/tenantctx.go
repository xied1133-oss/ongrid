// Package tenantctx stores the per-request caller identity on
// context.Context.
//
// The auth middleware (internal/pkg/auth) decodes JWT claims and calls With;
// downstream service/biz/data layers read the Tenant via From to check role /
// ownership. The name "tenant" is vestigial — in the single-tenant private MVP
// there is just one user namespace, so Tenant is really just the
// authenticated caller.
package tenantctx

import "context"

// Tenant is the caller's user id + email + role + superuser flag,
// populated from the JWT claims by the auth middleware. Role values:
// admin / user (legacy). IsSuperuser is the authoritative system-admin
// flag — the auth middleware sets it from the IsSuperuser JWT claim,
// falling back to Role=="admin" for tokens issued before the claim
// shipped. Email is included so the audit middleware can label rows
// without an extra DB lookup (added 2026-05-21 after the audit_view
// rows showed up with empty user fields).
type Tenant struct {
	UserID      uint64
	Email       string
	Role        string
	IsSuperuser bool
}

type ctxKey struct{}

// With attaches t to ctx for downstream handlers (service / biz / data
// layers) — they read it via From. Set on the request context AFTER
// the auth middleware verifies the JWT.
func With(ctx context.Context, t Tenant) context.Context {
	return context.WithValue(ctx, ctxKey{}, t)
}

// From extracts the Tenant from ctx. The bool is false when no tenant
// has been attached (e.g. public endpoint or missing middleware). When
// a mutable slot is installed and populated, From prefers the slot —
// this lets outer middlewares (audit) see the tenant value that an
// inner middleware (auth) wrote even though the outer middleware's
// request reference doesn't carry the inner WithContext.
func From(ctx context.Context) (Tenant, bool) {
	if s, ok := ctx.Value(slotKey{}).(*slot); ok && s != nil && s.set {
		return s.t, true
	}
	t, ok := ctx.Value(ctxKey{}).(Tenant)
	return t, ok
}

// ----- mutable slot -----
//
// Mirrors the auditSlot pattern in the audit middleware: a *slot
// pointer in the OUTER request context lets outer code see what an
// inner middleware wrote, even though the inner's r.WithContext()
// produced a new ctx that the outer didn't capture.
//
// Wired by:
//   - AuditMiddleware: WithSlot(ctx) BEFORE next.ServeHTTP
//   - auth.Middleware: SetOnSlot(ctx, t) AFTER verifying the JWT
//   - enrichFromRequest (audit middleware post-handler): reads via From

type slotKey struct{}

type slot struct {
	t   Tenant
	set bool
}

// WithSlot installs an empty mutable Tenant slot in ctx. Subsequent
// SetOnSlot calls (typically from the auth middleware) populate it;
// From reads from the slot first so outer middlewares pick up the
// inner-set value.
func WithSlot(ctx context.Context) context.Context {
	return context.WithValue(ctx, slotKey{}, &slot{})
}

// SetOnSlot writes t into the slot stored in ctx, if one is installed.
// No-op when no slot was installed (defensive — public endpoints don't
// install one and shouldn't crash auth middleware).
func SetOnSlot(ctx context.Context, t Tenant) {
	if s, ok := ctx.Value(slotKey{}).(*slot); ok && s != nil {
		s.t = t
		s.set = true
	}
}
