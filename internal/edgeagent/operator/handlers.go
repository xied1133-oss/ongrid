// Package operator registers controlled, interactive troubleshooting tools.
package operator

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/creack/pty"

	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

const (
	defaultTimeout = 5 * time.Second
	maxTimeout     = 5 * time.Minute
	outputCap      = 64 << 10
)

func Register(client tunnel.Client, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	log.Info("operator: controlled exec ready",
		slog.String("method", tunnel.MethodOperatorExec),
		slog.String("start_method", tunnel.MethodOperatorExecStart),
		slog.Int("commands", 5),
	)
	client.RegisterHandler(tunnel.MethodOperatorExec, makeHandler(log))
	client.RegisterHandler(tunnel.MethodOperatorExecStart, makeStartHandler(client, log))
}

func HandleStream(stream tunnel.StreamConn, log *slog.Logger) {
	defer stream.Close()
	if log == nil {
		log = slog.Default()
	}
	var meta tunnel.OperatorStreamMeta
	if err := json.Unmarshal(stream.Meta(), &meta); err != nil {
		if err := writeStreamEvent(stream, tunnel.OperatorStreamEvent{Type: "done", Status: "error", Allowed: false, Reason: "bad stream meta: " + err.Error(), ExitCode: -1}); err != nil {
			log.Debug("operator: write bad-meta stream event", slog.Any("err", err))
		}
		return
	}
	if meta.Kind != tunnel.StreamKindOperatorExec {
		if err := writeStreamEvent(stream, tunnel.OperatorStreamEvent{Type: "done", Status: "error", Allowed: false, Reason: "unsupported stream kind", ExitCode: -1}); err != nil {
			log.Debug("operator: write unsupported-kind stream event", slog.Any("err", err))
		}
		return
	}
	name, argv, timeout, err := buildCommand(meta.Req)
	if err != nil {
		if err := writeStreamEvent(stream, tunnel.OperatorStreamEvent{Type: "done", Status: "error", Allowed: false, Reason: err.Error(), ExitCode: -1}); err != nil {
			log.Debug("operator: write rejected stream event", slog.Any("err", err))
		}
		return
	}
	runStream(stream, name, argv, timeout)
	log.Debug("operator: stream completed", slog.String("command", meta.Req.Command))
}

func makeHandler(log *slog.Logger) tunnel.Handler {
	return func(ctx context.Context, _ tunnel.Session, _ string, body []byte) ([]byte, error) {
		var req tunnel.OperatorExecRequest
		if len(body) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, fmt.Errorf("operator: bad req: %w", err)
			}
		}
		cmd, argv, timeout, err := buildCommand(req)
		if err != nil {
			return json.Marshal(tunnel.OperatorExecResponse{Allowed: false, Reason: err.Error(), ExitCode: -1})
		}
		resp := run(ctx, cmd, argv, timeout)
		log.Debug("operator: exec completed",
			slog.String("command", req.Command),
			slog.Int("exit_code", resp.ExitCode),
			slog.Int64("duration_ms", resp.DurationMs),
		)
		return json.Marshal(resp)
	}
}

func makeStartHandler(client tunnel.Client, log *slog.Logger) tunnel.Handler {
	return func(ctx context.Context, _ tunnel.Session, _ string, body []byte) ([]byte, error) {
		var in tunnel.OperatorExecStartRequest
		if err := json.Unmarshal(body, &in); err != nil {
			return nil, fmt.Errorf("operator: bad start req: %w", err)
		}
		if strings.TrimSpace(in.RunID) == "" {
			return nil, errors.New("operator: run_id required")
		}
		name, argv, timeout, err := buildCommand(in.Req)
		if err != nil {
			pushOperatorEvent(ctx, client, in.RunID, tunnel.OperatorStreamEvent{Type: "done", Status: "error", Allowed: false, Reason: err.Error(), ExitCode: -1})
			return json.Marshal(tunnel.OperatorExecStartResponse{Accepted: false, Reason: err.Error()})
		}
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Error("operator: async run panic", slog.Any("panic", r))
				}
			}()
			runPush(client, in.RunID, name, argv, timeout)
		}()
		return json.Marshal(tunnel.OperatorExecStartResponse{Accepted: true})
	}
}

func buildCommand(req tunnel.OperatorExecRequest) (string, []string, time.Duration, error) {
	timeout := defaultTimeout
	if req.TimeoutMs > 0 {
		timeout = time.Duration(req.TimeoutMs) * time.Millisecond
		if timeout > maxTimeout {
			return "", nil, 0, errors.New("timeout_ms max 300000")
		}
	}
	namespace, err := argNamespace(req.Args)
	if err != nil {
		return "", nil, 0, err
	}
	timeoutSeconds := max(1, int((timeout+time.Second-1)/time.Second))
	switch strings.TrimSpace(req.Command) {
	case "list_netns":
		return "ip", []string{"netns", "list"}, timeout, nil
	case "ping":
		host, err := argHost(req.Args, "host")
		if err != nil {
			return "", nil, 0, err
		}
		count := argInt(req.Args, "count", 4)
		if count <= 0 || count > 20 {
			return "", nil, 0, errors.New("count must be between 1 and 20")
		}
		return scopedCommand(namespace, "ping", []string{"-c", strconv.Itoa(count), "-W", strconv.Itoa(timeoutSeconds), host}, timeout+time.Duration(count)*time.Second)
	case "dig":
		host, err := argHost(req.Args, "host")
		if err != nil {
			return "", nil, 0, err
		}
		qtype := strings.ToUpper(strings.TrimSpace(argString(req.Args, "type", "A")))
		switch qtype {
		case "A", "AAAA", "CNAME", "MX", "TXT":
		default:
			return "", nil, 0, errors.New("unsupported dns type")
		}
		return scopedCommand(namespace, "dig", []string{"+time=" + strconv.Itoa(timeoutSeconds), host, qtype}, timeout+2*time.Second)
	case "tcp":
		hostInput := argString(req.Args, "host", "")
		if strings.TrimSpace(hostInput) == "" {
			hostInput = argString(req.Args, "target", "")
		}
		hostInput, targetPort, err := splitTCPHostPort(hostInput)
		if err != nil {
			return "", nil, 0, err
		}
		host, err := validateHost(hostInput)
		if err != nil {
			return "", nil, 0, err
		}
		port := argInt(req.Args, "port", 0)
		if port <= 0 && targetPort > 0 {
			port = targetPort
		}
		if port <= 0 || port > 65535 {
			return "", nil, 0, errors.New("invalid port")
		}
		return scopedCommand(namespace, "nc", []string{"-vz", "-w", strconv.Itoa(timeoutSeconds), host, strconv.Itoa(port)}, timeout+2*time.Second)
	case "http":
		target, err := argURL(req.Args, "url")
		if err != nil {
			return "", nil, 0, err
		}
		method := strings.ToUpper(strings.TrimSpace(argString(req.Args, "method", "HEAD")))
		if method != "HEAD" && method != "GET" {
			return "", nil, 0, errors.New("unsupported http method")
		}
		argv := []string{"-I", "-X", method, "--max-time", strconv.Itoa(timeoutSeconds)}
		if argBool(req.Args, "skip_tls", false) {
			argv = append(argv, "-k")
		}
		argv = append(argv, target)
		return scopedCommand(namespace, "curl", argv, timeout+5*time.Second)
	default:
		return "", nil, 0, errors.New("unsupported operator command")
	}
}

func scopedCommand(namespace, name string, argv []string, timeout time.Duration) (string, []string, time.Duration, error) {
	if namespace == "" {
		return name, argv, timeout, nil
	}
	scoped := append([]string{"netns", "exec", namespace, name}, argv...)
	return "ip", scoped, timeout, nil
}

func run(ctx context.Context, name string, argv []string, timeout time.Duration) tunnel.OperatorExecResponse {
	if _, err := exec.LookPath(name); err != nil {
		return tunnel.OperatorExecResponse{Allowed: false, Reason: "binary not found: " + name, ExitCode: -1}
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	var stdout, stderr cappedBuffer
	cmd := exec.CommandContext(callCtx, name, argv...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	resp := tunnel.OperatorExecResponse{
		Allowed:    true,
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		ExitCode:   0,
		Truncated:  stdout.truncated || stderr.truncated,
		DurationMs: time.Since(started).Milliseconds(),
	}
	if err == nil {
		return resp
	}
	if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
		resp.Stderr = appendLine(resp.Stderr, "operator: command timed out")
		resp.ExitCode = -1
		return resp
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		resp.ExitCode = exitErr.ExitCode()
		return resp
	}
	resp.Stderr = appendLine(resp.Stderr, err.Error())
	resp.ExitCode = -1
	return resp
}

func runStream(w io.Writer, name string, argv []string, timeout time.Duration) {
	if _, err := exec.LookPath(name); err != nil {
		if err := writeStreamEvent(w, tunnel.OperatorStreamEvent{Type: "done", Status: "error", Allowed: false, Reason: "binary not found: " + name, ExitCode: -1}); err != nil {
			slog.Debug("operator: write missing-binary stream event", slog.Any("err", err))
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	started := time.Now()
	cmd := exec.CommandContext(ctx, name, argv...)
	cmd.Env = append(cmd.Environ(), "TERM=xterm-256color")
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 120})
	if err != nil {
		if err := writeStreamEvent(w, tunnel.OperatorStreamEvent{Type: "done", Status: "error", Allowed: true, Reason: "start: " + err.Error(), ExitCode: -1}); err != nil {
			slog.Debug("operator: write start-error stream event", slog.Any("err", err))
		}
		return
	}
	defer tty.Close()

	var writeMu sync.Mutex
	var truncated atomic.Bool
	write := func(ev tunnel.OperatorStreamEvent) bool {
		writeMu.Lock()
		defer writeMu.Unlock()
		if err := writeStreamEvent(w, ev); err != nil {
			cancel()
			return false
		}
		return true
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go scanChunks(tty, &truncated, &wg, write)
	err = cmd.Wait()
	if closeErr := tty.Close(); closeErr != nil {
		slog.Debug("operator: close pty", slog.Any("err", closeErr))
	}
	wg.Wait()

	status := "success"
	exitCode := 0
	reason := ""
	if err != nil {
		status = "error"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			exitCode = -1
			reason = "operator: command timed out"
			write(tunnel.OperatorStreamEvent{Type: "stderr", Stream: "stderr", Message: reason + "\n"})
		} else {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = -1
				reason = err.Error()
			}
		}
	}
	write(tunnel.OperatorStreamEvent{Type: "done", Status: status, Allowed: true, Reason: reason, ExitCode: exitCode, DurationMs: time.Since(started).Milliseconds(), Truncated: truncated.Load()})
}

func runPush(client tunnel.Client, runID string, name string, argv []string, timeout time.Duration) {
	push := func(ev tunnel.OperatorStreamEvent) bool {
		return pushOperatorEvent(context.Background(), client, runID, ev) == nil
	}
	if _, err := exec.LookPath(name); err != nil {
		push(tunnel.OperatorStreamEvent{Type: "done", Status: "error", Allowed: false, Reason: "binary not found: " + name, ExitCode: -1})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	started := time.Now()
	cmd := exec.CommandContext(ctx, name, argv...)
	cmd.Env = append(cmd.Environ(), "TERM=xterm-256color")
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 120})
	if err != nil {
		push(tunnel.OperatorStreamEvent{Type: "done", Status: "error", Allowed: true, Reason: "start: " + err.Error(), ExitCode: -1})
		return
	}
	defer tty.Close()

	var truncated atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go scanChunks(tty, &truncated, &wg, push)
	err = cmd.Wait()
	if closeErr := tty.Close(); closeErr != nil {
		slog.Debug("operator: close push pty", slog.Any("err", closeErr))
	}
	wg.Wait()

	status := "success"
	exitCode := 0
	reason := ""
	if err != nil {
		status = "error"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			exitCode = -1
			reason = "operator: command timed out"
			push(tunnel.OperatorStreamEvent{Type: "stderr", Stream: "stderr", Message: reason + "\n"})
		} else {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = -1
				reason = err.Error()
			}
		}
	}
	push(tunnel.OperatorStreamEvent{Type: "done", Status: status, Allowed: true, Reason: reason, ExitCode: exitCode, DurationMs: time.Since(started).Milliseconds(), Truncated: truncated.Load()})
}

func pushOperatorEvent(ctx context.Context, client tunnel.Client, runID string, ev tunnel.OperatorStreamEvent) error {
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var resp tunnel.OperatorPushEventResponse
	if err := client.Call(callCtx, tunnel.MethodOperatorPushEvent, tunnel.OperatorPushEventRequest{RunID: runID, Event: ev}, &resp); err != nil {
		return fmt.Errorf("push operator event: %w", err)
	}
	if !resp.OK {
		return errors.New("push operator event rejected")
	}
	return nil
}

func scanChunks(r io.Reader, truncated *atomic.Bool, wg *sync.WaitGroup, write func(tunnel.OperatorStreamEvent) bool) {
	defer wg.Done()
	buf := make([]byte, 4096)
	written := 0
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if written+n > outputCap {
				allowed := outputCap - written
				if allowed > 0 {
					chunk = chunk[:allowed]
				} else {
					chunk = nil
				}
				truncated.Store(true)
			}
			written += n
			if len(chunk) > 0 && !write(tunnel.OperatorStreamEvent{Type: "stdout", Stream: "stdout", Message: string(chunk)}) {
				return
			}
			if written >= outputCap {
				truncated.Store(true)
				return
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) || strings.Contains(err.Error(), "input/output error") || strings.Contains(err.Error(), "file already closed") {
			return
		}
		truncated.Store(true)
		write(tunnel.OperatorStreamEvent{Type: "stderr", Stream: "stderr", Message: "stdout: " + err.Error() + "\n"})
		return
	}
}

func writeStreamEvent(w io.Writer, ev tunnel.OperatorStreamEvent) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		payload = []byte(`{"type":"stderr","stream":"stderr","message":"encode event failed\n"}`)
	}
	if _, err := io.WriteString(w, base64.StdEncoding.EncodeToString(payload)+"\n"); err != nil {
		return fmt.Errorf("write operator stream event: %w", err)
	}
	return nil
}

func argString(args map[string]any, key, fallback string) string {
	if args == nil {
		return fallback
	}
	if v, ok := args[key].(string); ok {
		return v
	}
	return fallback
}

func argInt(args map[string]any, key string, fallback int) int {
	if args == nil {
		return fallback
	}
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, err := strconv.Atoi(v)
		if err == nil {
			return n
		}
	}
	return fallback
}

func argBool(args map[string]any, key string, fallback bool) bool {
	if args == nil {
		return fallback
	}
	switch v := args[key].(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "y", "on":
			return true
		case "0", "false", "no", "n", "off":
			return false
		}
	}
	return fallback
}

func argNamespace(args map[string]any) (string, error) {
	ns := strings.TrimSpace(argString(args, "namespace", ""))
	if ns == "" {
		return "", nil
	}
	for _, r := range ns {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' || r == ':' {
			continue
		}
		return "", errors.New("invalid namespace")
	}
	return ns, nil
}

func argHost(args map[string]any, key string) (string, error) {
	host := strings.TrimSpace(argString(args, key, ""))
	return validateHost(host)
}

func validateHost(host string) (string, error) {
	if host == "" || strings.ContainsAny(host, " \t\r\n'\"`$;&|<>") {
		return "", errors.New("invalid host")
	}
	if ip := net.ParseIP(host); ip != nil {
		return host, nil
	}
	for _, r := range host {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '.' {
			continue
		}
		return "", errors.New("invalid host")
	}
	return host, nil
}

func splitTCPHostPort(value string) (string, int, error) {
	host := strings.TrimSpace(value)
	if host == "" {
		return "", 0, nil
	}
	if strings.HasPrefix(host, "[") {
		h, p, err := net.SplitHostPort(host)
		if err != nil {
			return host, 0, nil
		}
		port, err := strconv.Atoi(p)
		if err != nil {
			return "", 0, errors.New("invalid port")
		}
		return h, port, nil
	}
	if strings.Count(host, ":") != 1 {
		return host, 0, nil
	}
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		return host, 0, nil
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		return "", 0, errors.New("invalid port")
	}
	return h, port, nil
}

func argURL(args map[string]any, key string) (string, error) {
	raw := strings.TrimSpace(argString(args, key, ""))
	if raw == "" || strings.ContainsAny(raw, " \t\r\n'\"`$;&|<>") {
		return "", errors.New("invalid url")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", errors.New("invalid url")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("unsupported url scheme")
	}
	return raw, nil
}

func appendLine(existing, line string) string {
	if existing == "" {
		return line
	}
	return existing + "\n" + line
}

type cappedBuffer struct {
	buf       bytes.Buffer
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.buf.Len() >= outputCap {
		b.truncated = true
		return len(p), nil
	}
	remaining := outputCap - b.buf.Len()
	if len(p) > remaining {
		b.truncated = true
		_, _ = b.buf.Write(p[:remaining])
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *cappedBuffer) String() string {
	return b.buf.String()
}
