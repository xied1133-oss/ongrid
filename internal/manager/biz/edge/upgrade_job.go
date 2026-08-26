package edge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	devicemodel "github.com/ongridio/ongrid/internal/manager/model/device"
	model "github.com/ongridio/ongrid/internal/manager/model/edge"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

const maxUpgradeJobEdges = 500

type UpgradePackageResolver interface {
	ResolveBundle(arch, version string) (url, sha256, resolvedVersion string, err error)
}

type UpgradePackageDispatcher interface {
	FetchPackage(ctx context.Context, edgeID uint64, url, sha256, version string) (tunnel.FetchPackageResponse, error)
	ApplyPackage(ctx context.Context, edgeID uint64) (tunnel.ApplyPackageResponse, error)
}

type UpgradeDeviceReader interface {
	Get(ctx context.Context, id uint64) (*devicemodel.Device, error)
}

type UpgradeClusterValidator interface {
	ValidateUpgradeCluster(ctx context.Context, clusterNodeID uint64, deviceNodeIDs []uint64) error
}

type UpgradeJobConfig struct {
	BatchSize       int
	Concurrency     int
	VerifyInterval  time.Duration
	VerifyTimeout   time.Duration
	DispatchTimeout time.Duration
	Retention       time.Duration
}

type CreateUpgradeJobInput struct {
	ClusterNodeID  *uint64
	EdgeIDs        []uint64
	TargetVersion  string
	ForceReinstall bool
	CreatedBy      *uint64
}

// UpgradeJobUsecase owns the persistent rollout state machine. HTTP requests
// only create or inspect records; Run continues dispatch and verification with
// the Manager's lifecycle context after the browser disconnects.
type UpgradeJobUsecase struct {
	repo       UpgradeJobRepo
	edges      UpgradeEdgeReader
	devices    UpgradeDeviceReader
	clusters   UpgradeClusterValidator
	dispatcher UpgradePackageDispatcher
	resolver   UpgradePackageResolver
	config     UpgradeJobConfig
	log        *slog.Logger
	wake       chan struct{}
}

func NewUpgradeJobUsecase(
	repo UpgradeJobRepo,
	edges UpgradeEdgeReader,
	devices UpgradeDeviceReader,
	clusters UpgradeClusterValidator,
	dispatcher UpgradePackageDispatcher,
	resolver UpgradePackageResolver,
	config UpgradeJobConfig,
	log *slog.Logger,
) *UpgradeJobUsecase {
	if config.BatchSize <= 0 {
		config.BatchSize = model.DefaultUpgradeJobBatchSize
	}
	if config.Concurrency <= 0 {
		config.Concurrency = 8
	}
	if config.VerifyInterval <= 0 {
		config.VerifyInterval = 2 * time.Second
	}
	if config.VerifyTimeout <= 0 {
		config.VerifyTimeout = 90 * time.Second
	}
	if config.DispatchTimeout <= 0 {
		config.DispatchTimeout = 5 * time.Minute
	}
	if config.Retention <= 0 {
		config.Retention = 90 * 24 * time.Hour
	}
	return &UpgradeJobUsecase{
		repo: repo, edges: edges, devices: devices, clusters: clusters,
		dispatcher: dispatcher, resolver: resolver, config: config, log: log,
		wake: make(chan struct{}, 1),
	}
}

func (u *UpgradeJobUsecase) Create(ctx context.Context, in CreateUpgradeJobInput) (*model.UpgradeJob, error) {
	if u == nil || u.repo == nil || u.edges == nil || u.devices == nil || u.dispatcher == nil || u.resolver == nil {
		return nil, errs.ErrNotWiredYet
	}
	ids, err := normalizeUpgradeJobEdgeIDs(in.EdgeIDs)
	if err != nil {
		return nil, err
	}
	targetVersion := strings.TrimSpace(in.TargetVersion)
	if targetVersion == "" || len(targetVersion) > 32 {
		return nil, fmt.Errorf("%w: target_version is required", errs.ErrInvalid)
	}
	if in.ClusterNodeID != nil && *in.ClusterNodeID == 0 {
		return nil, fmt.Errorf("%w: cluster_node_id must be positive", errs.ErrInvalid)
	}

	items := make([]*model.UpgradeJobItem, 0, len(ids))
	deviceNodeIDs := make([]uint64, 0, len(ids))
	archSet := make(map[string]struct{})
	for _, edgeID := range ids {
		edge, err := u.edges.GetByID(ctx, edgeID)
		if err != nil {
			return nil, fmt.Errorf("get edge %d: %w", edgeID, err)
		}
		if edge.DeviceID == nil || *edge.DeviceID == 0 {
			return nil, fmt.Errorf("%w: edge %d is not linked to a host device", errs.ErrInvalid, edgeID)
		}
		device, err := u.devices.Get(ctx, *edge.DeviceID)
		if err != nil {
			return nil, fmt.Errorf("get device %d for edge %d: %w", *edge.DeviceID, edgeID, err)
		}
		if in.ClusterNodeID != nil {
			if device.NodeID == nil || *device.NodeID == 0 {
				return nil, fmt.Errorf("%w: device %d has no topology node", errs.ErrInvalid, device.ID)
			}
			deviceNodeIDs = append(deviceNodeIDs, *device.NodeID)
		}

		arch := normalizeUpgradeArch(device.OS, device.Arch)
		status, code, message := model.UpgradeJobItemStatusQueued, "", ""
		switch {
		case edge.Status != model.StatusOnline || !device.Online:
			status, code, message = model.UpgradeJobItemStatusSkipped, "edge_offline", "device or host edge is offline"
		case arch == "":
			status, code, message = model.UpgradeJobItemStatusSkipped, "unsupported_arch", "only linux-amd64 and linux-arm64 are supported"
		case !in.ForceReinstall && upgradeVersionsEqual(edge.AgentVersion, targetVersion):
			status, code, message = model.UpgradeJobItemStatusSkipped, "already_current", "device already reports the target version"
		default:
			archSet[arch] = struct{}{}
		}
		deviceName := strings.TrimSpace(device.Name)
		if deviceName == "" {
			deviceName = strings.TrimSpace(device.Hostname)
		}
		items = append(items, &model.UpgradeJobItem{
			EdgeID: edge.ID, DeviceID: edge.DeviceID, EdgeName: edge.Name,
			DeviceName: deviceName, Arch: arch, FromVersion: edge.AgentVersion,
			TargetVersion: targetVersion, Status: status, ErrorCode: code,
			ErrorMessage: message, BaselineRegisteredAt: cloneTime(edge.LastRegisteredAt),
		})
	}

	if in.ClusterNodeID != nil {
		if u.clusters == nil {
			return nil, errs.ErrNotWiredYet
		}
		if err := u.clusters.ValidateUpgradeCluster(ctx, *in.ClusterNodeID, deviceNodeIDs); err != nil {
			return nil, fmt.Errorf("validate upgrade cluster: %w", err)
		}
	}
	for arch := range archSet {
		_, _, resolvedVersion, err := u.resolver.ResolveBundle(arch, targetVersion)
		if err != nil {
			return nil, fmt.Errorf("%w: resolve %s bundle: %v", errs.ErrInvalid, arch, err)
		}
		if !upgradeVersionsEqual(resolvedVersion, targetVersion) {
			return nil, fmt.Errorf("%w: %s bundle resolved to %s instead of %s", errs.ErrInvalid, arch, resolvedVersion, targetVersion)
		}
	}

	job := &model.UpgradeJob{
		ClusterNodeID: in.ClusterNodeID, TargetVersion: targetVersion,
		ForceReinstall: in.ForceReinstall, Status: model.UpgradeJobStatusQueued,
		BatchSize: configBatchSize(u.config), CreatedBy: in.CreatedBy,
	}
	if err := u.repo.CreateUpgradeJob(ctx, job, items); err != nil {
		return nil, fmt.Errorf("create upgrade job: %w", err)
	}
	if job.Pending > 0 {
		u.notify()
	}
	return job, nil
}

func (u *UpgradeJobUsecase) List(ctx context.Context, filter UpgradeJobListFilter) ([]*model.UpgradeJob, int64, error) {
	if u == nil || u.repo == nil {
		return nil, 0, errs.ErrNotWiredYet
	}
	return u.repo.ListUpgradeJobs(ctx, filter)
}

func (u *UpgradeJobUsecase) Get(ctx context.Context, id uint64) (*model.UpgradeJob, []*model.UpgradeJobItem, error) {
	if u == nil || u.repo == nil {
		return nil, nil, errs.ErrNotWiredYet
	}
	if id == 0 {
		return nil, nil, errs.ErrInvalid
	}
	return u.repo.GetUpgradeJob(ctx, id)
}

func (u *UpgradeJobUsecase) Retry(ctx context.Context, id uint64) (*model.UpgradeJob, error) {
	if u == nil || u.repo == nil || u.edges == nil {
		return nil, errs.ErrNotWiredYet
	}
	job, items, err := u.repo.GetUpgradeJob(ctx, id)
	if err != nil {
		return nil, err
	}
	if job.Status == model.UpgradeJobStatusQueued || job.Status == model.UpgradeJobStatusRunning {
		return nil, errs.ErrConflict
	}
	snapshots := make([]UpgradeRetrySnapshot, 0)
	deviceNodeIDs := make([]uint64, 0)
	for _, item := range items {
		if item.Status != model.UpgradeJobItemStatusFailed && item.Status != model.UpgradeJobItemStatusTimedOut {
			continue
		}
		edge, err := u.edges.GetByID(ctx, item.EdgeID)
		if err != nil {
			return nil, fmt.Errorf("get edge %d for retry: %w", item.EdgeID, err)
		}
		if job.ClusterNodeID != nil {
			if u.devices == nil || u.clusters == nil {
				return nil, errs.ErrNotWiredYet
			}
			if edge.DeviceID == nil || *edge.DeviceID == 0 {
				return nil, fmt.Errorf("%w: edge %d is not linked to a host device", errs.ErrInvalid, edge.ID)
			}
			device, err := u.devices.Get(ctx, *edge.DeviceID)
			if err != nil {
				return nil, fmt.Errorf("get device %d for retry edge %d: %w", *edge.DeviceID, item.EdgeID, err)
			}
			if device.NodeID == nil || *device.NodeID == 0 {
				return nil, fmt.Errorf("%w: device %d has no topology node", errs.ErrInvalid, device.ID)
			}
			deviceNodeIDs = append(deviceNodeIDs, *device.NodeID)
		}
		snapshots = append(snapshots, UpgradeRetrySnapshot{
			EdgeID: item.EdgeID, FromVersion: edge.AgentVersion,
			BaselineRegisteredAt: cloneTime(edge.LastRegisteredAt),
		})
	}
	if len(snapshots) == 0 {
		return nil, errs.ErrConflict
	}
	if job.ClusterNodeID != nil {
		if err := u.clusters.ValidateUpgradeCluster(ctx, *job.ClusterNodeID, deviceNodeIDs); err != nil {
			return nil, fmt.Errorf("validate retry upgrade cluster: %w", err)
		}
	}
	job, err = u.repo.RetryUpgradeJob(ctx, id, snapshots, time.Now())
	if err != nil {
		return nil, err
	}
	u.notify()
	return job, nil
}

// UpgradeJobClusterDeleteGuard blocks cluster deletion while its background
// rollout can still dispatch or verify devices.
type UpgradeJobClusterDeleteGuard struct {
	repo UpgradeJobRepo
}

func NewUpgradeJobClusterDeleteGuard(repo UpgradeJobRepo) *UpgradeJobClusterDeleteGuard {
	return &UpgradeJobClusterDeleteGuard{repo: repo}
}

func (g *UpgradeJobClusterDeleteGuard) ValidateClusterDelete(ctx context.Context, clusterNodeID uint64) error {
	if g == nil || g.repo == nil {
		return errs.ErrNotWiredYet
	}
	if clusterNodeID == 0 {
		return errs.ErrInvalid
	}
	count, err := g.repo.CountActiveUpgradeJobsForCluster(ctx, clusterNodeID)
	if err != nil {
		return fmt.Errorf("count active upgrade jobs for cluster: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("%w: cluster %d still has %d active upgrade job(s)", errs.ErrConflict, clusterNodeID, count)
	}
	return nil
}

// Run recovers interrupted work, then drains queued jobs until ctx is
// cancelled. It is intended to run exactly once from the Manager root context.
func (u *UpgradeJobUsecase) Run(ctx context.Context) {
	defer func() {
		if recovered := recover(); recovered != nil && u.log != nil {
			u.log.Error("edge upgrade coordinator panic",
				slog.Any("panic", recovered), slog.String("stack", string(debug.Stack())))
		}
	}()
	if u == nil || u.repo == nil {
		return
	}
	if err := u.repo.RecoverUpgradeJobs(ctx); err != nil {
		if u.log != nil {
			u.log.Error("recover edge upgrade jobs", slog.Any("err", err))
		}
		return
	}
	u.cleanup(ctx)
	poll := time.NewTicker(5 * time.Second)
	defer poll.Stop()
	cleanup := time.NewTicker(24 * time.Hour)
	defer cleanup.Stop()

	for {
		if err := u.drain(ctx); err != nil && !errors.Is(err, context.Canceled) && u.log != nil {
			u.log.Error("process edge upgrade jobs", slog.Any("err", err))
		}
		select {
		case <-ctx.Done():
			return
		case <-u.wake:
		case <-poll.C:
		case <-cleanup.C:
			u.cleanup(ctx)
		}
	}
}

func (u *UpgradeJobUsecase) drain(ctx context.Context) error {
	for ctx.Err() == nil {
		job, err := u.repo.ClaimNextUpgradeJob(ctx, time.Now())
		if errors.Is(err, errs.ErrNotFound) || errors.Is(err, errs.ErrConflict) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := u.processJob(ctx, job); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			if requeueErr := u.repo.RequeueUpgradeJob(ctx, job.ID, time.Now()); requeueErr != nil && u.log != nil {
				u.log.Error("requeue edge upgrade job", slog.Uint64("job_id", job.ID), slog.Any("err", requeueErr))
			}
			if u.log != nil {
				u.log.Error("process edge upgrade job", slog.Uint64("job_id", job.ID), slog.Any("err", err))
			}
			return err
		}
	}
	return ctx.Err()
}

func (u *UpgradeJobUsecase) processJob(ctx context.Context, job *model.UpgradeJob) error {
	for ctx.Err() == nil {
		waiting, err := u.repo.ListUpgradeJobItems(ctx, job.ID, model.UpgradeJobItemStatusWaitingRegistration)
		if err != nil {
			return fmt.Errorf("list waiting upgrade items: %w", err)
		}
		if len(waiting) > 0 {
			batchNumber, batch := nextUpgradeBatch(waiting)
			if err := u.startBatch(ctx, job, batchNumber); err != nil {
				return err
			}
			if err := u.verifyWaiting(ctx, job.ID, batchNumber); err != nil {
				return err
			}
			if _, err := u.repo.RefreshUpgradeJob(ctx, job.ID, time.Now()); err != nil {
				return fmt.Errorf("refresh upgrade job batch %d: %w", batchNumber, err)
			}
			if len(batch) == 0 {
				return fmt.Errorf("upgrade batch %d has no waiting items", batchNumber)
			}
			continue
		}

		queued, err := u.repo.ListUpgradeJobItems(ctx, job.ID, model.UpgradeJobItemStatusQueued)
		if err != nil {
			return fmt.Errorf("list queued upgrade items: %w", err)
		}
		if len(queued) == 0 {
			if _, err := u.repo.RefreshUpgradeJob(ctx, job.ID, time.Now()); err != nil {
				return fmt.Errorf("finish upgrade job: %w", err)
			}
			return nil
		}

		batchNumber, batch := nextUpgradeBatch(queued)
		if err := u.startBatch(ctx, job, batchNumber); err != nil {
			return err
		}
		u.dispatchQueued(ctx, batch, job.ForceReinstall)
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining, err := u.repo.ListUpgradeJobItems(ctx, job.ID, model.UpgradeJobItemStatusQueued)
		if err != nil {
			return fmt.Errorf("recheck queued upgrade items: %w", err)
		}
		if countUpgradeBatch(remaining, batchNumber) > 0 {
			return fmt.Errorf("upgrade batch %d still has queued items after dispatch", batchNumber)
		}
		stranded, err := u.repo.ListUpgradeJobItems(ctx, job.ID, model.UpgradeJobItemStatusDispatching)
		if err != nil {
			return fmt.Errorf("recheck dispatching upgrade items: %w", err)
		}
		if countUpgradeBatch(stranded, batchNumber) > 0 {
			return fmt.Errorf("upgrade batch %d still has dispatching items after dispatch", batchNumber)
		}
		if err := u.verifyWaiting(ctx, job.ID, batchNumber); err != nil {
			return err
		}
		if _, err := u.repo.RefreshUpgradeJob(ctx, job.ID, time.Now()); err != nil {
			return fmt.Errorf("refresh upgrade job batch %d: %w", batchNumber, err)
		}
	}
	return ctx.Err()
}

func (u *UpgradeJobUsecase) startBatch(ctx context.Context, job *model.UpgradeJob, batchNumber int) error {
	if batchNumber <= 0 {
		return fmt.Errorf("invalid upgrade batch number %d", batchNumber)
	}
	if err := u.repo.SetUpgradeJobCurrentBatch(ctx, job.ID, batchNumber, time.Now()); err != nil {
		return fmt.Errorf("start upgrade batch %d: %w", batchNumber, err)
	}
	job.CurrentBatch = batchNumber
	return nil
}

func (u *UpgradeJobUsecase) dispatchQueued(ctx context.Context, items []*model.UpgradeJobItem, forceReinstall bool) {
	if len(items) == 0 {
		return
	}
	workers := u.config.Concurrency
	if workers > len(items) {
		workers = len(items)
	}
	work := make(chan *model.UpgradeJobItem, len(items))
	for _, item := range items {
		work <- item
	}
	close(work)

	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if recovered := recover(); recovered != nil && u.log != nil {
					u.log.Error("edge upgrade worker panic",
						slog.Any("panic", recovered), slog.String("stack", string(debug.Stack())))
				}
			}()
			for item := range work {
				if ctx.Err() != nil {
					return
				}
				u.dispatchItemSafely(ctx, item, forceReinstall)
			}
		}()
	}
	wg.Wait()
}

func (u *UpgradeJobUsecase) dispatchItemSafely(ctx context.Context, item *model.UpgradeJobItem, forceReinstall bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			u.failDispatch(ctx, item, "worker_panic", fmt.Sprintf("upgrade worker panic: %v", recovered))
			if u.log != nil {
				u.log.Error("edge upgrade item panic", slog.Uint64("item_id", item.ID),
					slog.Any("panic", recovered), slog.String("stack", string(debug.Stack())))
			}
		}
	}()
	u.dispatchItem(ctx, item, forceReinstall)
}

func (u *UpgradeJobUsecase) dispatchItem(ctx context.Context, item *model.UpgradeJobItem, forceReinstall bool) {
	current, err := u.edges.GetByID(ctx, item.EdgeID)
	if err != nil {
		u.failDispatch(ctx, item, "edge_state_unavailable", fmt.Sprintf("read edge state before dispatch: %v", err))
		return
	}
	converged := hasConverged(item.BaselineRegisteredAt, current.LastRegisteredAt, current.AgentVersion, item.TargetVersion)
	recovered := item.Attempt > 0 && converged
	alreadyConverged := !forceReinstall && converged
	// Later batches may wait long enough for an unrelated re-registration. Use
	// the state immediately before dispatch as the verification baseline, while
	// preserving an interrupted attempt's baseline so recovery stays idempotent.
	baseline := cloneTime(current.LastRegisteredAt)
	if recovered || alreadyConverged {
		baseline = cloneTime(item.BaselineRegisteredAt)
	}
	now := time.Now()
	if err := u.repo.MarkUpgradeItemDispatching(ctx, item.ID, baseline, now); err != nil {
		return
	}
	if recovered || alreadyConverged {
		deadline := now.Add(u.config.VerifyTimeout)
		if err := u.repo.MarkUpgradeItemWaiting(ctx, item.ID, deadline); err != nil {
			if u.log != nil {
				u.log.Warn("mark recovered edge upgrade waiting", slog.Uint64("item_id", item.ID), slog.Any("err", err))
			}
		} else if err := u.repo.MarkUpgradeItemSucceeded(ctx, item.ID, current.AgentVersion, cloneTime(current.LastRegisteredAt), time.Now()); err != nil && u.log != nil {
			u.log.Warn("mark recovered edge upgrade succeeded", slog.Uint64("item_id", item.ID), slog.Any("err", err))
		}
		return
	}
	url, sha, resolvedVersion, err := u.resolver.ResolveBundle(item.Arch, item.TargetVersion)
	if err != nil {
		u.failDispatch(ctx, item, "bundle_unavailable", fmt.Sprintf("resolve bundle: %v", err))
		return
	}
	if !upgradeVersionsEqual(resolvedVersion, item.TargetVersion) {
		u.failDispatch(ctx, item, "bundle_version_mismatch", fmt.Sprintf("resolved bundle version %s", resolvedVersion))
		return
	}
	dispatchCtx, cancel := context.WithTimeout(ctx, u.config.DispatchTimeout)
	defer cancel()
	if _, err := u.dispatcher.FetchPackage(dispatchCtx, item.EdgeID, url, sha, resolvedVersion); err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return
		}
		u.failDispatch(ctx, item, "fetch_failed", fmt.Sprintf("fetch package: %v", err))
		return
	}
	// FetchPackage may spend minutes downloading and verifying the bundle while
	// the existing agent remains active. Exclude any registration observed in
	// that staging window before ApplyPackage asks the agent to restart.
	if err := u.repo.RefreshUpgradeItemBaseline(ctx, item.ID, time.Now().UTC()); err != nil {
		if u.log != nil {
			u.log.Warn("refresh edge upgrade baseline before apply", slog.Uint64("item_id", item.ID), slog.Any("err", err))
		}
		return
	}
	apply, err := u.dispatcher.ApplyPackage(dispatchCtx, item.EdgeID)
	if err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return
		}
		u.failDispatch(ctx, item, "apply_failed", fmt.Sprintf("apply package: %v", err))
		return
	}
	if !apply.Accepted {
		u.failDispatch(ctx, item, "apply_rejected", "edge did not accept package activation")
		return
	}
	if err := u.repo.MarkUpgradeItemWaiting(ctx, item.ID, time.Now().Add(u.config.VerifyTimeout)); err != nil && u.log != nil {
		u.log.Warn("mark edge upgrade waiting", slog.Uint64("item_id", item.ID), slog.Any("err", err))
	}
}

func (u *UpgradeJobUsecase) failDispatch(ctx context.Context, item *model.UpgradeJobItem, code, message string) {
	if err := u.repo.MarkUpgradeItemFailed(ctx, item.ID, model.UpgradeJobItemStatusFailed,
		code, truncateUpgradeError(message), "", nil, time.Now()); err != nil && u.log != nil {
		u.log.Warn("mark edge upgrade failed", slog.Uint64("item_id", item.ID), slog.Any("err", err))
	}
}

func (u *UpgradeJobUsecase) verifyWaiting(ctx context.Context, jobID uint64, batchNumber int) error {
	for {
		waiting, err := u.repo.ListUpgradeJobItems(ctx, jobID, model.UpgradeJobItemStatusWaitingRegistration)
		if err != nil {
			return fmt.Errorf("list waiting upgrade items: %w", err)
		}
		items := filterUpgradeBatch(waiting, batchNumber)
		if len(items) == 0 {
			return nil
		}
		ids := make([]uint64, 0, len(items))
		for _, item := range items {
			ids = append(ids, item.EdgeID)
		}
		edges, err := u.edges.GetManyByIDs(ctx, ids)
		if err != nil {
			return fmt.Errorf("read upgrade edge states: %w", err)
		}
		now := time.Now()
		for _, item := range items {
			edge := edges[item.EdgeID]
			if edge != nil && isNewRegistration(item.BaselineRegisteredAt, edge.LastRegisteredAt) {
				if upgradeVersionsEqual(edge.AgentVersion, item.TargetVersion) && edge.Status == model.StatusOnline {
					if err := u.repo.MarkUpgradeItemSucceeded(ctx, item.ID, edge.AgentVersion, cloneTime(edge.LastRegisteredAt), now); err != nil && !errors.Is(err, errs.ErrConflict) {
						return err
					}
					continue
				}
				message := fmt.Sprintf("edge re-registered with version %q; target is %q", edge.AgentVersion, item.TargetVersion)
				if err := u.repo.MarkUpgradeItemFailed(ctx, item.ID, model.UpgradeJobItemStatusFailed,
					"version_mismatch", message, edge.AgentVersion, cloneTime(edge.LastRegisteredAt), now); err != nil && !errors.Is(err, errs.ErrConflict) {
					return err
				}
				continue
			}
			if item.VerificationDeadlineAt != nil && !now.Before(*item.VerificationDeadlineAt) {
				if err := u.repo.MarkUpgradeItemFailed(ctx, item.ID, model.UpgradeJobItemStatusTimedOut,
					"verification_timeout", "device did not re-register with the target version before the deadline", "", nil, now); err != nil && !errors.Is(err, errs.ErrConflict) {
					return err
				}
			}
		}
		if _, err := u.repo.RefreshUpgradeJob(ctx, jobID, now); err != nil {
			return err
		}
		timer := time.NewTimer(u.config.VerifyInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func nextUpgradeBatch(items []*model.UpgradeJobItem) (int, []*model.UpgradeJobItem) {
	batchNumber := 0
	for _, item := range items {
		number := item.BatchNumber
		if number <= 0 {
			number = 1
		}
		if batchNumber == 0 || number < batchNumber {
			batchNumber = number
		}
	}
	return batchNumber, filterUpgradeBatch(items, batchNumber)
}

func filterUpgradeBatch(items []*model.UpgradeJobItem, batchNumber int) []*model.UpgradeJobItem {
	out := make([]*model.UpgradeJobItem, 0, len(items))
	for _, item := range items {
		number := item.BatchNumber
		if number <= 0 {
			number = 1
		}
		if number == batchNumber {
			out = append(out, item)
		}
	}
	return out
}

func countUpgradeBatch(items []*model.UpgradeJobItem, batchNumber int) int {
	return len(filterUpgradeBatch(items, batchNumber))
}

func configBatchSize(config UpgradeJobConfig) int {
	if config.BatchSize <= 0 {
		return model.DefaultUpgradeJobBatchSize
	}
	return config.BatchSize
}

func (u *UpgradeJobUsecase) cleanup(ctx context.Context) {
	deleted, err := u.repo.DeleteFinishedUpgradeJobsBefore(ctx, time.Now().Add(-u.config.Retention))
	if err != nil {
		if u.log != nil {
			u.log.Warn("clean old edge upgrade jobs", slog.Any("err", err))
		}
		return
	}
	if deleted > 0 && u.log != nil {
		u.log.Info("cleaned old edge upgrade jobs", slog.Int64("count", deleted))
	}
}

func (u *UpgradeJobUsecase) notify() {
	select {
	case u.wake <- struct{}{}:
	default:
	}
}

func normalizeUpgradeJobEdgeIDs(ids []uint64) ([]uint64, error) {
	if len(ids) == 0 || len(ids) > maxUpgradeJobEdges {
		return nil, fmt.Errorf("%w: edge_ids must contain 1-%d entries", errs.ErrInvalid, maxUpgradeJobEdges)
	}
	seen := make(map[uint64]struct{}, len(ids))
	out := make([]uint64, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			return nil, fmt.Errorf("%w: edge_ids must be positive", errs.ErrInvalid)
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func normalizeUpgradeArch(osName, arch string) string {
	osName = strings.ToLower(strings.TrimSpace(osName))
	if osName != "" && osName != "linux" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(arch)) {
	case "amd64", "x86_64", "x64", "linux-amd64", "linux/amd64":
		return "linux-amd64"
	case "arm64", "aarch64", "linux-arm64", "linux/arm64":
		return "linux-arm64"
	default:
		return ""
	}
}

func upgradeVersionsEqual(left, right string) bool {
	normalize := func(value string) string {
		value = strings.ToLower(strings.TrimSpace(value))
		if len(value) > 1 && value[0] == 'v' && value[1] >= '0' && value[1] <= '9' {
			return value[1:]
		}
		return value
	}
	left, right = normalize(left), normalize(right)
	return left != "" && left == right
}

func isNewRegistration(previous, current *time.Time) bool {
	if current == nil {
		return false
	}
	return previous == nil || current.After(*previous)
}

func hasConverged(previous, current *time.Time, actualVersion, targetVersion string) bool {
	return isNewRegistration(previous, current) && upgradeVersionsEqual(actualVersion, targetVersion)
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func truncateUpgradeError(message string) string {
	message = strings.TrimSpace(message)
	if len(message) <= 1024 {
		return message
	}
	return message[:1024]
}
