package edge_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	biz "github.com/ongridio/ongrid/internal/manager/biz/edge"
	store "github.com/ongridio/ongrid/internal/manager/data/edge/store"
	devicemodel "github.com/ongridio/ongrid/internal/manager/model/device"
	edgemodel "github.com/ongridio/ongrid/internal/manager/model/edge"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

type upgradeDeviceReader struct {
	rows map[uint64]*devicemodel.Device
}

func (r upgradeDeviceReader) Get(_ context.Context, id uint64) (*devicemodel.Device, error) {
	if device, ok := r.rows[id]; ok {
		return device, nil
	}
	return nil, errs.ErrNotFound
}

type upgradeResolver struct{}

type upgradeClusterValidator struct {
	err   error
	calls int
}

func (v *upgradeClusterValidator) ValidateUpgradeCluster(_ context.Context, _ uint64, _ []uint64) error {
	v.calls++
	return v.err
}

func (upgradeResolver) ResolveBundle(arch, version string) (string, string, string, error) {
	if arch != "linux-amd64" && arch != "linux-arm64" {
		return "", "", "", fmt.Errorf("unsupported arch %s", arch)
	}
	return "https://manager.example/edge/bundle.tar.gz", strings.Repeat("a", 64), version, nil
}

type upgradeDispatcher struct {
	repo *store.Repo
}

func (d upgradeDispatcher) FetchPackage(context.Context, uint64, string, string, string) (tunnel.FetchPackageResponse, error) {
	return tunnel.FetchPackageResponse{ManifestFiles: 10}, nil
}

func (d upgradeDispatcher) ApplyPackage(ctx context.Context, edgeID uint64) (tunnel.ApplyPackageResponse, error) {
	if err := d.repo.SetAgentVersion(ctx, edgeID, "v0.10.2"); err != nil {
		return tunnel.ApplyPackageResponse{}, err
	}
	if err := d.repo.MarkRegistered(ctx, edgeID, time.Now().Add(time.Second)); err != nil {
		return tunnel.ApplyPackageResponse{}, err
	}
	return tunnel.ApplyPackageResponse{Accepted: true}, nil
}

type controlledUpgradeDispatcher struct {
	mu      sync.Mutex
	applied []uint64
}

func (d *controlledUpgradeDispatcher) FetchPackage(context.Context, uint64, string, string, string) (tunnel.FetchPackageResponse, error) {
	return tunnel.FetchPackageResponse{ManifestFiles: 10}, nil
}

func (d *controlledUpgradeDispatcher) ApplyPackage(_ context.Context, edgeID uint64) (tunnel.ApplyPackageResponse, error) {
	d.mu.Lock()
	d.applied = append(d.applied, edgeID)
	d.mu.Unlock()
	return tunnel.ApplyPackageResponse{Accepted: true}, nil
}

func (d *controlledUpgradeDispatcher) appliedIDs() []uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]uint64(nil), d.applied...)
}

type fetchReconnectUpgradeDispatcher struct {
	repo *store.Repo
	mu   sync.Mutex
	at   *time.Time
}

func (d *fetchReconnectUpgradeDispatcher) FetchPackage(ctx context.Context, edgeID uint64, _ string, _ string, _ string) (tunnel.FetchPackageResponse, error) {
	registeredAt := time.Now().UTC()
	if err := d.repo.MarkRegistered(ctx, edgeID, registeredAt); err != nil {
		return tunnel.FetchPackageResponse{}, err
	}
	d.mu.Lock()
	d.at = &registeredAt
	d.mu.Unlock()
	return tunnel.FetchPackageResponse{ManifestFiles: 10}, nil
}

func (d *fetchReconnectUpgradeDispatcher) ApplyPackage(context.Context, uint64) (tunnel.ApplyPackageResponse, error) {
	return tunnel.ApplyPackageResponse{Accepted: true}, nil
}

func (d *fetchReconnectUpgradeDispatcher) registeredAt() *time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	return cloneTestTime(d.at)
}

func cloneTestTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func TestUpgradeJobRunContinuesWithoutRequestContext(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:upgrade-job-run?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := store.Migrate(db); err != nil {
		t.Fatalf("store.Migrate() error = %v", err)
	}
	repo := store.NewRepo(db)
	deviceID := uint64(501)
	registeredAt := time.Now().Add(-time.Minute)
	edge := &edgemodel.Edge{
		Name: "edge-a", AccessKeyID: "upgrade-job-edge-a", SecretKeyHash: "hash",
		Status: edgemodel.StatusOnline, DeviceID: &deviceID,
		AgentVersion: "v0.10.1", LastRegisteredAt: &registeredAt,
	}
	if err := repo.Create(context.Background(), edge); err != nil {
		t.Fatalf("repo.Create() error = %v", err)
	}
	devices := upgradeDeviceReader{rows: map[uint64]*devicemodel.Device{
		deviceID: {ID: deviceID, Name: "device-a", OS: "linux", Arch: "amd64", Online: true},
	}}
	uc := biz.NewUpgradeJobUsecase(
		repo, repo, devices, nil, upgradeDispatcher{repo: repo}, upgradeResolver{},
		biz.UpgradeJobConfig{Concurrency: 1, VerifyInterval: time.Millisecond, VerifyTimeout: time.Second}, nil,
	)
	job, err := uc.Create(context.Background(), biz.CreateUpgradeJobInput{
		EdgeIDs: []uint64{edge.ID}, TargetVersion: "v0.10.2",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		uc.Run(runCtx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("upgrade coordinator did not stop")
		}
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, items, err := uc.Get(context.Background(), job.ID)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if current.Status == edgemodel.UpgradeJobStatusSucceeded {
			if current.Succeeded != 1 || current.Pending != 0 || len(items) != 1 || items[0].Status != edgemodel.UpgradeJobItemStatusSucceeded {
				t.Fatalf("completed job=%+v items=%+v", current, items)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("upgrade job did not converge")
}

func TestUpgradeJobForceReinstallDoesNotShortCircuitQueuedConvergence(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:upgrade-job-force-reinstall?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := store.Migrate(db); err != nil {
		t.Fatalf("store.Migrate() error = %v", err)
	}
	repo := store.NewRepo(db)
	deviceID := uint64(551)
	registeredAt := time.Now().Add(-time.Minute)
	edge := &edgemodel.Edge{
		Name: "edge-force", AccessKeyID: "upgrade-job-edge-force", SecretKeyHash: "hash",
		Status: edgemodel.StatusOnline, DeviceID: &deviceID,
		AgentVersion: "v0.10.1", LastRegisteredAt: &registeredAt,
	}
	if err := repo.Create(context.Background(), edge); err != nil {
		t.Fatalf("repo.Create() error = %v", err)
	}
	devices := upgradeDeviceReader{rows: map[uint64]*devicemodel.Device{
		deviceID: {ID: deviceID, Name: "device-force", OS: "linux", Arch: "amd64", Online: true},
	}}
	dispatcher := &controlledUpgradeDispatcher{}
	uc := biz.NewUpgradeJobUsecase(
		repo, repo, devices, nil, dispatcher, upgradeResolver{},
		biz.UpgradeJobConfig{Concurrency: 1, VerifyInterval: time.Millisecond, VerifyTimeout: time.Second}, nil,
	)
	job, err := uc.Create(context.Background(), biz.CreateUpgradeJobInput{
		EdgeIDs: []uint64{edge.ID}, TargetVersion: "v0.10.2", ForceReinstall: true,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	queuedRegistrationAt := time.Now()
	if err := repo.SetAgentVersion(context.Background(), edge.ID, "v0.10.2"); err != nil {
		t.Fatalf("SetAgentVersion() error = %v", err)
	}
	if err := repo.MarkRegistered(context.Background(), edge.ID, queuedRegistrationAt); err != nil {
		t.Fatalf("MarkRegistered() error = %v", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		uc.Run(runCtx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("upgrade coordinator did not stop")
		}
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(dispatcher.appliedIDs()) == 1 {
			_, items, err := uc.Get(context.Background(), job.ID)
			if err != nil {
				t.Fatalf("Get() error = %v", err)
			}
			if len(items) != 1 || items[0].BaselineRegisteredAt == nil || !items[0].BaselineRegisteredAt.After(queuedRegistrationAt) {
				t.Fatalf("force reinstall dispatch baseline = %+v", items)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("force reinstall was short-circuited instead of being dispatched")
}

func TestUpgradeJobRefreshesBaselineAfterFetch(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:upgrade-job-fetch-reconnect?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := store.Migrate(db); err != nil {
		t.Fatalf("store.Migrate() error = %v", err)
	}
	repo := store.NewRepo(db)
	deviceID := uint64(581)
	registeredAt := time.Now().Add(-time.Minute)
	edge := &edgemodel.Edge{
		Name: "edge-fetch-reconnect", AccessKeyID: "upgrade-job-edge-fetch-reconnect", SecretKeyHash: "hash",
		Status: edgemodel.StatusOnline, DeviceID: &deviceID,
		AgentVersion: "v0.10.1", LastRegisteredAt: &registeredAt,
	}
	if err := repo.Create(context.Background(), edge); err != nil {
		t.Fatalf("repo.Create() error = %v", err)
	}
	devices := upgradeDeviceReader{rows: map[uint64]*devicemodel.Device{
		deviceID: {ID: deviceID, Name: "device-fetch-reconnect", OS: "linux", Arch: "amd64", Online: true},
	}}
	dispatcher := &fetchReconnectUpgradeDispatcher{repo: repo}
	uc := biz.NewUpgradeJobUsecase(
		repo, repo, devices, nil, dispatcher, upgradeResolver{},
		biz.UpgradeJobConfig{Concurrency: 1, VerifyInterval: time.Millisecond, VerifyTimeout: time.Second}, nil,
	)
	job, err := uc.Create(context.Background(), biz.CreateUpgradeJobInput{
		EdgeIDs: []uint64{edge.ID}, TargetVersion: "v0.10.2",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		uc.Run(runCtx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("upgrade coordinator did not stop")
		}
	})

	waitingObserved := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, items, err := uc.Get(context.Background(), job.ID)
		if err != nil {
			t.Fatalf("Get(waiting) error = %v", err)
		}
		if len(items) != 1 {
			t.Fatalf("Get(waiting) items = %+v", items)
		}
		if items[0].Status == edgemodel.UpgradeJobItemStatusFailed {
			t.Fatalf("registration during FetchPackage was treated as an upgrade result: %+v", items[0])
		}
		if items[0].Status == edgemodel.UpgradeJobItemStatusWaitingRegistration {
			fetchRegisteredAt := dispatcher.registeredAt()
			if fetchRegisteredAt == nil || items[0].BaselineRegisteredAt == nil || !items[0].BaselineRegisteredAt.After(*fetchRegisteredAt) {
				t.Fatalf("pre-apply baseline did not advance past fetch registration: fetch=%v item=%+v", fetchRegisteredAt, items[0])
			}
			waitingObserved = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !waitingObserved {
		t.Fatal("upgrade item did not enter registration verification")
	}

	if err := repo.SetAgentVersion(context.Background(), edge.ID, "v0.10.2"); err != nil {
		t.Fatalf("SetAgentVersion() error = %v", err)
	}
	if err := repo.MarkRegistered(context.Background(), edge.ID, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("MarkRegistered(target) error = %v", err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, items, err := uc.Get(context.Background(), job.ID)
		if err != nil {
			t.Fatalf("Get(completed) error = %v", err)
		}
		if current.Status == edgemodel.UpgradeJobStatusSucceeded {
			if len(items) != 1 || items[0].Status != edgemodel.UpgradeJobItemStatusSucceeded {
				t.Fatalf("completed job=%+v items=%+v", current, items)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("upgrade job did not converge after target registration")
}

func TestRetryUpgradeJobRejectsDeviceRemovedFromCluster(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:upgrade-job-retry-cluster?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := store.Migrate(db); err != nil {
		t.Fatalf("store.Migrate() error = %v", err)
	}
	repo := store.NewRepo(db)
	deviceID, nodeID, clusterID := uint64(701), uint64(1701), uint64(101)
	baseline := time.Now().Add(-time.Minute)
	edge := &edgemodel.Edge{
		Name: "edge-retry", AccessKeyID: "upgrade-job-retry-cluster", SecretKeyHash: "hash",
		Status: edgemodel.StatusOnline, DeviceID: &deviceID,
		AgentVersion: "v0.10.1", LastRegisteredAt: &baseline,
	}
	if err := repo.Create(context.Background(), edge); err != nil {
		t.Fatalf("repo.Create(edge) error = %v", err)
	}
	devices := upgradeDeviceReader{rows: map[uint64]*devicemodel.Device{
		deviceID: {ID: deviceID, NodeID: &nodeID, Name: "device-retry", OS: "linux", Arch: "amd64", Online: true},
	}}
	validator := &upgradeClusterValidator{}
	uc := biz.NewUpgradeJobUsecase(
		repo, repo, devices, validator, &controlledUpgradeDispatcher{}, upgradeResolver{},
		biz.UpgradeJobConfig{}, nil,
	)
	job, err := uc.Create(context.Background(), biz.CreateUpgradeJobInput{
		ClusterNodeID: &clusterID, EdgeIDs: []uint64{edge.ID}, TargetVersion: "v0.10.2",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, items, err := uc.Get(context.Background(), job.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("Get() items = %+v, error = %v", items, err)
	}
	if err := repo.MarkUpgradeItemDispatching(context.Background(), items[0].ID, &baseline, time.Now()); err != nil {
		t.Fatalf("MarkUpgradeItemDispatching() error = %v", err)
	}
	if err := repo.MarkUpgradeItemFailed(context.Background(), items[0].ID, edgemodel.UpgradeJobItemStatusFailed,
		"fetch_failed", "offline", "", nil, time.Now()); err != nil {
		t.Fatalf("MarkUpgradeItemFailed() error = %v", err)
	}
	if _, err := repo.RefreshUpgradeJob(context.Background(), job.ID, time.Now()); err != nil {
		t.Fatalf("RefreshUpgradeJob() error = %v", err)
	}

	validator.err = errs.ErrConflict
	if _, err := uc.Retry(context.Background(), job.ID); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("Retry() error = %v, want ErrConflict", err)
	}
	if validator.calls != 2 {
		t.Fatalf("ValidateUpgradeCluster calls = %d, want 2", validator.calls)
	}
	_, items, err = uc.Get(context.Background(), job.ID)
	if err != nil || items[0].Status != edgemodel.UpgradeJobItemStatusFailed {
		t.Fatalf("retry rejection changed item = %+v, error = %v", items, err)
	}
}

func TestUpgradeJobRunWaitsForCurrentBatchBeforeDispatchingNext(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:upgrade-job-batches?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := store.Migrate(db); err != nil {
		t.Fatalf("store.Migrate() error = %v", err)
	}
	repo := store.NewRepo(db)
	devices := upgradeDeviceReader{rows: make(map[uint64]*devicemodel.Device)}
	edges := make([]*edgemodel.Edge, 0, 3)
	baseline := time.Now().Add(-time.Minute)
	for i := 0; i < 3; i++ {
		deviceID := uint64(601 + i)
		edge := &edgemodel.Edge{
			Name: fmt.Sprintf("edge-%d", i+1), AccessKeyID: fmt.Sprintf("upgrade-job-batch-%d", i+1), SecretKeyHash: "hash",
			Status: edgemodel.StatusOnline, DeviceID: &deviceID,
			AgentVersion: "v0.10.1", LastRegisteredAt: &baseline,
		}
		if err := repo.Create(context.Background(), edge); err != nil {
			t.Fatalf("repo.Create(edge %d) error = %v", i+1, err)
		}
		edges = append(edges, edge)
		devices.rows[deviceID] = &devicemodel.Device{
			ID: deviceID, Name: fmt.Sprintf("device-%d", i+1), OS: "linux", Arch: "amd64", Online: true,
		}
	}
	dispatcher := &controlledUpgradeDispatcher{}
	uc := biz.NewUpgradeJobUsecase(
		repo, repo, devices, nil, dispatcher, upgradeResolver{},
		biz.UpgradeJobConfig{BatchSize: 2, Concurrency: 1, VerifyInterval: 2 * time.Millisecond, VerifyTimeout: time.Second}, nil,
	)
	job, err := uc.Create(context.Background(), biz.CreateUpgradeJobInput{
		EdgeIDs: []uint64{edges[0].ID, edges[1].ID, edges[2].ID}, TargetVersion: "v0.10.2",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if job.BatchSize != 2 || job.TotalBatches != 2 {
		t.Fatalf("created job batch metadata = %+v", job)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		uc.Run(runCtx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("upgrade coordinator did not stop")
		}
	})

	waitForApplied := func(want int) []uint64 {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			ids := dispatcher.appliedIDs()
			if len(ids) >= want {
				return ids
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %d dispatches; got %v", want, dispatcher.appliedIDs())
		return nil
	}

	firstBatch := waitForApplied(2)
	time.Sleep(25 * time.Millisecond)
	if got := dispatcher.appliedIDs(); len(got) != 2 {
		t.Fatalf("next batch dispatched before current batch converged: %v", got)
	}
	queuedRegistrationAt := time.Now()
	if err := repo.MarkRegistered(context.Background(), edges[2].ID, queuedRegistrationAt); err != nil {
		t.Fatalf("MarkRegistered(second batch while queued) error = %v", err)
	}
	current, items, err := uc.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get(current batch) error = %v", err)
	}
	if current.CurrentBatch != 1 || len(items) != 3 || items[0].BatchNumber != 1 || items[1].BatchNumber != 1 || items[2].BatchNumber != 2 {
		t.Fatalf("current job=%+v items=%+v", current, items)
	}
	for _, edgeID := range firstBatch {
		if err := repo.SetAgentVersion(context.Background(), edgeID, "v0.10.2"); err != nil {
			t.Fatalf("SetAgentVersion(first batch edge %d) error = %v", edgeID, err)
		}
		if err := repo.MarkRegistered(context.Background(), edgeID, time.Now().Add(time.Second)); err != nil {
			t.Fatalf("MarkRegistered(first batch edge %d) error = %v", edgeID, err)
		}
	}

	allApplied := waitForApplied(3)
	lastEdgeID := allApplied[2]
	time.Sleep(25 * time.Millisecond)
	current, items, err = uc.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get(second batch waiting) error = %v", err)
	}
	if items[2].Status != edgemodel.UpgradeJobItemStatusWaitingRegistration ||
		items[2].BaselineRegisteredAt == nil || !items[2].BaselineRegisteredAt.After(queuedRegistrationAt) {
		t.Fatalf("queued registration was treated as an upgrade result: job=%+v item=%+v", current, items[2])
	}
	if err := repo.SetAgentVersion(context.Background(), lastEdgeID, "v0.10.2"); err != nil {
		t.Fatalf("SetAgentVersion(second batch) error = %v", err)
	}
	if err := repo.MarkRegistered(context.Background(), lastEdgeID, time.Now().Add(2*time.Second)); err != nil {
		t.Fatalf("MarkRegistered(second batch) error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, items, err = uc.Get(context.Background(), job.ID)
		if err != nil {
			t.Fatalf("Get(completed job) error = %v", err)
		}
		if current.Status == edgemodel.UpgradeJobStatusSucceeded {
			if current.CurrentBatch != 2 || current.TotalBatches != 2 || current.Succeeded != 3 || current.Pending != 0 {
				t.Fatalf("completed job=%+v items=%+v", current, items)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("upgrade job did not complete: applied=%v", dispatcher.appliedIDs())
}
