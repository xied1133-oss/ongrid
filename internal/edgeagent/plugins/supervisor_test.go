package plugins

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakePlugin is a controllable Plugin for supervisor tests.
type fakePlugin struct {
	name        string
	configCalls atomic.Int32
	startCalls  atomic.Int32
	stopCalls   atomic.Int32
	startErr    error
	configErr   error

	mu      sync.Mutex
	running bool
	last    PluginConfig
}

func (f *fakePlugin) Name() string { return f.name }

func (f *fakePlugin) Configure(cfg PluginConfig) error {
	f.configCalls.Add(1)
	if f.configErr != nil {
		return f.configErr
	}
	f.mu.Lock()
	f.last = cfg
	f.mu.Unlock()
	return nil
}

func (f *fakePlugin) Start(ctx context.Context) error {
	f.startCalls.Add(1)
	if f.startErr != nil {
		return f.startErr
	}
	f.mu.Lock()
	f.running = true
	f.mu.Unlock()
	return nil
}

func (f *fakePlugin) Stop(ctx context.Context) error {
	f.stopCalls.Add(1)
	f.mu.Lock()
	f.running = false
	f.mu.Unlock()
	return nil
}

func (f *fakePlugin) HealthSnapshot() PluginHealth {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := StateStopped
	if f.running {
		st = StateRunning
	}
	return PluginHealth{Name: f.name, State: st, UpdatedAt: time.Now()}
}

// staticFetcher returns a fixed snapshot.
type staticFetcher struct {
	mu   sync.Mutex
	snap map[string]PluginConfig
	err  error
}

func (s *staticFetcher) set(snap map[string]PluginConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap = snap
}

func (s *staticFetcher) Fetch(_ context.Context) (map[string]PluginConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]PluginConfig, len(s.snap))
	for k, v := range s.snap {
		out[k] = v
	}
	return out, nil
}

func TestSupervisorEnablesAndDisables(t *testing.T) {
	f := &staticFetcher{snap: map[string]PluginConfig{
		"logs": {Enabled: true, EdgeID: 1, Endpoint: "https://x", Spec: map[string]any{"a": 1}},
	}}
	p := &fakePlugin{name: "logs"}

	sup := NewSupervisor(SupervisorOpts{Fetcher: f, ReloadInterval: 50 * time.Millisecond})
	sup.Register(p)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)

	waitFor(t, "first start", time.Second, func() bool {
		return p.startCalls.Load() == 1 && p.configCalls.Load() == 1
	})

	// Disable: supervisor should call Stop next reconcile.
	f.set(map[string]PluginConfig{
		"logs": {Enabled: false},
	})
	sup.TriggerReload()

	waitFor(t, "stop after disable", time.Second, func() bool {
		return p.stopCalls.Load() == 1
	})
}

func TestSupervisorReconfigureRestarts(t *testing.T) {
	f := &staticFetcher{snap: map[string]PluginConfig{
		"logs": {Enabled: true, EdgeID: 1, Endpoint: "https://x", Spec: map[string]any{"v": 1}},
	}}
	p := &fakePlugin{name: "logs"}

	sup := NewSupervisor(SupervisorOpts{Fetcher: f, ReloadInterval: 50 * time.Millisecond})
	sup.Register(p)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)

	waitFor(t, "first start", time.Second, func() bool { return p.startCalls.Load() == 1 })

	// New spec value → reconfigure + restart.
	f.set(map[string]PluginConfig{
		"logs": {Enabled: true, EdgeID: 1, Endpoint: "https://x", Spec: map[string]any{"v": 2}},
	})
	sup.TriggerReload()

	waitFor(t, "restart on cfg change", time.Second, func() bool {
		return p.stopCalls.Load() == 1 && p.startCalls.Load() == 2 && p.configCalls.Load() == 2
	})
}

func TestSupervisorIgnoresBrokenFetch(t *testing.T) {
	f := &staticFetcher{snap: map[string]PluginConfig{
		"logs": {Enabled: true, EdgeID: 1, Endpoint: "https://x"},
	}}
	p := &fakePlugin{name: "logs"}

	sup := NewSupervisor(SupervisorOpts{Fetcher: f, ReloadInterval: 50 * time.Millisecond})
	sup.Register(p)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)

	waitFor(t, "first start", time.Second, func() bool { return p.startCalls.Load() == 1 })

	// Now fetcher errors — supervisor should keep current state, not stop.
	f.mu.Lock()
	f.err = errors.New("manager unreachable")
	f.mu.Unlock()
	sup.TriggerReload()

	// Wait a bit and confirm no spurious stop.
	time.Sleep(200 * time.Millisecond)
	if p.stopCalls.Load() != 0 {
		t.Errorf("supervisor stopped plugin on transient fetch error; that's wrong")
	}
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", what)
}
