//go:build e2e

// Catalog: HLD-017 — credential vault + approval inbox + cloud_bash
// registration, over HTTP on a clean manager. Covers the deterministic API
// surface (the propose→approve→execute chain itself is pinned by the Go
// integration test tests/integration/cloudbash_chain_test.go, since
// cloud_bash is destructive-class and can't be driven through /v1/skills/
// execute, and there's no public propose endpoint).
//
// Routes (all admin-gated, /api prefix):
//
//	POST/GET /api/v1/secrets ; GET /api/v1/credential-types
//	GET /api/v1/approvals ; GET /api/v1/skills (cloud_bash must appear)
package e2e

import (
	"testing"

	"github.com/ongridio/ongrid/tests/e2e/testenv"
)

func TestCredentials_VaultAndApprovalSurface_HLD017(t *testing.T) {
	env := testenv.Start(t)
	pair := env.LoginAdmin()
	tok := pair.AccessToken

	// ─── auth gate ────────────────────────────────────────────────────────
	if status, _, err := env.DoJSON("GET", "/api/v1/secrets", nil, ""); err != nil || status != 401 {
		t.Fatalf("GET /v1/secrets no token: status=%d err=%v want 401", status, err)
	}

	// ─── credential types are served (drives the create form) ─────────────
	status, body, err := env.DoJSON("GET", "/api/v1/credential-types", nil, tok)
	if err != nil || status != 200 {
		t.Fatalf("GET /v1/credential-types: status=%d err=%v", status, err)
	}
	types, _ := body["items"].([]any)
	if len(types) == 0 || !hasNamed(types, "custom") {
		t.Fatalf("credential-types must include 'custom': %v", body["items"])
	}

	// ─── create a custom credential ───────────────────────────────────────
	status, _, err = env.DoJSON("POST", "/api/v1/secrets", map[string]any{
		"name":   "e2e-cred",
		"type":   "custom",
		"fields": map[string]string{"API_TOKEN": "s3cr3t-value"},
	}, tok)
	if err != nil || status != 200 {
		t.Fatalf("POST /v1/secrets: status=%d err=%v", status, err)
	}

	// ─── list is REDACTED: field keys present, value absent ───────────────
	status, body, err = env.DoJSON("GET", "/api/v1/secrets", nil, tok)
	if err != nil || status != 200 {
		t.Fatalf("GET /v1/secrets: status=%d err=%v", status, err)
	}
	items, _ := body["items"].([]any)
	row := findByField(items, "name", "e2e-cred")
	if row == nil {
		t.Fatalf("created credential not listed: %v", items)
	}
	if _, leaked := row["value"]; leaked {
		t.Fatalf("SECURITY: credential list leaked a value field: %v", row)
	}
	fks, _ := row["field_keys"].([]any)
	if len(fks) != 1 || fks[0] != "API_TOKEN" {
		t.Fatalf("field_keys = %v, want [API_TOKEN]", row["field_keys"])
	}

	// ─── approvals inbox endpoint works (empty on a clean manager) ────────
	status, body, err = env.DoJSON("GET", "/api/v1/approvals", nil, tok)
	if err != nil || status != 200 {
		t.Fatalf("GET /v1/approvals: status=%d err=%v", status, err)
	}
	if _, ok := body["items"]; !ok {
		t.Fatalf("GET /v1/approvals missing items: %v", body)
	}

	// ─── cloud_bash is registered (so the agent CAN reach it) ─────────────
	status, body, err = env.DoJSON("GET", "/api/v1/skills", nil, tok)
	if err != nil || status != 200 {
		t.Fatalf("GET /v1/skills: status=%d err=%v", status, err)
	}
	skills, _ := body["items"].([]any)
	if !hasKeyed(skills, "key", "cloud_bash") {
		t.Fatalf("cloud_bash not registered in /v1/skills — the agent can't reach it")
	}
}

// --- helpers ---

func hasNamed(items []any, name string) bool { return hasKeyed(items, "name", name) }

func hasKeyed(items []any, key, want string) bool {
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			if s, _ := m[key].(string); s == want {
				return true
			}
		}
	}
	return false
}

func findByField(items []any, key, want string) map[string]any {
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			if s, _ := m[key].(string); s == want {
				return m
			}
		}
	}
	return nil
}
