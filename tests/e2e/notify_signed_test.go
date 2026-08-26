//go:build e2e

// Catalog: G4 — Feishu / DingTalk notification 通道：创建带 secret 的
//          channel → POST /test → 假 webhook endpoint 收到带签名的请求。
//          Feishu 签名进 JSON body (timestamp + sign 顶层字段，sign 是
//          HMAC-SHA256 base64 of "<timestamp>\n<secret>")；DingTalk 签名
//          进 URL query (?timestamp=...&sign=...，sign 是 HMAC-SHA256
//          base64 of "<timestamp>\n<secret>", URL-encoded)。验证
//          internal/pkg/notify/webhook.go NewFeishuSender + NewDingTalkSender
//          的输出与 G4 描述一致。
//
// Both subtests reuse the in-process FakeSlack httptest server — it just
// captures POSTs (URL path + raw query + body) and replies 200, which is
// exactly what real Feishu and DingTalk bot endpoints do too. We added
// SlackCapture.RawQuery for the DingTalk URL-signing path; that's the
// only delta to the shared fake.
package e2e

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	"github.com/ongridio/ongrid/tests/e2e/testenv"
)

// G4 covers both providers in one test file (one catalog row, two
// payload-shape subtests). Each subtest gets its own channel id so the
// fake's capture list isolates cleanly even though they share one
// httptest.Server instance.
func TestNotify_Signed_G4(t *testing.T) {
	env := testenv.Start(t)
	pair := env.LoginAdmin()

	t.Run("Feishu_sign_in_body", func(t *testing.T) {
		const secret = "e2e-secret-feishu"

		// Each subtest captures only its own POST: snapshot the existing
		// capture count, then assert exactly +1 after the test send.
		baselineCount := len(env.FakeSlack().Captures())

		channelID := createChannel(t, env, pair.AccessToken, channelSpec{
			name:     "e2e-feishu",
			channel:  "feishu",
			endpoint: env.FakeSlack().WebhookURL(),
			secret:   secret,
		})
		fireTestSend(t, env, pair.AccessToken, channelID)

		caps := env.FakeSlack().Captures()
		if len(caps) != baselineCount+1 {
			t.Fatalf("expected exactly 1 new Feishu POST, got %d (baseline=%d)", len(caps)-baselineCount, baselineCount)
		}
		c := caps[baselineCount]

		// Body shape: msg_type=text, content.text non-empty, timestamp +
		// sign on the top-level JSON. The two signing fields are the
		// whole point of G4.
		if mt, _ := c.Body["msg_type"].(string); mt != "text" {
			t.Errorf("Feishu body msg_type = %q, want %q (body=%v)", mt, "text", c.Body)
		}
		content, _ := c.Body["content"].(map[string]any)
		if content == nil {
			t.Fatalf("Feishu body missing 'content' object (body=%v)", c.Body)
		}
		if txt, _ := content["text"].(string); txt == "" {
			t.Errorf("Feishu content.text empty — formatText output dropped (content=%v)", content)
		}
		ts, _ := c.Body["timestamp"].(string)
		if ts == "" {
			t.Fatalf("Feishu body missing top-level 'timestamp' string field (body=%v)", c.Body)
		}
		sig, _ := c.Body["sign"].(string)
		if sig == "" {
			t.Fatalf("Feishu body missing top-level 'sign' string field (body=%v)", c.Body)
		}

		// Recompute the signature: Feishu's scheme is unambiguous —
		// HMAC-SHA256 over the empty string, KEY = "<timestamp>\n<secret>",
		// base64-std. Since timestamp travels in the body alongside the
		// signature, we have both halves and there's no clock-drift
		// concern; we can assert exact equality.
		want := signFeishu(ts, secret)
		if sig != want {
			t.Errorf("Feishu sign mismatch:\n  got  = %q\n  want = %q\n  (timestamp=%q secret=%q)", sig, want, ts, secret)
		}

		// URL must NOT carry signing query params — Feishu signs in the
		// body, not the URL. Catches accidentally wiring signDingTalkURL
		// onto the Feishu sender.
		if strings.Contains(c.RawQuery, "sign=") || strings.Contains(c.RawQuery, "timestamp=") {
			t.Errorf("Feishu request URL leaked signing into query: RawQuery=%q (Feishu signs in body)", c.RawQuery)
		}
	})

	t.Run("DingTalk_sign_in_url", func(t *testing.T) {
		const secret = "e2e-secret-dingtalk"

		baselineCount := len(env.FakeSlack().Captures())

		channelID := createChannel(t, env, pair.AccessToken, channelSpec{
			name:     "e2e-dingtalk",
			channel:  "dingtalk",
			endpoint: env.FakeSlack().WebhookURL(),
			secret:   secret,
		})
		fireTestSend(t, env, pair.AccessToken, channelID)

		caps := env.FakeSlack().Captures()
		if len(caps) != baselineCount+1 {
			t.Fatalf("expected exactly 1 new DingTalk POST, got %d (baseline=%d)", len(caps)-baselineCount, baselineCount)
		}
		c := caps[baselineCount]

		// Body: DingTalk text payload — {"msgtype":"text","text":{"content":"..."}}.
		// timestamp / sign are NOT in the body (they ride the URL).
		if mt, _ := c.Body["msgtype"].(string); mt != "text" {
			t.Errorf("DingTalk body msgtype = %q, want %q (body=%v)", mt, "text", c.Body)
		}
		text, _ := c.Body["text"].(map[string]any)
		if text == nil {
			t.Fatalf("DingTalk body missing 'text' object (body=%v)", c.Body)
		}
		if content, _ := text["content"].(string); content == "" {
			t.Errorf("DingTalk text.content empty — formatText output dropped (text=%v)", text)
		}
		if _, hasTs := c.Body["timestamp"]; hasTs {
			t.Errorf("DingTalk body unexpectedly carries 'timestamp' (it must travel in the URL, body=%v)", c.Body)
		}
		if _, hasSign := c.Body["sign"]; hasSign {
			t.Errorf("DingTalk body unexpectedly carries 'sign' (it must travel in the URL, body=%v)", c.Body)
		}

		// URL must carry timestamp + sign as query params. Parse the
		// raw query (net/url handles the URL-decode of the base64 sign).
		if c.RawQuery == "" {
			t.Fatalf("DingTalk URL missing query string — expected ?timestamp=…&sign=… (path=%q)", c.Path)
		}
		vals, err := url.ParseQuery(c.RawQuery)
		if err != nil {
			t.Fatalf("DingTalk RawQuery = %q, ParseQuery err = %v", c.RawQuery, err)
		}
		ts := vals.Get("timestamp")
		sig := vals.Get("sign")
		if ts == "" {
			t.Fatalf("DingTalk URL missing 'timestamp' query param (raw=%q)", c.RawQuery)
		}
		if sig == "" {
			t.Fatalf("DingTalk URL missing 'sign' query param (raw=%q)", c.RawQuery)
		}

		// DingTalk uses HMAC-SHA256(secret, "<timestamp>\n<secret>") →
		// base64-std (32-byte digest → 44-char base64 with one '='). We
		// can't recompute the *exact* sign without recapturing the
		// manager's clock at request time (timestamp = UnixMilli at send,
		// not a value we control), but we CAN:
		//   1. assert sig base64-decodes to a 32-byte SHA-256 digest;
		//   2. assert that some plausible timestamp (the one in the URL,
		//      which the server captured verbatim) produces this exact
		//      sign with our known secret — that's a complete check.
		raw, err := base64.StdEncoding.DecodeString(sig)
		if err != nil {
			t.Fatalf("DingTalk sign is not valid base64-std (sign=%q): %v", sig, err)
		}
		if len(raw) != sha256.Size {
			t.Errorf("DingTalk sign decoded length = %d, want %d (sign=%q)", len(raw), sha256.Size, sig)
		}
		want := signDingTalk(ts, secret)
		if sig != want {
			t.Errorf("DingTalk sign mismatch:\n  got  = %q\n  want = %q\n  (timestamp=%q secret=%q)", sig, want, ts, secret)
		}
	})
}

// signFeishu mirrors internal/pkg/notify/webhook.go signFeishu — Feishu
// custom-bot scheme: HMAC-SHA256 with KEY = "<timestamp>\n<secret>" over
// empty message, base64-std encoded.
func signFeishu(timestamp, secret string) string {
	stringToSign := timestamp + "\n" + secret
	mac := hmac.New(sha256.New, []byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// signDingTalk mirrors internal/pkg/notify/webhook.go signDingTalkURL —
// DingTalk custom-bot scheme: HMAC-SHA256 with KEY = secret over the
// payload "<timestamp>\n<secret>", base64-std encoded. The URL appends
// timestamp= and sign= (URL-encoded by net/url at request time).
func signDingTalk(timestamp, secret string) string {
	stringToSign := timestamp + "\n" + secret
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// channelSpec is the minimal channel-create payload used by both
// subtests. We use a separate type rather than a positional helper
// because the subtests' intent reads better as a named struct.
type channelSpec struct {
	name     string
	channel  string // "feishu" | "dingtalk"
	endpoint string
	secret   string
}

// createChannel issues POST /api/v1/notification-channels and returns
// the new channel id as a path-component string. Fails the test on any
// non-2xx.
func createChannel(t *testing.T, env *testenv.Env, bearer string, s channelSpec) string {
	t.Helper()
	status, body, err := env.DoJSON("POST", "/api/v1/notification-channels", map[string]any{
		"name":     s.name,
		"type":     s.channel,
		"endpoint": s.endpoint,
		"secret":   s.secret,
		"enabled":  true,
	}, bearer)
	if err != nil {
		t.Fatalf("create %s channel: %v", s.channel, err)
	}
	if status != 200 && status != 201 {
		t.Fatalf("create %s channel: status=%d body=%v", s.channel, status, body)
	}
	idAny, ok := body["id"]
	if !ok {
		t.Fatalf("create %s channel: response missing id (body=%v)", s.channel, body)
	}
	id := numberToString(idAny)
	if id == "" {
		t.Fatalf("create %s channel: id was not a number-like (got %T %v)", s.channel, idAny, idAny)
	}
	return id
}

// fireTestSend triggers the manager's "send a probe to this channel"
// flow, which goes through BuildSenderFromChannel and ultimately
// NewFeishuSender / NewDingTalkSender — the production code path
// exercised by the SPA's "测试" button on the channel-edit page.
func fireTestSend(t *testing.T, env *testenv.Env, bearer, channelID string) {
	t.Helper()
	status, body, err := env.DoJSON("POST", "/api/v1/notification-channels/"+channelID+"/test", nil, bearer)
	if err != nil {
		t.Fatalf("test channel %s: %v", channelID, err)
	}
	if status != 200 {
		t.Fatalf("test channel %s: status=%d body=%v", channelID, status, body)
	}
}
