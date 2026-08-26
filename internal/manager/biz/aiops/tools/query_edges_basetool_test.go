package tools

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	edgebiz "github.com/ongridio/ongrid/internal/manager/biz/edge"
	edgemodel "github.com/ongridio/ongrid/internal/manager/model/edge"
)

func TestQueryEdgesTool_Info(t *testing.T) {
	uc := edgebiz.NewUsecase(newFakeEdgeRepo(), nil, nil, slog.Default())
	tool := NewQueryEdgesTool(nil, uc, nil)
	info, err := tool.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Name != ToolNameQueryEdges {
		t.Errorf("Name = %q", info.Name)
	}
	if !strings.Contains(strings.ToLower(info.WhenToUse), "not") {
		t.Errorf("WhenToUse needs reverse guard")
	}
}

func TestQueryEdgesTool_LegacyEdgeFallback(t *testing.T) {
	// devices nil → falls back to edges path.
	e1 := &edgemodel.Edge{ID: 1, Name: "alpha", Status: "online"}
	e2 := &edgemodel.Edge{ID: 2, Name: "beta", Status: "offline"}
	uc := edgebiz.NewUsecase(newFakeEdgeRepo(e1, e2), nil, nil, slog.Default())
	tool := NewQueryEdgesTool(nil, uc, nil)

	out, err := tool.InvokableRun(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	devs, _ := got["devices"].([]any)
	if len(devs) == 0 {
		t.Errorf("expected devices in response")
	}
}

func TestQueryEdgesTool_BadArgs(t *testing.T) {
	uc := edgebiz.NewUsecase(newFakeEdgeRepo(), nil, nil, slog.Default())
	tool := NewQueryEdgesTool(nil, uc, nil)
	if _, err := tool.InvokableRun(context.Background(), `not json`); err == nil {
		t.Errorf("expected error for non-JSON")
	}
}

func TestQueryEdgesTool_NilDeps(t *testing.T) {
	tool := NewQueryEdgesTool(nil, nil, nil)
	_, err := tool.InvokableRun(context.Background(), `{}`)
	if err == nil {
		t.Errorf("expected error when both devices and edges are nil")
	}
}
