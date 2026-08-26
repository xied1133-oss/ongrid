// Package secretbox is the at-rest encryption used by the credential vault
// (HLD-017). AES-256-GCM, key derived from ONGRID_SECRET_KEY. Mirrors
// n8n's posture (encryption key lives OUTSIDE the DB, in the environment)
// so a DB dump alone never yields plaintext credentials.
//
// Ciphertext wire format: "v1:" + base64(nonce || ciphertext+tag). The
// version prefix lets us rotate the scheme later without ambiguity.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

const prefix = "v1:"

var (
	keyOnce sync.Once
	keyVal  [32]byte
	keyWeak bool // true when ONGRID_SECRET_KEY was unset (derived fallback)
)

// loadKey derives the 32-byte AES key from ONGRID_SECRET_KEY (sha256 of the
// env value). When the env is unset it falls back to a fixed-salt derived
// key so the vault still works out of the box — but KeyIsWeak() reports it
// so the boot path can warn the operator to set a real key.
func loadKey() {
	keyOnce.Do(func() {
		env := strings.TrimSpace(os.Getenv("ONGRID_SECRET_KEY"))
		if env == "" {
			keyWeak = true
			env = "ongrid-insecure-default-secret-key-set-ONGRID_SECRET_KEY"
		}
		keyVal = sha256.Sum256([]byte(env))
	})
}

// KeyIsWeak reports whether the encryption key is the insecure built-in
// fallback (ONGRID_SECRET_KEY unset). main.go logs a warning when true.
func KeyIsWeak() bool {
	loadKey()
	return keyWeak
}

// Encrypt seals plaintext with AES-256-GCM and returns the versioned,
// base64 wire string. Empty input returns empty (so an absent field stays
// absent rather than encrypting to noise).
func Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	loadKey()
	block, err := aes.NewCipher(keyVal[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return prefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt. Empty input returns empty. A value without the
// version prefix is treated as legacy plaintext and returned as-is (lets a
// pre-encryption row still read while we migrate forward).
func Decrypt(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	if !strings.HasPrefix(enc, prefix) {
		return enc, nil // legacy plaintext — read through
	}
	loadKey()
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(enc, prefix))
	if err != nil {
		return "", fmt.Errorf("secretbox: base64: %w", err)
	}
	block, err := aes.NewCipher(keyVal[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("secretbox: ciphertext too short")
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	out, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("secretbox: open (wrong ONGRID_SECRET_KEY?): %w", err)
	}
	return string(out), nil
}
