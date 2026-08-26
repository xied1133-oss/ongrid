package imbridge

import (
	"fmt"
	"testing"
)

func TestDedupSetSeenOrAdd(t *testing.T) {
	d := newDedupSet(8)
	if d.seenOrAdd("a") {
		t.Fatal("first 'a' should be new (false)")
	}
	if !d.seenOrAdd("a") {
		t.Fatal("second 'a' should be a duplicate (true)")
	}
	if d.seenOrAdd("b") {
		t.Fatal("'b' should be new")
	}
	if !d.seenOrAdd("b") {
		t.Fatal("'b' repeat should be a duplicate")
	}
}

// Keys must survive at least one generation rotation, and fall out after the
// set has churned well past its capacity (bounded memory).
func TestDedupSetBounded(t *testing.T) {
	d := newDedupSet(2)
	d.seenOrAdd("old")
	// One full generation back: "old" is still remembered.
	d.seenOrAdd("g1")
	d.seenOrAdd("g2") // rotates: prev={old,g1}, cur={g2}
	if !d.seenOrAdd("old") {
		t.Error("'old' should still be seen one generation back")
	}
	// Churn well past 2*cap distinct keys → "old" drops out of both gens.
	for i := 0; i < 12; i++ {
		d.seenOrAdd(fmt.Sprintf("k%d", i))
	}
	if d.seenOrAdd("old") {
		t.Error("'old' should have been evicted after heavy churn (bounded set)")
	}
}
