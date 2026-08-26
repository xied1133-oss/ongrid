//go:build e2e

// Catalog: G1 — Notification channel CRUD + secret 加密 at-rest.
//
// Covers the full /v1/notification-channels lifecycle and proves the
// "secret never leaves the manager via the read APIs" invariant that
// G1's "secret 加密 at-rest" requirement boils down to in practice:
//
//   1. POST   /v1/notification-channels                — create
//   2. GET    /v1/notification-channels                — list, asserts
//      our row is present AND its `secret` is NOT present in any shape
//      (the Channel DTO at internal/manager/service/alert/service.go
//      has no `Secret` field — masking is by *omission*, not by
//      bullet-string substitution).
//   3. GET    /v1/notification-channels/{id}           — get-one, same
//      "secret never appears" assertion.
//   4. PUT    /v1/notification-channels/{id}           — update name +
//      endpoint + enabled toggle, re-list to confirm.
//   5. DELETE /v1/notification-channels/{id}           — delete, re-list
//      to confirm the row is gone.
//
// There is *no* per-channel /reveal endpoint analogous to O1's
// /system-settings/{cat}/{key}/reveal — channel secrets never need to
// be shown back to the operator (Slack webhook URLs / Feishu signing
// secrets are write-only as far as the SPA is concerned). So G1's
// "reveal" column in the catalog turns into "list-and-get omit the
// secret end-to-end", which is what this test asserts.
package e2e

import (
	"strings"
	"testing"

	"github.com/ongridio/ongrid/tests/e2e/testenv"
)

func TestNotify_ChannelCRUD_G1(t *testing.T) {
	env := testenv.Start(t)
	pair := env.LoginAdmin()

	const (
		initialName     = "e2e-g1-channel"
		initialEndpoint = "https://example.invalid/hook-g1-initial"
		// Distinctive sentinel — any response body that contains this
		// substring is a leak. Long enough that an accidental partial
		// match on a generic word ("secret") doesn't false-positive.
		cleartextSecret = "e2e-channel-secret-zzz-G1-sentinel"
	)

	// 1. CREATE ---------------------------------------------------------
	createStatus, createBody, err := env.DoJSON("POST", "/api/v1/notification-channels", map[string]any{
		"name":     initialName,
		"type":     "feishu", // arbitrary — G1 is shape-only, not provider-specific.
		"endpoint": initialEndpoint,
		"secret":   cleartextSecret,
		"enabled":  true,
	}, pair.AccessToken)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if createStatus != 200 && createStatus != 201 {
		t.Fatalf("create channel: status=%d body=%v", createStatus, createBody)
	}
	idAny, ok := createBody["id"]
	if !ok {
		t.Fatalf("create channel: response missing id (body=%v)", createBody)
	}
	channelID := numberToString(idAny)
	if channelID == "" || channelID == "0" {
		t.Fatalf("create channel: id was not a non-zero number-like (got %T %v)", idAny, idAny)
	}
	// The create response itself must not echo the cleartext secret.
	// The DTO has no `secret` json field at all, so this is really a
	// "no field smuggled it back" guard against future regressions.
	if _, hasSecret := createBody["secret"]; hasSecret {
		t.Errorf("create response carries a 'secret' field — it must never be returned (body=%v)", createBody)
	}
	if leaks := bodyContains(createBody, cleartextSecret); leaks != "" {
		t.Errorf("create response leaks cleartext secret in field %q (body=%v)", leaks, createBody)
	}

	// 2. LIST -----------------------------------------------------------
	listStatus, listBody, err := env.DoJSON("GET", "/api/v1/notification-channels", nil, pair.AccessToken)
	if err != nil || listStatus != 200 {
		t.Fatalf("list channels: status=%d err=%v body=%v", listStatus, err, listBody)
	}
	items, _ := listBody["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("list channels: items empty after create (body=%v)", listBody)
	}
	row := findChannelByID(items, channelID)
	if row == nil {
		t.Fatalf("list channels: did not find our row id=%s (items=%v)", channelID, items)
	}
	if got, _ := row["name"].(string); got != initialName {
		t.Errorf("list row name = %q, want %q", got, initialName)
	}
	// `secret` MUST be absent from the row.
	if _, hasSecret := row["secret"]; hasSecret {
		t.Errorf("list row carries a 'secret' field — must never be in the list response (row=%v)", row)
	}
	// And no field anywhere in the list payload may contain the cleartext.
	if leaks := bodyContains(listBody, cleartextSecret); leaks != "" {
		t.Errorf("list response leaks cleartext secret in field %q (body=%v)", leaks, listBody)
	}

	// 3. GET ONE --------------------------------------------------------
	getStatus, getBody, err := env.DoJSON("GET", "/api/v1/notification-channels/"+channelID, nil, pair.AccessToken)
	if err != nil || getStatus != 200 {
		t.Fatalf("get channel: status=%d err=%v body=%v", getStatus, err, getBody)
	}
	if got, _ := getBody["name"].(string); got != initialName {
		t.Errorf("get response name = %q, want %q", got, initialName)
	}
	if _, hasSecret := getBody["secret"]; hasSecret {
		t.Errorf("get response carries a 'secret' field — must never be returned (body=%v)", getBody)
	}
	if leaks := bodyContains(getBody, cleartextSecret); leaks != "" {
		t.Errorf("get response leaks cleartext secret in field %q (body=%v)", leaks, getBody)
	}

	// 4. UPDATE ---------------------------------------------------------
	// Flip enabled, rename, swap endpoint. Send an empty secret to
	// exercise the "preserve existing" branch of mergeChannelConfig
	// (passing "-" would clear it; "" leaves it alone). Per
	// internal/manager/service/alert/service.go.
	const (
		updatedName     = "e2e-g1-channel-renamed"
		updatedEndpoint = "https://example.invalid/hook-g1-updated"
	)
	putStatus, putBody, err := env.DoJSON("PUT", "/api/v1/notification-channels/"+channelID, map[string]any{
		"name":     updatedName,
		"type":     "feishu",
		"endpoint": updatedEndpoint,
		"secret":   "", // preserve
		"enabled":  false,
	}, pair.AccessToken)
	if err != nil || putStatus != 200 {
		t.Fatalf("update channel: status=%d err=%v body=%v", putStatus, err, putBody)
	}
	if got, _ := putBody["name"].(string); got != updatedName {
		t.Errorf("update response name = %q, want %q", got, updatedName)
	}
	if got, _ := putBody["enabled"].(bool); got != false {
		t.Errorf("update response enabled = %v, want false", got)
	}
	if _, hasSecret := putBody["secret"]; hasSecret {
		t.Errorf("update response carries a 'secret' field — must never be returned (body=%v)", putBody)
	}

	// Re-list and confirm the row reflects the update.
	relistStatus, relistBody, err := env.DoJSON("GET", "/api/v1/notification-channels", nil, pair.AccessToken)
	if err != nil || relistStatus != 200 {
		t.Fatalf("relist channels: status=%d err=%v body=%v", relistStatus, err, relistBody)
	}
	relistItems, _ := relistBody["items"].([]any)
	relistedRow := findChannelByID(relistItems, channelID)
	if relistedRow == nil {
		t.Fatalf("relist: row id=%s missing after update (items=%v)", channelID, relistItems)
	}
	if got, _ := relistedRow["name"].(string); got != updatedName {
		t.Errorf("relist row name = %q, want %q", got, updatedName)
	}
	if got, _ := relistedRow["enabled"].(bool); got != false {
		t.Errorf("relist row enabled = %v, want false", got)
	}
	if endpoint, _ := relistedRow["endpoint"].(string); !strings.Contains(endpoint, "hook-g1-updated") {
		// Note: the endpoint is `EndpointMasked` server-side and gets
		// truncated past 50 chars. Our updated URL stays under that
		// limit, so the suffix should appear verbatim.
		t.Errorf("relist row endpoint = %q, want it to contain %q", endpoint, "hook-g1-updated")
	}
	// Still no secret leak in the relist payload, post-update.
	if leaks := bodyContains(relistBody, cleartextSecret); leaks != "" {
		t.Errorf("relist response leaks cleartext secret in field %q (body=%v)", leaks, relistBody)
	}

	// 5. DELETE ---------------------------------------------------------
	delStatus, delBody, err := env.DoJSON("DELETE", "/api/v1/notification-channels/"+channelID, nil, pair.AccessToken)
	if err != nil {
		t.Fatalf("delete channel: %v", err)
	}
	// Handler writes 204 No Content; DoJSON returns nil body on empty.
	if delStatus != 200 && delStatus != 204 {
		t.Fatalf("delete channel: status=%d body=%v", delStatus, delBody)
	}

	finalStatus, finalBody, err := env.DoJSON("GET", "/api/v1/notification-channels", nil, pair.AccessToken)
	if err != nil || finalStatus != 200 {
		t.Fatalf("final list: status=%d err=%v body=%v", finalStatus, err, finalBody)
	}
	finalItems, _ := finalBody["items"].([]any)
	if row := findChannelByID(finalItems, channelID); row != nil {
		t.Errorf("final list: row id=%s still present after delete (row=%v)", channelID, row)
	}
}

// findChannelByID scans a /v1/notification-channels list payload for the
// row matching channelID. Channel ids round-trip through JSON as float64
// (encoding/json default), so we compare via numberToString to handle
// either shape robustly.
func findChannelByID(items []any, channelID string) map[string]any {
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if numberToString(m["id"]) == channelID {
			return m
		}
	}
	return nil
}

// bodyContains walks a decoded JSON map recursively looking for any
// string value that contains `needle`. Returns the dotted path of the
// first hit (e.g. "items[0].endpoint") so the test failure points at
// the offending field, or "" when nothing matches.
//
// We use this as the secret-leak guard rather than a top-level "is
// there a `secret` key" check because secrets could plausibly leak
// embedded inside the endpoint URL, a config_json blob, or some future
// new field — a recursive scan catches all those at once.
func bodyContains(body any, needle string) string {
	return bodyContainsAt(body, needle, "")
}

func bodyContainsAt(node any, needle, path string) string {
	switch v := node.(type) {
	case string:
		if strings.Contains(v, needle) {
			return path
		}
	case map[string]any:
		for k, child := range v {
			childPath := k
			if path != "" {
				childPath = path + "." + k
			}
			if hit := bodyContainsAt(child, needle, childPath); hit != "" {
				return hit
			}
		}
	case []any:
		for i, child := range v {
			childPath := indexedPath(path, i)
			if hit := bodyContainsAt(child, needle, childPath); hit != "" {
				return hit
			}
		}
	}
	return ""
}

func indexedPath(base string, i int) string {
	// avoid strconv import — small int formatter.
	digits := func(n int) string {
		if n == 0 {
			return "0"
		}
		var buf [20]byte
		j := len(buf)
		for n > 0 {
			j--
			buf[j] = byte('0' + n%10)
			n /= 10
		}
		return string(buf[j:])
	}
	if base == "" {
		return "[" + digits(i) + "]"
	}
	return base + "[" + digits(i) + "]"
}
