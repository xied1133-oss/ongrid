//go:build linux

package packetcapture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

const (
	TaskQueued    = "queued"
	TaskRunning   = "running"
	TaskSucceeded = "succeeded"
	TaskFailed    = "failed"
	TaskCancelled = "cancelled"

	maxQueuedCaptures = 10
)

// Task is the edge-owned view of one capture. The manager later mirrors this
// into its own durable task/artifact records; keeping the edge result explicit
// lets upload retry independently from capture execution.
type Task struct {
	ID         string     `json:"id"`
	Request    Request    `json:"request"`
	State      string     `json:"state"`
	Result     Result     `json:"result,omitempty"`
	Error      string     `json:"error,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type RawObject struct {
	Data      []byte
	SizeBytes uint64
	SHA256Hex string
}

// Service runs bounded capture work asynchronously. It intentionally runs only
// one capture at a time per edge: two simultaneous AF_PACKET captures can
// double memory, disk, and packet-copy pressure on an already unhealthy host.
// Multiple queued tasks are accepted so manager-coordinated repeat captures can
// execute as a durable sequence.
type Service struct {
	capturer *Capturer

	mu             sync.RWMutex
	tasks          map[string]*taskState
	runner         func(context.Context, Request) (Result, error)
	progressRunner func(context.Context, Request, ProgressReporter) (Result, error)
}

type taskState struct {
	task          Task
	cancel        context.CancelFunc
	stopRequested bool
}

func NewService(capturer *Capturer) (*Service, error) {
	if capturer == nil {
		return nil, errors.New("packet capture: capturer required")
	}
	return &Service{
		capturer:       capturer,
		tasks:          make(map[string]*taskState),
		runner:         capturer.Capture,
		progressRunner: capturer.CaptureWithProgress,
	}, nil
}

// Start validates the request before accepting it, preventing a manager retry
// from producing two writers for the same capture ID. It returns immediately;
// the capture owns a background context rather than the tunnel RPC context.
func (s *Service) Start(in Request) (Task, error) {
	if s == nil {
		return Task{}, errors.New("packet capture: nil service")
	}
	normalized, err := normalizeRequest(in)
	if err != nil {
		return Task{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.tasks[normalized.CaptureID]; ok {
		return existing.task, nil
	}
	if queued := s.liveTaskCountLocked(); queued >= maxQueuedCaptures {
		return Task{}, fmt.Errorf("packet capture: queue is full (%d live captures)", queued)
	}
	now := time.Now().UTC()
	ctx, cancel := context.WithCancel(context.Background())
	state := &taskState{task: Task{
		ID:        normalized.CaptureID,
		Request:   normalized,
		State:     TaskQueued,
		CreatedAt: now,
	}, cancel: cancel}
	s.tasks[state.task.ID] = state
	go s.run(ctx, state.task.ID)
	return state.task, nil
}

func (s *Service) run(ctx context.Context, captureID string) {
	s.mu.RLock()
	state, ok := s.tasks[captureID]
	if !ok {
		s.mu.RUnlock()
		return
	}
	startAt := state.task.Request.StartAt
	s.mu.RUnlock()
	if startAt != nil && startAt.After(time.Now().UTC()) {
		timer := time.NewTimer(time.Until(*startAt))
		defer timer.Stop()
		select {
		case <-ctx.Done():
			finished := time.Now().UTC()
			s.mu.Lock()
			if state, ok := s.tasks[captureID]; ok {
				state.task.State = TaskCancelled
				state.task.FinishedAt = &finished
			}
			s.mu.Unlock()
			return
		case <-timer.C:
		}
	}
	for {
		s.mu.RLock()
		ready := s.canRunLocked(captureID)
		s.mu.RUnlock()
		if ready {
			break
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			finished := time.Now().UTC()
			s.mu.Lock()
			if state, ok := s.tasks[captureID]; ok {
				state.task.State = TaskCancelled
				state.task.FinishedAt = &finished
			}
			s.mu.Unlock()
			return
		case <-timer.C:
		}
	}
	started := time.Now().UTC()
	s.mu.Lock()
	state, ok = s.tasks[captureID]
	if !ok {
		s.mu.Unlock()
		return
	}
	state.task.State = TaskRunning
	state.task.StartedAt = &started
	req := state.task.Request
	runner := s.runner
	progressRunner := s.progressRunner
	s.mu.Unlock()

	var result Result
	var err error
	if progressRunner != nil {
		result, err = progressRunner(ctx, req, func(progress Result) {
			s.updateProgress(captureID, progress)
		})
	} else {
		result, err = runner(ctx, req)
	}
	finished := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok = s.tasks[captureID]
	if !ok {
		return
	}
	state.task.Result = result
	state.task.FinishedAt = &finished
	if ctx.Err() != nil {
		if state.stopRequested && err == nil && result.Path != "" && result.FileBytes > 0 {
			result.StopReason = "stopped"
			state.task.Result = result
			state.task.State = TaskSucceeded
			return
		}
		state.task.State = TaskCancelled
		return
	}
	if err != nil {
		state.task.State = TaskFailed
		state.task.Error = err.Error()
		return
	}
	state.task.State = TaskSucceeded
}

func (s *Service) updateProgress(captureID string, progress Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.tasks[captureID]
	if !ok || state.task.State != TaskRunning {
		return
	}
	state.task.Result = progress
}

func (s *Service) liveTaskCountLocked() int {
	count := 0
	for _, state := range s.tasks {
		if state.task.State == TaskQueued || state.task.State == TaskRunning {
			count++
		}
	}
	return count
}

func (s *Service) canRunLocked(captureID string) bool {
	current, ok := s.tasks[captureID]
	if !ok || current.task.State != TaskQueued {
		return false
	}
	for id, state := range s.tasks {
		if id == captureID {
			continue
		}
		switch state.task.State {
		case TaskRunning:
			return false
		case TaskQueued:
			if queuedBefore(state.task, current.task) {
				return false
			}
		}
	}
	return true
}

func queuedBefore(a, b Task) bool {
	aStart, bStart := queuedOrderTime(a), queuedOrderTime(b)
	if !aStart.Equal(bStart) {
		return aStart.Before(bStart)
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ID < b.ID
}

func queuedOrderTime(task Task) time.Time {
	if task.Request.StartAt != nil {
		return *task.Request.StartAt
	}
	return task.CreatedAt
}

func (s *Service) Get(captureID string) (Task, bool) {
	if s == nil {
		return Task{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.tasks[captureID]
	if !ok {
		return Task{}, false
	}
	return cloneTask(state.task), true
}

func (s *Service) Read(captureID string, readLimit uint64) (RawObject, error) {
	task, ok := s.Get(captureID)
	if !ok {
		return RawObject{}, fmt.Errorf("packet capture: %q not found", captureID)
	}
	if task.State != TaskSucceeded {
		return RawObject{}, fmt.Errorf("packet capture: %q is %s, not succeeded", captureID, task.State)
	}
	if task.Result.Path == "" {
		return RawObject{}, errors.New("packet capture: raw object path missing")
	}
	if readLimit == 0 || readLimit > maxBytes {
		readLimit = maxBytes
	}
	info, err := os.Stat(task.Result.Path)
	if err != nil {
		return RawObject{}, fmt.Errorf("packet capture: stat raw object: %w", err)
	}
	if info.Size() < 0 || uint64(info.Size()) > readLimit {
		return RawObject{}, fmt.Errorf("packet capture: raw object exceeds read limit")
	}
	data, err := os.ReadFile(task.Result.Path)
	if err != nil {
		return RawObject{}, fmt.Errorf("packet capture: read raw object: %w", err)
	}
	sum := sha256.Sum256(data)
	return RawObject{
		Data:      data,
		SizeBytes: uint64(len(data)),
		SHA256Hex: hex.EncodeToString(sum[:]),
	}, nil
}

func (s *Service) List() []Task {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Task, 0, len(s.tasks))
	for _, state := range s.tasks {
		out = append(out, cloneTask(state.task))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (s *Service) Cancel(captureID string) (Task, error) {
	if s == nil {
		return Task{}, errors.New("packet capture: nil service")
	}
	s.mu.RLock()
	state, ok := s.tasks[captureID]
	if !ok {
		s.mu.RUnlock()
		return Task{}, fmt.Errorf("packet capture: %q not found", captureID)
	}
	if state.task.State != TaskQueued && state.task.State != TaskRunning {
		task := cloneTask(state.task)
		s.mu.RUnlock()
		return task, nil
	}
	cancel := state.cancel
	s.mu.RUnlock()
	cancel()
	return s.GetAfterCancel(captureID)
}

// Stop gracefully interrupts a running capture while retaining any valid PCAP
// prefix. A queued capture has no data to preserve and therefore becomes
// cancelled when its runner observes the stop signal.
func (s *Service) Stop(captureID string) (Task, error) {
	if s == nil {
		return Task{}, errors.New("packet capture: nil service")
	}
	s.mu.Lock()
	state, ok := s.tasks[captureID]
	if !ok {
		s.mu.Unlock()
		return Task{}, fmt.Errorf("packet capture: %q not found", captureID)
	}
	if state.task.State != TaskQueued && state.task.State != TaskRunning {
		task := cloneTask(state.task)
		s.mu.Unlock()
		return task, nil
	}
	state.stopRequested = true
	cancel := state.cancel
	s.mu.Unlock()
	cancel()
	return s.GetAfterCancel(captureID)
}

// GetAfterCancel returns the state at cancellation dispatch time. The runner
// changes it to cancelled once the capture loop observes the context.
func (s *Service) GetAfterCancel(captureID string) (Task, error) {
	task, ok := s.Get(captureID)
	if !ok {
		return Task{}, fmt.Errorf("packet capture: %q not found", captureID)
	}
	return task, nil
}

func cloneTask(in Task) Task {
	out := in
	if in.StartedAt != nil {
		v := *in.StartedAt
		out.StartedAt = &v
	}
	if in.FinishedAt != nil {
		v := *in.FinishedAt
		out.FinishedAt = &v
	}
	out.Result.LivePreview = append([]string(nil), in.Result.LivePreview...)
	return out
}
