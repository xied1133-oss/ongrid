package metric

import (
	"context"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/metric"
)

// Writer persists host metrics. Implemented in
// internal/manager/data/metric/store (and, later, .../clickhouse).
//
// The interface is storage-agnostic: it takes domain Point / Bucket types
// (plain Go, no gorm tags) so the biz layer never learns what table the
// data ends up in.
type Writer interface {
	// WriteRaw persists a batch of raw samples to host_metrics_raw.
	WriteRaw(ctx context.Context, batch []model.Point) error

	// WriteDeadLetter records points that failed to flush after retry.
	// reason is a free-form short description of why the flush gave up.
	WriteDeadLetter(ctx context.Context, batch []model.Point, reason string) error

	// Write5m / Write1h persist pre-aggregated buckets.
	Write5m(ctx context.Context, buckets []model.Bucket5m) error
	Write1h(ctx context.Context, buckets []model.Bucket1h) error

	// Delete*Before deletes up to limit rows strictly older than cutoff
	// and returns the number deleted. Callers loop until rows==0.
	DeleteRawBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error)
	Delete5mBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error)
	Delete1hBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error)
}

// Reader queries host metrics. Post-pivot scoping is by edge_id
// alone; there is no org_id to enforce.
type Reader interface {
	// QueryRaw returns raw samples for edgeID within [from, to], ordered
	// by ts ascending. Both ends inclusive.
	QueryRaw(ctx context.Context, edgeID uint64, from, to time.Time) ([]model.Point, error)
	// Query5m / Query1h return pre-aggregated buckets; same inclusivity.
	Query5m(ctx context.Context, edgeID uint64, from, to time.Time) ([]model.Bucket5m, error)
	Query1h(ctx context.Context, edgeID uint64, from, to time.Time) ([]model.Bucket1h, error)

	// ScanRawForDownsample returns every raw point in [from, to] across
	// all edges, to feed the 5m aggregator. Ordered by (edge_id, ts)
	// ascending so the downsampler can group cheaply.
	ScanRawForDownsample(ctx context.Context, from, to time.Time) ([]model.Point, error)
	// Scan5mForDownsample returns every 5m bucket in [from, to] across
	// all edges, to feed the 1h aggregator.
	Scan5mForDownsample(ctx context.Context, from, to time.Time) ([]model.Bucket5m, error)
}
