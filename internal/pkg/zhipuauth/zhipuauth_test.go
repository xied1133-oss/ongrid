package zhipuauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLooksLikeZhipuKey(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"218d4158bba24a96a554f13d3b4c4bb4.U1BNvr3TeK7o3yln", true},
		{"sk-abc123", false},
		{"", false},
		{".onlysecret", false},
		{"onlyid.", false},
	}
	for _, c := range cases {
		if got := LooksLikeZhipuKey(c.in); got != c.want {
			t.Errorf("LooksLikeZhipuKey(%q)=%v want %v", c.in, got, c.want)
		}
	}
}

func TestLooksLikeZhipuURL(t *testing.T) {
	if !LooksLikeZhipuURL("https://open.bigmodel.cn/api/paas/v4") {
		t.Error("expected canonical zhipu URL to match")
	}
	if LooksLikeZhipuURL("https://api.openai.com/v1") {
		t.Error("openai URL must not match")
	}
}

func TestSignJWT(t *testing.T) {
	key := "abc123.shhsecret"
	token, err := SignJWT(key, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token must have 3 parts, got %d: %q", len(parts), token)
	}

	// Decode + verify the payload includes the id half and looks fresh.
	dec, err := base64.URLEncoding.DecodeString(parts[1] + strings.Repeat("=", (4-len(parts[1])%4)%4))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(dec, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["api_key"] != "abc123" {
		t.Errorf("api_key claim: %v want abc123", payload["api_key"])
	}

	// Verify the HMAC signature over header.payload with the secret half.
	mac := hmac.New(sha256.New, []byte("shhsecret"))
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	wantSig := strings.TrimRight(base64.URLEncoding.EncodeToString(mac.Sum(nil)), "=")
	if parts[2] != wantSig {
		t.Errorf("signature mismatch: got %q want %q", parts[2], wantSig)
	}
}

func TestSignJWT_BadKey(t *testing.T) {
	if _, err := SignJWT("nodot", time.Hour); err == nil {
		t.Error("expected error for key without dot")
	}
}
