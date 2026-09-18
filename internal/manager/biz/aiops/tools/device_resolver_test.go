package tools

import (
	"context"
	"testing"
	"time"
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

// TestPickBestHostEdge guards the live-edge preference added to rule 1.
// The production regression it encodes: device 2 carried TWO type=host
// junctions — a stale credential edge (id=2, offline, never connected)
// whose junction row had the LARGER id, plus the real running agent
// (id=4, online, fresh last_seen). The legacy blind id-DESC pick returned
// the dead edge 2, so every host command (get_host_load / processes /
// host_bash) failed with "edge not online" while the device page — reading
// devices.online, kept alive by edge 4's heartbeat — showed it online.
// pickBestHostEdge must return the online edge 4 instead.
func TestPickBestHostEdge(t *testing.T) {
	base := time.Date(2026, 9, 9, 8, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		cands []edgeHostCandidate
		want  uint64
	}{
		{
			name: "online edge beats dead edge with larger junction id",
			cands: []edgeHostCandidate{
				{edgeID: 2, junctionID: 100, online: false},
				{edgeID: 4, junctionID: 50, online: true, lastSeen: base},
			},
			want: 4,
		},
		{
			name: "freshest last_seen wins among online",
			cands: []edgeHostCandidate{
				{edgeID: 7, junctionID: 1, online: true, lastSeen: base.Add(-time.Hour)},
				{edgeID: 9, junctionID: 2, online: true, lastSeen: base},
			},
			want: 9,
		},
		{
			name: "equal last_seen tie-breaks on larger junction id",
			cands: []edgeHostCandidate{
				{edgeID: 11, junctionID: 3, online: true, lastSeen: base},
				{edgeID: 13, junctionID: 8, online: true, lastSeen: base},
			},
			want: 13,
		},
		{
			name: "no online candidate returns 0 so caller falls back to legacy",
			cands: []edgeHostCandidate{
				{edgeID: 2, junctionID: 100, online: false, lastSeen: base},
				{edgeID: 3, junctionID: 90, online: false},
			},
			want: 0,
		},
		{
			name:  "empty returns 0",
			cands: nil,
			want:  0,
		},
		{
			name: "offline with freshest last_seen is still ignored",
			cands: []edgeHostCandidate{
				{edgeID: 20, junctionID: 9, online: false, lastSeen: base.Add(time.Hour)},
				{edgeID: 21, junctionID: 1, online: true, lastSeen: base.Add(-time.Hour)},
			},
			want: 21,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickBestHostEdge(tc.cands); got != tc.want {
				t.Errorf("pickBestHostEdge() = %d, want %d", got, tc.want)
			}
		})
	}
}
