package basetool

import "context"

// host_write.go — ctx propagation for the admin "allow Agent write actions"
// gate. The active kernel resolves the live AgentWriteEnabled setting once per
// request and stamps the result here; write tools read it before proposing or
// dispatching work. host_bash also forwards it to the edge as
// BashExecRequest.Unrestricted, which makes the edge bypass cmdpolicy and run
// the raw command through a shell. Gate OFF (the default) leaves host_bash on
// the locked read-only cmdpolicy path.
//
// Same leaf-package rationale as session.go / artifact_source.go: both the
// producer (chatruntime) and the consumer (tools/bash_basetool) depend on
// basetool without an import cycle.

type hostWriteAllowedCtxKeyT struct{}

var hostWriteAllowedCtxKey = hostWriteAllowedCtxKeyT{}

// WithAgentWriteAllowed tags ctx with the resolved global write gate.
func WithAgentWriteAllowed(ctx context.Context, allowed bool) context.Context {
	return context.WithValue(ctx, hostWriteAllowedCtxKey, allowed)
}

// AgentWriteAllowedFromContext reports whether the write gate authorized this
// request. Absent key means false so incomplete wiring fails closed.
func AgentWriteAllowedFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(hostWriteAllowedCtxKey).(bool)
	return v
}

// WithHostWriteAllowed is the compatibility name used by the graph runtime.
func WithHostWriteAllowed(ctx context.Context, allowed bool) context.Context {
	return WithAgentWriteAllowed(ctx, allowed)
}

// HostWriteAllowedFromContext is the compatibility name used by host_bash.
func HostWriteAllowedFromContext(ctx context.Context) bool {
	return AgentWriteAllowedFromContext(ctx)
}
