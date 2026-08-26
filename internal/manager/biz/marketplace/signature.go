package marketplace

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SignatureState mirrors the model package constants and is the
// canonical wire / DB shape for a pack's signature verdict.
type SignatureState = string

// SigStateVerified / SigStateUnsigned / SigStateFailed mirror the
// model package constants so callers in the biz layer don't need to
// reach across into internal/manager/model/marketplace just to compare
// states.
const (
	SigStateVerified SignatureState = "verified"
	SigStateUnsigned SignatureState = "unsigned"
	SigStateFailed   SignatureState = "failed"
)

// signatureManifestName is the wire shape file dropped at the pack
// root. Absence ⇒ unsigned (not failed).
const signatureManifestName = "signature.json"

// signatureManifest is the JSON wire shape persisted alongside the
// pack. Both fields are base64-encoded:
//   - Sig: raw ASN.1 ECDSA signature bytes (the form crypto/ecdsa
//     produces via ecdsa.SignASN1) — base64 std encoding
//   - PubKey: PEM-encoded ECDSA public key (PUBLIC KEY block, PKIX) —
//     base64 std encoding around the PEM block
//
// We base64 the PEM too so the JSON stays one-line / clipboard-safe
// without escaping every newline. Tooling that produces signature.json
// is documented in the marketplace docs.
type signatureManifest struct {
	Sig    string `json:"sig"`
	PubKey string `json:"pub_key"`
}

// VerifySignature is the entry point. It walks
// packPath looking for signature.json, recomputes the canonical hash
// (see computePackHash) and runs ECDSA P-256 verify against the
// embedded pubkey.
//
// expectedKey, when non-empty, is the registry's pinned PEM public key
// — verification will additionally require that the manifest's
// pub_key matches this pin (DER-equal). Empty expectedKey skips the
// pin check (any well-formed key passes).
//
// Return contract (matches model.SigState* constants):
//   - ("verified", nil) — signature.json present, hash &
//     sig valid (and pin matches if set)
//   - ("unsigned", nil) — no signature.json at the pack root
//   - ("failed", non-nil error) — signature.json present but invalid
//     in any of: malformed JSON / bad
//     base64 / non-PEM pubkey / non-ECDSA
//     key / signature verify mismatch
//     / pinned-key mismatch
func VerifySignature(packPath string, expectedKey string) (SignatureState, error) {
	cleanPath := filepath.Clean(packPath)

	manifestPath := filepath.Join(cleanPath, signatureManifestName)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		if errors.Is(err, iofs.ErrNotExist) {
			return SigStateUnsigned, nil
		}
		// Anything other than "missing" — e.g. permission denied —
		// is a real failure. We don't treat it as "unsigned" because
		// that would let a misconfigured deployment silently downgrade
		// trust.
		return SigStateFailed, fmt.Errorf("read signature.json: %w", err)
	}

	var manifest signatureManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return SigStateFailed, fmt.Errorf("parse signature.json: %w", err)
	}
	if manifest.Sig == "" || manifest.PubKey == "" {
		return SigStateFailed, errors.New("signature.json missing sig or pub_key")
	}

	sigBytes, err := base64.StdEncoding.DecodeString(manifest.Sig)
	if err != nil {
		return SigStateFailed, fmt.Errorf("decode sig: %w", err)
	}

	pubPEM, err := base64.StdEncoding.DecodeString(manifest.PubKey)
	if err != nil {
		return SigStateFailed, fmt.Errorf("decode pub_key: %w", err)
	}

	pubKey, pubDER, err := parseECDSAPublicKey(pubPEM)
	if err != nil {
		return SigStateFailed, err
	}

	if expectedKey != "" {
		_, expectedDER, err := parseECDSAPublicKey([]byte(expectedKey))
		if err != nil {
			return SigStateFailed, fmt.Errorf("expected key: %w", err)
		}
		if !bytesEqual(pubDER, expectedDER) {
			return SigStateFailed, errors.New("pub_key does not match pinned registry key")
		}
	}

	hash, err := computePackHash(cleanPath)
	if err != nil {
		return SigStateFailed, fmt.Errorf("compute pack hash: %w", err)
	}

	if !ecdsa.VerifyASN1(pubKey, hash[:], sigBytes) {
		return SigStateFailed, errors.New("ecdsa signature does not verify")
	}
	return SigStateVerified, nil
}

// parseECDSAPublicKey decodes a PEM "PUBLIC KEY" (PKIX) block and
// returns the parsed *ecdsa.PublicKey plus the raw DER bytes (used
// for pinned-key equality comparisons). Non-PEM, non-ECDSA, or
// malformed inputs error out.
func parseECDSAPublicKey(pemBytes []byte) (*ecdsa.PublicKey, []byte, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, nil, errors.New("pub_key not PEM-encoded")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse PKIX pub_key: %w", err)
	}
	ec, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, nil, fmt.Errorf("pub_key is not ECDSA (got %T)", pub)
	}
	return ec, block.Bytes, nil
}

// computePackHash is the canonical pack hash: SHA-256 over a
// deterministic concatenation of every signable file's bytes.
//
// Signable = every regular *.md / *.json under packPath, EXCLUDING
// signature.json itself (so the manifest doesn't sign over its own
// signature).
//
// Determinism: files are walked then sorted by their forward-slash
// relative path (ascending lexicographic) before concat. We use
// forward slashes regardless of OS so a pack signed on Linux verifies
// identically on macOS / Windows.
func computePackHash(packPath string) ([32]byte, error) {
	type entry struct {
		rel  string
		full string
	}
	var entries []entry
	root := filepath.Clean(packPath)

	walkErr := filepath.WalkDir(root, func(p string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Skip non-regular entries (symlinks, sockets, …). os.ReadFile
		// on a symlink would follow it, but we pretend they don't
		// exist for hashing purposes — packs are expected to be plain
		// trees.
		info, statErr := d.Info()
		if statErr != nil {
			return statErr
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		base := filepath.Base(p)
		if base == signatureManifestName {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(base))
		if ext != ".md" && ext != ".json" {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		entries = append(entries, entry{
			rel:  filepath.ToSlash(rel),
			full: p,
		})
		return nil
	})
	if walkErr != nil {
		return [32]byte{}, walkErr
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })

	h := sha256.New()
	for _, e := range entries {
		data, err := os.ReadFile(e.full)
		if err != nil {
			return [32]byte{}, fmt.Errorf("read %s: %w", e.rel, err)
		}
		// Domain separator: include the relative path + a NUL so a
		// rename can't pass the same bytes through the hash. Cheap
		// canonicalisation insurance — without it, swapping two
		// files' contents between equally-named slots would still
		// hash-match.
		_, _ = h.Write([]byte(e.rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
	}

	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

// bytesEqual is a constant-time equality check for the pinned-key DER
// comparison. Uses crypto/subtle to prevent timing side-channels.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare(a, b) == 1
}
