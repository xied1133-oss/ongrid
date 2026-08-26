package basetool

import "context"

// toolCallIDCtxKeyT stores the persisted tool-call row id for kernels that do
// not use eino compose. The graph kernel gets its id from compose directly;
// the legacy kernel stamps this value before invoking the closure registry so
// approval cards can attach to the existing streaming tool card.
type toolCallIDCtxKeyT struct{}

var toolCallIDCtxKey = toolCallIDCtxKeyT{}

// WithToolCallID tags ctx with the current persisted tool-call row id.
func WithToolCallID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, toolCallIDCtxKey, id)
}

// ToolCallIDFromContext returns the legacy-kernel tool-call row id, if any.
func ToolCallIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(toolCallIDCtxKey).(string)
	return v
}
