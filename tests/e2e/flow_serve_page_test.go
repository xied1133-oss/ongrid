//go:build e2e

// Catalog: T4 — serve_page builtin produces a page artifact via a flow node
// (v0.9.0). A flow tool node hosts an HTML page; the run must succeed, return an
// in-app /pages/<id> url, and the page must show up under GET /v1/pages. Guards
// the "AI report → serve_page → hosted page" workflow shape end to end.
package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ongridio/ongrid/tests/e2e/testenv"
)

func TestFlow_ServePageArtifact_T4(t *testing.T) {
	env := testenv.Start(t)
	tok := env.LoginAdmin().AccessToken

	const title = "e2e-served-page-T4"
	graph := map[string]any{
		"nodes": []any{
			map[string]any{"id": "t", "type": "trigger.manual", "name": "手动触发", "config": map[string]any{}},
			map[string]any{"id": "p", "type": "tool", "name": "托管网页", "config": map[string]any{
				"tool": "serve_page",
				"args": map[string]any{
					"html":  "<!DOCTYPE html><html><body><h1>Hello T4</h1></body></html>",
					"title": title,
				},
			}},
		},
		"edges": []any{map[string]any{"id": "e1", "source": "t", "target": "p"}},
	}
	status, body, err := env.DoJSON("POST", "/api/v1/flows", map[string]any{"name": "e2e-serve-page", "graph": graph}, tok)
	if err != nil || status != 201 {
		t.Fatalf("create flow: status=%d err=%v body=%v", status, err, body)
	}
	flowID := numToStr(body["id"])

	status, body, _ = env.DoJSON("POST", fmt.Sprintf("/api/v1/flows/%s/run", flowID), map[string]any{"input": map[string]any{}}, tok)
	if status != 200 && status != 201 && status != 202 {
		t.Fatalf("run flow: status=%d body=%v", status, body)
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
		if status == 200 {
			run, _ := body["run"].(map[string]any)
			st, _ := run["status"].(string)
			if st != "running" && st != "pending" {
				nodes, _ := body["nodes"].([]any)
				if flowNodeStatus(nodes, "p") != "succeeded" {
					t.Fatalf("serve_page node status=%q err=%v", flowNodeStatus(nodes, "p"), run["error"])
				}
				if !flowNodeOutputContains(nodes, "p", "/pages/") {
					t.Fatalf("serve_page output missing /pages/ url; nodes=%v", nodes)
				}
				break
			}
		}
		time.Sleep(400 * time.Millisecond)
	}

	// the hosted page is now a real artifact
	status, body, _ = env.DoJSON("GET", "/api/v1/pages", nil, tok)
	if status != 200 {
		t.Fatalf("list pages: status=%d", status)
	}
	found := false
	for _, it := range firstList(body, "pages", "items") {
		if m, ok := it.(map[string]any); ok {
			if s, _ := m["title"].(string); strings.Contains(s, title) {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("served page %q not found under /v1/pages: %v", title, body)
	}
}
