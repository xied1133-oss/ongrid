package device

import (
	"net"
	"strings"
)

// NetworkLinkEndpoints is the endpoint identity used to deduplicate one
// physical/logical line. Interface ID 0 means the interface is not known yet.
type NetworkLinkEndpoints struct {
	DeviceA    uint64
	InterfaceA uint64
	DeviceB    uint64
	InterfaceB uint64
}

// CanonicalizeNetworkLink orders endpoints deterministically. The database
// unique key then rejects both observations of the same line, regardless of
// which endpoint reported it first. Interface IDs break ties for parallel
// links between the same pair of devices.
func CanonicalizeNetworkLink(in NetworkLinkEndpoints) NetworkLinkEndpoints {
	if in.DeviceA > in.DeviceB || (in.DeviceA == in.DeviceB && in.InterfaceA > in.InterfaceB) {
		return NetworkLinkEndpoints{
			DeviceA: in.DeviceB, InterfaceA: in.InterfaceB,
			DeviceB: in.DeviceA, InterfaceB: in.InterfaceA,
		}
	}
	return in
}

// NetworkIdentity is the protocol-neutral identity evidence emitted by a
// discovery adapter. Empty fields are ignored when building candidates.
type NetworkIdentity struct {
	LLDPChassisID      string
	LLDPChassisSubtype string
	SNMPEngineID       string
	SNMPChassisID      string
	SNMPChassisSubtype string
	Vendor             string
	SerialNumber       string
	BridgeBaseMAC      string
	ManagementAddress  string
	SysName            string
}

// IdentityCandidate is a normalized external key. Strong is false for
// management addresses and names; those values may move or be reused.
type IdentityCandidate struct {
	Kind    string
	Subtype string
	Value   string
	Strong  bool
}

// NetworkIdentityCandidates returns candidates in descending identity
// strength. The order is part of the merge contract and is deliberately
// independent of which protocol produced the observation.
func NetworkIdentityCandidates(in NetworkIdentity) []IdentityCandidate {
	var out []IdentityCandidate
	if v := normalizeIdentityValue(in.LLDPChassisID, in.LLDPChassisSubtype); v != "" {
		out = append(out, IdentityCandidate{Kind: "lldp_chassis_id", Subtype: normalizeID(in.LLDPChassisSubtype), Value: v, Strong: true})
	}
	if v := normalizeID(in.SNMPEngineID); v != "" {
		out = append(out, IdentityCandidate{Kind: "snmp_engine_id", Value: v, Strong: true})
	}
	if v := normalizeIdentityValue(in.SNMPChassisID, in.SNMPChassisSubtype); v != "" {
		out = append(out, IdentityCandidate{Kind: "lldp_chassis_id", Subtype: normalizeID(in.SNMPChassisSubtype), Value: v, Strong: true})
	}
	if serial := normalizeID(in.SerialNumber); serial != "" {
		if vendor := normalizeID(in.Vendor); vendor != "" {
			out = append(out, IdentityCandidate{Kind: "vendor_serial", Value: vendor + ":" + serial, Strong: true})
		}
	}
	if v := normalizeMAC(in.BridgeBaseMAC); v != "" {
		out = append(out, IdentityCandidate{Kind: "bridge_base_mac", Value: v, Strong: true})
	}
	if v := normalizeAddress(in.ManagementAddress); v != "" {
		out = append(out, IdentityCandidate{Kind: "management_address", Value: v})
	}
	if v := normalizeID(in.SysName); v != "" {
		out = append(out, IdentityCandidate{Kind: "sys_name", Value: v})
	}
	return out
}

func normalizeIdentityValue(value, subtype string) string {
	if strings.EqualFold(strings.TrimSpace(subtype), "mac") {
		return normalizeMAC(value)
	}
	return normalizeID(value)
}

// IdentityMatch reports whether an existing identifier set can be safely
// merged with a new observation. A single weak match never authorizes a
// permanent merge. Strong identity conflicts are explicitly rejected.
type IdentityMatch struct {
	Matched     bool
	Conflict    bool
	StrongCount int
	WeakCount   int
	Reason      string
}

func MatchNetworkIdentity(observed NetworkIdentity, existing []IdentityCandidate) IdentityMatch {
	obs := NetworkIdentityCandidates(observed)
	strongMatches := 0
	weakMatches := 0
	strongConflict := false
	for _, candidate := range obs {
		found := false
		for _, prior := range existing {
			if candidate.Kind == prior.Kind && candidate.Subtype == prior.Subtype && candidate.Value == prior.Value {
				found = true
				break
			}
		}
		if found {
			if candidate.Strong {
				strongMatches++
			} else {
				weakMatches++
			}
		}
	}
	for _, prior := range existing {
		if !prior.Strong {
			continue
		}
		for _, candidate := range obs {
			if candidate.Strong && candidate.Kind == prior.Kind && candidate.Subtype == prior.Subtype && candidate.Value != prior.Value {
				strongConflict = true
			}
		}
	}
	if strongConflict {
		return IdentityMatch{Conflict: true, StrongCount: strongMatches, WeakCount: weakMatches, Reason: "strong identity conflict"}
	}
	if strongMatches > 0 {
		return IdentityMatch{Matched: true, StrongCount: strongMatches, WeakCount: weakMatches, Reason: "strong identity match"}
	}
	if weakMatches >= 2 {
		return IdentityMatch{Matched: true, WeakCount: weakMatches, Reason: "multiple weak identity matches"}
	}
	return IdentityMatch{WeakCount: weakMatches, Reason: "insufficient identity evidence"}
}

func normalizeID(v string) string { return strings.ToLower(strings.TrimSpace(v)) }

func normalizeMAC(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	v = strings.NewReplacer(":", "", "-", "", ".", "").Replace(v)
	if len(v) != 12 {
		return ""
	}
	for _, r := range v {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return ""
		}
	}
	parts := make([]string, 0, 6)
	for i := 0; i < len(v); i += 2 {
		parts = append(parts, v[i:i+2])
	}
	return strings.Join(parts, ":")
}

func normalizeAddress(v string) string {
	v = strings.TrimSpace(v)
	if ip := net.ParseIP(v); ip != nil {
		return ip.String()
	}
	return ""
}
