package builtin

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/ongridio/ongrid/internal/skill"
)

const (
	CapturePCAPSkillKey      = "capture_pcap"
	GetPacketCaptureSkillKey = "get_packet_capture"
)

func init() {
	skill.Register(&CapturePCAP{})
	skill.Register(&GetPacketCapture{})
}

// CapturePCAP declares the packet capture API. Edge dispatch owns execution:
// the manager imports this package for the shared catalogue/LLM schema while
// the target edge owns its local capture service and file permissions.
type CapturePCAP struct{}

func (CapturePCAP) Metadata() skill.Metadata {
	return skill.Metadata{
		Key:         CapturePCAPSkillKey,
		Name:        "Capture packets",
		Description: "Start a bounded PCAP capture on a Linux Edge network interface. This reads packet payloads and must be requested explicitly by an authorized operator. Returns a background task id; use get_packet_capture to query status. Filter grammar supports tcp, udp, icmp, icmp6, host <IP>, port <N>, joined by and.",
		Class:       skill.ClassMutating,
		Scope:       skill.ScopeHost,
		Category:    "network",
		Params: skill.ParamSchema{
			{Name: "interface", Param: skill.Param{Type: "string", Required: true, Desc: "Interface name on the Edge host, for example eth0 or ens5. Do not use any."}},
			{Name: "filter", Param: skill.Param{Type: "string", Desc: "Optional filter, for example tcp and host 10.0.0.8 and port 443."}},
			{Name: "duration_seconds", Param: skill.Param{Type: "int", Default: 30, Desc: "Capture duration in seconds, maximum 300."}},
			{Name: "max_bytes", Param: skill.Param{Type: "int", Default: 67108864, Desc: "Maximum captured payload bytes, maximum 268435456."}},
			{Name: "max_packets", Param: skill.Param{Type: "int", Default: 100000, Desc: "Maximum packet count, maximum 500000."}},
			{Name: "snaplen", Param: skill.Param{Type: "int", Default: 1514, Desc: "Maximum bytes retained per packet, maximum 65535."}},
			{Name: "promiscuous", Param: skill.Param{Type: "bool", Default: false, Desc: "Enable promiscuous mode. Requires CAP_NET_ADMIN on the Edge host."}},
		},
		ResultPreview: "{id, state, created_at}",
	}
}

func (CapturePCAP) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	return nil, errors.New("capture_pcap: must execute on an edge agent")
}

// GetPacketCapture reads an edge-local task state. The manager uses the same
// declaration to expose the status lookup to chat and workflow nodes.
type GetPacketCapture struct{}

func (GetPacketCapture) Metadata() skill.Metadata {
	return skill.Metadata{
		Key:         GetPacketCaptureSkillKey,
		Name:        "Get packet capture",
		Description: "Query the status, stop reason, file size, and error details for a PCAP capture task on a Linux Edge.",
		Class:       skill.ClassSafe,
		Scope:       skill.ScopeHost,
		Category:    "network",
		Params: skill.ParamSchema{
			{Name: "capture_id", Param: skill.Param{Type: "string", Required: true, Desc: "Packet capture task id."}},
		},
		ResultPreview: "{id, state, result, error?}",
	}
}

func (GetPacketCapture) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	return nil, errors.New("get_packet_capture: must execute on an edge agent")
}
