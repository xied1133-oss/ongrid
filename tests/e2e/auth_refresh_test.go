//go:build e2e

// Catalog: B2 — JWT access 过期 + refresh。流程：
//   - 把 access TTL 压到 2s 起 manager（refresh TTL 留默认 7d）
//   - LoginAdmin 拿到 (access, refresh)
//   - 用 access 立即调 /api/v1/self → 200
//   - 等 3s 让 access 过期
//   - 再用同一 access 调 /api/v1/self → 401
//   - 调 /api/v1/auth/refresh 拿到新 (access', refresh')
//   - 用 access' 调 /api/v1/self → 200
package e2e

import (
	"testing"
	"time"

	"github.com/ongridio/ongrid/tests/e2e/testenv"
)

func TestAuth_RefreshAfterAccessExpiry_B2(t *testing.T) {
	env := testenv.Start(t, testenv.WithEnv("ONGRID_JWT_ACCESS_TTL", "2s"))

	pair := env.LoginAdmin()

	// access 还新，/v1/self 应当 200
	if status, _, err := env.DoJSON("GET", "/api/v1/self", nil, pair.AccessToken); err != nil || status != 200 {
		t.Fatalf("/v1/self with fresh access: status=%d err=%v", status, err)
	}

	// 等到 access 过期。2s TTL + 1s 余量。
	time.Sleep(3 * time.Second)

	if status, _, err := env.DoJSON("GET", "/api/v1/self", nil, pair.AccessToken); err != nil || status != 401 {
		t.Fatalf("/v1/self with expired access: status=%d err=%v (expected 401)", status, err)
	}

	if pair.RefreshToken == "" {
		t.Fatalf("login returned no refresh_token; cannot exercise refresh path")
	}
	status, body, err := env.DoJSON("POST", "/api/v1/auth/refresh", map[string]string{
		"refresh_token": pair.RefreshToken,
	}, "")
	if err != nil || status != 200 {
		t.Fatalf("refresh: status=%d body=%v err=%v", status, body, err)
	}
	newAccess, _ := body["access_token"].(string)
	if newAccess == "" || newAccess == pair.AccessToken {
		t.Fatalf("refresh: expected a new access_token (got %q vs old %q)", short(newAccess), short(pair.AccessToken))
	}

	if status, _, err := env.DoJSON("GET", "/api/v1/self", nil, newAccess); err != nil || status != 200 {
		t.Fatalf("/v1/self with refreshed access: status=%d err=%v", status, err)
	}
}

func short(s string) string {
	if len(s) <= 16 {
		return s
	}
	return s[:8] + "…" + s[len(s)-8:]
}
