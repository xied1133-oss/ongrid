package llm

import (
	"context"
	"sync"
	"time"
)

// InMemoryBudget is an MVP BudgetChecker with a single global per-UTC-day
// token cap. Good enough for private MVP (single tenant); switch to the
// MySQL/sqlite `usage_daily` table when the agent runs for real users.
//
// dailyLimit <= 0 means unlimited.
//
// The cap is GLOBAL (not per-user) on purpose — pivot collapses the
// tenant model to single-tenant, so there's no per-user billing surface yet.
// userID still flows through so the future per-user backend is a drop-in.
type InMemoryBudget struct {
	mu         sync.Mutex
	dailyLimit int            // tokens per UTC day; <=0 means unlimited
	used       map[string]int // key = "YYYY-MM-DD" (UTC)
	now        func() time.Time
}

// NewInMemoryBudget builds an InMemoryBudget with the given daily token cap.
// dailyLimit <= 0 means unlimited (the Checker then always accepts).
func NewInMemoryBudget(dailyLimit int) *InMemoryBudget {
	return &InMemoryBudget{
		dailyLimit: dailyLimit,
		used:       make(map[string]int),
		now:        time.Now,
	}
}

// Check returns ErrBudgetExceeded if estPromptTokens would push today's
// running total over dailyLimit. It never blocks or sleeps.
func (b *InMemoryBudget) Check(ctx context.Context, userID uint64, estPromptTokens int) error {
	_ = ctx
	_ = userID
	if b.dailyLimit <= 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	key := b.dayKey()
	if b.used[key]+estPromptTokens > b.dailyLimit {
		return ErrBudgetExceeded
	}
	return nil
}

// Record adds usage.TotalTokens to the current UTC-day bucket.
func (b *InMemoryBudget) Record(ctx context.Context, userID uint64, usage Usage) error {
	_ = ctx
	_ = userID
	b.mu.Lock()
	defer b.mu.Unlock()
	b.used[b.dayKey()] += usage.TotalTokens
	return nil
}

// Used returns the tokens consumed on the current UTC day. Exposed for
// tests; callers should treat it as a best-effort gauge.
func (b *InMemoryBudget) Used() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used[b.dayKey()]
}

func (b *InMemoryBudget) dayKey() string {
	return b.now().UTC().Format("2006-01-02")
}
