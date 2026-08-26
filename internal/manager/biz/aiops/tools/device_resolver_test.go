package tools

import (
	"context"
	"testing"
)

// TestDeviceResolver_ZeroDeviceID exercises the early return for
// deviceID==0 — guards against accidentally walking through to the
// junction lookup when the LLM omits device_id.
func TestDeviceResolver_ZeroDeviceID(t *testing.T) {
	r := NewDeviceResolver(nil, nil)
	got, err := r.ResolveEdgeID(context.Background(), 0)
	if err != nil {
		t.Fatalf("ResolveEdgeID(0): unexpected err %v", err)
	}
	if got != 0 {
		t.Errorf("ResolveEdgeID(0) = %d, want 0", got)
	}
}

// TestDeviceResolver_NilDependencies returns 0 when both repos are
// nil. The resolver MUST stay nil-safe so downstream tools surface a
// clean "no host link" message rather than panicking.
func TestDeviceResolver_NilDependencies(t *testing.T) {
	r := NewDeviceResolver(nil, nil)
	got, err := r.ResolveEdgeID(context.Background(), 42)
	if err != nil {
		t.Fatalf("ResolveEdgeID(42, nil deps): unexpected err %v", err)
	}
	if got != 0 {
		t.Errorf("ResolveEdgeID(42, nil deps) = %d, want 0", got)
	}
}

// TestDeviceResolver_AdaptedToHostFiles confirms the adapter shim used
// by the three host_files BaseTools delegates to DeviceResolver and
// returns 0 (no error) for an unmapped id. Production wiring goes
// through this adapter; the adapter's correctness gates every
// host_files tool path.
func TestDeviceResolver_AdaptedToHostFiles(t *testing.T) {
	a := deviceResolverAdapter{inner: NewDeviceResolver(nil, nil)}
	got, err := a.LookupHostEdge(context.Background(), 7)
	if err != nil {
		t.Fatalf("LookupHostEdge: unexpected err %v", err)
	}
	if got != 0 {
		t.Errorf("LookupHostEdge = %d, want 0", got)
	}

	// Adapter with nil inner must also be nil-safe (defensive — no
	// production caller passes nil today, but a future test
	// constructing an adapter directly might).
	a2 := deviceResolverAdapter{inner: nil}
	got, err = a2.LookupHostEdge(context.Background(), 7)
	if err != nil {
		t.Fatalf("LookupHostEdge(nil inner): unexpected err %v", err)
	}
	if got != 0 {
		t.Errorf("LookupHostEdge(nil inner) = %d, want 0", got)
	}
}
