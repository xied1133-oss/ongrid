// Package zhipuauth implements the JWT-signed Bearer auth scheme that
// open.bigmodel.cn requires.
//
// Zhipu's API keys take the form `<id>.<secret>`. Some endpoints accept
// the raw key as a Bearer token; the v4 `/api/paas/v4/*` family does
// not — those reject raw keys with HTTP 401 "令牌已过期或验证不正确"
// and require a JWT signed with the secret half of the key.
//
// The JWT shape Zhipu expects:
//
//	header  = {"alg":"HS256","sign_type":"SIGN"}
//	payload = {"api_key":"<id>","exp":<now_ms+ttl>,"timestamp":<now_ms>}
//	key     = <secret>  // HMAC-SHA256 key
//
// timestamps are milliseconds-since-epoch. Tokens are short-lived
// (caller decides TTL; 1h is the recommended default).
//
// Usage:
//
//	token, err := zhipuauth.SignJWT("<id>.<secret>", time.Hour)
//	// then: req.Header.Set("Authorization", "Bearer "+token)
package zhipuauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// LooksLikeZhipuKey reports whether key has the `<id>.<secret>` shape
// Zhipu uses. The id half is the API key id (a hex-ish blob) and the
// secret half is the HMAC key. Callers use this as a cheap probe to
// decide between raw-Bearer and JWT-signed auth.
func LooksLikeZhipuKey(key string) bool {
	idx := strings.IndexByte(key, '.')
	return idx > 0 && idx < len(key)-1
}

// LooksLikeZhipuURL reports whether base looks like a Zhipu endpoint.
// Catches both the canonical open.bigmodel.cn host and any forwarded
// proxy whose host name still mentions "bigmodel".
func LooksLikeZhipuURL(base string) bool {
	return strings.Contains(strings.ToLower(base), "bigmodel")
}

// SignJWT produces a JWT suitable for the Authorization: Bearer header
// against open.bigmodel.cn. key must be the `<id>.<secret>` form; ttl
// is the validity window (Zhipu enforces <= 30 days; 1h is plenty for
// a typical batch of API calls).
func SignJWT(key string, ttl time.Duration) (string, error) {
	idx := strings.IndexByte(key, '.')
	if idx <= 0 || idx >= len(key)-1 {
		return "", errors.New("zhipuauth: key must be <id>.<secret>")
	}
	id := key[:idx]
	secret := key[idx+1:]

	nowMs := time.Now().UnixMilli()
	expMs := time.Now().Add(ttl).UnixMilli()

	header, err := json.Marshal(map[string]any{"alg": "HS256", "sign_type": "SIGN"})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]any{
		"api_key":   id,
		"exp":       expMs,
		"timestamp": nowMs,
	})
	if err != nil {
		return "", err
	}
	headerB64 := base64URLNoPad(header)
	payloadB64 := base64URLNoPad(payload)
	signingInput := headerB64 + "." + payloadB64

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signingInput))
	sigB64 := base64URLNoPad(mac.Sum(nil))

	return signingInput + "." + sigB64, nil
}

func base64URLNoPad(b []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(b), "=")
}
