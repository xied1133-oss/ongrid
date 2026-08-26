package restart_service

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

// fakeClient is a tunnel.Client stub. Same shape as the host_files
// test fixture; kept local to avoid depending on biz/test internals.
type fakeClient struct {
	mu       sync.Mutex
	handlers map[string]tunnel.Handler
}

func newFakeClient() *fakeClient { return &fakeClient{handlers: map[string]tunnel.Handler{}} }

func (f *fakeClient) Dial(_ context.Context) error                     { return nil }
func (f *fakeClient) Call(_ context.Context, _ string, _, _ any) error { return nil }
func (f *fakeClient) OnReconnect(_ func())                             {}
func (f *fakeClient) Close() error                                     { return nil }
func (f *fakeClient) AcceptStream() (tunnel.StreamConn, error)         { return nil, nil }
func (f *fakeClient) RegisterHandler(method string, h tunnel.Handler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[method] = h
}
func (f *fakeClient) handler(method string) tunnel.Handler {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.handlers[method]
}

func TestRegister_RegistersMethodAndDefaultsAreSane(t *testing.T) {
	c := newFakeClient()
	if err := Register(c, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if c.handler(tunnel.MethodRestartService) == nil {
		t.Fatalf("handler not registered for %q", tunnel.MethodRestartService)
	}
	sb := DefaultSandboxConfig()
	if !sb.Mocked {
		t.Errorf("PR-7 default must be Mocked=true (real systemctl not implemented)")
	}
	if err := sb.Validate(); err != nil {
		t.Errorf("default sandbox should validate: %v", err)
	}
}

func TestSandbox_AllowsCanonicalForms(t *testing.T) {
	sb := DefaultSandboxConfig()
	cases := []struct {
		input string
		want  bool
	}{
		{"nginx", true},
		{"NGINX", true},
		{"  nginx  ", true},
		{"nginx.service", true},
		{"sshd", false},
		{"", false},
		{"nginx; rm -rf /", false},
	}
	for _, c := range cases {
		got := sb.Allows(c.input)
		if got != c.want {
			t.Errorf("Allows(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestSandbox_ValidateRejectsEmpty(t *testing.T) {
	sb := &SandboxConfig{}
	if err := sb.Validate(); err == nil {
		t.Errorf("empty AllowedUnits should fail Validate")
	}
}

func TestHandler_MockSuccess(t *testing.T) {
	c := newFakeClient()
	if err := Register(c, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	h := c.handler(tunnel.MethodRestartService)
	body, _ := json.Marshal(tunnel.RestartServiceRequest{Service: "nginx", Reason: "502s"})
	respBody, err := h(context.Background(), tunnel.Session{}, tunnel.MethodRestartService, body)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	var resp tunnel.RestartServiceResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		t.Fatalf("decode resp: %v", err)
	}
	if !resp.Restarted {
		t.Errorf("Restarted should be true on mock success")
	}
	if !resp.Mocked {
		t.Errorf("Mocked must be true while real systemctl is not implemented")
	}
	if resp.Service != "nginx" {
		t.Errorf("Service echo = %q, want nginx", resp.Service)
	}
	if resp.StartedAt.After(resp.EndedAt) {
		t.Errorf("StartedAt should be <= EndedAt")
	}
}

func TestHandler_RejectsOutOfList(t *testing.T) {
	c := newFakeClient()
	if err := Register(c, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	h := c.handler(tunnel.MethodRestartService)
	body, _ := json.Marshal(tunnel.RestartServiceRequest{Service: "sshd"})
	_, err := h(context.Background(), tunnel.Session{}, tunnel.MethodRestartService, body)
	if err == nil {
		t.Fatalf("expected allow-list rejection")
	}
	if !strings.Contains(err.Error(), "allow-list") {
		t.Errorf("error should mention allow-list: %v", err)
	}
}

func TestHandler_RejectsEmpty(t *testing.T) {
	c := newFakeClient()
	if err := Register(c, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	h := c.handler(tunnel.MethodRestartService)
	_, err := h(context.Background(), tunnel.Session{}, tunnel.MethodRestartService, []byte(`{"service":""}`))
	if err == nil {
		t.Errorf("empty service should error")
	}
}

func TestHandler_RealSystemctlNotImplemented(t *testing.T) {
	// Operator who flips Mocked=false anticipating real shell-out
	// must hit a clean error, not a silent restart.
	sb := DefaultSandboxConfig()
	sb.Mocked = false
	c := newFakeClient()
	c.RegisterHandler(tunnel.MethodRestartService, makeRestartHandler(sb, nil))
	h := c.handler(tunnel.MethodRestartService)
	body, _ := json.Marshal(tunnel.RestartServiceRequest{Service: "nginx"})
	_, err := h(context.Background(), tunnel.Session{}, tunnel.MethodRestartService, body)
	if err == nil {
		t.Fatalf("Mocked=false should error in PR-7")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("error should explain real shell-out is missing: %v", err)
	}
}
