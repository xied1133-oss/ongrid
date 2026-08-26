// Package operatorrun runs short-lived operator tools on edge hosts.
// Results are kept in memory and streamed to the browser; they are not
// artifacts and are intentionally separate from Agent skills.
package operatorrun

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

const (
	StatusRunning   = "running"
	StatusSuccess   = "success"
	StatusPartial   = "partial"
	StatusError     = "error"
	StatusCancelled = "cancelled"

	EventCreated  = "created"
	EventEdgeRun  = "edge_running"
	EventStdout   = "stdout"
	EventStderr   = "stderr"
	EventEdgeDone = "edge_done"
	EventDone     = "done"
)

type Caller struct {
	UserID uint64
	Role   string
}

type EdgeCaller interface {
	Call(ctx context.Context, edgeID uint64, method string, body []byte) ([]byte, error)
}

type EdgeStreamer interface {
	OpenStreamWithMeta(ctx context.Context, edgeID uint64, meta []byte) (io.ReadWriteCloser, error)
}

type Service struct {
	caller   EdgeCaller
	streamer EdgeStreamer
	log      *slog.Logger

	mu   sync.Mutex
	runs map[string]*Run

	pending map[string]map[uint64]chan EdgeResult
}

func New(caller EdgeCaller, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	svc := &Service{caller: caller, log: log, runs: make(map[string]*Run), pending: make(map[string]map[uint64]chan EdgeResult)}
	if streamer, ok := caller.(EdgeStreamer); ok {
		svc.streamer = streamer
	}
	return svc
}

type CreateInput struct {
	EdgeIDs   []uint64        `json:"edge_ids"`
	Command   string          `json:"command"`
	Args      json.RawMessage `json:"args,omitempty"`
	TimeoutMs int             `json:"timeout_ms,omitempty"`
}

type NetNSList struct {
	EdgeID     uint64   `json:"edge_id"`
	Namespaces []string `json:"namespaces"`
}

type Run struct {
	ID         string       `json:"id"`
	Command    string       `json:"command"`
	Title      string       `json:"title"`
	Status     string       `json:"status"`
	EdgeIDs    []uint64     `json:"edge_ids"`
	CreatedBy  uint64       `json:"-"`
	StartedAt  time.Time    `json:"started_at"`
	FinishedAt *time.Time   `json:"finished_at,omitempty"`
	Events     []Event      `json:"events,omitempty"`
	Results    []EdgeResult `json:"results,omitempty"`

	cancel      context.CancelFunc
	subscribers map[chan Event]struct{}
}

type EdgeResult struct {
	EdgeID     uint64 `json:"edge_id"`
	Status     string `json:"status"`
	Allowed    bool   `json:"allowed"`
	Reason     string `json:"reason,omitempty"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	ExitCode   int    `json:"exit_code"`
	Truncated  bool   `json:"truncated,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

type Event struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Ts         time.Time `json:"ts"`
	RunID      string    `json:"run_id"`
	EdgeID     uint64    `json:"edge_id,omitempty"`
	Stream     string    `json:"stream,omitempty"`
	Message    string    `json:"message,omitempty"`
	Status     string    `json:"status,omitempty"`
	ExitCode   int       `json:"exit_code,omitempty"`
	DurationMs int64     `json:"duration_ms,omitempty"`
}

func (s *Service) Create(ctx context.Context, caller Caller, in CreateInput) (*Run, error) {
	if s.caller == nil {
		return nil, fmt.Errorf("%w: operator runner not configured", errs.ErrNotWiredYet)
	}
	if !caller.CanOperate() {
		return nil, errs.ErrForbidden
	}
	if len(in.EdgeIDs) == 0 {
		return nil, fmt.Errorf("%w: edge_ids required", errs.ErrInvalid)
	}
	if len(in.EdgeIDs) > 16 {
		return nil, fmt.Errorf("%w: edge_ids max 16", errs.ErrInvalid)
	}
	req, displayCmd, title, timeoutSeconds, err := buildRequest(in)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	now := time.Now().UTC()
	run := &Run{
		ID:          uuid.NewString(),
		Command:     in.Command,
		Title:       title,
		Status:      StatusRunning,
		EdgeIDs:     append([]uint64(nil), in.EdgeIDs...),
		CreatedBy:   caller.UserID,
		StartedAt:   now,
		cancel:      cancel,
		subscribers: make(map[chan Event]struct{}),
	}
	s.mu.Lock()
	s.runs[run.ID] = run
	s.mu.Unlock()
	s.emit(run.ID, Event{Type: EventCreated, RunID: run.ID, Ts: now, Status: StatusRunning, Message: "$ " + displayCmd})
	go s.execute(runCtx, run.ID, in.EdgeIDs, req, timeoutSeconds)
	return s.Get(ctx, caller, run.ID)
}

func (s *Service) Get(_ context.Context, caller Caller, id string) (*Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[id]
	if !ok {
		return nil, errs.ErrNotFound
	}
	if !caller.CanRead(run) {
		return nil, errs.ErrForbidden
	}
	return cloneRun(run), nil
}

func (s *Service) Cancel(ctx context.Context, caller Caller, id string) (*Run, error) {
	s.mu.Lock()
	run, ok := s.runs[id]
	if !ok {
		s.mu.Unlock()
		return nil, errs.ErrNotFound
	}
	if !caller.CanRead(run) || !caller.CanOperate() {
		s.mu.Unlock()
		return nil, errs.ErrForbidden
	}
	if run.cancel != nil && run.Status == StatusRunning {
		run.cancel()
	}
	s.mu.Unlock()
	return s.Get(context.WithoutCancel(ctx), caller, id)
}

func (s *Service) ListNetNS(ctx context.Context, caller Caller, edgeID uint64) (*NetNSList, error) {
	if s.caller == nil {
		return nil, fmt.Errorf("%w: operator runner not configured", errs.ErrNotWiredYet)
	}
	if !caller.CanOperate() {
		return nil, errs.ErrForbidden
	}
	if edgeID == 0 {
		return nil, fmt.Errorf("%w: edge_id required", errs.ErrInvalid)
	}
	req := tunnel.OperatorExecRequest{Command: "list_netns", TimeoutMs: 3000}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal list_netns request: %w", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	respBody, err := s.caller.Call(callCtx, edgeID, tunnel.MethodOperatorExec, body)
	if err != nil {
		return nil, fmt.Errorf("list netns dispatch: %w", err)
	}
	var resp tunnel.OperatorExecResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("decode list_netns response: %w", err)
	}
	if !resp.Allowed || resp.ExitCode != 0 {
		reason := strings.TrimSpace(resp.Reason)
		if reason == "" {
			reason = strings.TrimSpace(resp.Stderr)
		}
		if reason == "" {
			reason = "list_netns failed"
		}
		return nil, fmt.Errorf("%w: %s", errs.ErrInvalid, reason)
	}
	return &NetNSList{EdgeID: edgeID, Namespaces: parseNetNSList(resp.Stdout)}, nil
}

func (s *Service) Subscribe(ctx context.Context, caller Caller, id string) ([]Event, <-chan Event, func(), error) {
	ch := make(chan Event, 64)
	s.mu.Lock()
	run, ok := s.runs[id]
	if !ok {
		s.mu.Unlock()
		close(ch)
		return nil, nil, nil, errs.ErrNotFound
	}
	if !caller.CanRead(run) {
		s.mu.Unlock()
		close(ch)
		return nil, nil, nil, errs.ErrForbidden
	}
	history := append([]Event(nil), run.Events...)
	run.subscribers[ch] = struct{}{}
	s.mu.Unlock()
	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			s.mu.Lock()
			if run, ok := s.runs[id]; ok {
				delete(run.subscribers, ch)
			}
			s.mu.Unlock()
			close(ch)
		})
	}
	go func() {
		<-ctx.Done()
		unsubscribe()
	}()
	return history, ch, unsubscribe, nil
}

func parseNetNSList(stdout string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		name, err := safeNamespace(fields[0])
		if err != nil || name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func (s *Service) execute(ctx context.Context, runID string, edgeIDs []uint64, req tunnel.OperatorExecRequest, timeoutSeconds int) {
	var wg sync.WaitGroup
	results := make(chan EdgeResult, len(edgeIDs))
	for _, edgeID := range edgeIDs {
		edgeID := edgeID
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- s.executeOne(ctx, runID, edgeID, req, timeoutSeconds)
		}()
	}
	wg.Wait()
	close(results)
	var all []EdgeResult
	for result := range results {
		all = append(all, result)
	}
	s.finish(runID, all)
}

func (s *Service) executeOne(ctx context.Context, runID string, edgeID uint64, req tunnel.OperatorExecRequest, timeoutSeconds int) EdgeResult {
	s.emit(runID, Event{Type: EventEdgeRun, RunID: runID, Ts: time.Now().UTC(), EdgeID: edgeID, Status: StatusRunning, Message: "started"})
	if result, ok := s.executeOneAsync(ctx, runID, edgeID, req, timeoutSeconds); ok {
		return result
	}
	body, err := json.Marshal(req)
	if err != nil {
		return s.edgeError(runID, edgeID, fmt.Errorf("marshal req: %w", err))
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds+5)*time.Second)
	defer cancel()
	if s.streamer != nil {
		if result, ok := s.executeOneStream(callCtx, runID, edgeID, req); ok {
			return result
		}
	}
	respBody, err := s.caller.Call(callCtx, edgeID, tunnel.MethodOperatorExec, body)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return EdgeResult{EdgeID: edgeID, Status: StatusCancelled, Error: "cancelled"}
		}
		return s.edgeError(runID, edgeID, fmt.Errorf("dispatch: %w", err))
	}
	var resp tunnel.OperatorExecResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return s.edgeError(runID, edgeID, fmt.Errorf("decode resp: %w", err))
	}
	status := StatusSuccess
	if !resp.Allowed || resp.ExitCode != 0 {
		status = StatusError
	}
	if resp.Stdout != "" {
		s.emit(runID, Event{Type: EventStdout, RunID: runID, Ts: time.Now().UTC(), EdgeID: edgeID, Stream: "stdout", Message: resp.Stdout})
	}
	if resp.Stderr != "" {
		s.emit(runID, Event{Type: EventStderr, RunID: runID, Ts: time.Now().UTC(), EdgeID: edgeID, Stream: "stderr", Message: resp.Stderr})
	}
	message := "completed"
	if !resp.Allowed && resp.Reason != "" {
		message = resp.Reason
	}
	s.emit(runID, Event{Type: EventEdgeDone, RunID: runID, Ts: time.Now().UTC(), EdgeID: edgeID, Status: status, ExitCode: resp.ExitCode, DurationMs: resp.DurationMs, Message: message})
	return EdgeResult{EdgeID: edgeID, Status: status, Allowed: resp.Allowed, Reason: resp.Reason, Stdout: resp.Stdout, Stderr: resp.Stderr, ExitCode: resp.ExitCode, Truncated: resp.Truncated, DurationMs: resp.DurationMs}
}

func (s *Service) executeOneAsync(ctx context.Context, runID string, edgeID uint64, req tunnel.OperatorExecRequest, timeoutSeconds int) (EdgeResult, bool) {
	ch := s.registerPending(runID, edgeID)
	defer s.unregisterPending(runID, edgeID)
	body, err := json.Marshal(tunnel.OperatorExecStartRequest{RunID: runID, Req: req})
	if err != nil {
		return s.edgeError(runID, edgeID, fmt.Errorf("marshal start req: %w", err)), true
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	respBody, err := s.caller.Call(callCtx, edgeID, tunnel.MethodOperatorExecStart, body)
	if err != nil {
		return EdgeResult{}, false
	}
	var resp tunnel.OperatorExecStartResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return s.edgeError(runID, edgeID, fmt.Errorf("decode start resp: %w", err)), true
	}
	if !resp.Accepted {
		return EdgeResult{EdgeID: edgeID, Status: StatusError, Allowed: false, Reason: resp.Reason, ExitCode: -1, Error: resp.Reason}, true
	}
	select {
	case result := <-ch:
		return result, true
	case <-ctx.Done():
		return EdgeResult{EdgeID: edgeID, Status: StatusCancelled, Error: "cancelled"}, true
	case <-time.After(time.Duration(timeoutSeconds+10) * time.Second):
		return s.edgeError(runID, edgeID, errors.New("operator async run timed out waiting for done")), true
	}
}

func (s *Service) executeOneStream(ctx context.Context, runID string, edgeID uint64, req tunnel.OperatorExecRequest) (EdgeResult, bool) {
	meta, err := json.Marshal(tunnel.OperatorStreamMeta{Kind: tunnel.StreamKindOperatorExec, Req: req})
	if err != nil {
		return s.edgeError(runID, edgeID, fmt.Errorf("marshal stream meta: %w", err)), true
	}
	stream, err := s.streamer.OpenStreamWithMeta(ctx, edgeID, meta)
	if err != nil {
		return EdgeResult{}, false
	}
	defer stream.Close()

	var stdout, stderr strings.Builder
	result := EdgeResult{EdgeID: edgeID, Status: StatusRunning, Allowed: true, ExitCode: -1}
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
	seenOperatorFrame := false
	for scanner.Scan() {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(scanner.Text()))
		if err != nil {
			if !seenOperatorFrame {
				return EdgeResult{}, false
			}
			s.emit(runID, Event{Type: EventStderr, RunID: runID, Ts: time.Now().UTC(), EdgeID: edgeID, Stream: "stderr", Message: "decode stream frame: " + err.Error()})
			continue
		}
		seenOperatorFrame = true
		var ev tunnel.OperatorStreamEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			s.emit(runID, Event{Type: EventStderr, RunID: runID, Ts: time.Now().UTC(), EdgeID: edgeID, Stream: "stderr", Message: "decode stream event: " + err.Error()})
			continue
		}
		switch ev.Type {
		case EventStdout:
			stdout.WriteString(ev.Message)
			s.emit(runID, Event{Type: EventStdout, RunID: runID, Ts: time.Now().UTC(), EdgeID: edgeID, Stream: "stdout", Message: ev.Message})
		case EventStderr:
			stderr.WriteString(ev.Message)
			s.emit(runID, Event{Type: EventStderr, RunID: runID, Ts: time.Now().UTC(), EdgeID: edgeID, Stream: "stderr", Message: ev.Message})
		case EventDone:
			status := StatusSuccess
			if !ev.Allowed || ev.ExitCode != 0 || ev.Status == StatusError {
				status = StatusError
			}
			message := "completed"
			if !ev.Allowed && ev.Reason != "" {
				message = ev.Reason
			}
			s.emit(runID, Event{Type: EventEdgeDone, RunID: runID, Ts: time.Now().UTC(), EdgeID: edgeID, Status: status, ExitCode: ev.ExitCode, DurationMs: ev.DurationMs, Message: message})
			result.Status = status
			result.Allowed = ev.Allowed
			result.Reason = ev.Reason
			result.Stdout = stdout.String()
			result.Stderr = stderr.String()
			result.ExitCode = ev.ExitCode
			result.Truncated = ev.Truncated
			result.DurationMs = ev.DurationMs
			return result, true
		}
	}
	if err := scanner.Err(); err != nil {
		if !seenOperatorFrame {
			return EdgeResult{}, false
		}
		return s.edgeError(runID, edgeID, fmt.Errorf("read stream: %w", err)), true
	}
	if !seenOperatorFrame {
		return EdgeResult{}, false
	}
	return s.edgeError(runID, edgeID, errors.New("operator stream ended before done")), true
}

func (s *Service) HandlePushEvent(_ context.Context, edgeID uint64, body []byte) ([]byte, error) {
	var in tunnel.OperatorPushEventRequest
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("operator push_event: decode: %w", err)
	}
	if !s.runAllowsEdge(in.RunID, edgeID) {
		return json.Marshal(tunnel.OperatorPushEventResponse{OK: false})
	}
	result, done := s.applyPushedEvent(in.RunID, edgeID, in.Event)
	if done {
		s.completePending(in.RunID, edgeID, result)
	}
	return json.Marshal(tunnel.OperatorPushEventResponse{OK: true})
}

func (s *Service) runAllowsEdge(runID string, edgeID uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[runID]
	if !ok {
		return false
	}
	for _, id := range run.EdgeIDs {
		if id == edgeID {
			return true
		}
	}
	return false
}

func (s *Service) applyPushedEvent(runID string, edgeID uint64, ev tunnel.OperatorStreamEvent) (EdgeResult, bool) {
	s.mu.Lock()
	run, ok := s.runs[runID]
	if !ok {
		s.mu.Unlock()
		return EdgeResult{}, false
	}
	result := EdgeResult{EdgeID: edgeID, Status: StatusRunning, Allowed: true, ExitCode: -1}
	for _, existing := range run.Results {
		if existing.EdgeID == edgeID {
			result = existing
			break
		}
	}
	switch ev.Type {
	case EventStdout:
		result.Stdout += ev.Message
	case EventStderr:
		result.Stderr += ev.Message
	case EventDone:
		status := StatusSuccess
		if !ev.Allowed || ev.ExitCode != 0 || ev.Status == StatusError {
			status = StatusError
		}
		result.Status = status
		result.Allowed = ev.Allowed
		result.Reason = ev.Reason
		result.ExitCode = ev.ExitCode
		result.Truncated = ev.Truncated
		result.DurationMs = ev.DurationMs
	}
	replaced := false
	for i := range run.Results {
		if run.Results[i].EdgeID == edgeID {
			run.Results[i] = result
			replaced = true
			break
		}
	}
	if !replaced {
		run.Results = append(run.Results, result)
	}
	s.mu.Unlock()

	switch ev.Type {
	case EventStdout:
		s.emit(runID, Event{Type: EventStdout, RunID: runID, Ts: time.Now().UTC(), EdgeID: edgeID, Stream: "stdout", Message: ev.Message})
	case EventStderr:
		s.emit(runID, Event{Type: EventStderr, RunID: runID, Ts: time.Now().UTC(), EdgeID: edgeID, Stream: "stderr", Message: ev.Message})
	case EventDone:
		message := "completed"
		if !ev.Allowed && ev.Reason != "" {
			message = ev.Reason
		}
		s.emit(runID, Event{Type: EventEdgeDone, RunID: runID, Ts: time.Now().UTC(), EdgeID: edgeID, Status: result.Status, ExitCode: ev.ExitCode, DurationMs: ev.DurationMs, Message: message})
		return result, true
	}
	return result, false
}

func (s *Service) registerPending(runID string, edgeID uint64) chan EdgeResult {
	ch := make(chan EdgeResult, 1)
	s.mu.Lock()
	if s.pending[runID] == nil {
		s.pending[runID] = make(map[uint64]chan EdgeResult)
	}
	s.pending[runID][edgeID] = ch
	s.mu.Unlock()
	return ch
}

func (s *Service) unregisterPending(runID string, edgeID uint64) {
	s.mu.Lock()
	if edges := s.pending[runID]; edges != nil {
		delete(edges, edgeID)
		if len(edges) == 0 {
			delete(s.pending, runID)
		}
	}
	s.mu.Unlock()
}

func (s *Service) completePending(runID string, edgeID uint64, result EdgeResult) {
	s.mu.Lock()
	ch := s.pending[runID][edgeID]
	s.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- result:
	default:
	}
}

func (s *Service) edgeError(runID string, edgeID uint64, err error) EdgeResult {
	msg := err.Error()
	s.emit(runID, Event{Type: EventStderr, RunID: runID, Ts: time.Now().UTC(), EdgeID: edgeID, Stream: "stderr", Message: msg})
	s.emit(runID, Event{Type: EventEdgeDone, RunID: runID, Ts: time.Now().UTC(), EdgeID: edgeID, Status: StatusError, Message: msg})
	return EdgeResult{EdgeID: edgeID, Status: StatusError, Error: msg}
}

func (s *Service) finish(runID string, results []EdgeResult) {
	status := StatusSuccess
	failed := 0
	cancelled := 0
	for _, result := range results {
		switch result.Status {
		case StatusCancelled:
			cancelled++
		case StatusError:
			failed++
		}
	}
	switch {
	case cancelled == len(results):
		status = StatusCancelled
	case failed == len(results):
		status = StatusError
	case failed > 0 || cancelled > 0:
		status = StatusPartial
	}
	now := time.Now().UTC()
	s.mu.Lock()
	if run, ok := s.runs[runID]; ok {
		run.Status = status
		run.FinishedAt = &now
		run.Results = append([]EdgeResult(nil), results...)
	}
	s.mu.Unlock()
	s.emit(runID, Event{Type: EventDone, RunID: runID, Ts: now, Status: status, Message: fmt.Sprintf("finished: %s", status)})
}

func (s *Service) emit(runID string, event Event) {
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.Ts.IsZero() {
		event.Ts = time.Now().UTC()
	}
	s.mu.Lock()
	run, ok := s.runs[runID]
	if ok {
		run.Events = append(run.Events, event)
		for ch := range run.subscribers {
			select {
			case ch <- event:
			default:
			}
		}
	}
	s.mu.Unlock()
}

func cloneRun(run *Run) *Run {
	cp := *run
	cp.EdgeIDs = append([]uint64(nil), run.EdgeIDs...)
	cp.Events = append([]Event(nil), run.Events...)
	cp.Results = append([]EdgeResult(nil), run.Results...)
	cp.cancel = nil
	cp.subscribers = nil
	return &cp
}

func (c Caller) CanOperate() bool {
	return c.UserID != 0 && c.Role != "viewer"
}

func (c Caller) CanRead(run *Run) bool {
	return run != nil && (c.Role == "admin" || (c.UserID != 0 && c.UserID == run.CreatedBy))
}

func buildRequest(in CreateInput) (tunnel.OperatorExecRequest, string, string, int, error) {
	timeoutMs := in.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 5000
	}
	if timeoutMs > 300000 {
		return tunnel.OperatorExecRequest{}, "", "", 0, fmt.Errorf("%w: timeout_ms max 300000", errs.ErrInvalid)
	}
	timeoutSeconds := max(1, (timeoutMs+999)/1000)
	switch strings.TrimSpace(in.Command) {
	case "ping":
		var args struct {
			Host      string `json:"host"`
			Count     int    `json:"count"`
			Namespace string `json:"namespace"`
		}
		if err := decodeArgs(in.Args, &args); err != nil {
			return tunnel.OperatorExecRequest{}, "", "", 0, err
		}
		if args.Count <= 0 {
			args.Count = 4
		}
		if args.Count > 20 {
			return tunnel.OperatorExecRequest{}, "", "", 0, fmt.Errorf("%w: count max 20", errs.ErrInvalid)
		}
		host, err := safeHost(args.Host)
		if err != nil {
			return tunnel.OperatorExecRequest{}, "", "", 0, err
		}
		reqArgs, scopePrefix, err := scopedArgs(map[string]any{"host": host, "count": args.Count}, args.Namespace)
		if err != nil {
			return tunnel.OperatorExecRequest{}, "", "", 0, err
		}
		req := tunnel.OperatorExecRequest{Command: "ping", Args: reqArgs, TimeoutMs: timeoutMs}
		display := scopePrefix + "ping -c " + strconv.Itoa(args.Count) + " -W " + strconv.Itoa(timeoutSeconds) + " " + host
		return req, display, "Ping " + host, timeoutSeconds + args.Count, nil
	case "dig":
		var args struct {
			Host      string `json:"host"`
			Type      string `json:"type"`
			Namespace string `json:"namespace"`
		}
		if err := decodeArgs(in.Args, &args); err != nil {
			return tunnel.OperatorExecRequest{}, "", "", 0, err
		}
		host, err := safeHost(args.Host)
		if err != nil {
			return tunnel.OperatorExecRequest{}, "", "", 0, err
		}
		qtype := strings.ToUpper(strings.TrimSpace(args.Type))
		if qtype == "" {
			qtype = "A"
		}
		if qtype != "A" && qtype != "AAAA" && qtype != "CNAME" && qtype != "MX" && qtype != "TXT" {
			return tunnel.OperatorExecRequest{}, "", "", 0, fmt.Errorf("%w: unsupported dns type", errs.ErrInvalid)
		}
		reqArgs, scopePrefix, err := scopedArgs(map[string]any{"host": host, "type": qtype}, args.Namespace)
		if err != nil {
			return tunnel.OperatorExecRequest{}, "", "", 0, err
		}
		req := tunnel.OperatorExecRequest{Command: "dig", Args: reqArgs, TimeoutMs: timeoutMs}
		display := scopePrefix + "dig +time=" + strconv.Itoa(timeoutSeconds) + " " + host + " " + qtype
		return req, display, "Dig " + host, timeoutSeconds + 2, nil
	case "tcp":
		var args struct {
			Host      string `json:"host"`
			Port      int    `json:"port"`
			Target    string `json:"target"`
			Namespace string `json:"namespace"`
		}
		if err := decodeArgs(in.Args, &args); err != nil {
			return tunnel.OperatorExecRequest{}, "", "", 0, err
		}
		hostInput := strings.TrimSpace(args.Host)
		if hostInput == "" {
			hostInput = strings.TrimSpace(args.Target)
		}
		hostInput, targetPort, err := splitTCPHostPort(hostInput)
		if err != nil {
			return tunnel.OperatorExecRequest{}, "", "", 0, err
		}
		host, err := safeHost(hostInput)
		if err != nil {
			return tunnel.OperatorExecRequest{}, "", "", 0, err
		}
		if args.Port <= 0 && targetPort > 0 {
			args.Port = targetPort
		}
		if args.Port <= 0 || args.Port > 65535 {
			return tunnel.OperatorExecRequest{}, "", "", 0, fmt.Errorf("%w: invalid port", errs.ErrInvalid)
		}
		reqArgs, scopePrefix, err := scopedArgs(map[string]any{"host": host, "port": args.Port}, args.Namespace)
		if err != nil {
			return tunnel.OperatorExecRequest{}, "", "", 0, err
		}
		req := tunnel.OperatorExecRequest{Command: "tcp", Args: reqArgs, TimeoutMs: timeoutMs}
		display := scopePrefix + "nc -vz -w " + strconv.Itoa(timeoutSeconds) + " " + host + " " + strconv.Itoa(args.Port)
		return req, display, "TCP " + host + ":" + strconv.Itoa(args.Port), timeoutSeconds + 2, nil
	case "http":
		var args struct {
			URL       string `json:"url"`
			Method    string `json:"method"`
			SkipTLS   bool   `json:"skip_tls"`
			Namespace string `json:"namespace"`
		}
		if err := decodeArgs(in.Args, &args); err != nil {
			return tunnel.OperatorExecRequest{}, "", "", 0, err
		}
		target, err := safeURL(args.URL)
		if err != nil {
			return tunnel.OperatorExecRequest{}, "", "", 0, err
		}
		method := strings.ToUpper(strings.TrimSpace(args.Method))
		if method == "" {
			method = "HEAD"
		}
		if method != "HEAD" && method != "GET" {
			return tunnel.OperatorExecRequest{}, "", "", 0, fmt.Errorf("%w: unsupported http method", errs.ErrInvalid)
		}
		reqArgs := map[string]any{"url": target, "method": method}
		if args.SkipTLS {
			reqArgs["skip_tls"] = true
		}
		reqArgs, scopePrefix, err := scopedArgs(reqArgs, args.Namespace)
		if err != nil {
			return tunnel.OperatorExecRequest{}, "", "", 0, err
		}
		req := tunnel.OperatorExecRequest{Command: "http", Args: reqArgs, TimeoutMs: timeoutMs}
		tlsFlag := ""
		if args.SkipTLS {
			tlsFlag = " -k"
		}
		display := scopePrefix + "curl -I -X " + method + " --max-time " + strconv.Itoa(timeoutSeconds) + tlsFlag + " " + target
		return req, display, "HTTP " + target, timeoutSeconds + 5, nil
	default:
		return tunnel.OperatorExecRequest{}, "", "", 0, fmt.Errorf("%w: unsupported operator command", errs.ErrInvalid)
	}
}

func scopedArgs(args map[string]any, namespace string) (map[string]any, string, error) {
	ns, err := safeNamespace(namespace)
	if err != nil {
		return nil, "", err
	}
	if ns == "" {
		return args, "", nil
	}
	args["namespace"] = ns
	return args, "ip netns exec " + ns + " ", nil
}

func decodeArgs(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: args: %s", errs.ErrInvalid, err)
	}
	return nil
}

func safeHost(value string) (string, error) {
	host := strings.TrimSpace(value)
	if host == "" || strings.ContainsAny(host, " \t\r\n'\"`$;&|<>") {
		return "", fmt.Errorf("%w: invalid host", errs.ErrInvalid)
	}
	if ip := net.ParseIP(host); ip != nil {
		return host, nil
	}
	for _, r := range host {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '.' {
			continue
		}
		return "", fmt.Errorf("%w: invalid host", errs.ErrInvalid)
	}
	return host, nil
}

func safeNamespace(value string) (string, error) {
	ns := strings.TrimSpace(value)
	if ns == "" {
		return "", nil
	}
	for _, r := range ns {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' || r == ':' {
			continue
		}
		return "", fmt.Errorf("%w: invalid namespace", errs.ErrInvalid)
	}
	return ns, nil
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
			return "", 0, fmt.Errorf("%w: invalid port", errs.ErrInvalid)
		}
		return h, port, nil
	}
	if strings.Count(host, ":") != 1 {
		return host, 0, nil
	}
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		h, p, err = net.SplitHostPort(host + ":")
		if err != nil || p != "" {
			return host, 0, nil
		}
		return host, 0, nil
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		return "", 0, fmt.Errorf("%w: invalid port", errs.ErrInvalid)
	}
	return h, port, nil
}

func safeURL(value string) (string, error) {
	raw := strings.TrimSpace(value)
	if raw == "" || strings.ContainsAny(raw, " \t\r\n'\"`$;&|<>") {
		return "", fmt.Errorf("%w: invalid url", errs.ErrInvalid)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("%w: invalid url", errs.ErrInvalid)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("%w: unsupported url scheme", errs.ErrInvalid)
	}
	return raw, nil
}
