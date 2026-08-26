package marketplace

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// signedFixture is a fully wired pack on disk: minimal *.md / *.json
// content + a freshly generated ECDSA P-256 keypair + a signature.json
// authoring the canonical hash.
type signedFixture struct {
	dir        string
	priv       *ecdsa.PrivateKey
	pubPEM     []byte
	manifest   signatureManifest
	rawSigJSON []byte
}

func newSignedPack(t *testing.T) signedFixture {
	t.Helper()
	dir := t.TempDir()

	// A handful of real-shaped files so computePackHash has something
	// to chew on. Picking both *.md and *.json proves the extension
	// filter while avoiding accidental empties.
	mustWrite(t, filepath.Join(dir, ".claude-plugin", "plugin.json"),
		`{"id":"sig-test","name":"sig-test","version":"0.1.0"}`)
	mustWrite(t, filepath.Join(dir, "README.md"), "# Hello\n")
	mustWrite(t, filepath.Join(dir, "skills", "probe", "SKILL.md"),
		"---\nname: probe\ndescription: probe\n---\n\nbody\n")
	mustWrite(t, filepath.Join(dir, "skills", "probe", "config.json"),
		`{"k":"v"}`)
	// A non-md/json file that must NOT participate in the hash.
	mustWrite(t, filepath.Join(dir, "notes.txt"), "ignored")

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal pub: %v", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})

	hash, err := computePackHash(dir)
	if err != nil {
		t.Fatalf("computePackHash: %v", err)
	}
	sig, err := ecdsa.SignASN1(rand.Reader, priv, hash[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	manifest := signatureManifest{
		Sig:    base64.StdEncoding.EncodeToString(sig),
		PubKey: base64.StdEncoding.EncodeToString(pubPEM),
	}
	raw, _ := json.Marshal(manifest)
	mustWriteRaw(t, filepath.Join(dir, signatureManifestName), raw)

	return signedFixture{
		dir:        dir,
		priv:       priv,
		pubPEM:     pubPEM,
		manifest:   manifest,
		rawSigJSON: raw,
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	mustWriteRaw(t, path, []byte(body))
}

func mustWriteRaw(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// 1. Happy path: round-tripping a freshly generated keypair → "verified".
func TestVerifySignature_Happy(t *testing.T) {
	f := newSignedPack(t)
	state, err := VerifySignature(f.dir, "")
	if err != nil {
		t.Fatalf("VerifySignature: err = %v want nil", err)
	}
	if state != SigStateVerified {
		t.Fatalf("state = %q want %q", state, SigStateVerified)
	}

	// Pinned key matches the pack's pub_key → still verified.
	state, err = VerifySignature(f.dir, string(f.pubPEM))
	if err != nil {
		t.Fatalf("VerifySignature pinned: err = %v want nil", err)
	}
	if state != SigStateVerified {
		t.Fatalf("pinned state = %q want %q", state, SigStateVerified)
	}
}

// 2. Missing signature.json → "unsigned" + nil err.
func TestVerifySignature_Missing(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "README.md"), "# hi\n")

	state, err := VerifySignature(dir, "")
	if err != nil {
		t.Fatalf("VerifySignature: err = %v want nil", err)
	}
	if state != SigStateUnsigned {
		t.Fatalf("state = %q want %q", state, SigStateUnsigned)
	}
}

// 3. Tampered content → "failed".
func TestVerifySignature_Tampered(t *testing.T) {
	f := newSignedPack(t)
	// Flip a byte in a signed file.
	readme := filepath.Join(f.dir, "README.md")
	if err := os.WriteFile(readme, []byte("# tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := VerifySignature(f.dir, "")
	if err == nil {
		t.Fatalf("VerifySignature: err = nil want non-nil")
	}
	if state != SigStateFailed {
		t.Fatalf("state = %q want %q", state, SigStateFailed)
	}
}

// 4. Malformed signature.json (not JSON) → "failed".
func TestVerifySignature_MalformedManifest(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "README.md"), "# hi\n")
	mustWriteRaw(t, filepath.Join(dir, signatureManifestName), []byte("{not json"))

	state, err := VerifySignature(dir, "")
	if err == nil {
		t.Fatalf("err = nil want parse failure")
	}
	if state != SigStateFailed {
		t.Fatalf("state = %q want %q", state, SigStateFailed)
	}
}

// 5. Malformed pub_key (well-formed JSON, base64-decodable but the
// decoded bytes aren't PEM) → "failed".
func TestVerifySignature_MalformedPubKey(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "README.md"), "# hi\n")

	// Sig field needs to be base64 to get past the early decode; we
	// don't care that it can't actually verify because the pubkey
	// parse failure short-circuits first.
	manifest := signatureManifest{
		Sig:    base64.StdEncoding.EncodeToString([]byte("garbage")),
		PubKey: base64.StdEncoding.EncodeToString([]byte("not a pem block")),
	}
	raw, _ := json.Marshal(manifest)
	mustWriteRaw(t, filepath.Join(dir, signatureManifestName), raw)

	state, err := VerifySignature(dir, "")
	if err == nil {
		t.Fatalf("err = nil want pub_key parse failure")
	}
	if !strings.Contains(err.Error(), "pub_key") && !strings.Contains(err.Error(), "PEM") {
		t.Fatalf("err = %v want pub_key/PEM-related", err)
	}
	if state != SigStateFailed {
		t.Fatalf("state = %q want %q", state, SigStateFailed)
	}
}

// 6. computePackHash excludes signature.json itself: writing /
// rewriting signature.json must not change the hash.
func TestComputePackHash_ExcludesSignatureManifest(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.md"), "alpha")
	mustWrite(t, filepath.Join(dir, "b.json"), `{"x":1}`)

	h1, err := computePackHash(dir)
	if err != nil {
		t.Fatalf("hash 1: %v", err)
	}

	// Drop a signature.json with arbitrary contents.
	mustWriteRaw(t, filepath.Join(dir, signatureManifestName),
		[]byte(`{"sig":"AAAA","pub_key":"BBBB"}`))
	h2, err := computePackHash(dir)
	if err != nil {
		t.Fatalf("hash 2: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("hash changed when signature.json was added: %x vs %x", h1, h2)
	}

	// Rewrite signature.json with different bytes — hash still stable.
	mustWriteRaw(t, filepath.Join(dir, signatureManifestName),
		[]byte(`{"sig":"CCCC","pub_key":"DDDD"}`))
	h3, err := computePackHash(dir)
	if err != nil {
		t.Fatalf("hash 3: %v", err)
	}
	if h1 != h3 {
		t.Fatalf("hash changed when signature.json was rewritten: %x vs %x", h1, h3)
	}

	// And a non-md/json file mustn't participate either — sanity
	// check on the extension filter.
	mustWrite(t, filepath.Join(dir, "ignored.txt"), "anything")
	h4, err := computePackHash(dir)
	if err != nil {
		t.Fatalf("hash 4: %v", err)
	}
	if h1 != h4 {
		t.Fatalf("hash changed for a .txt file: %x vs %x", h1, h4)
	}
}

// Pin mismatch is a separate failure path worth exercising — the
// signature.json itself is valid, just the operator-pinned key
// disagrees.
func TestVerifySignature_PinnedKeyMismatch(t *testing.T) {
	f := newSignedPack(t)

	// Generate an unrelated key to pin.
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherDER, _ := x509.MarshalPKIXPublicKey(&other.PublicKey)
	otherPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: otherDER})

	state, err := VerifySignature(f.dir, string(otherPEM))
	if err == nil {
		t.Fatalf("err = nil want pin mismatch")
	}
	if state != SigStateFailed {
		t.Fatalf("state = %q want %q", state, SigStateFailed)
	}
	if !strings.Contains(err.Error(), "pinned") && !strings.Contains(err.Error(), "match") {
		t.Fatalf("err = %v want pin-mismatch wording", err)
	}
}
