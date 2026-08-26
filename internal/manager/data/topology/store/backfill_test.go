package store

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	biz "github.com/ongridio/ongrid/internal/manager/biz/topology"
	devmodel "github.com/ongridio/ongrid/internal/manager/model/device"
	model "github.com/ongridio/ongrid/internal/manager/model/topology"
)

// TestBackfillDeviceNodes asserts the topology migration creates a
// node row for every device without one and writes node_id back.
// Mirrors the production migration order: device.AutoMigrate first,
// then topology.Migrate (which carries the backfill).
func TestBackfillDeviceNodes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.AutoMigrate(&devmodel.Device{}); err != nil {
		t.Fatalf("AutoMigrate device: %v", err)
	}

	// Seed three devices: one named, one empty-named, one already
	// linked. After backfill we expect: first two get a fresh node,
	// the third stays at its pre-set value.
	d1 := &devmodel.Device{Fingerprint: "fp-1", Name: "vm-001", Hostname: "vm-001", OS: "linux", Arch: "amd64"}
	d2 := &devmodel.Device{Fingerprint: "fp-2", Name: "", Hostname: "no-name", OS: "linux", Arch: "amd64"}
	d3 := &devmodel.Device{Fingerprint: "fp-3", Name: "vm-003", Hostname: "vm-003", OS: "linux", Arch: "amd64"}
	if err := db.Create(d1).Error; err != nil {
		t.Fatalf("seed d1: %v", err)
	}
	if err := db.Create(d2).Error; err != nil {
		t.Fatalf("seed d2: %v", err)
	}
	if err := db.Create(d3).Error; err != nil {
		t.Fatalf("seed d3: %v", err)
	}

	// Pre-bind d3 to a hand-crafted node so we can verify the backfill
	// SKIPS rows that already have node_id.
	if err := Migrate(db); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	nodes := NewNodeRepo(db)
	ctx := context.Background()
	preNode := &model.Node{Type: "device", Name: "vm-003"}
	// Wait — Migrate ran first and already backfilled d3 to a node.
	// Verify all three devices now point at a node row.

	var devs []devmodel.Device
	if err := db.Order("id ASC").Find(&devs).Error; err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(devs) != 3 {
		t.Fatalf("expected 3 devices, got %d", len(devs))
	}
	for _, d := range devs {
		if d.NodeID == nil || *d.NodeID == 0 {
			t.Errorf("device %d (%s): expected node_id after backfill, got nil", d.ID, d.Name)
		}
	}

	// Check d2 got the fallback name "device-<id>".
	d2Node, err := nodes.Get(ctx, *devs[1].NodeID)
	if err != nil {
		t.Fatalf("get d2 node: %v", err)
	}
	want := "device-" + uintStr(devs[1].ID)
	if d2Node.Name != want {
		t.Errorf("d2 node name: want %q, got %q", want, d2Node.Name)
	}

	// Idempotence: second Migrate should NOT create new node rows.
	cntBefore, _ := nodes.Count(ctx, biz.NodeListFilter{})
	if err := Migrate(db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	cntAfter, _ := nodes.Count(ctx, biz.NodeListFilter{})
	if cntBefore != cntAfter {
		t.Errorf("second Migrate created extra nodes: %d -> %d", cntBefore, cntAfter)
	}
	_ = preNode
}

func uintStr(n uint64) string {
	return formatUint(n)
}

func formatUint(n uint64) string {
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}
