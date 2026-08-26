package edge

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	biz "github.com/ongridio/ongrid/internal/manager/biz/edge"
	model "github.com/ongridio/ongrid/internal/manager/model/edge"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

type createUpgradeJobReq struct {
	EdgeIDs        []uint64 `json:"edge_ids"`
	TargetVersion  string   `json:"target_version"`
	ClusterNodeID  *uint64  `json:"cluster_node_id,omitempty"`
	ForceReinstall bool     `json:"force_reinstall,omitempty"`
}

type upgradeJobDTO struct {
	ID             uint64     `json:"id"`
	ClusterNodeID  *uint64    `json:"cluster_node_id,omitempty"`
	TargetVersion  string     `json:"target_version"`
	Status         string     `json:"status"`
	ForceReinstall bool       `json:"force_reinstall"`
	BatchSize      int        `json:"batch_size"`
	CurrentBatch   int        `json:"current_batch"`
	TotalBatches   int        `json:"total_batches"`
	Total          int        `json:"total"`
	Succeeded      int        `json:"succeeded"`
	Failed         int        `json:"failed"`
	Skipped        int        `json:"skipped"`
	Pending        int        `json:"pending"`
	CreatedBy      *uint64    `json:"created_by,omitempty"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type upgradeJobItemDTO struct {
	ID                     uint64     `json:"id"`
	JobID                  uint64     `json:"job_id"`
	EdgeID                 uint64     `json:"edge_id"`
	DeviceID               *uint64    `json:"device_id,omitempty"`
	EdgeName               string     `json:"edge_name"`
	DeviceName             string     `json:"device_name"`
	Arch                   string     `json:"arch"`
	FromVersion            string     `json:"from_version"`
	TargetVersion          string     `json:"target_version"`
	BatchNumber            int        `json:"batch_number"`
	Status                 string     `json:"status"`
	Attempt                int        `json:"attempt"`
	ErrorCode              string     `json:"error_code,omitempty"`
	ErrorMessage           string     `json:"error_message,omitempty"`
	ObservedVersion        string     `json:"observed_version,omitempty"`
	BaselineRegisteredAt   *time.Time `json:"baseline_registered_at,omitempty"`
	ObservedRegisteredAt   *time.Time `json:"observed_registered_at,omitempty"`
	VerificationDeadlineAt *time.Time `json:"verification_deadline_at,omitempty"`
	StartedAt              *time.Time `json:"started_at,omitempty"`
	FinishedAt             *time.Time `json:"finished_at,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

type upgradeJobDetailResp struct {
	Job   upgradeJobDTO       `json:"job"`
	Items []upgradeJobItemDTO `json:"items"`
}

type upgradeJobListResp struct {
	Items    []upgradeJobDTO `json:"items"`
	Total    int64           `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
}

// createUpgradeJob godoc
// @Summary Create a persistent Edge package upgrade job
// @Router /api/v1/edge-upgrade-jobs [post]
// @Success 202 {object} upgradeJobDTO
func (h *Handler) createUpgradeJob(w http.ResponseWriter, r *http.Request) {
	if h.upgradeJobs == nil {
		writeErr(w, errs.ErrNotWiredYet)
		return
	}
	var req createUpgradeJobReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	caller, ok := tenantctx.From(r.Context())
	if !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	createdBy := caller.UserID
	job, err := h.upgradeJobs.Create(r.Context(), biz.CreateUpgradeJobInput{
		ClusterNodeID: req.ClusterNodeID, EdgeIDs: req.EdgeIDs,
		TargetVersion: req.TargetVersion, ForceReinstall: req.ForceReinstall,
		CreatedBy: &createdBy,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, toUpgradeJobDTO(job))
}

// listUpgradeJobs godoc
// @Summary List persistent Edge package upgrade jobs
// @Router /api/v1/edge-upgrade-jobs [get]
// @Success 200 {object} upgradeJobListResp
func (h *Handler) listUpgradeJobs(w http.ResponseWriter, r *http.Request) {
	if h.upgradeJobs == nil {
		writeErr(w, errs.ErrNotWiredYet)
		return
	}
	page, pageSize, err := parseUpgradeJobPage(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var clusterNodeID *uint64
	if raw := r.URL.Query().Get("cluster_node_id"); raw != "" {
		value, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || value == 0 {
			writeErr(w, errs.ErrInvalid)
			return
		}
		clusterNodeID = &value
	}
	jobs, total, err := h.upgradeJobs.List(r.Context(), biz.UpgradeJobListFilter{
		ClusterNodeID: clusterNodeID,
		Limit:         pageSize,
		Offset:        (page - 1) * pageSize,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	items := make([]upgradeJobDTO, 0, len(jobs))
	for _, job := range jobs {
		items = append(items, toUpgradeJobDTO(job))
	}
	writeJSON(w, http.StatusOK, upgradeJobListResp{Items: items, Total: total, Page: page, PageSize: pageSize})
}

// getUpgradeJob godoc
// @Summary Get an Edge package upgrade job and per-device results
// @Router /api/v1/edge-upgrade-jobs/{id} [get]
// @Success 200 {object} upgradeJobDetailResp
func (h *Handler) getUpgradeJob(w http.ResponseWriter, r *http.Request) {
	if h.upgradeJobs == nil {
		writeErr(w, errs.ErrNotWiredYet)
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	job, rows, err := h.upgradeJobs.Get(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	items := make([]upgradeJobItemDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, toUpgradeJobItemDTO(row))
	}
	writeJSON(w, http.StatusOK, upgradeJobDetailResp{Job: toUpgradeJobDTO(job), Items: items})
}

// retryUpgradeJob godoc
// @Summary Retry failed or timed-out devices in an Edge upgrade job
// @Router /api/v1/edge-upgrade-jobs/{id}/retry [post]
// @Success 202 {object} upgradeJobDTO
func (h *Handler) retryUpgradeJob(w http.ResponseWriter, r *http.Request) {
	if h.upgradeJobs == nil {
		writeErr(w, errs.ErrNotWiredYet)
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	job, err := h.upgradeJobs.Retry(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, toUpgradeJobDTO(job))
}

func parseUpgradeJobPage(r *http.Request) (int, int, error) {
	page, pageSize := 1, 20
	if raw := r.URL.Query().Get("page"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return 0, 0, errs.ErrInvalid
		}
		page = value
	}
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 || value > 100 {
			return 0, 0, errs.ErrInvalid
		}
		pageSize = value
	}
	return page, pageSize, nil
}

func toUpgradeJobDTO(job *model.UpgradeJob) upgradeJobDTO {
	return upgradeJobDTO{
		ID: job.ID, ClusterNodeID: job.ClusterNodeID, TargetVersion: job.TargetVersion,
		Status: job.Status, ForceReinstall: job.ForceReinstall,
		BatchSize: job.BatchSize, CurrentBatch: job.CurrentBatch, TotalBatches: job.TotalBatches, Total: job.Total,
		Succeeded: job.Succeeded, Failed: job.Failed, Skipped: job.Skipped,
		Pending: job.Pending, CreatedBy: job.CreatedBy, StartedAt: job.StartedAt,
		FinishedAt: job.FinishedAt, CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt,
	}
}

func toUpgradeJobItemDTO(item *model.UpgradeJobItem) upgradeJobItemDTO {
	return upgradeJobItemDTO{
		ID: item.ID, JobID: item.JobID, EdgeID: item.EdgeID, DeviceID: item.DeviceID,
		EdgeName: item.EdgeName, DeviceName: item.DeviceName, Arch: item.Arch,
		FromVersion: item.FromVersion, TargetVersion: item.TargetVersion,
		BatchNumber: item.BatchNumber, Status: item.Status, Attempt: item.Attempt, ErrorCode: item.ErrorCode,
		ErrorMessage: item.ErrorMessage, ObservedVersion: item.ObservedVersion,
		BaselineRegisteredAt:   item.BaselineRegisteredAt,
		ObservedRegisteredAt:   item.ObservedRegisteredAt,
		VerificationDeadlineAt: item.VerificationDeadlineAt,
		StartedAt:              item.StartedAt, FinishedAt: item.FinishedAt,
		CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
	}
}
