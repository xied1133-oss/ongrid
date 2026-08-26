package operator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

func TestBuildCommandRejectsUnsafeHost(t *testing.T) {
	_, _, _, err := buildCommand(tunnel.OperatorExecRequest{
		Command: "ping",
		Args:    map[string]any{"host": "127.0.0.1;rm -rf /"},
	})
	if err == nil {
		t.Fatal("error = nil")
	}
}

func TestHandlerRejectsUnsupportedCommand(t *testing.T) {
	h := makeHandler(nil)
	body, err := json.Marshal(tunnel.OperatorExecRequest{Command: "rm"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	respBody, err := h(context.Background(), tunnel.Session{}, tunnel.MethodOperatorExec, body)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var resp tunnel.OperatorExecResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Allowed || resp.Reason == "" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestBuildCommandPing(t *testing.T) {
	name, argv, _, err := buildCommand(tunnel.OperatorExecRequest{
		Command:   "ping",
		Args:      map[string]any{"host": "example.com", "count": float64(2)},
		TimeoutMs: 3000,
	})
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	want := []string{"-c", "2", "-W", "3", "example.com"}
	if name != "ping" || len(argv) != len(want) {
		t.Fatalf("name=%q argv=%v", name, argv)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv[%d]=%q want %q", i, argv[i], want[i])
		}
	}
}

func TestBuildCommandListNetNS(t *testing.T) {
	name, argv, _, err := buildCommand(tunnel.OperatorExecRequest{
		Command:   "list_netns",
		TimeoutMs: 3000,
	})
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	want := []string{"netns", "list"}
	if name != "ip" || len(argv) != len(want) {
		t.Fatalf("name=%q argv=%v", name, argv)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv[%d]=%q want %q", i, argv[i], want[i])
		}
	}
}

func TestBuildCommandTCPAcceptsTargetHostPort(t *testing.T) {
	name, argv, _, err := buildCommand(tunnel.OperatorExecRequest{
		Command:   "tcp",
		Args:      map[string]any{"target": "101.34.63.91:443"},
		TimeoutMs: 3000,
	})
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	want := []string{"-vz", "-w", "3", "101.34.63.91", "443"}
	if name != "nc" || len(argv) != len(want) {
		t.Fatalf("name=%q argv=%v", name, argv)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv[%d]=%q want %q", i, argv[i], want[i])
		}
	}
}

func TestBuildCommandHTTPAdvancedOptions(t *testing.T) {
	name, argv, _, err := buildCommand(tunnel.OperatorExecRequest{
		Command:   "http",
		Args:      map[string]any{"url": "https://101.34.63.91/healthz", "method": "GET", "skip_tls": true, "namespace": "blue"},
		TimeoutMs: 5000,
	})
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	want := []string{"netns", "exec", "blue", "curl", "-I", "-X", "GET", "--max-time", "5", "-k", "https://101.34.63.91/healthz"}
	if name != "ip" || len(argv) != len(want) {
		t.Fatalf("name=%q argv=%v", name, argv)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv[%d]=%q want %q", i, argv[i], want[i])
		}
	}
}
