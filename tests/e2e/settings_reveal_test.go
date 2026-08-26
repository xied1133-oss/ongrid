//go:build e2e

// Catalog: O1 — system-settings reveal + 敏感字段加密。流程：
//   - PUT a known sensitive key (foo_api_key = "secret-payload-zzz") with
//     sensitive=true → response 200, listed value masked
//   - GET /v1/system-settings?category=test → list shows the row with
//     value masked, sensitive=true
//   - GET /v1/system-settings/test/foo_api_key/reveal → returns cleartext
//   - non-admin caller is out of scope here (PR-RBAC), so this test only
//     proves the mask + reveal symmetry, not the role gate
package e2e

import (
	"strings"
	"testing"

	"github.com/ongridio/ongrid/tests/e2e/testenv"
)

func TestSettings_RevealSensitive_O1(t *testing.T) {
	env := testenv.Start(t)
	pair := env.LoginAdmin()

	const (
		category  = "test"
		key       = "foo_api_key" // suffix triggers auto-sensitive
		cleartext = "secret-payload-zzz"
	)

	// PUT — server should auto-flag sensitive because the key ends in _api_key.
	putStatus, putBody, err := env.DoJSON("PUT",
		"/api/v1/system-settings/"+category+"/"+key,
		map[string]any{"value": cleartext},
		pair.AccessToken,
	)
	if err != nil || putStatus != 200 {
		t.Fatalf("PUT setting: status=%d body=%v err=%v", putStatus, putBody, err)
	}
	if sens, _ := putBody["sensitive"].(bool); !sens {
		t.Errorf("PUT response: sensitive should be true (key ends in _api_key); body=%v", putBody)
	}
	if val, _ := putBody["value"].(string); val == cleartext {
		t.Errorf("PUT response: value returned cleartext %q — should be masked", val)
	}

	// LIST — list endpoint must mask the value.
	listStatus, listBody, err := env.DoJSON("GET",
		"/api/v1/system-settings?category="+category,
		nil, pair.AccessToken,
	)
	if err != nil || listStatus != 200 {
		t.Fatalf("LIST settings: status=%d err=%v", listStatus, err)
	}
	items, _ := listBody["items"].([]any)
	var found map[string]any
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m["key"] == key {
			found = m
			break
		}
	}
	if found == nil {
		t.Fatalf("LIST: did not find the row we just PUT (items=%v)", items)
	}
	if val, _ := found["value"].(string); strings.Contains(val, cleartext) {
		t.Errorf("LIST: value leaked the cleartext %q (value=%q)", cleartext, val)
	}

	// REVEAL — admin can get the cleartext back.
	revealStatus, revealBody, err := env.DoJSON("GET",
		"/api/v1/system-settings/"+category+"/"+key+"/reveal",
		nil, pair.AccessToken,
	)
	if err != nil || revealStatus != 200 {
		t.Fatalf("REVEAL: status=%d err=%v body=%v", revealStatus, err, revealBody)
	}
	if val, _ := revealBody["value"].(string); val != cleartext {
		t.Fatalf("REVEAL: got %q, expected %q", val, cleartext)
	}

	// No-bearer REVEAL must 401, not leak the value.
	bad, _, err := env.DoJSON("GET",
		"/api/v1/system-settings/"+category+"/"+key+"/reveal",
		nil, "")
	if err != nil {
		t.Fatalf("REVEAL unauthorized: %v", err)
	}
	if bad != 401 {
		t.Fatalf("REVEAL unauthorized: status=%d want 401", bad)
	}
}
