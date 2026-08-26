//go:build e2e

// Catalog: HLD-016 workflow orchestration — natural-language → workflow.
// Exercises POST /api/v1/flows/generate {prompt}: the model drafts a graph from
// the live tool catalog, the manager validates + persists it, and returns the
// new flow for the SPA to open. The FakeLLM returns a canned {name,description,
// graph} (trigger.manual → set), so the path is deterministic; we then RUN the
// generated flow to prove the AI-drafted graph is actually executable.
package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/ongridio/ongrid/tests/e2e/testenv"
)

func TestWorkflow_GenerateFromNaturalLanguage_HLD016(t *testing.T) {
	env := testenv.Start(t)
	tok := env.LoginAdmin().AccessToken

	// The "model" drafts a workflow: manual trigger → set greeting. Deterministic
	// (no real tool needed) so the subsequent run is reproducible. This is the
	// exact JSON envelope GenerateGraph expects (name/description/graph).
	env.FakeLLM().SetLLMReply(`{"name":"NL 自动巡检","description":"自然语言生成的工作流","graph":{"nodes":[{"id":"t","type":"trigger.manual","config":{}},{"id":"a","type":"set","config":{"name":"greeting","value":"hello-from-nl"}}],"edges":[{"id":"e1","source":"t","target":"a"}]}}`)

	// ─── auth gate ────────────────────────────────────────────────────────
	if status, _, err := env.DoJSON("POST", "/api/v1/flows/generate", map[string]any{"prompt": "x"}, ""); err != nil || status != 401 {
		t.Fatalf("generate no token: status=%d err=%v want 401", status, err)
	}

	// ─── empty prompt rejected ────────────────────────────────────────────
	if status, _, _ := env.DoJSON("POST", "/api/v1/flows/generate", map[string]any{"prompt": "   "}, tok); status/100 != 4 {
		t.Fatalf("empty prompt should be 4xx, got %d", status)
	}

	// ─── generate from natural language ───────────────────────────────────
	status, body, err := env.DoJSON("POST", "/api/v1/flows/generate", map[string]any{
		"prompt": "做一个手动触发、然后设置问候语 greeting 的工作流",
	}, tok)
	if err != nil || status != 201 {
		t.Fatalf("generate: status=%d err=%v body=%v", status, err, body)
	}
	if env.FakeLLM().CallCount() < 1 {
		t.Fatalf("generation should have called the LLM at least once")
	}
	if body["name"] != "NL 自动巡检" {
		t.Fatalf("generated flow name = %v, want 'NL 自动巡检'", body["name"])
	}
	id := wfInt(body["id"])
	if id == 0 {
		t.Fatalf("generate returned no flow id: %v", body)
	}

	// enable so the manual run isn't gated, then RUN the AI-drafted flow.
	if status, _, _ = env.DoJSON("POST", fmt.Sprintf("/api/v1/flows/%d/toggle", id), map[string]any{"enabled": true}, tok); status/100 != 2 {
		t.Fatalf("toggle generated flow: status=%d", status)
	}
	status, body, _ = env.DoJSON("POST", fmt.Sprintf("/api/v1/flows/%d/run", id), map[string]any{"input": map[string]any{}}, tok)
	if status/100 != 2 {
		t.Fatalf("run generated flow: status=%d body=%v", status, body)
	}
	runID, _ := body["id"].(string)
	if runID == "" {
		t.Fatalf("run returned no run id: %v", body)
	}

	// ─── poll until terminal + assert the drafted set node executed ───────
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
		t.Fatalf("generated flow run did not succeed: status=%v err=%v", run["status"], run["error"])
	}
	// The set node 'a' from the AI-drafted graph must have run and carried the
	// drafted value — proves the generated graph is well-formed AND executable.
	if !wfNodeSucceededWith(nodes, "a", "hello-from-nl") {
		t.Fatalf("generated set node 'a' missing/failed or output lacks hello-from-nl: %v", nodes)
	}
}
