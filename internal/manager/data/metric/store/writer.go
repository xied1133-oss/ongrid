package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	biz "github.com/ongridio/ongrid/internal/manager/biz/metric"
	model "github.com/ongridio/ongrid/internal/manager/model/metric"
)

// Writer is the GORM-backed biz.Writer implementation.
type Writer struct {
	db *gorm.DB
}

// NewWriter constructs the writer.
func NewWriter(db *gorm.DB) *Writer { return &Writer{db: db} }

// Compile-time interface check.
var _ biz.Writer = (*Writer)(nil)

// createInBatchesSize is the inner batch size passed to gorm.
const createInBatchesSize = 500

// Physical table names, mirroring model.*.TableName().
const (
	tableRaw  = "host_metrics_raw"
	table5m   = "host_metrics_5m"
	table1h   = "host_metrics_1h"
	tableDLQ  = "host_metrics_dead_letter"
)

// WriteRaw persists a batch of raw Points to host_metrics_raw.
func (w *Writer) WriteRaw(ctx context.Context, batch []model.Point) error {
	if len(batch) == 0 {
		return nil
	}
	rows := make([]model.HostMetric, len(batch))
	for i, p := range batch {
		rows[i] = model.HostMetric{
			EdgeID:      p.EdgeID,
			Ts:          p.Ts,
			CPUPct:      p.CPUPct,
			MemPct:      p.MemPct,
			Load1:       p.Load1,
			Load5:       p.Load5,
			Load15:      p.Load15,
			NetRxBps:    p.NetRxBps,
			NetTxBps:    p.NetTxBps,
			DiskUsedPct: p.DiskUsedPct,
		}
	}
	return w.db.WithContext(ctx).CreateInBatches(rows, createInBatchesSize).Error
}

// WriteDeadLetter stores failed points in host_metrics_dead_letter with
// reason attached. Each point becomes one DLQ row (callers looking at
// the table therefore see every sample that was lost, not a blob).
func (w *Writer) WriteDeadLetter(ctx context.Context, batch []model.Point, reason string) error {
	if len(batch) == 0 {
		return nil
	}
	if len(reason) > 256 {
		reason = reason[:256]
	}
	failedAt := time.Now().UTC()
	rows := make([]model.DeadLetter, len(batch))
	for i, p := range batch {
		rows[i] = model.DeadLetter{
			EdgeID:      p.EdgeID,
			Ts:          p.Ts,
			CPUPct:      p.CPUPct,
			MemPct:      p.MemPct,
			Load1:       p.Load1,
			Load5:       p.Load5,
			Load15:      p.Load15,
			NetRxBps:    p.NetRxBps,
			NetTxBps:    p.NetTxBps,
			DiskUsedPct: p.DiskUsedPct,
			ErrorReason: reason,
			FailedAt:    failedAt,
		}
	}
	return w.db.WithContext(ctx).CreateInBatches(rows, createInBatchesSize).Error
}

// Write5m persists 5-minute aggregate buckets. On conflict (edge_id, ts)
// we overwrite — rerunning the 5m job is idempotent
func (w *Writer) Write5m(ctx context.Context, buckets []model.Bucket5m) error {
	if len(buckets) == 0 {
		return nil
	}
	rows := make([]model.HostMetric5m, len(buckets))
	for i, b := range buckets {
		rows[i] = model.HostMetric5m{
			EdgeID:      b.EdgeID,
			Ts:          b.Ts,
			CPUAvg:      b.CPUAvg,
			CPUMax:      b.CPUMax,
			MemAvg:      b.MemAvg,
			MemMax:      b.MemMax,
			Load1Avg:    b.Load1Avg,
			Load1Max:    b.Load1Max,
			Load5Avg:    b.Load5Avg,
			Load5Max:    b.Load5Max,
			Load15Avg:   b.Load15Avg,
			Load15Max:   b.Load15Max,
			NetRxSum:    b.NetRxSum,
			NetTxSum:    b.NetTxSum,
			DiskUsedAvg: b.DiskUsedAvg,
			DiskUsedMax: b.DiskUsedMax,
		}
	}
	return w.db.WithContext(ctx).Save(&rows).Error
}

// Write1h persists 1-hour aggregate buckets with the same idempotency
// contract as Write5m.
func (w *Writer) Write1h(ctx context.Context, buckets []model.Bucket1h) error {
	if len(buckets) == 0 {
		return nil
	}
	rows := make([]model.HostMetric1h, len(buckets))
	for i, b := range buckets {
		rows[i] = model.HostMetric1h{
			EdgeID:      b.EdgeID,
			Ts:          b.Ts,
			CPUAvg:      b.CPUAvg,
			CPUMax:      b.CPUMax,
			MemAvg:      b.MemAvg,
			MemMax:      b.MemMax,
			Load1Avg:    b.Load1Avg,
			Load1Max:    b.Load1Max,
			Load5Avg:    b.Load5Avg,
			Load5Max:    b.Load5Max,
			Load15Avg:   b.Load15Avg,
			Load15Max:   b.Load15Max,
			NetRxSum:    b.NetRxSum,
			NetTxSum:    b.NetTxSum,
			DiskUsedAvg: b.DiskUsedAvg,
			DiskUsedMax: b.DiskUsedMax,
		}
	}
	return w.db.WithContext(ctx).Save(&rows).Error
}

// DeleteRawBefore deletes up to limit rows strictly older than cutoff.
// Returns the number deleted.
func (w *Writer) DeleteRawBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	return deleteBefore(ctx, w.db, tableRaw, cutoff, limit)
}

// Delete5mBefore is the 5m-tier retention path.
func (w *Writer) Delete5mBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	return deleteBefore(ctx, w.db, table5m, cutoff, limit)
}

// Delete1hBefore is the 1h-tier retention path.
func (w *Writer) Delete1hBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	return deleteBefore(ctx, w.db, table1h, cutoff, limit)
}

// deleteBefore is the shared limit-capped delete used by all three tiers.
// SQLite's DELETE ... LIMIT is compiled out by default, so we rowid-bound
// the rows to delete via a subselect.
func deleteBefore(ctx context.Context, db *gorm.DB, table string, cutoff time.Time, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	sql := fmt.Sprintf(
		"DELETE FROM %s WHERE rowid IN (SELECT rowid FROM %s WHERE ts < ? LIMIT ?)",
		table, table,
	)
	res := db.WithContext(ctx).Exec(sql, cutoff, limit)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}
