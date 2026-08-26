package tunnel

import "time"

const (
	// MethodStartPacketCapture starts an edge-local AF_PACKET capture in the
	// background. The edge returns immediately with the accepted task state.
	MethodStartPacketCapture = "packet_capture.start"
	// MethodGetPacketCapture returns the edge-local status for one capture
	// task. Raw PCAP file paths never cross this wire response.
	MethodGetPacketCapture = "packet_capture.get"
	// MethodReadPacketCapture returns the raw PCAP bytes for a completed
	// edge-local capture. The edge still never returns a filesystem path; this
	// is a manager-only follow-up used before private parser ingestion.
	MethodReadPacketCapture = "packet_capture.read"
	// MethodCancelPacketCapture requests cancellation of a queued or running
	// edge-local capture and discards its partial output.
	MethodCancelPacketCapture = "packet_capture.cancel"
	// MethodStopPacketCapture gracefully stops a running capture while keeping
	// its valid PCAP prefix available for manager upload and analysis.
	MethodStopPacketCapture = "packet_capture.stop"
)

// PacketCaptureStartRequest is the manager-to-edge request for a bounded
// packet capture. The manager owns CaptureID; the edge never accepts an output
// path from callers.
type PacketCaptureStartRequest struct {
	CaptureID        string `json:"capture_id"`
	Interface        string `json:"interface"`
	NetworkNamespace string `json:"network_namespace,omitempty"`
	Filter           string `json:"filter,omitempty"`
	DurationSeconds  int    `json:"duration_seconds,omitempty"`
	MaxBytes         int64  `json:"max_bytes,omitempty"`
	MaxPackets       int    `json:"max_packets,omitempty"`
	Snaplen          int    `json:"snaplen,omitempty"`
	Promiscuous      bool   `json:"promiscuous,omitempty"`
	// StartAt is manager-coordinated UTC time. Edges schedule locally and still
	// report their actual StartedAt so analysis can surface clock uncertainty.
	StartAt *time.Time `json:"start_at,omitempty"`
}

// PacketCaptureGetRequest identifies one edge-local capture task.
type PacketCaptureGetRequest struct {
	CaptureID string `json:"capture_id"`
}

type PacketCaptureCancelRequest struct {
	CaptureID string `json:"capture_id"`
}

// PacketCaptureStopRequest identifies a capture that should be stopped and
// retained. It is deliberately separate from cancellation so callers cannot
// accidentally discard evidence when they mean to finish early.
type PacketCaptureStopRequest struct {
	CaptureID string `json:"capture_id"`
}

// PacketCaptureReadRequest identifies one completed capture and caps how many
// bytes the edge may return. Manager must keep this method internal-only.
type PacketCaptureReadRequest struct {
	CaptureID string `json:"capture_id"`
	MaxBytes  uint64 `json:"max_bytes,omitempty"`
}

// PacketCaptureReadResponse carries raw PCAP bytes without revealing the
// edge-local path. Data is base64 because tunnel calls are JSON encoded today.
type PacketCaptureReadResponse struct {
	CaptureID  string `json:"capture_id"`
	SizeBytes  uint64 `json:"size_bytes"`
	SHA256Hex  string `json:"sha256_hex"`
	DataBase64 string `json:"data_base64"`
}

// PacketCaptureTask is the wire-safe edge-owned task view. Path and other
// local filesystem details are deliberately omitted.
type PacketCaptureTask struct {
	ID         string              `json:"id"`
	Request    PacketCaptureWireIn `json:"request"`
	State      string              `json:"state"`
	Result     PacketCaptureResult `json:"result,omitempty"`
	Error      string              `json:"error,omitempty"`
	CreatedAt  time.Time           `json:"created_at"`
	StartedAt  *time.Time          `json:"started_at,omitempty"`
	FinishedAt *time.Time          `json:"finished_at,omitempty"`
}

// PacketCaptureWireIn echoes the normalized edge request without any
// manager-only metadata.
type PacketCaptureWireIn struct {
	CaptureID        string     `json:"capture_id"`
	Interface        string     `json:"interface"`
	NetworkNamespace string     `json:"network_namespace,omitempty"`
	Filter           string     `json:"filter,omitempty"`
	DurationSeconds  int        `json:"duration_seconds"`
	MaxBytes         int64      `json:"max_bytes"`
	MaxPackets       int        `json:"max_packets"`
	Snaplen          int        `json:"snaplen"`
	Promiscuous      bool       `json:"promiscuous"`
	StartAt          *time.Time `json:"start_at,omitempty"`
}

// PacketCaptureResult describes the completed capture. No file path is
// included; upload/viewing is a manager-managed follow-up.
type PacketCaptureResult struct {
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	Packets       int       `json:"packets"`
	PayloadBytes  int64     `json:"payload_bytes"`
	FileBytes     int64     `json:"file_bytes"`
	StopReason    string    `json:"stop_reason"`
	InterfaceName string    `json:"interface"`
	LivePreview   []string  `json:"live_preview,omitempty"`
}
