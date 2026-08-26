//go:build e2e

// Catalog: T3 — MCP tool as a deterministic flow node (HLD-018 P5). Register an
// external MCP server, then a flow whose tool node is mcp__<server>__echo; the
// run must dispatch tools/call to the (mock) server and surface its result. This
// covers the gap mcp_test.go leaves: CRUD/probe are tested there, but not an
// actual tools/call routed through the flow engine + live MCP palette source.
package e2e

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ongridio/ongrid/tests/e2e/testenv"
)

func TestFlow_MCPToolNode_T3(t *testing.T) {
	var authSeen string
	var mu sync.Mutex
	srv := mockMCP(&authSeen, &mu) // answers initialize / tools/list (echo) / tools/call
	defer srv.Close()

	env := testenv.Start(t)
	tok := env.LoginAdmin().AccessToken

	// register the MCP server (ASCII name → clean wire name mcp__echosrv__echo)
	status, body, err := env.DoJSON("POST", "/api/v1/mcp/servers", map[string]any{
		"name":      "echosrv",
		"transport": "http",
		"endpoint":  srv.URL,
		"trusted":   true,
		"enabled":   true,
	}, tok)
	if err != nil || (status != 200 && status != 201) {
		t.Fatalf("register mcp server: status=%d err=%v body=%v", status, err, body)
	}

	// flow: trigger → mcp tool node
	graph := map[string]any{
		"nodes": []any{
			map[string]any{"id": "t", "type": "trigger.manual", "name": "手动触发", "config": map[string]any{}},
			map[string]any{"id": "m", "type": "tool", "name": "MCP echo", "config": map[string]any{
				"tool": "mcp__echosrv__echo",
				"args": map[string]any{"msg": "hi-from-flow"},
			}},
		},
		"edges": []any{map[string]any{"id": "e1", "source": "t", "target": "m"}},
	}
	status, body, err = env.DoJSON("POST", "/api/v1/flows", map[string]any{"name": "e2e-mcp-node", "graph": graph}, tok)
	if err != nil || status != 201 {
		t.Fatalf("create flow with mcp node: status=%d err=%v body=%v", status, err, body)
	}
	flowID := numToStr(body["id"])

	status, body, _ = env.DoJSON("POST", fmt.Sprintf("/api/v1/flows/%s/run", flowID), map[string]any{"input": map[string]any{}}, tok)
	if status != 200 && status != 201 && status != 202 {
		t.Fatalf("run mcp flow: status=%d body=%v", status, body)
	}
	runID, _ := body["id"].(string)
	if runID == "" {
		if r, ok := body["run"].(map[string]any); ok {
			runID, _ = r["id"].(string)
		}
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		status, body, _ := env.DoJSON("GET", "/api/v1/flow-runs/"+runID, nil, tok)
		if status != 200 {
			time.Sleep(400 * time.Millisecond)
			continue
		}
		run, _ := body["run"].(map[string]any)
		st, _ := run["status"].(string)
		if st == "running" || st == "pending" {
			time.Sleep(400 * time.Millisecond)
			continue
		}
		nodes, _ := body["nodes"].([]any)
		if flowNodeStatus(nodes, "m") != "succeeded" {
			t.Fatalf("mcp node status=%q (want succeeded), run.status=%q err=%v", flowNodeStatus(nodes, "m"), st, run["error"])
		}
		// the mock's tools/call result must have flowed through
		if !flowNodeOutputContains(nodes, "m", "echo-ok-T3") {
			t.Fatalf("mcp node output missing tools/call result; nodes=%v", nodes)
		}
		return
	}
	t.Fatalf("mcp flow run did not finish in 30s")
}

func flowNodeOutputContains(nodes []any, nodeID, want string) bool {
	for _, it := range nodes {
		if m, ok := it.(map[string]any); ok {
			if id, _ := m["node_id"].(string); id == nodeID {
				return strings.Contains(fmt.Sprintf("%v", m["output"]), want)
			}
		}
	}
	return false
}
