package store

import (
	"context"
	"testing"
	"time"

	biz "github.com/ongridio/ongrid/internal/manager/biz/edge"
	model "github.com/ongridio/ongrid/internal/manager/model/edge"
)

func TestUpgradeJobLifecycleAndRetry(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	baseline := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	deviceA, deviceB, deviceC := uint64(101), uint64(102), uint64(103)
	clusterID := uint64(501)
	job := &model.UpgradeJob{ClusterNodeID: &clusterID, TargetVersion: "v0.10.2"}
	items := []*model.UpgradeJobItem{
		{EdgeID: 1, DeviceID: &deviceA, Arch: "linux-amd64", FromVersion: "v0.10.1", TargetVersion: "v0.10.2", Status: model.UpgradeJobItemStatusQueued, BaselineRegisteredAt: &baseline},
		{EdgeID: 2, DeviceID: &deviceB, Arch: "linux-arm64", FromVersion: "v0.10.1", TargetVersion: "v0.10.2", Status: model.UpgradeJobItemStatusQueued, BaselineRegisteredAt: &baseline},
		{EdgeID: 3, DeviceID: &deviceC, Arch: "linux-amd64", FromVersion: "v0.10.2", TargetVersion: "v0.10.2", Status: model.UpgradeJobItemStatusSkipped, ErrorCode: "already_current"},
	}
	if err := repo.CreateUpgradeJob(ctx, job, items); err != nil {
		t.Fatalf("CreateUpgradeJob() error = %v", err)
	}
	if job.Total != 3 || job.Pending != 2 || job.Skipped != 1 || job.Status != model.UpgradeJobStatusQueued {
		t.Fatalf("created job = %+v", job)
	}
	if job.BatchSize != model.DefaultUpgradeJobBatchSize || job.CurrentBatch != 0 || job.TotalBatches != 1 ||
		items[0].BatchNumber != 1 || items[1].BatchNumber != 1 || items[2].BatchNumber != 0 {
		t.Fatalf("created batch metadata job=%+v items=%+v", job, items)
	}
	if active, err := repo.CountActiveUpgradeJobsForCluster(ctx, clusterID); err != nil || active != 1 {
		t.Fatalf("CountActiveUpgradeJobsForCluster(active) = %d, %v; want 1", active, err)
	}

	claimed, err := repo.ClaimNextUpgradeJob(ctx, baseline.Add(time.Minute))
	if err != nil {
		t.Fatalf("ClaimNextUpgradeJob() error = %v", err)
	}
	if claimed.ID != job.ID || claimed.Status != model.UpgradeJobStatusRunning {
		t.Fatalf("claimed = %+v", claimed)
	}
	if err := repo.SetUpgradeJobCurrentBatch(ctx, job.ID, 1, baseline.Add(time.Minute)); err != nil {
		t.Fatalf("SetUpgradeJobCurrentBatch() error = %v", err)
	}

	completedAt := baseline.Add(2 * time.Minute)
	firstDispatchBaseline := baseline.Add(30 * time.Second)
	if err := repo.MarkUpgradeItemDispatching(ctx, items[0].ID, &firstDispatchBaseline, baseline.Add(time.Minute)); err != nil {
		t.Fatalf("MarkUpgradeItemDispatching(first) error = %v", err)
	}
	_, dispatchedItems, err := repo.GetUpgradeJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetUpgradeJob(dispatched) error = %v", err)
	}
	if len(dispatchedItems) != 3 || dispatchedItems[0].BaselineRegisteredAt == nil || !dispatchedItems[0].BaselineRegisteredAt.Equal(firstDispatchBaseline) {
		t.Fatalf("dispatch baseline was not refreshed: items=%+v", dispatchedItems)
	}
	preApplyBaseline := baseline.Add(45 * time.Second)
	if err := repo.RefreshUpgradeItemBaseline(ctx, items[0].ID, preApplyBaseline); err != nil {
		t.Fatalf("RefreshUpgradeItemBaseline() error = %v", err)
	}
	_, dispatchedItems, err = repo.GetUpgradeJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetUpgradeJob(pre-apply) error = %v", err)
	}
	if dispatchedItems[0].BaselineRegisteredAt == nil || !dispatchedItems[0].BaselineRegisteredAt.Equal(preApplyBaseline) {
		t.Fatalf("pre-apply baseline was not refreshed: item=%+v", dispatchedItems[0])
	}
	if err := repo.MarkUpgradeItemWaiting(ctx, items[0].ID, completedAt.Add(time.Minute)); err != nil {
		t.Fatalf("MarkUpgradeItemWaiting(first) error = %v", err)
	}
	if err := repo.MarkUpgradeItemSucceeded(ctx, items[0].ID, "v0.10.2", &completedAt, completedAt); err != nil {
		t.Fatalf("MarkUpgradeItemSucceeded(first) error = %v", err)
	}
	if err := repo.MarkUpgradeItemDispatching(ctx, items[1].ID, &baseline, baseline.Add(time.Minute)); err != nil {
		t.Fatalf("MarkUpgradeItemDispatching(second) error = %v", err)
	}
	if err := repo.MarkUpgradeItemFailed(ctx, items[1].ID, model.UpgradeJobItemStatusFailed,
		"fetch_failed", "offline", "", nil, completedAt); err != nil {
		t.Fatalf("MarkUpgradeItemFailed(second) error = %v", err)
	}

	refreshed, err := repo.RefreshUpgradeJob(ctx, job.ID, completedAt)
	if err != nil {
		t.Fatalf("RefreshUpgradeJob() error = %v", err)
	}
	if refreshed.Status != model.UpgradeJobStatusPartialFailed || refreshed.Succeeded != 1 || refreshed.Failed != 1 || refreshed.Skipped != 1 || refreshed.Pending != 0 {
		t.Fatalf("refreshed job = %+v", refreshed)
	}
	if active, err := repo.CountActiveUpgradeJobsForCluster(ctx, clusterID); err != nil || active != 0 {
		t.Fatalf("CountActiveUpgradeJobsForCluster(finished) = %d, %v; want 0", active, err)
	}

	retryBaseline := completedAt.Add(time.Minute)
	retried, err := repo.RetryUpgradeJob(ctx, job.ID, []biz.UpgradeRetrySnapshot{{
		EdgeID: 2, FromVersion: "v0.10.1", BaselineRegisteredAt: &retryBaseline,
	}}, retryBaseline)
	if err != nil {
		t.Fatalf("RetryUpgradeJob() error = %v", err)
	}
	if retried.Status != model.UpgradeJobStatusQueued || retried.Pending != 1 || retried.Failed != 0 || retried.Succeeded != 1 ||
		retried.CurrentBatch != 0 || retried.TotalBatches != 1 {
		t.Fatalf("retried job = %+v", retried)
	}
	_, rows, err := repo.GetUpgradeJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetUpgradeJob() error = %v", err)
	}
	if rows[1].Status != model.UpgradeJobItemStatusQueued || rows[1].BatchNumber != 1 || rows[1].ErrorCode != "" || rows[1].BaselineRegisteredAt == nil || !rows[1].BaselineRegisteredAt.Equal(retryBaseline) {
		t.Fatalf("retried item = %+v", rows[1])
	}
	if rows[0].Status != model.UpgradeJobItemStatusSucceeded || rows[0].BatchNumber != 1 {
		t.Fatalf("completed item lost its original batch = %+v", rows[0])
	}
}

func TestRecoverUpgradeJobsRequeuesInterruptedDispatch(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	job := &model.UpgradeJob{TargetVersion: "v0.10.2"}
	items := []*model.UpgradeJobItem{
		{
			EdgeID: 9, Arch: "linux-amd64", TargetVersion: "v0.10.2",
			Status: model.UpgradeJobItemStatusQueued,
		},
		{
			EdgeID: 10, Arch: "linux-arm64", TargetVersion: "v0.10.2",
			Status: model.UpgradeJobItemStatusQueued,
		},
	}
	if err := repo.CreateUpgradeJob(ctx, job, items); err != nil {
		t.Fatalf("CreateUpgradeJob() error = %v", err)
	}
	if _, err := repo.ClaimNextUpgradeJob(ctx, time.Now()); err != nil {
		t.Fatalf("ClaimNextUpgradeJob() error = %v", err)
	}
	if err := repo.MarkUpgradeItemDispatching(ctx, items[0].ID, nil, time.Now()); err != nil {
		t.Fatalf("MarkUpgradeItemDispatching() error = %v", err)
	}
	if err := repo.MarkUpgradeItemDispatching(ctx, items[1].ID, nil, time.Now()); err != nil {
		t.Fatalf("MarkUpgradeItemDispatching(waiting) error = %v", err)
	}
	if err := repo.MarkUpgradeItemWaiting(ctx, items[1].ID, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("MarkUpgradeItemWaiting() error = %v", err)
	}
	if err := repo.RecoverUpgradeJobs(ctx); err != nil {
		t.Fatalf("RecoverUpgradeJobs() error = %v", err)
	}
	recovered, rows, err := repo.GetUpgradeJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetUpgradeJob() error = %v", err)
	}
	if recovered.Status != model.UpgradeJobStatusQueued ||
		rows[0].Status != model.UpgradeJobItemStatusQueued ||
		rows[1].Status != model.UpgradeJobItemStatusWaitingRegistration {
		t.Fatalf("recovered job=%+v items=%+v", recovered, rows)
	}
}

func TestRequeueUpgradeJobResetsStrandedDispatch(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	job := &model.UpgradeJob{TargetVersion: "v0.10.2"}
	items := []*model.UpgradeJobItem{{
		EdgeID: 11, Arch: "linux-amd64", TargetVersion: "v0.10.2",
		Status: model.UpgradeJobItemStatusQueued,
	}}
	if err := repo.CreateUpgradeJob(ctx, job, items); err != nil {
		t.Fatalf("CreateUpgradeJob() error = %v", err)
	}
	if _, err := repo.ClaimNextUpgradeJob(ctx, time.Now()); err != nil {
		t.Fatalf("ClaimNextUpgradeJob() error = %v", err)
	}
	if err := repo.MarkUpgradeItemDispatching(ctx, items[0].ID, nil, time.Now()); err != nil {
		t.Fatalf("MarkUpgradeItemDispatching() error = %v", err)
	}
	if err := repo.RequeueUpgradeJob(ctx, job.ID, time.Now()); err != nil {
		t.Fatalf("RequeueUpgradeJob() error = %v", err)
	}

	requeued, rows, err := repo.GetUpgradeJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetUpgradeJob() error = %v", err)
	}
	if requeued.Status != model.UpgradeJobStatusQueued || rows[0].Status != model.UpgradeJobItemStatusQueued {
		t.Fatalf("requeued job=%+v item=%+v", requeued, rows[0])
	}
}
