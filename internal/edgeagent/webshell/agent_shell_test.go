package webshell

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

// fakeClient records edge→manager pushes and stores registered
// handlers so tests can drive them as the manager would.
type fakeClient struct {
	mu       sync.Mutex
	handlers map[string]tunnel.Handler
	pushCh   chan pushEvent
}

type pushEvent struct {
	method string
	body   []byte
}

func newFakeClient() *fakeClient {
	return &fakeClient{
		handlers: make(map[string]tunnel.Handler),
		pushCh:   make(chan pushEvent, 256),
	}
}

func (f *fakeClient) RegisterHandler(method string, h tunnel.Handler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[method] = h
}

func (f *fakeClient) Call(_ context.Context, method string, req, _ any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	f.pushCh <- pushEvent{method: method, body: body}
	return nil
}

func (f *fakeClient) invoke(t *testing.T, method string, req any) []byte {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal %s: %v", method, err)
	}
	f.mu.Lock()
	h := f.handlers[method]
	f.mu.Unlock()
	if h == nil {
		t.Fatalf("handler %q not registered", method)
	}
	out, err := h(context.Background(), tunnel.Session{}, method, body)
	if err != nil {
		t.Fatalf("invoke %s: %v", method, err)
	}
	return out
}

func (f *fakeClient) waitPush(t *testing.T, method string, wantSubstr string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-f.pushCh:
			if ev.method != method {
				continue
			}
			if wantSubstr == "" || bytes.Contains(ev.body, []byte(wantSubstr)) {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s push containing %q", method, wantSubstr)
		}
	}
}

func openReq(sid string) tunnel.ShellOpenRequest {
	return tunnel.ShellOpenRequest{
		SessionID: sid,
		Mode:      tunnel.ModeAgent,
		Cols:      80,
		Rows:      24,
		Term:      "xterm-256color",
	}
}

func TestAgentShellLifecycle(t *testing.T) {
	fc := newFakeClient()
	RegisterAgentShell(fc, nil)

	out := fc.invoke(t, tunnel.MethodShellOpen, openReq("s1"))
	var resp tunnel.ShellOpenResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode open resp: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("open not ok: %s", resp.Err)
	}
	if resp.OSUser == "" {
		t.Fatal("open resp missing os_user")
	}

	// An interactive shell emits a prompt / banner right away; any
	// output push proves the PTY pump works. (Echo round-trip timing
	// varies across dev machines — verified end-to-end on Linux hosts.)
	fc.waitPush(t, tunnel.MethodShellOutput, "", 10*time.Second)

	// Interactive shell echoes stdin; send an exit command so the
	// session terminates below.
	fc.invoke(t, tunnel.MethodShellInput, tunnel.ShellInputRequest{
		SessionID: "s1", Data: []byte("exit\n"),
	})

	// Resize must not error on a live session.
	fc.invoke(t, tunnel.MethodShellResize, tunnel.ShellResizeRequest{
		SessionID: "s1", Cols: 120, Rows: 40,
	})

	// Exit the shell; expect the terminal frame.
	fc.waitPush(t, tunnel.MethodShellExit, `"exit_code"`, 10*time.Second)
}

func TestAgentShellDisabledByHostPolicy(t *testing.T) {
	t.Setenv(EnvAgentShellDisabled, "true")
	fc := newFakeClient()
	RegisterAgentShell(fc, nil)

	out := fc.invoke(t, tunnel.MethodShellOpen, openReq("s1"))
	var resp tunnel.ShellOpenResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode open resp: %v", err)
	}
	if resp.Ok {
		t.Fatal("open succeeded despite kill switch")
	}
	if !strings.Contains(resp.Err, EnvAgentShellDisabled) {
		t.Fatalf("err should name the kill switch, got %q", resp.Err)
	}
}

func TestAgentShellRejectsNonAgentMode(t *testing.T) {
	fc := newFakeClient()
	RegisterAgentShell(fc, nil)

	req := openReq("s1")
	req.Mode = "ssh"
	out := fc.invoke(t, tunnel.MethodShellOpen, req)
	var resp tunnel.ShellOpenResponse
	_ = json.Unmarshal(out, &resp)
	if resp.Ok || !strings.Contains(resp.Err, "agent") {
		t.Fatalf("ssh-mode open over RPC must be rejected, got ok=%v err=%q", resp.Ok, resp.Err)
	}
}

func TestAgentShellSessionCap(t *testing.T) {
	fc := newFakeClient()
	RegisterAgentShell(fc, nil)

	for i := 0; i < maxAgentSessions; i++ {
		out := fc.invoke(t, tunnel.MethodShellOpen, openReq(sid(i)))
		var resp tunnel.ShellOpenResponse
		_ = json.Unmarshal(out, &resp)
		if !resp.Ok {
			t.Fatalf("open %d not ok: %s", i, resp.Err)
		}
	}

	out := fc.invoke(t, tunnel.MethodShellOpen, openReq(sid(maxAgentSessions)))
	var resp tunnel.ShellOpenResponse
	_ = json.Unmarshal(out, &resp)
	if resp.Ok || !strings.Contains(resp.Err, "too many") {
		t.Fatalf("cap not enforced, got ok=%v err=%q", resp.Ok, resp.Err)
	}

	// Closing one frees a slot.
	fc.invoke(t, tunnel.MethodShellClose, tunnel.ShellCloseRequest{SessionID: sid(0), Reason: "test"})
	deadline := time.Now().Add(5 * time.Second)
	for {
		out = fc.invoke(t, tunnel.MethodShellOpen, openReq(sid(maxAgentSessions)))
		_ = json.Unmarshal(out, &resp)
		if resp.Ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("slot not freed after close: %s", resp.Err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Cleanup: kill everything we spawned.
	for i := 1; i <= maxAgentSessions; i++ {
		fc.invoke(t, tunnel.MethodShellClose, tunnel.ShellCloseRequest{SessionID: sid(i)})
	}
}

func sid(i int) string { return "cap-" + string(rune('a'+i)) }
