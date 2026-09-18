package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	topologybiz "github.com/ongridio/ongrid/internal/manager/biz/topology"
	store "github.com/ongridio/ongrid/internal/manager/data/topology/store"
	alertmodel "github.com/ongridio/ongrid/internal/manager/model/alert"
	topologymodel "github.com/ongridio/ongrid/internal/manager/model/topology"
)

// topology_impact_test.go lives in package tools (not tools_test) so it can
// reach the unexported topologyImpactPanel / resolveIncidentTopologyNode. The
// topology Usecase + graph seeding mirror the external expand_topology test
// seam but are redefined here because helpers don't cross the package-tools /
// package-tools_test boundary.

func newImpactTopoUC(t *testing.T) *topologybiz.Usecase {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return topologybiz.NewUsecase(
		store.NewNodeRepo(db),
		store.NewRelationRepo(db),
		store.NewRelationTypeRepo(db),
		store.NewNodeTypeRepo(db),
		nil,
	)
}

// seedImpactGraph builds:
//
//	app(checkout) <-member_of-  service(order-api) -depends_on-> service(db)
//	                                                |
//	                                                v deployed_on
//	                                            device(host-1)
//
// An incident labelled name=order-api must resolve the SERVICE node and report
// db (depends_on) + host-1 (deployed_on) as the blast radius, while checkout
// (member_of, non-propagating) stays out — same contract the external
// TestExpandTopologyOnlyPropagating asserts for the LLM-driven tool.
func seedImpactGraph(t *testing.T, uc *topologybiz.Usecase) (appID, orderID, dbID, hostID uint64) {
	t.Helper()
	ctx := context.Background()
	app, err := uc.CreateNode(ctx, string(topologymodel.NodeTypeApp), "checkout", "")
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	order, err := uc.CreateNode(ctx, string(topologymodel.NodeTypeService), "order-api", "")
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	db, err := uc.CreateNode(ctx, string(topologymodel.NodeTypeService), "db", "")
	if err != nil {
		t.Fatalf("create db: %v", err)
	}
	host, err := uc.CreateNode(ctx, string(topologymodel.NodeTypeDevice), "host-1", "")
	if err != nil {
		t.Fatalf("create host: %v", err)
	}
	if _, err := uc.CreateRelation(ctx, order.ID, app.ID, topologymodel.RelMemberOf, ""); err != nil {
		t.Fatalf("member_of: %v", err)
	}
	if _, err := uc.CreateRelation(ctx, order.ID, db.ID, topologymodel.RelDependsOn, ""); err != nil {
		t.Fatalf("depends_on: %v", err)
	}
	if _, err := uc.CreateRelation(ctx, order.ID, host.ID, topologymodel.RelDeployedOn, ""); err != nil {
		t.Fatalf("deployed_on: %v", err)
	}
	return app.ID, order.ID, db.ID, host.ID
}

// The core deterministic-injection contract: a service-name label resolves the
// service node and the panel carries its propagating blast radius.
func TestTopologyImpactPanel_ResolvesServiceNode(t *testing.T) {
	uc := newImpactTopoUC(t)
	appID, orderID, dbID, hostID := seedImpactGraph(t, uc)

	inc := &alertmodel.Incident{ID: 7, Title: "order-api down", Rule: "metric_raw"}
	labels := map[string]string{"name": "order-api"}

	panel, reason := topologyImpactPanel(context.Background(), uc, nil, inc, labels, nil)
	if panel == nil {
		t.Fatalf("expected a panel, got skip reason %q", reason)
	}
	if panel.Center.NodeID != orderID {
		t.Errorf("center = %d (%s), want order-api %d", panel.Center.NodeID, panel.Center.Name, orderID)
	}
	seen := map[uint64]topologyAffected{}
	for _, a := range panel.Affected {
		seen[a.NodeID] = a
	}
	if _, ok := seen[dbID]; !ok {
		t.Errorf("expected db (%d) in affected, got %+v", dbID, panel.Affected)
	}
	if _, ok := seen[hostID]; !ok {
		t.Errorf("expected host-1 (%d) in affected, got %+v", hostID, panel.Affected)
	}
	if _, ok := seen[appID]; ok {
		t.Errorf("member_of app (%d) must NOT appear in a propagating blast radius", appID)
	}
	if a, ok := seen[dbID]; ok && a.Direction == "" {
		t.Errorf("db affected entry should carry a direction (downstream|upstream)")
	}
}

// A hub with no propagating edges still yields a panel (center present) with an
// explanatory Note — the report writes 影响面局限于自身 rather than skipping.
func TestTopologyImpactPanel_LoneNodeHasNote(t *testing.T) {
	uc := newImpactTopoUC(t)
	if _, err := uc.CreateNode(context.Background(), string(topologymodel.NodeTypeService), "loner", ""); err != nil {
		t.Fatalf("create loner: %v", err)
	}
	panel, reason := topologyImpactPanel(context.Background(), uc, nil,
		&alertmodel.Incident{ID: 9}, map[string]string{"service": "loner"}, nil)
	if panel == nil {
		t.Fatalf("expected a panel for a resolvable lone node, got skip %q", reason)
	}
	if len(panel.Affected) != 0 {
		t.Errorf("lone node should have no affected, got %+v", panel.Affected)
	}
	if panel.Note == "" {
		t.Errorf("lone node panel should carry an explanatory Note")
	}
}

func TestTopologyImpactPanel_NilTopoSkips(t *testing.T) {
	panel, reason := topologyImpactPanel(context.Background(), nil, nil,
		&alertmodel.Incident{ID: 1}, map[string]string{"name": "x"}, nil)
	if panel != nil {
		t.Fatalf("expected nil panel when the topology graph is not configured")
	}
	if reason == "" {
		t.Errorf("expected a skip reason")
	}
}

func TestTopologyImpactPanel_NoNodeResolvedSkips(t *testing.T) {
	uc := newImpactTopoUC(t)
	seedImpactGraph(t, uc)
	// No service labels, no device → nothing to expand from.
	panel, reason := topologyImpactPanel(context.Background(), uc, nil, &alertmodel.Incident{ID: 1}, nil, nil)
	if panel != nil {
		t.Fatalf("expected nil panel when no node resolves")
	}
	if reason == "" {
		t.Errorf("expected a skip reason")
	}
}

// End-to-end wiring: the production path (CorrelateIncidentTool.singleCorrelate)
// must fold the panel into the bundle JSON so the report can read it. This is
// the assertion that actually guards the regression seen in prod.
func TestCorrelateIncidentBundleCarriesTopologyPanel(t *testing.T) {
	uc := newImpactTopoUC(t)
	_, orderID, dbID, _ := seedImpactGraph(t, uc)
	now := time.Now().UTC()
	fake := &fakeAlertUC{
		incidentByID: map[uint64]*alertmodel.Incident{
			7: {ID: 7, Title: "order-api down", Rule: "metric_raw", Severity: "critical",
				FirstFiredAt: now, LastFiredAt: now,
				LabelsJSON: `{"name":"order-api"}`, AnnotationsJSON: "{}"},
		},
	}
	// prom/log/trace/edges/devices nil → those panels skip; topologyGraph=uc
	// is the one under test.
	tool := NewCorrelateIncidentTool(fake, nil, nil, nil, nil, nil, uc, nil)
	out, err := tool.InvokableRun(context.Background(), `{"incident_ids":[7]}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	var env CorrelateIncidentBatchResponse
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if env.SuccessCount != 1 || env.ErrorCount != 0 {
		t.Fatalf("counts = %d/%d, want 1/0", env.SuccessCount, env.ErrorCount)
	}
	bundle := env.Results[0].Bundle
	if bundle == nil {
		t.Fatal("nil bundle")
	}
	if bundle.TopologyPanel == nil {
		t.Fatalf("expected topology_impact panel in bundle, skipped = %+v", bundle.Skipped)
	}
	if bundle.TopologyPanel.Center.NodeID != orderID {
		t.Errorf("center = %d, want order-api %d", bundle.TopologyPanel.Center.NodeID, orderID)
	}
	foundDB := false
	for _, a := range bundle.TopologyPanel.Affected {
		if a.NodeID == dbID {
			foundDB = true
		}
	}
	if !foundDB {
		t.Errorf("expected db (%d) in bundle affected, got %+v", dbID, bundle.TopologyPanel.Affected)
	}
}

// resolveIncidentDeviceID is the device-node fallback's ID picker. The live
// regression it guards: device_offline incidents (host down — exactly where
// blast radius matters most, every service deployed_on the host) carry
// device_id ONLY in labels with the COLUMN NULL, so a column-only read resolved
// nothing and the report dropped the 拓扑影响面 section. It must pick the id out
// of the label noise (instance/job/device_name ride along) and ignore
// non-numeric lookalikes rather than mis-parse them.
func TestResolveIncidentDeviceID(t *testing.T) {
	col := uint64(5)
	cases := []struct {
		name        string
		inc         *alertmodel.Incident
		labels      map[string]string
		annotations map[string]string
		want        uint64
	}{
		{"column set wins over labels", &alertmodel.Incident{DeviceID: &col}, map[string]string{"device_id": "2"}, nil, 5},
		{"device_offline live case: column nil, id in labels among noise", &alertmodel.Incident{},
			map[string]string{"device_id": "2", "device_name": "QMS", "instance": "ongrid:9100", "job": "ongrid-manager", "rule": "device_offline"}, nil, 2},
		{"non-numeric device_id ignored", &alertmodel.Incident{}, map[string]string{"device_id": "abc"}, nil, 0},
		{"annotations fallback", &alertmodel.Incident{}, nil, map[string]string{"device_id": "7"}, 7},
		{"nil incident no labels", nil, nil, nil, 0},
		{"zero column falls back to label", &alertmodel.Incident{DeviceID: new(uint64)}, map[string]string{"device_id": "9"}, nil, 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveIncidentDeviceID(tc.inc, tc.labels, tc.annotations); got != tc.want {
				t.Errorf("resolveIncidentDeviceID = %d, want %d", got, tc.want)
			}
		})
	}
}
