//go:build e2e

// Catalog: HLD-016 workflow orchestration — over HTTP on a clean manager.
// Covers the full surface the SPA drives:
//
//	GET  /api/v1/flow-node-types     (palette: node types, incl trigger.manual/set)
//	GET  /api/v1/flow-tools          (palette: registered BaseTools)
//	POST/GET/PUT/DELETE /api/v1/flows ; POST /flows/{id}/toggle
//	POST /api/v1/flows/{id}/test-node (single-node试跑)
//	POST /api/v1/flows/{id}/run + GET /api/v1/flow-runs/{runId} (端到端执行)
//
// The exercised flow uses ONLY the `set` node so the run is deterministic
// (no LLM / external tool needed): trigger.manual → set(greeting=hello-e2e).
package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ongridio/ongrid/tests/e2e/testenv"
)

func TestWorkflow_CatalogCRUDAndRun_HLD016(t *testing.T) {
	env := testenv.Start(t)
	tok := env.LoginAdmin().AccessToken

	// ─── auth gate ────────────────────────────────────────────────────────
	if status, _, err := env.DoJSON("GET", "/api/v1/flows", nil, ""); err != nil || status != 401 {
		t.Fatalf("GET /flows no token: status=%d err=%v want 401", status, err)
	}

	// ─── catalog: node types include trigger.manual + set ─────────────────
	status, body, err := env.DoJSON("GET", "/api/v1/flow-node-types", nil, tok)
	if err != nil || status/100 != 2 {
		t.Fatalf("flow-node-types: status=%d err=%v", status, err)
	}
	types, _ := body["items"].([]any)
	if !wfHasNodeType(types, "trigger.manual") || !wfHasNodeType(types, "set") {
		t.Fatalf("node-type catalog must include trigger.manual + set: %v", types)
	}

	// ─── catalog: flow-tools non-empty (registered BaseTools as nodes) ────
	status, body, err = env.DoJSON("GET", "/api/v1/flow-tools", nil, tok)
	if err != nil || status/100 != 2 {
		t.Fatalf("flow-tools: status=%d err=%v", status, err)
	}
	if tools, _ := body["items"].([]any); len(tools) == 0 {
		t.Fatalf("flow-tools is empty — no BaseTools surfaced to the canvas")
	}

	// ─── create flow: trigger.manual → set(greeting=hello-e2e) ────────────
	graph := map[string]any{
		"nodes": []any{
			map[string]any{"id": "t", "type": "trigger.manual"},
			map[string]any{"id": "a", "type": "set", "config": map[string]any{"name": "greeting", "value": "hello-e2e"}},
		},
		"edges": []any{
			map[string]any{"id": "e1", "source": "t", "target": "a"},
		},
	}
	status, body, err = env.DoJSON("POST", "/api/v1/flows", map[string]any{
		"name": "e2e-workflow", "description": "e2e deterministic", "graph": graph,
	}, tok)
	if err != nil || status/100 != 2 {
		t.Fatalf("create flow: status=%d err=%v body=%v", status, err, body)
	}
	id := wfInt(body["id"])
	if id == 0 {
		t.Fatalf("create flow returned no id: %v", body)
	}

	// ─── get / update / toggle ────────────────────────────────────────────
	status, body, _ = env.DoJSON("GET", fmt.Sprintf("/api/v1/flows/%d", id), nil, tok)
	if status/100 != 2 || body["name"] != "e2e-workflow" {
		t.Fatalf("get flow: status=%d body=%v", status, body)
	}
	if status, _, _ = env.DoJSON("PUT", fmt.Sprintf("/api/v1/flows/%d", id), map[string]any{"name": "e2e-workflow-2"}, tok); status/100 != 2 {
		t.Fatalf("update flow: status=%d", status)
	}
	if status, _, _ = env.DoJSON("POST", fmt.Sprintf("/api/v1/flows/%d/toggle", id), map[string]any{"enabled": true}, tok); status/100 != 2 {
		t.Fatalf("toggle flow: status=%d", status)
	}

	// ─── single-node test-run ─────────────────────────────────────────────
	status, body, _ = env.DoJSON("POST", fmt.Sprintf("/api/v1/flows/%d/test-node", id), map[string]any{
		"node_type": "set", "config": map[string]any{"name": "x", "value": "v1"},
	}, tok)
	if status/100 != 2 {
		t.Fatalf("test-node: status=%d body=%v", status, body)
	}
	if e, _ := body["error"].(string); e != "" {
		t.Fatalf("test-node returned error: %s", e)
	}
	if !wfJSONContains(body["output"], "v1") {
		t.Fatalf("test-node output missing the set value 'v1': %v", body["output"])
	}

	// ─── full run + poll until terminal ───────────────────────────────────
	status, body, _ = env.DoJSON("POST", fmt.Sprintf("/api/v1/flows/%d/run", id), map[string]any{"input": map[string]any{}}, tok)
	if status/100 != 2 {
		t.Fatalf("run flow: status=%d body=%v", status, body)
	}
	runID, _ := body["id"].(string)
	if runID == "" {
		t.Fatalf("run returned no run id: %v", body)
	}

	var run map[string]any
	var nodes []any
	for i := 0; i < 50; i++ {
		status, body, _ = env.DoJSON("GET", "/api/v1/flow-runs/"+runID, nil, tok)
		if status == 200 {
			run, _ = body["run"].(map[string]any)
			nodes, _ = body["nodes"].([]any)
			if st, _ := run["status"].(string); st == "succeeded" || st == "failed" || st == "canceled" {
				break
			}
		}
		time.Sleep(400 * time.Millisecond)
	}
	if st, _ := run["status"].(string); st != "succeeded" {
		t.Fatalf("run did not succeed: status=%v err=%v", run["status"], run["error"])
	}
	if !wfNodeSucceededWith(nodes, "a", "hello-e2e") {
		t.Fatalf("set node 'a' missing/failed or output lacks hello-e2e: %v", nodes)
	}

	// ─── delete ───────────────────────────────────────────────────────────
	if status, _, _ = env.DoJSON("DELETE", fmt.Sprintf("/api/v1/flows/%d", id), nil, tok); status/100 != 2 && status != 204 {
		t.Fatalf("delete flow: status=%d", status)
	}
}

// ── helpers (wf-prefixed to avoid clashes with other e2e files) ──────────

func wfHasNodeType(items []any, typ string) bool {
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			if s, _ := m["type"].(string); s == typ {
				return true
			}
		}
	}
	return false
}

func wfInt(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}

func wfJSONContains(v any, sub string) bool {
	b, _ := json.Marshal(v)
	return strings.Contains(string(b), sub)
}

func wfNodeSucceededWith(nodes []any, nodeID, want string) bool {
	for _, n := range nodes {
		m, ok := n.(map[string]any)
		if !ok {
			continue
		}
		if did, _ := m["node_id"].(string); did == nodeID {
			if st, _ := m["status"].(string); st != "succeeded" {
				return false
			}
			return wfJSONContains(m["output"], want)
		}
	}
	return false
}
