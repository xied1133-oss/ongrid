package device

import "testing"

func TestNetworkIdentityCandidates_NormalizesStrongAndWeakEvidence(t *testing.T) {
	got := NetworkIdentityCandidates(NetworkIdentity{
		LLDPChassisID:      "  AA-BB-CC-DD-EE-FF ",
		LLDPChassisSubtype: "MAC",
		SNMPEngineID:       " Engine-01 ",
		Vendor:             " Acme ",
		SerialNumber:       " SN-7 ",
		BridgeBaseMAC:      "aa:bb:cc:dd:ee:ff",
		ManagementAddress:  "2001:db8::1",
		SysName:            " Core-01 ",
	})
	if len(got) != 6 {
		t.Fatalf("candidate count = %d, want 6: %#v", len(got), got)
	}
	if got[0].Kind != "lldp_chassis_id" || got[0].Value != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("LLDP candidate = %#v", got[0])
	}
	if got[3].Value != "aa:bb:cc:dd:ee:ff" || !got[3].Strong {
		t.Fatalf("MAC candidate = %#v", got[3])
	}
	if got[4].Value != "2001:db8::1" || got[4].Strong {
		t.Fatalf("address candidate = %#v", got[4])
	}
}

func TestMatchNetworkIdentity_StrongMatchWins(t *testing.T) {
	observed := NetworkIdentity{
		LLDPChassisID:     "chassis-1",
		ManagementAddress: "192.0.2.10",
		SysName:           "switch-a",
	}
	existing := NetworkIdentityCandidates(NetworkIdentity{SNMPChassisID: "chassis-1", SNMPChassisSubtype: ""})
	got := MatchNetworkIdentity(observed, existing)
	if !got.Matched || got.Conflict || got.StrongCount != 1 {
		t.Fatalf("match = %#v, want a strong match", got)
	}
}

func TestMatchNetworkIdentity_WeakEvidenceNeedsTwoFields(t *testing.T) {
	one := MatchNetworkIdentity(
		NetworkIdentity{ManagementAddress: "192.0.2.10", SysName: "switch-a"},
		NetworkIdentityCandidates(NetworkIdentity{ManagementAddress: "192.0.2.10"}),
	)
	if one.Matched || one.Conflict || one.WeakCount != 1 {
		t.Fatalf("single weak match = %#v, want no merge", one)
	}
	two := MatchNetworkIdentity(
		NetworkIdentity{ManagementAddress: "192.0.2.10", SysName: "switch-a"},
		NetworkIdentityCandidates(NetworkIdentity{ManagementAddress: "192.0.2.10", SysName: "switch-a"}),
	)
	if !two.Matched || two.WeakCount != 2 {
		t.Fatalf("two weak matches = %#v, want merge", two)
	}
}

func TestMatchNetworkIdentity_StrongConflictDoesNotMerge(t *testing.T) {
	got := MatchNetworkIdentity(
		NetworkIdentity{SNMPEngineID: "engine-new", BridgeBaseMAC: "aa:bb:cc:dd:ee:ff"},
		NetworkIdentityCandidates(NetworkIdentity{SNMPEngineID: "engine-old", BridgeBaseMAC: "aa:bb:cc:dd:ee:ff"}),
	)
	if !got.Conflict || got.Matched {
		t.Fatalf("conflict = %#v, want conflict without merge", got)
	}
}

func TestNetworkIdentityCandidates_RejectsInvalidAddressAndMAC(t *testing.T) {
	got := NetworkIdentityCandidates(NetworkIdentity{
		BridgeBaseMAC:     "not-a-mac",
		ManagementAddress: "not-an-ip",
	})
	if len(got) != 0 {
		t.Fatalf("invalid candidates = %#v, want none", got)
	}
}

func TestCanonicalizeNetworkLink_CollapsesReverseObservation(t *testing.T) {
	forward := CanonicalizeNetworkLink(NetworkLinkEndpoints{DeviceA: 8, InterfaceA: 42, DeviceB: 1, InterfaceB: 11})
	reverse := CanonicalizeNetworkLink(NetworkLinkEndpoints{DeviceA: 1, InterfaceA: 11, DeviceB: 8, InterfaceB: 42})
	if forward != reverse {
		t.Fatalf("forward=%#v reverse=%#v, want same canonical key", forward, reverse)
	}
	if forward.DeviceA != 1 || forward.InterfaceA != 11 || forward.DeviceB != 8 || forward.InterfaceB != 42 {
		t.Fatalf("canonical=%#v, want stable endpoint order", forward)
	}
}

func TestCanonicalizeNetworkLink_KeepsParallelInterfacesDistinct(t *testing.T) {
	a := CanonicalizeNetworkLink(NetworkLinkEndpoints{DeviceA: 1, InterfaceA: 10, DeviceB: 2, InterfaceB: 20})
	b := CanonicalizeNetworkLink(NetworkLinkEndpoints{DeviceA: 1, InterfaceA: 11, DeviceB: 2, InterfaceB: 21})
	if a == b {
		t.Fatalf("parallel links collapsed: a=%#v b=%#v", a, b)
	}
}
