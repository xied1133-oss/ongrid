package grafana

import (
	"testing"

	monitormodel "github.com/ongridio/ongrid/internal/manager/model/monitor"
)

// TestBuildMonitorDashboardJSON verifies the renderer maps each ongrid
// panel onto the Grafana wire shape and lays out the grid 2-wide. This
// is the only logic that runs without a live Grafana, so the test
// focuses there; SyncMonitorPanels itself is exercised by integration
// tests / manual smoke against an embedded Grafana.
func TestBuildMonitorDashboardJSON(t *testing.T) {
	t.Parallel()
	panels := []*monitormodel.Panel{
		{ID: 1, Title: "CPU", Type: monitormodel.PanelTypeTimeseries, PromQL: "cpu_pct", Legend: "{{device_id}}", Unit: "percent"},
		{ID: 2, Title: "Mem", Type: monitormodel.PanelTypeStat, PromQL: "mem_pct", Unit: "percent"},
		{ID: 3, Title: "Disk", Type: monitormodel.PanelTypeGauge, PromQL: "disk_pct", Unit: "percent"},
	}
	out := buildMonitorDashboardJSON("ongrid-monitor", "Title", panels)

	if got := out["uid"]; got != "ongrid-monitor" {
		t.Fatalf("uid = %v", got)
	}
	gp, ok := out["panels"].([]map[string]any)
	if !ok || len(gp) != 3 {
		t.Fatalf("panels shape = %T len=%d", out["panels"], len(gp))
	}

	// Layout: panel 0 at (x=0,y=0), panel 1 at (x=12,y=0), panel 2 at (x=0,y=8).
	type pos struct{ x, y int }
	want := []pos{{0, 0}, {12, 0}, {0, 8}}
	for i, p := range gp {
		grid := p["gridPos"].(map[string]any)
		if grid["x"].(int) != want[i].x || grid["y"].(int) != want[i].y {
			t.Fatalf("panel %d gridPos = %v, want %+v", i, grid, want[i])
		}
		if w, h := grid["w"].(int), grid["h"].(int); w != 12 || h != 8 {
			t.Fatalf("panel %d wh = %d,%d", i, w, h)
		}
	}

	// Type mapping: timeseries / stat / gauge survive 1:1.
	wantTypes := []string{"timeseries", "stat", "gauge"}
	for i, p := range gp {
		if got := p["type"].(string); got != wantTypes[i] {
			t.Fatalf("panel %d type = %s, want %s", i, got, wantTypes[i])
		}
	}

	// Target carries the operator's PromQL + legend verbatim, with refId
	// "A" so Grafana's query editor lights up the row.
	tgt := gp[0]["targets"].([]map[string]any)[0]
	if tgt["expr"] != "cpu_pct" {
		t.Fatalf("target expr = %v", tgt["expr"])
	}
	if tgt["legendFormat"] != "{{device_id}}" {
		t.Fatalf("target legend = %v", tgt["legendFormat"])
	}
	if tgt["refId"] != "A" {
		t.Fatalf("target refId = %v", tgt["refId"])
	}
	ds := tgt["datasource"].(map[string]any)
	if ds["uid"] != datasourceUID {
		t.Fatalf("target datasource uid = %v", ds["uid"])
	}
}

// TestMapPanelType pins the supported panel types and the
// timeseries-fallback for unknown values. Defends against silent regressions
// when adding new panel types.
func TestMapPanelType(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		monitormodel.PanelTypeTimeseries: "timeseries",
		monitormodel.PanelTypeStat:       "stat",
		monitormodel.PanelTypeGauge:      "gauge",
		"":                               "timeseries",
		"nonsense":                       "timeseries",
	}
	for in, want := range cases {
		if got := mapPanelType(in); got != want {
			t.Fatalf("mapPanelType(%q) = %s, want %s", in, got, want)
		}
	}
}
