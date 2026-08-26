package tools

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestRunBatch_OrderPreserved — fan-out completes in arbitrary order but
// runBatch must write back into the result slice at the original input
// index. Encode that by sleeping inversely to position so ids[0] is the
// last to complete; if we returned in completion order, results[0]
// would carry id 9 instead of id 0.
func TestRunBatch_OrderPreserved(t *testing.T) {
	ids := []uint64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	out := runBatch(context.Background(), ids, func(_ context.Context, id uint64) uint64 {
		// Larger id → finishes earlier.
		time.Sleep(time.Duration(10-int(id)) * time.Millisecond)
		return id
	})
	if len(out) != len(ids) {
		t.Fatalf("len = %d, want %d", len(out), len(ids))
	}
	for i, v := range out {
		if v != ids[i] {
			t.Errorf("results[%d] = %d, want %d", i, v, ids[i])
		}
	}
}

// TestRunBatch_ConcurrencyCap — 10 ids, each blocking long enough that
// the peak in-flight counter must observe batchConcurrency. We check
// peak ≤ batchConcurrency to prove the semaphore actually gates.
func TestRunBatch_ConcurrencyCap(t *testing.T) {
	var inflight atomic.Int64
	var peak atomic.Int64
	bumpPeak := func() {
		now := inflight.Add(1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
	}

	ids := make([]int, 10)
	for i := range ids {
		ids[i] = i
	}
	runBatch(context.Background(), ids, func(_ context.Context, _ int) int {
		bumpPeak()
		defer inflight.Add(-1)
		// Long enough that all goroutines pile up if the semaphore is broken.
		time.Sleep(50 * time.Millisecond)
		return 0
	})
	if peak.Load() > int64(batchConcurrency) {
		t.Errorf("peak in-flight = %d, want ≤ %d", peak.Load(), batchConcurrency)
	}
	if peak.Load() == 0 {
		t.Errorf("peak counter never bumped — fan-out didn't run")
	}
}

// TestRunBatch_EmptyIDs — empty input is a no-op, no goroutines spawned,
// returned slice has len 0.
func TestRunBatch_EmptyIDs(t *testing.T) {
	called := false
	out := runBatch(context.Background(), []uint64{}, func(_ context.Context, _ uint64) int {
		called = true
		return 0
	})
	if called {
		t.Errorf("fn invoked on empty ids")
	}
	if len(out) != 0 {
		t.Errorf("len(out) = %d, want 0", len(out))
	}
}

// TestRunBatch_FanOutActuallyParallel — sanity that runBatch isn't
// accidentally serialised. With 4 ids each sleeping 50ms and
// batchConcurrency=4, total wall time should be < 4*50=200ms (well
// below the serial sum). Use 150ms as a generous upper bound to keep
// the test stable under load.
func TestRunBatch_FanOutActuallyParallel(t *testing.T) {
	ids := []int{1, 2, 3, 4}
	start := time.Now()
	runBatch(context.Background(), ids, func(_ context.Context, _ int) int {
		time.Sleep(50 * time.Millisecond)
		return 0
	})
	elapsed := time.Since(start)
	if elapsed > 150*time.Millisecond {
		t.Errorf("fan-out elapsed %v, want < 150ms (would be 200ms+ if serialised)", elapsed)
	}
}

// TestRunBatch_RaceFree — runs many concurrent runBatch calls; the
// `-race` flag exercise here is the actual assertion. Helper itself
// can't deadlock if waitgroup is balanced.
func TestRunBatch_RaceFree(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runBatch(context.Background(), []int{1, 2, 3, 4, 5}, func(_ context.Context, x int) int {
				return x * 2
			})
		}()
	}
	wg.Wait()
}

// TestValidateBatchIDs_Empty — empty slice is rejected with the field
// label baked into the message so the LLM gets actionable feedback.
func TestValidateBatchIDs_Empty(t *testing.T) {
	err := validateBatchIDs[uint64]("device_ids", nil)
	if err == nil {
		t.Fatalf("expected error for nil ids")
	}
	if !strings.Contains(err.Error(), "device_ids") {
		t.Errorf("error should mention label: %v", err)
	}
}

// TestValidateBatchIDs_TooMany — > batchMaxIDs is rejected with a clear
// over-cap message.
func TestValidateBatchIDs_TooMany(t *testing.T) {
	ids := make([]uint64, batchMaxIDs+1)
	err := validateBatchIDs("device_ids", ids)
	if err == nil {
		t.Fatalf("expected error for over-cap ids")
	}
	if !strings.Contains(err.Error(), "too many") {
		t.Errorf("error should say too many: %v", err)
	}
}

// TestValidateBatchIDs_HappyPath — 1..batchMaxIDs is accepted.
func TestValidateBatchIDs_HappyPath(t *testing.T) {
	for _, n := range []int{1, batchMaxIDs / 2, batchMaxIDs} {
		ids := make([]uint64, n)
		if err := validateBatchIDs("ids", ids); err != nil {
			t.Errorf("len=%d: unexpected err %v", n, err)
		}
	}
}
