package store

import (
	"context"
	"time"

	"gorm.io/gorm"

	biz "github.com/ongridio/ongrid/internal/manager/biz/metric"
	model "github.com/ongridio/ongrid/internal/manager/model/metric"
)

// Reader is the GORM-backed biz.Reader implementation.
type Reader struct {
	db *gorm.DB
}

// NewReader constructs the reader.
func NewReader(db *gorm.DB) *Reader { return &Reader{db: db} }

// Compile-time interface check.
var _ biz.Reader = (*Reader)(nil)

// QueryRaw returns raw samples for edgeID in [from, to] ordered by ts asc.
func (r *Reader) QueryRaw(ctx context.Context, edgeID uint64, from, to time.Time) ([]model.Point, error) {
	var rows []model.HostMetric
	err := r.db.WithContext(ctx).
		Where("edge_id = ? AND ts BETWEEN ? AND ?", edgeID, from, to).
		Order("ts ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]model.Point, len(rows))
	for i, row := range rows {
		out[i] = model.Point{
			EdgeID:      row.EdgeID,
			Ts:          row.Ts,
			CPUPct:      row.CPUPct,
			MemPct:      row.MemPct,
			Load1:       row.Load1,
			Load5:       row.Load5,
			Load15:      row.Load15,
			NetRxBps:    row.NetRxBps,
			NetTxBps:    row.NetTxBps,
			DiskUsedPct: row.DiskUsedPct,
		}
	}
	return out, nil
}

// Query5m returns 5m buckets for edgeID in [from, to] ordered by ts asc.
func (r *Reader) Query5m(ctx context.Context, edgeID uint64, from, to time.Time) ([]model.Bucket5m, error) {
	var rows []model.HostMetric5m
	err := r.db.WithContext(ctx).
		Where("edge_id = ? AND ts BETWEEN ? AND ?", edgeID, from, to).
		Order("ts ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]model.Bucket5m, len(rows))
	for i, row := range rows {
		out[i] = rowToBucket5m(row)
	}
	return out, nil
}

// Query1h returns 1h buckets for edgeID in [from, to] ordered by ts asc.
func (r *Reader) Query1h(ctx context.Context, edgeID uint64, from, to time.Time) ([]model.Bucket1h, error) {
	var rows []model.HostMetric1h
	err := r.db.WithContext(ctx).
		Where("edge_id = ? AND ts BETWEEN ? AND ?", edgeID, from, to).
		Order("ts ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]model.Bucket1h, len(rows))
	for i, row := range rows {
		out[i] = model.Bucket1h{
			EdgeID:      row.EdgeID,
			Ts:          row.Ts,
			CPUAvg:      row.CPUAvg,
			CPUMax:      row.CPUMax,
			MemAvg:      row.MemAvg,
			MemMax:      row.MemMax,
			Load1Avg:    row.Load1Avg,
			Load1Max:    row.Load1Max,
			Load5Avg:    row.Load5Avg,
			Load5Max:    row.Load5Max,
			Load15Avg:   row.Load15Avg,
			Load15Max:   row.Load15Max,
			NetRxSum:    row.NetRxSum,
			NetTxSum:    row.NetTxSum,
			DiskUsedAvg: row.DiskUsedAvg,
			DiskUsedMax: row.DiskUsedMax,
		}
	}
	return out, nil
}

// ScanRawForDownsample returns every raw point in [from, to] across all
// edges, ordered by (edge_id, ts) ascending.
func (r *Reader) ScanRawForDownsample(ctx context.Context, from, to time.Time) ([]model.Point, error) {
	var rows []model.HostMetric
	err := r.db.WithContext(ctx).
		Where("ts BETWEEN ? AND ?", from, to).
		Order("edge_id ASC, ts ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]model.Point, len(rows))
	for i, row := range rows {
		out[i] = model.Point{
			EdgeID:      row.EdgeID,
			Ts:          row.Ts,
			CPUPct:      row.CPUPct,
			MemPct:      row.MemPct,
			Load1:       row.Load1,
			Load5:       row.Load5,
			Load15:      row.Load15,
			NetRxBps:    row.NetRxBps,
			NetTxBps:    row.NetTxBps,
			DiskUsedPct: row.DiskUsedPct,
		}
	}
	return out, nil
}

// Scan5mForDownsample returns every 5m bucket in [from, to] across all
// edges, ordered by (edge_id, ts) ascending.
func (r *Reader) Scan5mForDownsample(ctx context.Context, from, to time.Time) ([]model.Bucket5m, error) {
	var rows []model.HostMetric5m
	err := r.db.WithContext(ctx).
		Where("ts BETWEEN ? AND ?", from, to).
		Order("edge_id ASC, ts ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]model.Bucket5m, len(rows))
	for i, row := range rows {
		out[i] = rowToBucket5m(row)
	}
	return out, nil
}

func rowToBucket5m(row model.HostMetric5m) model.Bucket5m {
	return model.Bucket5m{
		EdgeID:      row.EdgeID,
		Ts:          row.Ts,
		CPUAvg:      row.CPUAvg,
		CPUMax:      row.CPUMax,
		MemAvg:      row.MemAvg,
		MemMax:      row.MemMax,
		Load1Avg:    row.Load1Avg,
		Load1Max:    row.Load1Max,
		Load5Avg:    row.Load5Avg,
		Load5Max:    row.Load5Max,
		Load15Avg:   row.Load15Avg,
		Load15Max:   row.Load15Max,
		NetRxSum:    row.NetRxSum,
		NetTxSum:    row.NetTxSum,
		DiskUsedAvg: row.DiskUsedAvg,
		DiskUsedMax: row.DiskUsedMax,
	}
}
