package basetool

import "context"

// bound_credentials.go — ctx propagation for the credential NAMES that the
// session's active skills are bound to (HLD-017 design-time credential
// binding). The runtime resolves active skills, looks up each skill's
// pack-level bindings, and attaches the resulting credential names here.
// cloud_bash reads them at propose time so the queued approval injects those
// credentials at exec — the binding is decided at install time, not chosen
// by the LLM or the user at run time.
//
// Same leaf-package rationale as locale.go: both chatruntime (sets it) and
// tools/cloud_bash (reads it) can depend on basetool without an import cycle.

type boundCredsCtxKeyT struct{}

var boundCredsCtxKey = boundCredsCtxKeyT{}

// WithBoundCredentials returns ctx carrying the active skills' bound
// credential names. Empty/nil = no-op.
func WithBoundCredentials(ctx context.Context, names []string) context.Context {
	if len(names) == 0 {
		return ctx
	}
	return context.WithValue(ctx, boundCredsCtxKey, names)
}

// BoundCredentialsFromContext returns the active-skill bound credential
// names, or nil when none were attached.
func BoundCredentialsFromContext(ctx context.Context) []string {
	v, _ := ctx.Value(boundCredsCtxKey).([]string)
	return v
}
