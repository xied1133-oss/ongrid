package biz

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ongridio/ongrid/internal/edgeagent/packetcapture"
	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

func (a *Agent) startPacketCapture(raw json.RawMessage) ([]byte, error) {
	var in tunnel.PacketCaptureStartRequest
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("capture_pcap: decode parameters: %w", err)
	}
	task, err := a.startPacketCaptureTask(context.Background(), in)
	if err != nil {
		return nil, err
	}
	return jsonEncode(task, nil)
}

func (a *Agent) getPacketCapture(raw json.RawMessage) ([]byte, error) {
	var in tunnel.PacketCaptureGetRequest
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("get_packet_capture: decode parameters: %w", err)
	}
	task, err := a.getPacketCaptureTask(in)
	if err != nil {
		return nil, err
	}
	return jsonEncode(task, nil)
}

func (a *Agent) handleStartPacketCapture(_ context.Context, body []byte) ([]byte, error) {
	var in tunnel.PacketCaptureStartRequest
	if err := jsonDecode(body, &in); err != nil {
		return nil, err
	}
	task, err := a.startPacketCaptureTask(context.Background(), in)
	if err != nil {
		return nil, err
	}
	return jsonEncode(task, nil)
}

func (a *Agent) handleGetPacketCapture(_ context.Context, body []byte) ([]byte, error) {
	var in tunnel.PacketCaptureGetRequest
	if err := jsonDecode(body, &in); err != nil {
		return nil, err
	}
	task, err := a.getPacketCaptureTask(in)
	if err != nil {
		return nil, err
	}
	return jsonEncode(task, nil)
}

func (a *Agent) handleCancelPacketCapture(_ context.Context, body []byte) ([]byte, error) {
	var in tunnel.PacketCaptureCancelRequest
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, err
	}
	if a.packetCaptureErr != nil {
		return nil, fmt.Errorf("cancel_packet_capture unavailable: %w", a.packetCaptureErr)
	}
	if a.packetCapture == nil {
		return nil, fmt.Errorf("cancel_packet_capture unavailable")
	}
	task, err := a.packetCapture.Cancel(strings.TrimSpace(in.CaptureID))
	if err != nil {
		return nil, err
	}
	return json.Marshal(toTunnelPacketCaptureTask(task))
}

func (a *Agent) handleStopPacketCapture(_ context.Context, body []byte) ([]byte, error) {
	var in tunnel.PacketCaptureStopRequest
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, err
	}
	if a.packetCaptureErr != nil {
		return nil, fmt.Errorf("stop_packet_capture unavailable: %w", a.packetCaptureErr)
	}
	if a.packetCapture == nil {
		return nil, fmt.Errorf("stop_packet_capture unavailable")
	}
	task, err := a.packetCapture.Stop(strings.TrimSpace(in.CaptureID))
	if err != nil {
		return nil, err
	}
	return json.Marshal(toTunnelPacketCaptureTask(task))
}

func (a *Agent) handleReadPacketCapture(_ context.Context, body []byte) ([]byte, error) {
	var in tunnel.PacketCaptureReadRequest
	if err := jsonDecode(body, &in); err != nil {
		return nil, err
	}
	raw, err := a.readPacketCaptureRaw(in)
	if err != nil {
		return nil, err
	}
	return jsonEncode(raw, nil)
}

func (a *Agent) startPacketCaptureTask(_ context.Context, in tunnel.PacketCaptureStartRequest) (tunnel.PacketCaptureTask, error) {
	if a.packetCaptureErr != nil {
		return tunnel.PacketCaptureTask{}, fmt.Errorf("capture_pcap unavailable: %w", a.packetCaptureErr)
	}
	if a.packetCapture == nil {
		return tunnel.PacketCaptureTask{}, fmt.Errorf("capture_pcap unavailable: packet capture service is not configured")
	}
	task, err := a.packetCapture.Start(packetcapture.Request{
		CaptureID:        strings.TrimSpace(in.CaptureID),
		Interface:        strings.TrimSpace(in.Interface),
		NetworkNamespace: strings.TrimSpace(in.NetworkNamespace),
		Filter:           strings.TrimSpace(in.Filter),
		Duration:         time.Duration(in.DurationSeconds) * time.Second,
		MaxBytes:         in.MaxBytes,
		MaxPackets:       in.MaxPackets,
		Snaplen:          in.Snaplen,
		Promiscuous:      in.Promiscuous,
		StartAt:          in.StartAt,
	})
	if err != nil {
		return tunnel.PacketCaptureTask{}, err
	}
	return toTunnelPacketCaptureTask(task), nil
}

func (a *Agent) getPacketCaptureTask(in tunnel.PacketCaptureGetRequest) (tunnel.PacketCaptureTask, error) {
	if a.packetCaptureErr != nil {
		return tunnel.PacketCaptureTask{}, fmt.Errorf("get_packet_capture unavailable: %w", a.packetCaptureErr)
	}
	if a.packetCapture == nil {
		return tunnel.PacketCaptureTask{}, fmt.Errorf("get_packet_capture unavailable: packet capture service is not configured")
	}
	task, ok := a.packetCapture.Get(strings.TrimSpace(in.CaptureID))
	if !ok {
		return tunnel.PacketCaptureTask{}, fmt.Errorf("get_packet_capture: capture %q not found", in.CaptureID)
	}
	return toTunnelPacketCaptureTask(task), nil
}

func (a *Agent) readPacketCaptureRaw(in tunnel.PacketCaptureReadRequest) (tunnel.PacketCaptureReadResponse, error) {
	if a.packetCaptureErr != nil {
		return tunnel.PacketCaptureReadResponse{}, fmt.Errorf("read_packet_capture unavailable: %w", a.packetCaptureErr)
	}
	if a.packetCapture == nil {
		return tunnel.PacketCaptureReadResponse{}, fmt.Errorf("read_packet_capture unavailable: packet capture service is not configured")
	}
	captureID := strings.TrimSpace(in.CaptureID)
	raw, err := a.packetCapture.Read(captureID, in.MaxBytes)
	if err != nil {
		return tunnel.PacketCaptureReadResponse{}, err
	}
	return tunnel.PacketCaptureReadResponse{
		CaptureID:  captureID,
		SizeBytes:  raw.SizeBytes,
		SHA256Hex:  raw.SHA256Hex,
		DataBase64: base64.StdEncoding.EncodeToString(raw.Data),
	}, nil
}

func toTunnelPacketCaptureTask(task packetcapture.Task) tunnel.PacketCaptureTask {
	return tunnel.PacketCaptureTask{
		ID: task.ID,
		Request: tunnel.PacketCaptureWireIn{
			CaptureID:        task.Request.CaptureID,
			Interface:        task.Request.Interface,
			NetworkNamespace: task.Request.NetworkNamespace,
			Filter:           task.Request.Filter,
			DurationSeconds:  int(task.Request.Duration.Seconds()),
			MaxBytes:         task.Request.MaxBytes,
			MaxPackets:       task.Request.MaxPackets,
			Snaplen:          task.Request.Snaplen,
			Promiscuous:      task.Request.Promiscuous,
			StartAt:          task.Request.StartAt,
		},
		State: task.State,
		Result: tunnel.PacketCaptureResult{
			StartedAt:     task.Result.StartedAt,
			FinishedAt:    task.Result.FinishedAt,
			Packets:       task.Result.Packets,
			PayloadBytes:  task.Result.PayloadBytes,
			FileBytes:     task.Result.FileBytes,
			StopReason:    task.Result.StopReason,
			InterfaceName: task.Result.InterfaceName,
			LivePreview:   task.Result.LivePreview,
		},
		Error:      task.Error,
		CreatedAt:  task.CreatedAt,
		StartedAt:  task.StartedAt,
		FinishedAt: task.FinishedAt,
	}
}
