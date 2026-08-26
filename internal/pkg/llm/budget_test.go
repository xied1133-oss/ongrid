package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInMemoryBudgetUnlimited(t *testing.T) {
	b := NewInMemoryBudget(0)
	if err := b.Check(context.Background(), 1, 10_000_000); err != nil {
		t.Fatalf("Check on unlimited budget = %v, want nil", err)
	}
	if err := b.Record(context.Background(), 1, Usage{TotalTokens: 9999}); err != nil {
		t.Fatalf("Record: %v", err)
	}
}

func TestInMemoryBudgetCap(t *testing.T) {
	b := NewInMemoryBudget(100)
	ctx := context.Background()

	if err := b.Check(ctx, 1, 50); err != nil {
		t.Fatalf("Check 50 with empty bucket = %v, want nil", err)
	}
	if err := b.Record(ctx, 1, Usage{TotalTokens: 60}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	// used=60; asking for 50 more -> 110 > 100 -> reject.
	if err := b.Check(ctx, 1, 50); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("Check over cap = %v, want ErrBudgetExceeded", err)
	}
	// used=60; asking for 30 more -> 90 <= 100 -> accept.
	if err := b.Check(ctx, 1, 30); err != nil {
		t.Fatalf("Check still-under cap = %v, want nil", err)
	}
}

func TestInMemoryBudgetDayRollover(t *testing.T) {
	b := NewInMemoryBudget(100)
	now := time.Date(2026, 4, 23, 23, 59, 0, 0, time.UTC)
	b.now = func() time.Time { return now }

	_ = b.Record(context.Background(), 1, Usage{TotalTokens: 95})
	if err := b.Check(context.Background(), 1, 10); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("pre-rollover Check = %v, want ErrBudgetExceeded", err)
	}

	// Advance clock to next UTC day.
	now = now.Add(2 * time.Minute)
	if err := b.Check(context.Background(), 1, 10); err != nil {
		t.Fatalf("post-rollover Check = %v, want nil (new day)", err)
	}
	if used := b.Used(); used != 0 {
		t.Errorf("new-day Used = %d, want 0", used)
	}
}
