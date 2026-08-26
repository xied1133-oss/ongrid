// Package basetool — shared ctx propagation for the active UI locale.
//
// Lives here (not in chatruntime or tools) because:
//   - chatruntime sets it on the coordinator graph's ctx
//   - tools/agent_tool.go reads it inside InvokableRun to forward into
//     the sub-agent's SpawnWorkerRequest
//   - chatruntime can't import tools/agent_tool (cmd-side wiring is the
//     other direction, importing tools from chatruntime would close the
//     loop), so we need a third leaf package both sides can depend on.
//     basetool already is that leaf package — chatruntime imports it for
//     wire-test helpers, and tools/agent_tool can pick it up trivially.
//
// Without this seam, a coordinator that handles an English question
// hands off to a specialist that answers in zh (GLM default). See
// feedback_ai_output_locale.md regression 2026-06-02.

package basetool

import "context"

type localeCtxKeyT struct{}

var localeCtxKey = localeCtxKeyT{}

// WithLocale returns ctx augmented with locale. Empty locale = no-op,
// preserves back-compat with auto-spawn paths (investigator) that
// don't carry a UI locale.
func WithLocale(ctx context.Context, locale string) context.Context {
	if locale == "" {
		return ctx
	}
	return context.WithValue(ctx, localeCtxKey, locale)
}

// LocaleFromContext retrieves the active UI locale from ctx, if any.
// Returns "" when no locale was attached.
func LocaleFromContext(ctx context.Context) string {
	v, _ := ctx.Value(localeCtxKey).(string)
	return v
}
