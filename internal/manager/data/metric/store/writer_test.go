package store

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	model "github.com/ongridio/ongrid/internal/manager/model/metric"
)

// newTestDB opens an in-memory SQLite DB and runs this package's Migrate so
// the metric schema is present. Tests open sqlite directly (bypassing
// dbx.Open) to avoid constructing a config.DBConfig for the in-memory case.
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open sqlite :memory:: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

func TestWriter_WriteRaw_RoundTrip(t *testing.T) {
	db := newTestDB(t)
	w := NewWriter(db)
	r := NewReader(db)
	ctx := context.Background()

	base := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	points := []model.Point{
		{EdgeID: 1, Ts: base, CPUPct: 10, MemPct: 50, Load1: 0.5, Load5: 0.4, Load15: 0.3, NetRxBps: 100, NetTxBps: 200, DiskUsedPct: 11},
		{EdgeID: 1, Ts: base.Add(10 * time.Second), CPUPct: 20, MemPct: 60, Load1: 1, NetRxBps: 300, NetTxBps: 400, DiskUsedPct: 12},
		{EdgeID: 2, Ts: base, CPUPct: 90, MemPct: 99, Load1: 4},
	}
	if err := w.WriteRaw(ctx, points); err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}

	got, err := r.QueryRaw(ctx, 1, base.Add(-time.Minute), base.Add(time.Minute))
	if err != nil {
		t.Fatalf("QueryRaw: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 (for edge 1)", len(got))
	}
	if !got[0].Ts.Equal(base) || got[0].CPUPct != 10 {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].NetTxBps != 400 {
		t.Errorf("got[1].NetTxBps = %d, want 400", got[1].NetTxBps)
	}
}

func TestWriter_WriteRaw_Empty(t *testing.T) {
	db := newTestDB(t)
	w := NewWriter(db)
	if err := w.WriteRaw(context.Background(), nil); err != nil {
		t.Fatalf("WriteRaw nil: %v", err)
	}
}

func TestWriter_WriteDeadLetter(t *testing.T) {
	db := newTestDB(t)
	w := NewWriter(db)
	ctx := context.Background()
	base := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)

	points := []model.Point{
		{EdgeID: 9, Ts: base, CPUPct: 1, MemPct: 2, NetRxBps: 1, NetTxBps: 1},
	}
	if err := w.WriteDeadLetter(ctx, points, "flush exhausted"); err != nil {
		t.Fatalf("WriteDeadLetter: %v", err)
	}

	var rows []model.DeadLetter
	if err := db.WithContext(ctx).Find(&rows).Error; err != nil {
		t.Fatalf("select dlq: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("dlq rows = %d, want 1", len(rows))
	}
	if rows[0].ErrorReason != "flush exhausted" || rows[0].EdgeID != 9 {
		t.Errorf("dlq row = %+v", rows[0])
	}
}

func TestWriter_Write5m_And_1h_RoundTrip(t *testing.T) {
	db := newTestDB(t)
	w := NewWriter(db)
	r := NewReader(db)
	ctx := context.Background()

	bucketTs := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	b5 := []model.Bucket5m{
		{EdgeID: 1, Ts: bucketTs, CPUAvg: 10, CPUMax: 15, MemAvg: 50, MemMax: 60, NetRxSum: 1000, NetTxSum: 2000},
	}
	if err := w.Write5m(ctx, b5); err != nil {
		t.Fatalf("Write5m: %v", err)
	}
	b1 := []model.Bucket1h{
		{EdgeID: 1, Ts: bucketTs, CPUAvg: 20, CPUMax: 25, MemAvg: 40, MemMax: 50, NetRxSum: 7777, NetTxSum: 8888},
	}
	if err := w.Write1h(ctx, b1); err != nil {
		t.Fatalf("Write1h: %v", err)
	}

	got5, err := r.Query5m(ctx, 1, bucketTs.Add(-time.Minute), bucketTs.Add(time.Minute))
	if err != nil {
		t.Fatalf("Query5m: %v", err)
	}
	if len(got5) != 1 || got5[0].CPUMax != 15 || got5[0].NetRxSum != 1000 {
		t.Errorf("got5 = %+v", got5)
	}

	got1, err := r.Query1h(ctx, 1, bucketTs.Add(-time.Minute), bucketTs.Add(time.Minute))
	if err != nil {
		t.Fatalf("Query1h: %v", err)
	}
	if len(got1) != 1 || got1[0].NetTxSum != 8888 {
		t.Errorf("got1 = %+v", got1)
	}
}

func TestWriter_DeleteBefore_LimitAndCutoff(t *testing.T) {
	db := newTestDB(t)
	w := NewWriter(db)
	ctx := context.Background()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var points []model.Point
	for i := 0; i < 10; i++ {
		points = append(points, model.Point{
			EdgeID: 1,
			Ts:     base.Add(time.Duration(i) * time.Minute),
			CPUPct: float64(i),
		})
	}
	if err := w.WriteRaw(ctx, points); err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}

	cutoff := base.Add(5 * time.Minute) // delete rows with ts < cutoff → 5 rows
	n, err := w.DeleteRawBefore(ctx, cutoff, 1000)
	if err != nil {
		t.Fatalf("DeleteRawBefore: %v", err)
	}
	if n != 5 {
		t.Errorf("deleted = %d, want 5", n)
	}

	var remaining int64
	db.WithContext(ctx).Model(&model.HostMetric{}).Count(&remaining)
	if remaining != 5 {
		t.Errorf("remaining = %d, want 5", remaining)
	}

	// Re-run to verify 0 rows (pumps the retention loop exit).
	n2, err := w.DeleteRawBefore(ctx, cutoff, 1000)
	if err != nil {
		t.Fatalf("DeleteRawBefore pass2: %v", err)
	}
	if n2 != 0 {
		t.Errorf("second pass deleted = %d, want 0", n2)
	}
}

func TestWriter_DeleteBefore_RespectsLimit(t *testing.T) {
	db := newTestDB(t)
	w := NewWriter(db)
	ctx := context.Background()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var points []model.Point
	for i := 0; i < 10; i++ {
		points = append(points, model.Point{
			EdgeID: 1,
			Ts:     base.Add(time.Duration(i) * time.Minute),
		})
	}
	if err := w.WriteRaw(ctx, points); err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}

	// Cutoff far in the future → every row is eligible, but limit caps.
	n, err := w.DeleteRawBefore(ctx, base.Add(time.Hour), 3)
	if err != nil {
		t.Fatalf("DeleteRawBefore: %v", err)
	}
	if n != 3 {
		t.Errorf("deleted = %d, want 3 (limit)", n)
	}
}

func TestReader_ScanRawForDownsample_CrossEdge(t *testing.T) {
	db := newTestDB(t)
	w := NewWriter(db)
	r := NewReader(db)
	ctx := context.Background()

	base := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	points := []model.Point{
		{EdgeID: 1, Ts: base, CPUPct: 10},
		{EdgeID: 2, Ts: base, CPUPct: 20},
		{EdgeID: 1, Ts: base.Add(30 * time.Second), CPUPct: 30},
	}
	if err := w.WriteRaw(ctx, points); err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}

	got, err := r.ScanRawForDownsample(ctx, base, base.Add(time.Minute))
	if err != nil {
		t.Fatalf("ScanRawForDownsample: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	// Ordered by (edge_id, ts): edge 1 first (two rows), edge 2 last.
	if got[0].EdgeID != 1 || got[1].EdgeID != 1 || got[2].EdgeID != 2 {
		t.Errorf("unexpected ordering: %+v", got)
	}
}
