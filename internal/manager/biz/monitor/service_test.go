package monitor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/monitor"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// fakeRepo is an in-memory Repo implementation just rich enough for the
// service-level tests. It serialises mutations under a mutex so the
// async sync goroutine can race with the API call without sliding into
// undefined behaviour.
type fakeRepo struct {
	mu     sync.Mutex
	rows   map[uint64]*model.Panel
	nextID uint64
	syncs  []string // op log ("set:1:msg" / "delete:1") to assert on
}

func newFakeRepo() *fakeRepo { return &fakeRepo{rows: map[uint64]*model.Panel{}, nextID: 0} }

func (r *fakeRepo) List(_ context.Context) ([]*model.Panel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*model.Panel, 0, len(r.rows))
	for _, p := range r.rows {
		cp := *p
		out = append(out, &cp)
	}
	return out, nil
}

func (r *fakeRepo) Get(_ context.Context, id uint64) (*model.Panel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.rows[id]
	if !ok {
		return nil, errs.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (r *fakeRepo) MaxOrdinal(_ context.Context) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	max := 0
	for _, p := range r.rows {
		if p.Ordinal > max {
			max = p.Ordinal
		}
	}
	return max, nil
}

func (r *fakeRepo) Create(_ context.Context, p *model.Panel) (*model.Panel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	p.ID = r.nextID
	cp := *p
	r.rows[p.ID] = &cp
	return p, nil
}

func (r *fakeRepo) Update(_ context.Context, id uint64, fields map[string]any) (*model.Panel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.rows[id]
	if !ok {
		return nil, errs.ErrNotFound
	}
	if v, ok := fields["title"].(string); ok {
		p.Title = v
	}
	if v, ok := fields["promql"].(string); ok {
		p.PromQL = v
	}
	if v, ok := fields["ordinal"].(int); ok {
		p.Ordinal = v
	}
	cp := *p
	return &cp, nil
}

func (r *fakeRepo) SetSyncResult(_ context.Context, id uint64, msg string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.syncs = append(r.syncs, msg) // empty = ok, non-empty = err
	if p, ok := r.rows[id]; ok {
		p.LastSyncError = msg
	}
	return nil
}

func (r *fakeRepo) Delete(_ context.Context, id uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.rows[id]; !ok {
		return errs.ErrNotFound
	}
	delete(r.rows, id)
	return nil
}

// fakeSyncer counts SyncMonitorPanels invocations and optionally returns
// a fixed error so the failure path is exercised.
type fakeSyncer struct {
	mu       sync.Mutex
	calls    int
	failWith error
	done     chan struct{}
}

func (s *fakeSyncer) SyncMonitorPanels(_ context.Context, _ []*model.Panel) error {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	if s.done != nil {
		// Non-blocking signal; drained by the test once it sees calls > 0.
		select {
		case s.done <- struct{}{}:
		default:
		}
	}
	return s.failWith
}

func (s *fakeSyncer) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// TestCreateValidatesInputs checks the obvious bad-input rejections so a
// regression doesn't accidentally let through empty PromQL / unknown
// types (both would silently break the Grafana mirror).
func TestCreateValidatesInputs(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	svc := New(repo, nil, nil)

	cases := []struct {
		name string
		in   CreateInput
	}{
		{"empty title", CreateInput{Title: "", PromQL: "up"}},
		{"empty promql", CreateInput{Title: "x", PromQL: "  "}},
		{"bad type", CreateInput{Title: "x", PromQL: "up", Type: "table"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := svc.Create(context.Background(), c.in); !errors.Is(err, errs.ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

// TestCreateAssignsOrdinalAndAsyncSync verifies the create flow:
//   - returns 200 immediately (sync runs in a goroutine)
//   - assigns ordinal = max+1 when not provided
//   - eventually invokes the syncer
func TestCreateAssignsOrdinalAndAsyncSync(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	syncer := &fakeSyncer{done: make(chan struct{}, 4)}
	svc := New(repo, syncer, nil)

	first, err := svc.Create(context.Background(), CreateInput{Title: "A", PromQL: "up"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if first.Ordinal != 1 {
		t.Fatalf("first ordinal = %d, want 1", first.Ordinal)
	}
	second, err := svc.Create(context.Background(), CreateInput{Title: "B", PromQL: "up"})
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}
	if second.Ordinal != 2 {
		t.Fatalf("second ordinal = %d, want 2", second.Ordinal)
	}

	// Wait for the goroutines to drain. Two creates → at least two syncs.
	deadline := time.After(2 * time.Second)
	got := 0
	for got < 2 {
		select {
		case <-syncer.done:
			got++
		case <-deadline:
			t.Fatalf("syncer never invoked twice; calls = %d", syncer.callCount())
		}
	}
}

// TestCreateAPI200WhenSyncFails confirms the sync-failure invariant:
// the API call still returns 200 (no error from Create) and the failure
// is recorded via SetSyncResult so the UI can surface it.
func TestCreateAPI200WhenSyncFails(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	syncer := &fakeSyncer{failWith: errors.New("grafana down"), done: make(chan struct{}, 1)}
	svc := New(repo, syncer, nil)

	if _, err := svc.Create(context.Background(), CreateInput{Title: "A", PromQL: "up"}); err != nil {
		t.Fatalf("Create returned err despite sync failure: %v", err)
	}
	select {
	case <-syncer.done:
	case <-time.After(2 * time.Second):
		t.Fatal("syncer never ran")
	}
	// Give the goroutine a beat to write the result row.
	time.Sleep(20 * time.Millisecond)
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.syncs) == 0 {
		t.Fatal("SetSyncResult never called")
	}
	if repo.syncs[len(repo.syncs)-1] == "" {
		t.Fatalf("expected non-empty err message, got %v", repo.syncs)
	}
}

// TestCreateNoSyncerSkipsBackground exercises the nil-syncer branch —
// no goroutine, no panic.
func TestCreateNoSyncerSkipsBackground(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	svc := New(repo, nil, nil)
	if _, err := svc.Create(context.Background(), CreateInput{Title: "A", PromQL: "up"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
}
