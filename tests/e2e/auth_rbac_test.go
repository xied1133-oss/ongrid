//go:build e2e

// Catalog: B3 — admin / user / viewer 三角色 RBAC。证明三层权限闸门：
//
//   - Endpoint A — admin-only mutation: POST /v1/users
//     admin → 201, user → 403, viewer → 403.
//   - Endpoint B — admin + user can write, viewer cannot:
//     POST /v1/agents/custom (createUserAgent in aiops). 该 handler 显式
//     `caller.IsViewer() → 403`，admin/user 走到 200/201。
//   - Endpoint C — any authenticated role: GET /v1/self
//     admin / user / viewer 都 200.
//
// 全部走 testenv.Start，不依赖真实外部服务。
package e2e

import (
	"testing"

	"github.com/ongridio/ongrid/tests/e2e/testenv"
)

func TestAuth_RBAC_ThreeTier_B3(t *testing.T) {
	env := testenv.Start(t)
	adminPair := env.LoginAdmin()

	const (
		userEmail    = "rbac-user@ongrid.local"
		userPass     = "E2E!RBAC-user-pass"
		viewerEmail  = "rbac-viewer@ongrid.local"
		viewerPass   = "E2E!RBAC-viewer-pass"
	)

	// Admin seeds the two lesser-privileged accounts. Endpoint A on the
	// happy path: admin can create users → 201.
	mustCreateUser(t, env, adminPair.AccessToken, userEmail, userPass, "user")
	mustCreateUser(t, env, adminPair.AccessToken, viewerEmail, viewerPass, "viewer")

	userPair := env.Login(userEmail, userPass)
	viewerPair := env.Login(viewerEmail, viewerPass)

	// ─── Endpoint A: POST /v1/users — admin only ────────────────────────
	// Admin success was already proven above by the two seeded users.
	// Now both non-admin roles must be 403, never letting them mint
	// new identities.
	for _, c := range []struct {
		role   string
		token  string
		email  string
	}{
		{"user", userPair.AccessToken, "rbac-user-attempt@ongrid.local"},
		{"viewer", viewerPair.AccessToken, "rbac-viewer-attempt@ongrid.local"},
	} {
		status, body, err := env.DoJSON("POST", "/api/v1/users", map[string]any{
			"email":            c.email,
			"password":         "irrelevant-should-not-be-checked",
			"role":             "user",
			"skip_default_org": true,
		}, c.token)
		if err != nil {
			t.Fatalf("[A] POST /v1/users as %s: transport: %v", c.role, err)
		}
		if status != 403 {
			t.Fatalf("[A] POST /v1/users as %s: status=%d want 403 (body=%v)", c.role, status, body)
		}
	}

	// ─── Endpoint B: POST /v1/agents/custom — viewer only is denied ─────
	// admin + user can create a custom agent (201); viewer hits the
	// `caller.IsViewer()` short-circuit and gets 403.
	mustCreateCustomAgent(t, env, adminPair.AccessToken, "rbac-admin-agent")
	mustCreateCustomAgent(t, env, userPair.AccessToken, "rbac-user-agent")

	status, body, err := env.DoJSON("POST", "/api/v1/agents/custom", map[string]any{
		"name":          "rbac-viewer-agent",
		"description":   "viewer should never get this far",
		"system_prompt": "this prompt is never persisted",
	}, viewerPair.AccessToken)
	if err != nil {
		t.Fatalf("[B] POST /v1/agents/custom as viewer: transport: %v", err)
	}
	if status != 403 {
		t.Fatalf("[B] POST /v1/agents/custom as viewer: status=%d want 403 (body=%v)", status, body)
	}

	// ─── Endpoint C: GET /v1/self — every authenticated role works ──────
	for _, c := range []struct {
		role  string
		token string
		email string
	}{
		{"admin", adminPair.AccessToken, env.AdminEmail},
		{"user", userPair.AccessToken, userEmail},
		{"viewer", viewerPair.AccessToken, viewerEmail},
	} {
		status, body, err := env.DoJSON("GET", "/api/v1/self", nil, c.token)
		if err != nil {
			t.Fatalf("[C] GET /v1/self as %s: transport: %v", c.role, err)
		}
		if status != 200 {
			t.Fatalf("[C] GET /v1/self as %s: status=%d want 200 (body=%v)", c.role, status, body)
		}
		gotEmail, _ := body["email"].(string)
		if gotEmail != c.email {
			t.Fatalf("[C] GET /v1/self as %s: email=%q want %q (body=%v)", c.role, gotEmail, c.email, body)
		}
		gotRole, _ := body["role"].(string)
		if gotRole != c.role {
			t.Fatalf("[C] GET /v1/self as %s: role=%q want %q (body=%v)", c.role, gotRole, c.role, body)
		}
	}
}

// mustCreateUser POSTs /v1/users with the admin token and fails the
// test on any non-201 response. SkipDefaultOrg keeps the test
// independent of the seeded "默认组织" — we don't need memberships for
// this RBAC slice.
func mustCreateUser(t *testing.T, env *testenv.Env, adminToken, email, password, role string) {
	t.Helper()
	status, body, err := env.DoJSON("POST", "/api/v1/users", map[string]any{
		"email":            email,
		"password":         password,
		"role":             role,
		"display_name":     role + "-fixture",
		"skip_default_org": true,
	}, adminToken)
	if err != nil {
		t.Fatalf("seed user %q: transport: %v", email, err)
	}
	if status != 201 {
		t.Fatalf("seed user %q: status=%d body=%v", email, status, body)
	}
}

// mustCreateCustomAgent POSTs /v1/agents/custom and fails the test on
// any non-2xx. The user-agent service requires name (regex), description,
// system_prompt — we fill all three with the minimum needed to pass
// validation so the role check is the only meaningful gate.
func mustCreateCustomAgent(t *testing.T, env *testenv.Env, token, name string) {
	t.Helper()
	status, body, err := env.DoJSON("POST", "/api/v1/agents/custom", map[string]any{
		"name":          name,
		"description":   "RBAC fixture: " + name,
		"system_prompt": "You are " + name + ". This is an e2e fixture, never invoked.",
	}, token)
	if err != nil {
		t.Fatalf("create custom agent %q: transport: %v", name, err)
	}
	if status != 200 && status != 201 {
		t.Fatalf("create custom agent %q: status=%d body=%v", name, status, body)
	}
}
