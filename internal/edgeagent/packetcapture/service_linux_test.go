//go:build linux

package packetcapture

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestServiceStartIsIdempotentAndQueuesConcurrentCaptures(t *testing.T) {
	svc := newServiceForTest(t, func(ctx context.Context, _ Request) (Result, error) {
		<-ctx.Done()
		return Result{}, ctx.Err()
	})
	first, err := svc.Start(Request{CaptureID: "capture-123", Interface: "eth0"})
	if err != nil {
		t.Fatalf("Start first: %v", err)
	}
	second, err := svc.Start(Request{CaptureID: "capture-123", Interface: "eth0"})
	if err != nil {
		t.Fatalf("Start duplicate: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("duplicate IDs: %q %q", first.ID, second.ID)
	}
	queued, err := svc.Start(Request{CaptureID: "capture-456", Interface: "eth0"})
	if err != nil {
		t.Fatalf("Start queued: %v", err)
	}
	if queued.State != TaskQueued {
		t.Fatalf("queued state=%q want queued", queued.State)
	}
	if _, err := svc.Cancel(first.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if _, err := svc.Cancel(queued.ID); err != nil {
		t.Fatalf("Cancel queued: %v", err)
	}
}

func TestServiceRunsQueuedCapturesSerially(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	svc := newServiceForTest(t, func(_ context.Context, in Request) (Result, error) {
		started <- in.CaptureID
		<-release
		return Result{StopReason: "duration_limit"}, nil
	})
	if _, err := svc.Start(Request{CaptureID: "capture-1", Interface: "eth0"}); err != nil {
		t.Fatalf("Start first: %v", err)
	}
	if _, err := svc.Start(Request{CaptureID: "capture-2", Interface: "eth0"}); err != nil {
		t.Fatalf("Start second: %v", err)
	}
	select {
	case id := <-started:
		if id != "capture-1" {
			t.Fatalf("first started capture=%q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("first capture never started")
	}
	select {
	case id := <-started:
		t.Fatalf("second capture started while first was still running: %q", id)
	case <-time.After(50 * time.Millisecond):
	}
	release <- struct{}{}
	select {
	case id := <-started:
		if id != "capture-2" {
			t.Fatalf("second started capture=%q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("second capture never started after first completed")
	}
	release <- struct{}{}
}

func TestServiceCompletesTask(t *testing.T) {
	var once sync.Once
	svc := newServiceForTest(t, func(context.Context, Request) (Result, error) {
		once.Do(func() {})
		return Result{StopReason: "duration_limit"}, nil
	})
	if _, err := svc.Start(Request{CaptureID: "capture-123", Interface: "eth0"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		task, ok := svc.Get("capture-123")
		if ok && task.State == TaskSucceeded {
			return
		}
		time.Sleep(time.Millisecond)
	}
	task, _ := svc.Get("capture-123")
	t.Fatalf("task did not complete: %+v", task)
}

func TestServicePublishesProgressBeforeCaptureCompletes(t *testing.T) {
	reported := make(chan struct{})
	release := make(chan struct{})
	svc := newServiceForTest(t, func(context.Context, Request) (Result, error) {
		<-release
		return Result{Packets: 7, PayloadBytes: 512, StopReason: "duration_limit"}, nil
	})
	svc.progressRunner = func(_ context.Context, _ Request, report ProgressReporter) (Result, error) {
		report(Result{Packets: 3, PayloadBytes: 192, InterfaceName: "eth0"})
		close(reported)
		<-release
		return Result{Packets: 7, PayloadBytes: 512, StopReason: "duration_limit"}, nil
	}
	if _, err := svc.Start(Request{CaptureID: "capture-progress", Interface: "eth0"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-reported:
	case <-time.After(time.Second):
		t.Fatal("progress was not reported")
	}
	task, ok := svc.Get("capture-progress")
	if !ok || task.State != TaskRunning || task.Result.Packets != 3 || task.Result.PayloadBytes != 192 {
		t.Fatalf("live task = %+v, exists=%v", task, ok)
	}
	close(release)
}

func TestServiceHonorsPlannedStartTime(t *testing.T) {
	started := make(chan time.Time, 1)
	svc := newServiceForTest(t, func(_ context.Context, _ Request) (Result, error) {
		started <- time.Now().UTC()
		return Result{}, nil
	})
	planned := time.Now().UTC().Add(40 * time.Millisecond)
	if _, err := svc.Start(Request{CaptureID: "capture-start-at", Interface: "eth0", StartAt: &planned}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case actual := <-started:
		if actual.Before(planned.Add(-5 * time.Millisecond)) {
			t.Fatalf("started at %s before planned %s", actual, planned)
		}
	case <-time.After(time.Second):
		t.Fatal("capture never started")
	}
}

func TestServiceCancelsBeforePlannedStart(t *testing.T) {
	svc := newServiceForTest(t, func(context.Context, Request) (Result, error) {
		t.Fatal("runner should not start")
		return Result{}, nil
	})
	planned := time.Now().UTC().Add(time.Second)
	if _, err := svc.Start(Request{CaptureID: "capture-cancel-before-start", Interface: "eth0", StartAt: &planned}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := svc.Cancel("capture-cancel-before-start"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if task, ok := svc.Get("capture-cancel-before-start"); ok && task.State == TaskCancelled {
			return
		}
		time.Sleep(time.Millisecond)
	}
	task, _ := svc.Get("capture-cancel-before-start")
	t.Fatalf("task=%+v, want cancelled", task)
}

func TestServiceStopRunningCaptureKeepsPartialPCAP(t *testing.T) {
	dir := t.TempDir()
	started := make(chan struct{})
	svc := newServiceForDirTest(t, dir, func(ctx context.Context, in Request) (Result, error) {
		close(started)
		<-ctx.Done()
		path := filepath.Join(dir, in.CaptureID+".pcap")
		if err := os.WriteFile(path, []byte("partial-pcap"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		return Result{Path: path, FileBytes: int64(len("partial-pcap")), Packets: 3, StopReason: "cancelled"}, nil
	})
	if _, err := svc.Start(Request{CaptureID: "capture-stop-running", Interface: "eth0"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("runner never started")
	}
	if _, err := svc.Stop("capture-stop-running"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		task, ok := svc.Get("capture-stop-running")
		if ok && task.State == TaskSucceeded {
			if task.Result.StopReason != "stopped" || task.Result.FileBytes == 0 {
				t.Fatalf("stopped task = %+v", task)
			}
			raw, err := svc.Read("capture-stop-running", 1024)
			if err != nil || string(raw.Data) != "partial-pcap" {
				t.Fatalf("Read partial capture = %+v, %v", raw, err)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	task, _ := svc.Get("capture-stop-running")
	t.Fatalf("task=%+v, want succeeded", task)
}

func TestServiceStopBeforePlannedStartCancels(t *testing.T) {
	svc := newServiceForTest(t, func(context.Context, Request) (Result, error) {
		t.Fatal("runner should not start")
		return Result{}, nil
	})
	planned := time.Now().UTC().Add(time.Second)
	if _, err := svc.Start(Request{CaptureID: "capture-stop-before-start", Interface: "eth0", StartAt: &planned}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := svc.Stop("capture-stop-before-start"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if task, ok := svc.Get("capture-stop-before-start"); ok && task.State == TaskCancelled {
			return
		}
		time.Sleep(time.Millisecond)
	}
	task, _ := svc.Get("capture-stop-before-start")
	t.Fatalf("task=%+v, want cancelled", task)
}

func TestServiceReadCompletedCapture(t *testing.T) {
	dir := t.TempDir()
	svc := newServiceForDirTest(t, dir, func(_ context.Context, in Request) (Result, error) {
		path := filepath.Join(dir, in.CaptureID+".pcap")
		if err := os.WriteFile(path, []byte("pcap-data"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		return Result{Path: path, FileBytes: 9, StopReason: "duration_limit"}, nil
	})
	if _, err := svc.Start(Request{CaptureID: "capture-123", Interface: "eth0"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		task, _ := svc.Get("capture-123")
		if task.State == TaskSucceeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task did not complete: %+v", task)
		}
		time.Sleep(time.Millisecond)
	}
	raw, err := svc.Read("capture-123", 1024)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(raw.Data) != "pcap-data" || raw.SizeBytes != 9 || raw.SHA256Hex == "" {
		t.Fatalf("raw = %+v", raw)
	}
	if _, err := svc.Read("capture-123", 4); err == nil {
		t.Fatal("Read accepted object over limit")
	}
}

func newServiceForTest(t *testing.T, runner func(context.Context, Request) (Result, error)) *Service {
	t.Helper()
	return newServiceForDirTest(t, t.TempDir(), runner)
}

func newServiceForDirTest(t *testing.T, dir string, runner func(context.Context, Request) (Result, error)) *Service {
	t.Helper()
	capturer, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc, err := NewService(capturer)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc.runner = runner
	svc.progressRunner = nil
	return svc
}
