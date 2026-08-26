// Package feishu implements the Feishu (Lark) side of the IM bridge:
// inbound webhook verification + decryption, outbound message
// send/edit, tenant_access_token caching.
package feishu

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
)

// VerifyEventSignature checks the X-Lark-Signature header against the
// expected HMAC over (timestamp + nonce + encryptKey + body). Returns
// ErrBadSignature when the signature does not match; never panics on
// bad input.
//
// Feishu's algorithm (v2 events):
//
//	sig = sha256(timestamp + nonce + encryptKey + body), hex-encoded
//
// Yes — SHA-256, not HMAC. The encryptKey takes the role of a shared
// secret inside the hash input.
func VerifyEventSignature(timestamp, nonce, encryptKey string, body []byte, signatureHex string) error {
	h := sha256.New()
	h.Write([]byte(timestamp))
	h.Write([]byte(nonce))
	h.Write([]byte(encryptKey))
	h.Write(body)
	got := hex.EncodeToString(h.Sum(nil))
	if strings.EqualFold(got, signatureHex) {
		return nil
	}
	return ErrBadSignature
}

// DecryptEvent decrypts the AES-256-CBC payload that Feishu wraps
// around the event JSON when encrypt_key is configured. The key is
// SHA-256(encrypt_key); IV is the first 16 bytes of the base64-decoded
// ciphertext; the rest is PKCS#7-padded plaintext.
func DecryptEvent(encryptKey string, encryptedBase64 string) ([]byte, error) {
	if encryptKey == "" {
		return nil, errors.New("feishu: encrypt_key is empty")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encryptedBase64))
	if err != nil {
		return nil, errors.New("feishu: encrypted base64 decode: " + err.Error())
	}
	if len(raw) < aes.BlockSize*2 {
		return nil, errors.New("feishu: ciphertext too short")
	}
	keyHash := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(keyHash[:])
	if err != nil {
		return nil, err
	}
	iv := raw[:aes.BlockSize]
	ct := raw[aes.BlockSize:]
	if len(ct)%aes.BlockSize != 0 {
		return nil, errors.New("feishu: ciphertext is not block-aligned")
	}
	pt := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(pt, ct)
	return pkcs7Unpad(pt)
}

// SortedKeys is a tiny helper used by tests / signature builders that
// need a deterministic key order.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var _ = sortedKeys // exported only when tests want it

// ErrBadSignature is returned when VerifyEventSignature fails. Distinct
// type so HTTP handlers can map it cleanly to 401.
var ErrBadSignature = errors.New("feishu: signature mismatch")

func pkcs7Unpad(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("feishu: empty plaintext")
	}
	pad := int(b[len(b)-1])
	if pad <= 0 || pad > aes.BlockSize || pad > len(b) {
		return nil, errors.New("feishu: bad pkcs7 padding")
	}
	for i := len(b) - pad; i < len(b); i++ {
		if int(b[i]) != pad {
			return nil, errors.New("feishu: bad pkcs7 byte")
		}
	}
	return b[:len(b)-pad], nil
}
