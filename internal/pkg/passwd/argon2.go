package passwd

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters. Tuned for "reasonable on a laptop" — one hash ~= 60ms
// on a 2023 M-series Mac; adjust via env later if needed. Changing the
// parameters does not invalidate existing hashes: the encoded form carries
// the params that produced it.
const (
	argonTime    uint32 = 1
	argonMemory  uint32 = 64 * 1024 // 64 MiB
	argonThreads uint8  = 4
	argonSaltLen uint32 = 16
	argonKeyLen  uint32 = 32
)

// Hash returns the PHC-encoded argon2id hash of plain.
// Format: $argon2id$v=19$m=65536,t=1,p=4$<salt-b64>$<hash-b64>
func Hash(plain string) (string, error) {
	if plain == "" {
		return "", errors.New("passwd.Hash: empty plaintext")
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("passwd.Hash: read random salt: %w", err)
	}
	sum := argon2.IDKey([]byte(plain), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	encoded := fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum),
	)
	return encoded, nil
}

// Verify reports whether plain matches the PHC-encoded hash.
// Returns false for any decoding or mismatch error (Verify must never leak
// which part failed via a timing side-channel).
func Verify(plain, encoded string) bool {
	parts := strings.Split(encoded, "$")
	// Expected layout: ["", "argon2id", "v=19", "m=...,t=...,p=...", "<salt>", "<hash>"]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false
	}
	if version != argon2.Version {
		return false
	}
	var mem, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(plain), salt, t, mem, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}
