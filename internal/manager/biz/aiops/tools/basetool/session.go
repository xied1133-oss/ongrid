package basetool

import "context"

// session.go — ctx propagation for the chat session id. The runtime attaches
// it once per request; tools that queue work for later execution read it so
// the work can be tied back to its originating session. cloud_bash uses it to
// resolve a per-session agent workspace (HLD-019): the approval payload
// carries the session id so the execute-on-approve hook runs the command in
// <workspace>/sessions/<id>/ — files written in one command survive to the
// next, instead of a throwaway temp dir.
//
// Same leaf-package rationale as bound_credentials.go / llm_choice.go: both
// chatruntime (sets it) and tools/cloud_bash (reads it) depend on basetool
// without an import cycle.

type sessionIDCtxKeyT struct{}

var sessionIDCtxKey = sessionIDCtxKeyT{}

// WithSessionID returns ctx carrying the chat session id. Empty = no-op.
func WithSessionID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionIDCtxKey, id)
}

// SessionIDFromContext returns the chat session id, or "" when none was
// attached.
func SessionIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(sessionIDCtxKey).(string)
	return v
}
