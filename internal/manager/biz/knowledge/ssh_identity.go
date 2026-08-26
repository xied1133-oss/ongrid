// ssh_identity.go — phase 1.
//
// SSHIdentity is the dual-rail authentication's "key-based" half. Each
// row stores one private key + the hosts it's allowed to authenticate
// against. The Sync() path in usecase.go consults pickSSHIdentity at
// clone time to materialise a temp keyfile and feed it to git via
// GIT_SSH_COMMAND — the standard mechanism for "use this specific key
// for this specific git invocation."
//
// What this file owns:
//   - Identity DTOs (ListSSHIdentities / CreateSSHIdentity inputs)
//   - Public-key fingerprint derivation
//   - host pattern matching (glob via filepath.Match)
//   - GIT_SSH_COMMAND construction
//
// What it deliberately does NOT do:
//   - Generate new keys (the SPA gets the operator to paste an existing
//     PEM; manager-side keygen is P2 in)
//   - Encrypt private_key at rest. P1 stores cleartext in the same
//     MySQL the rest of the data lives in; turning on AES encryption
//     touches the secrets-management story for the whole platform and
//     is tracked separately (— not yet started).
//     Practical impact: anyone with DB read can lift these keys, same
//     blast radius as the GitHub PAT in system_settings today.
package knowledge

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	model "github.com/ongridio/ongrid/internal/manager/model/knowledge"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// CreateSSHIdentityInput is the create-form payload. PrivateKey is PEM-
// encoded (BEGIN OPENSSH PRIVATE KEY / BEGIN RSA PRIVATE KEY etc.);
// the biz layer parses it to derive the public key + fingerprint, so
// the caller doesn't need to supply those. Hosts is the list of host
// glob patterns this key is allowed to auth against.
type CreateSSHIdentityInput struct {
	Name       string
	PrivateKey string
	Hosts      []string
	KnownHosts string // optional; can grow over time via accept-new on first connect
}

// UpdateSSHIdentityInput edits the identity-mutable fields (name, host
// patterns, accumulated known_hosts). The private key is immutable —
// rotating means delete + create.
type UpdateSSHIdentityInput struct {
	Name       string
	Hosts      []string
	KnownHosts string
}

// ListSSHIdentities returns every identity, sorted by name. Private
// keys are scrubbed from the returned rows (only the public-facing
// metadata leaves the biz layer).
func (u *Usecase) ListSSHIdentities(ctx context.Context) ([]*model.SSHIdentity, error) {
	rows, err := u.repo.ListSSHIdentities(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		r.PrivateKey = ""
		r.Passphrase = ""
	}
	return rows, nil
}

// CreateSSHIdentity validates + persists. Returns the row with the
// private key scrubbed.
func (u *Usecase) CreateSSHIdentity(ctx context.Context, in CreateSSHIdentityInput) (*model.SSHIdentity, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: name required", errs.ErrInvalid)
	}
	if len(name) > 128 {
		return nil, fmt.Errorf("%w: name too long (max 128)", errs.ErrInvalid)
	}
	pk := strings.TrimSpace(in.PrivateKey)
	if pk == "" {
		return nil, fmt.Errorf("%w: private_key required", errs.ErrInvalid)
	}

	// Parse the private key:
	//   - golang.org/x/crypto/ssh.ParseRawPrivateKey panics on encrypted
	//     keys with a typed error we can detect and reject (P1 only
	//     supports passphrase-less keys).
	//   - We need the corresponding public key to derive the fingerprint
	//     + serve the UI.
	signer, err := ssh.ParsePrivateKey([]byte(pk))
	if err != nil {
		if _, ok := err.(*ssh.PassphraseMissingError); ok {
			return nil, fmt.Errorf("%w: encrypted keys not supported; generate a passphrase-less deploy key (ed25519 recommended)", errs.ErrInvalid)
		}
		return nil, fmt.Errorf("%w: parse private_key: %v", errs.ErrInvalid, err)
	}
	pub := signer.PublicKey()
	pubBytes := ssh.MarshalAuthorizedKey(pub)
	fingerprint := sshFingerprintSHA256(pub)

	hosts := normalizeHosts(in.Hosts)
	if len(hosts) == 0 {
		return nil, fmt.Errorf("%w: hosts required (at least one host pattern)", errs.ErrInvalid)
	}
	hostsJSON, err := json.Marshal(hosts)
	if err != nil {
		return nil, fmt.Errorf("encode hosts: %w", err)
	}

	now := time.Now().UTC()
	row := &model.SSHIdentity{
		Name:        name,
		PrivateKey:  pk,
		PublicKey:   strings.TrimSpace(string(pubBytes)),
		Fingerprint: fingerprint,
		HostsJSON:   string(hostsJSON),
		KnownHosts:  strings.TrimSpace(in.KnownHosts),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := u.repo.CreateSSHIdentity(ctx, row); err != nil {
		return nil, err
	}
	row.PrivateKey = ""
	row.Passphrase = ""
	return row, nil
}

// UpdateSSHIdentity edits name / hosts / known_hosts. Private key is
// not editable — to swap keys, delete and recreate.
func (u *Usecase) UpdateSSHIdentity(ctx context.Context, id uint64, in UpdateSSHIdentityInput) (*model.SSHIdentity, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: name required", errs.ErrInvalid)
	}
	hosts := normalizeHosts(in.Hosts)
	if len(hosts) == 0 {
		return nil, fmt.Errorf("%w: hosts required (at least one host pattern)", errs.ErrInvalid)
	}
	hostsJSON, err := json.Marshal(hosts)
	if err != nil {
		return nil, fmt.Errorf("encode hosts: %w", err)
	}
	if err := u.repo.UpdateSSHIdentity(ctx, id, name, string(hostsJSON), strings.TrimSpace(in.KnownHosts)); err != nil {
		return nil, err
	}
	row, err := u.repo.GetSSHIdentity(ctx, id)
	if err != nil {
		return nil, err
	}
	row.PrivateKey = ""
	row.Passphrase = ""
	return row, nil
}

// DeleteSSHIdentity removes by id.
func (u *Usecase) DeleteSSHIdentity(ctx context.Context, id uint64) error {
	return u.repo.DeleteSSHIdentity(ctx, id)
}

// GenerateSSHIdentityInput is the create-form payload for the "manager
// generates the key" flow (P2). Caller supplies name + hosts;
// manager creates a fresh ed25519 keypair, persists it, and returns
// the identity along with the public key so the admin can paste it
// into the host's Deploy keys list.
type GenerateSSHIdentityInput struct {
	Name       string
	Hosts      []string
	KnownHosts string
}

// GenerateSSHIdentity creates a fresh ed25519 keypair server-side and
// persists it as an SSHIdentity. The returned identity has PublicKey
// populated (and only that — never the private bytes); the operator
// pastes the public key into the git host's Deploy keys page to
// authorise this key for the target repo.
//
// ed25519 is the right default: smaller than RSA, faster, modern.
// Comment is fixed to "ongrid-deploy" so a quick `ssh-keygen -lf`
// against the public side tells the admin where the key came from.
func (u *Usecase) GenerateSSHIdentity(ctx context.Context, in GenerateSSHIdentityInput) (*model.SSHIdentity, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: name required", errs.ErrInvalid)
	}
	hosts := normalizeHosts(in.Hosts)
	if len(hosts) == 0 {
		return nil, fmt.Errorf("%w: hosts required (at least one host pattern)", errs.ErrInvalid)
	}
	hostsJSON, err := json.Marshal(hosts)
	if err != nil {
		return nil, fmt.Errorf("encode hosts: %w", err)
	}

	// Generate the key pair. crypto/rand.Reader is the cryptographically
	// secure entropy source; we explicitly pass it (rather than letting
	// ed25519 default) to keep the path obvious for future code review.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 key: %w", err)
	}

	// Serialise the private key to OpenSSH PEM. ssh.MarshalPrivateKey
	// produces the modern `BEGIN OPENSSH PRIVATE KEY` format that
	// every contemporary git client accepts; the comment field becomes
	// the per-key annotation visible when running `ssh-keygen -lf`.
	block, err := ssh.MarshalPrivateKey(priv, "ongrid-deploy")
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}
	privPEM := string(pem.EncodeToMemory(block))

	// Derive the public side in OpenSSH "authorized_keys" line shape
	// (e.g. `ssh-ed25519 AAAA... ongrid-deploy`). This is exactly what
	// gets pasted into the host's Deploy keys page.
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("derive ssh public: %w", err)
	}
	pubLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " ongrid-deploy"
	fingerprint := sshFingerprintSHA256(sshPub)

	now := time.Now().UTC()
	row := &model.SSHIdentity{
		Name:        name,
		PrivateKey:  privPEM,
		PublicKey:   pubLine,
		Fingerprint: fingerprint,
		HostsJSON:   string(hostsJSON),
		KnownHosts:  strings.TrimSpace(in.KnownHosts),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := u.repo.CreateSSHIdentity(ctx, row); err != nil {
		return nil, err
	}
	row.PrivateKey = ""
	row.Passphrase = ""
	return row, nil
}

// pickSSHIdentityForHost picks the SSH identity to use for the given
// host. Matching order:
//   1. Exact host present in an identity's Hosts list
//   2. Glob match (filepath.Match — same semantics as ~/.ssh/config Host)
//   3. nil → no SSH identity; let git try the container's default
//      ~/.ssh (almost always fails for non-public hosts; the operator
//      then knows to add an identity)
//
// The match is order-stable: identities are sorted by name in
// ListSSHIdentities, so two identities both glob-matching `git.acme.*`
// always resolve to the same one for the same host.
func (u *Usecase) pickSSHIdentityForHost(ctx context.Context, host string) (*model.SSHIdentity, error) {
	rows, err := u.repo.ListSSHIdentities(ctx)
	if err != nil {
		return nil, err
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return nil, nil
	}
	// Pass 1 — exact match.
	for _, r := range rows {
		for _, pat := range parseHosts(r.HostsJSON) {
			if strings.EqualFold(pat, host) {
				return r, nil
			}
		}
	}
	// Pass 2 — glob match.
	for _, r := range rows {
		for _, pat := range parseHosts(r.HostsJSON) {
			if strings.ContainsAny(pat, "*?[") {
				ok, _ := filepath.Match(pat, host)
				if ok {
					return r, nil
				}
			}
		}
	}
	return nil, nil
}

// sshFingerprintSHA256 produces the canonical OpenSSH fingerprint
// `SHA256:<base64-no-padding>` — same format `ssh-keygen -lf <key>`
// prints. We surface this on the UI so an operator can verify their
// key without exporting it.
func sshFingerprintSHA256(pub ssh.PublicKey) string {
	sum := sha256.Sum256(pub.Marshal())
	return "SHA256:" + strings.TrimRight(base64.StdEncoding.EncodeToString(sum[:]), "=")
}

// normalizeHosts strips whitespace + lowercases each entry and dedups
// while preserving caller order. Empty strings are dropped.
func normalizeHosts(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, h := range in {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}

// parseHosts is the inverse of the JSON encoding done on insert. We
// intentionally tolerate malformed rows (return empty list) — a bad
// row should mean "this identity won't match anything" not "blow up
// the whole sync."
func parseHosts(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// extractSSHHost parses the host out of an SSH-style git URL. Accepts
// both `git@github.com:owner/repo.git` (scp-style) and
// `ssh://git@github.com/owner/repo.git`.
func extractSSHHost(repoURL string) string {
	repoURL = strings.TrimSpace(repoURL)
	if strings.HasPrefix(repoURL, "ssh://") {
		// ssh://user@host[:port]/path
		rest := strings.TrimPrefix(repoURL, "ssh://")
		if at := strings.Index(rest, "@"); at >= 0 {
			rest = rest[at+1:]
		}
		if slash := strings.Index(rest, "/"); slash >= 0 {
			rest = rest[:slash]
		}
		if colon := strings.Index(rest, ":"); colon >= 0 {
			rest = rest[:colon]
		}
		return strings.ToLower(rest)
	}
	// scp-style: user@host:path
	if at := strings.Index(repoURL, "@"); at >= 0 {
		rest := repoURL[at+1:]
		if colon := strings.Index(rest, ":"); colon >= 0 {
			return strings.ToLower(rest[:colon])
		}
	}
	return ""
}

// isSSHURL reports whether repoURL is an ssh-style git URL.
func isSSHURL(repoURL string) bool {
	repoURL = strings.TrimSpace(repoURL)
	if strings.HasPrefix(repoURL, "ssh://") {
		return true
	}
	// scp-style: user@host:path — but not e.g. "user@email.com" which
	// has no colon-separated path. We require both an @ AND a :.
	if at := strings.Index(repoURL, "@"); at > 0 {
		rest := repoURL[at+1:]
		if colon := strings.Index(rest, ":"); colon > 0 {
			return true
		}
	}
	return false
}
