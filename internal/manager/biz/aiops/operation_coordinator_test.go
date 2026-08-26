package aiops

import (
	"context"
	"testing"
)

type testReconciler struct{ calls int }

func (*testReconciler) Kind() string                      { return "test" }
func (r *testReconciler) Reconcile(context.Context) error { r.calls++; return nil }

func TestOperationCoordinatorRejectsDuplicateKinds(t *testing.T) {
	c := NewOperationCoordinator(0, nil)
	first := &testReconciler{}
	if err := c.Register(first); err != nil {
		t.Fatalf("Register first: %v", err)
	}
	if err := c.Register(&testReconciler{}); err == nil {
		t.Fatal("duplicate reconciler accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if first.calls != 1 {
		t.Fatalf("initial reconcile calls=%d, want 1", first.calls)
	}
}
