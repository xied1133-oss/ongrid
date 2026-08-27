// Agent shell: edge-hosted PTY backend for WebSSH ("agent" mode).
//
// In legacy SSH mode the edge is a dumb byte forwarder to its local
// sshd and the manager owns the SSH client. In agent mode the edge
// spawns the PTY shell itself (as the edge process user — typically
// root) so hosts behind bastions / without distributed OS passwords
// still get an interactive terminal: authentication is the ongrid
// account system (casbin device:shell), audit lives on the manager.
//
// Wire: manager invokes shell_open (Mode="agent") / shell_input /
// shell_resize / shell_close RPCs; the edge pushes stdout chunks and
// the terminal frame back via shell_output / shell_exit.
//
// Host-side kill switch: ONGRID_EDGE_AGENT_SHELL_DISABLED=true (read
// on every open; host admin controls the env file).
package webshell

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"sync"

	"github.com/creack/pty"

	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

// AgentClient is the narrow tunnel surface RegisterAgentShell needs.
// *tunnel.Client satisfies it.
type AgentClient interface {
	RegisterHandler(method string, h tunnel.Handler)
	Call(ctx context.Context, method string, req, resp any) error
}

// EnvAgentShellDisabled is the host-side opt-out. Values "true"/"1"
// disable agent shells; checked per open so editing the env file +
// restarting the unit flips it without a binary change.
const EnvAgentShellDisabled = "ONGRID_EDGE_AGENT_SHELL_DISABLED"

// maxAgentSessions caps concurrent PTY sessions per edge — mirrors
// the manager-side per-device cap so a misbehaving browser can't fork
// bomb the host.
const maxAgentSessions = 5

type agentSession struct {
	id  string
	tty *os.File
	cmd *exec.Cmd

	// closed guards tty against use-after-close: teardown flips it
	// under a.mu, and input / resize check it before touching the fd.
	closed bool
}

type agentShell struct {
	client AgentClient
	log    *slog.Logger

	mu       sync.Mutex
	sessions map[string]*agentSession
}

// RegisterAgentShell installs the four shell_* RPC handlers. Called
// from cmd/ongrid-edge next to the stream forwarder Register.
func RegisterAgentShell(client AgentClient, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	a := &agentShell{
		client:   client,
		log:      log,
		sessions: make(map[string]*agentSession),
	}
	client.RegisterHandler(tunnel.MethodShellOpen, a.handleOpen)
	client.RegisterHandler(tunnel.MethodShellInput, a.handleInput)
	client.RegisterHandler(tunnel.MethodShellResize, a.handleResize)
	client.RegisterHandler(tunnel.MethodShellClose, a.handleClose)
	log.Info("webshell: agent shell handlers registered",
		slog.Int("max_sessions", maxAgentSessions))
}

func agentShellDisabled() bool {
	v := os.Getenv(EnvAgentShellDisabled)
	return v == "true" || v == "1"
}

func (a *agentShell) handleOpen(ctx context.Context, _ tunnel.Session, _ string, body []byte) ([]byte, error) {
	var req tunnel.ShellOpenRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return json.Marshal(tunnel.ShellOpenResponse{Err: "bad request: " + err.Error()})
	}
	if req.Mode != tunnel.ModeAgent {
		// SSH mode never arrives here (manager opens a stream instead);
		// be explicit rather than silently spawning a shell.
		return json.Marshal(tunnel.ShellOpenResponse{Err: "shell_open RPC supports mode=agent only"})
	}
	if req.SessionID == "" {
		return json.Marshal(tunnel.ShellOpenResponse{Err: "session_id required"})
	}
	if agentShellDisabled() {
		return json.Marshal(tunnel.ShellOpenResponse{
			Err: "agent shell disabled by host policy (" + EnvAgentShellDisabled + ")",
		})
	}

	a.mu.Lock()
	if _, dup := a.sessions[req.SessionID]; dup {
		a.mu.Unlock()
		return json.Marshal(tunnel.ShellOpenResponse{Err: "session already open"})
	}
	if len(a.sessions) >= maxAgentSessions {
		a.mu.Unlock()
		return json.Marshal(tunnel.ShellOpenResponse{
			Err: fmt.Sprintf("too many agent shells on this host (%d)", maxAgentSessions),
		})
	}
	// Reserve the slot before spawning so concurrent opens can't race
	// past the cap.
	a.sessions[req.SessionID] = nil
	a.mu.Unlock()

	resp := a.spawn(ctx, req)
	if !resp.Ok {
		a.mu.Lock()
		delete(a.sessions, req.SessionID)
		a.mu.Unlock()
	}
	return json.Marshal(resp)
}

// spawn starts the PTY shell and the output pump. Caller reserved the
// session slot.
func (a *agentShell) spawn(ctx context.Context, req tunnel.ShellOpenRequest) tunnel.ShellOpenResponse {
	shell := pickShell()
	cmd := exec.Command(shell)
	term := req.Term
	if term == "" {
		term = "xterm-256color"
	}
	cmd.Env = append(os.Environ(), "TERM="+term)
	if u, err := user.Current(); err == nil {
		cmd.Env = append(cmd.Env, "HOME="+u.HomeDir, "USER="+u.Username, "LOGNAME="+u.Username)
	}
	cmd.Dir = os.Getenv("HOME")
	if cmd.Dir == "" {
		if u, err := user.Current(); err == nil {
			cmd.Dir = u.HomeDir
		}
	}

	cols, rows := req.Cols, req.Rows
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: rows, Cols: cols})
	if err != nil {
		return tunnel.ShellOpenResponse{Err: "start pty: " + err.Error()}
	}

	sess := &agentSession{id: req.SessionID, tty: tty, cmd: cmd}
	a.mu.Lock()
	a.sessions[req.SessionID] = sess
	a.mu.Unlock()

	osUser := ""
	if u, uerr := user.Current(); uerr == nil {
		osUser = u.Username
	}
	a.log.Info("webshell: agent shell started",
		slog.String("session_id", req.SessionID),
		slog.String("shell", shell),
		slog.String("os_user", osUser),
	)

	go a.pump(sess)

	return tunnel.ShellOpenResponse{Ok: true, OSUser: osUser}
}

// pump copies PTY output to the manager until EOF, then reports the
// exit code and tears the session down.
func (a *agentShell) pump(sess *agentSession) {
	buf := make([]byte, 8192)
	for {
		n, err := sess.tty.Read(buf)
		if n > 0 {
			var out tunnel.ShellOutputResponse
			if cerr := a.client.Call(context.Background(), tunnel.MethodShellOutput,
				tunnel.ShellOutputRequest{SessionID: sess.id, Data: buf[:n]}, &out); cerr != nil {
				// Tunnel blip: drop the chunk rather than spin. The
				// browser just sees a gap, same as a lossy SSH link.
				a.log.Debug("webshell: push output", slog.Any("err", cerr))
			}
		}
		if err != nil {
			break
		}
	}

	exitCode := 0
	errMsg := ""
	if werr := sess.cmd.Wait(); werr != nil {
		var ee *exec.ExitError
		if errors.As(werr, &ee) {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
			errMsg = werr.Error()
		}
	}

	a.teardown(sess)

	var xr tunnel.ShellExitResponse
	if cerr := a.client.Call(context.Background(), tunnel.MethodShellExit,
		tunnel.ShellExitRequest{SessionID: sess.id, ExitCode: exitCode, Err: errMsg}, &xr); cerr != nil {
		a.log.Debug("webshell: push exit", slog.Any("err", cerr))
	}
	a.log.Info("webshell: agent shell exited",
		slog.String("session_id", sess.id), slog.Int("exit_code", exitCode))
}

// teardown closes the PTY and kills the shell. Idempotent; safe to
// call from both pump (natural exit) and handleClose (manager kill).
// The first teardown wins the flag so concurrent input / resize never
// touch a closed fd.
func (a *agentShell) teardown(sess *agentSession) {
	a.mu.Lock()
	if cur, ok := a.sessions[sess.id]; ok && (cur == nil || cur == sess) {
		delete(a.sessions, sess.id)
	}
	if sess.closed {
		a.mu.Unlock()
		return
	}
	sess.closed = true
	a.mu.Unlock()
	_ = sess.tty.Close()
	if sess.cmd.Process != nil {
		_ = sess.cmd.Process.Kill()
	}
}

func (a *agentShell) lookup(id string) *agentSession {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[id]
}

func (a *agentShell) handleInput(_ context.Context, _ tunnel.Session, _ string, body []byte) ([]byte, error) {
	var req tunnel.ShellInputRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("shell_input: decode: %w", err)
	}
	sess := a.lookup(req.SessionID)
	if sess == nil {
		return json.Marshal(tunnel.ShellInputResponse{}) // raced a teardown
	}
	a.mu.Lock()
	if sess.closed {
		a.mu.Unlock()
		return json.Marshal(tunnel.ShellInputResponse{})
	}
	_, err := sess.tty.Write(req.Data)
	a.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("shell_input: write: %w", err)
	}
	return json.Marshal(tunnel.ShellInputResponse{})
}

func (a *agentShell) handleResize(_ context.Context, _ tunnel.Session, _ string, body []byte) ([]byte, error) {
	var req tunnel.ShellResizeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("shell_resize: decode: %w", err)
	}
	sess := a.lookup(req.SessionID)
	if sess == nil {
		return json.Marshal(tunnel.ShellResizeResponse{})
	}
	a.mu.Lock()
	if sess.closed {
		a.mu.Unlock()
		return json.Marshal(tunnel.ShellResizeResponse{})
	}
	err := pty.Setsize(sess.tty, &pty.Winsize{Rows: req.Rows, Cols: req.Cols})
	a.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("shell_resize: %w", err)
	}
	return json.Marshal(tunnel.ShellResizeResponse{})
}

func (a *agentShell) handleClose(_ context.Context, _ tunnel.Session, _ string, body []byte) ([]byte, error) {
	var req tunnel.ShellCloseRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("shell_close: decode: %w", err)
	}
	if sess := a.lookup(req.SessionID); sess != nil {
		a.log.Info("webshell: agent shell closed by manager",
			slog.String("session_id", req.SessionID), slog.String("reason", req.Reason))
		a.teardown(sess) // pump observes EOF and pushes shell_exit
	}
	return json.Marshal(tunnel.ShellCloseResponse{})
}

// pickShell prefers bash (nicer prompts / rc) and falls back to sh.
func pickShell() string {
	for _, sh := range []string{"/bin/bash", "/bin/sh"} {
		if fi, err := os.Stat(sh); err == nil && !fi.IsDir() {
			return sh
		}
	}
	return "/bin/sh"
}
