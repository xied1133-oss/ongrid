package decorators

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"golang.org/x/time/rate"

	"github.com/ongridio/ongrid/internal/manager/biz/aiops/tools/basetool"
)

// DefaultRateLimit is the per-(tool, user) call budget in calls per
// minute, applied by the default Limiter when no explicit rate is set.
// — RateLimitTool (per-user / per-tool QPS 限流).
//
// Picked empirically: 10/min lets the LLM correlate aggressively
// (correlate_incident burst-fires query_promql + query_logql) without
// letting a runaway loop melt Prom. Override by injecting a custom
// Limiter at construction.
const DefaultRateLimit = 10

// ErrRateLimited wraps the inner error path when the limiter rejects a
// call. errors.Is checks let the agent loop classify these as
// status=error (with a recognizable error message) in chat_tool_calls.
var ErrRateLimited = errors.New("tool rate limit exceeded")

// Limiter is the seam for rate limiting. The default implementation
// (NewTokenBucketLimiter) wraps golang.org/x/time/rate; tests use a
// no-op or a tightly controlled fake. the limiter
// keys on (tool name, user id) so two users don't share a budget and a
// single user can't starve their other tools.
type Limiter interface {
	// Allow returns true if the next call for (toolName, userID) is
	// admissible. Implementations MAY block (e.g. WaitN-style); the
	// default does not. user 0 falls under a shared "anonymous" bucket
	// — fine for in-process tests, locked down by upstream auth in prod.
	Allow(ctx context.Context, toolName string, userID uint64) bool
}

// NoopLimiter never rate-limits. Used in tests and when the operator
// disables the limiter via deps.Limiter = NoopLimiter{}.
type NoopLimiter struct{}

// Allow always returns true.
func (NoopLimiter) Allow(_ context.Context, _ string, _ uint64) bool { return true }

// TokenBucketLimiter is the production limiter. It maintains one
// rate.Limiter per (tool, user) tuple, lazily created. Burst defaults
// to the per-minute rate (so an idle user can fire RatePerMinute calls
// in one go before throttling kicks in).
type TokenBucketLimiter struct {
	ratePerMinute int
	burst         int

	mu       sync.Mutex
	limiters map[limiterKey]*rate.Limiter
}

type limiterKey struct {
	tool string
	user uint64
}

// NewTokenBucketLimiter returns a Limiter with the given per-minute
// rate. Zero or negative falls back to DefaultRateLimit. Burst equals
// rate so the first call of an idle window is always admitted.
func NewTokenBucketLimiter(ratePerMinute int) *TokenBucketLimiter {
	if ratePerMinute <= 0 {
		ratePerMinute = DefaultRateLimit
	}
	return &TokenBucketLimiter{
		ratePerMinute: ratePerMinute,
		burst:         ratePerMinute,
		limiters:      map[limiterKey]*rate.Limiter{},
	}
}

// Allow checks the per-(tool, user) bucket and consumes a token if
// available. Non-blocking — the decorator turns a false return into
// ErrRateLimited so the agent loop sees a fast failure rather than a
// stalled tool call.
func (l *TokenBucketLimiter) Allow(_ context.Context, toolName string, userID uint64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := limiterKey{tool: toolName, user: userID}
	lim, ok := l.limiters[k]
	if !ok {
		// rate.Every(time / N) gives N events per time window.
		// rate.Limit is in events/sec; convert from per-minute.
		lim = rate.NewLimiter(rate.Limit(float64(l.ratePerMinute)/60.0), l.burst)
		l.limiters[k] = lim
	}
	return lim.Allow()
}

// RateLimitTool wraps inner so InvokableRun first consults the Limiter
// keyed on (tool name, user id from InvokeOption). —
// RateLimitTool 装饰器层。
type RateLimitTool struct {
	inner   basetool.BaseTool
	limiter Limiter
}

// WithRateLimit returns inner wrapped against limiter. A nil limiter is
// a pass-through (so production may disable rate limiting at config
// time without conditional construction at every call site).
func WithRateLimit(inner basetool.BaseTool, limiter Limiter) basetool.BaseTool {
	if limiter == nil {
		return inner
	}
	return &RateLimitTool{inner: inner, limiter: limiter}
}

// Info passes through — schema is invariant under rate limiting.
func (r *RateLimitTool) Info(ctx context.Context) (*basetool.ToolInfo, error) {
	return r.inner.Info(ctx)
}

// InvokableRun checks the limiter first; on rejection returns
// ErrRateLimited without invoking the inner tool.
func (r *RateLimitTool) InvokableRun(ctx context.Context, argsJSON string, opts ...basetool.InvokeOption) (string, error) {
	resolved := basetool.ResolveOptions(opts)

	name := ""
	if info, err := r.inner.Info(ctx); err == nil && info != nil {
		name = info.Name
	}

	if !r.limiter.Allow(ctx, name, resolved.UserID) {
		return "", fmt.Errorf("%w: tool=%s user=%d", ErrRateLimited, name, resolved.UserID)
	}
	return r.inner.InvokableRun(ctx, argsJSON, opts...)
}
