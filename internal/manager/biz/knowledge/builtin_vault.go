package knowledge

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// builtinVaultFS carries the platform-vendor knowledge vault straight in
// the binary. "Built-in" used to be a misnomer: the seed flow registered
// github.com/ongridio/vault.git and Sync() shelled `git clone` at first
// boot, so any operator without outbound github.com:443 (air-gapped, or
// just a mainland-China host where github times out) booted to an empty
// knowledge base — the exact thing "built-in" promises not to do.
//
// Now the markdown is embedded. Sync() materializes it to the repo dir
// instead of cloning, then the unchanged scan→chunk→embed→qdrant pipeline
// indexes it. Embedding the *files* doesn't bypass qdrant — vectors still
// require the embedder (ONGRID_EMBEDDING_API_KEY) + a live qdrant; we only
// remove the network dependency for getting the raw docs onto disk.
//
// Re-vendor with scripts/sync-builtin-vault.sh after the upstream
// ongridio/vault repo changes.
//
//go:embed all:builtin_vault
var builtinVaultFS embed.FS

// builtinVaultRoot is the embed root prefix stripped when materializing.
const builtinVaultRoot = "builtin_vault"

// BuiltinVaultURL is the sentinel "URL" used for the embedded vault. It is
// not a real git remote — it marks legacy repo rows so PurgeBuiltinVaultRepo
// can migrate them off the Repos table.
const BuiltinVaultURL = "builtin://vault"

// BuiltinVaultGitURL is the fixed cloud source for the platform vault
// (ADR-029). SyncBuiltinVault tries a runtime clone of this public repo
// first; the embedded snapshot above is the offline fallback. The URL is
// deliberately NOT operator-configurable — the vault always comes from this
// one repo, and a clone here never becomes a Repos-list row.
const (
	BuiltinVaultGitURL = "https://github.com/ongridio/vault.git"
	BuiltinVaultBranch = "main"
)

// IsBuiltinVaultURL reports whether url is the embedded-vault sentinel.
func IsBuiltinVaultURL(url string) bool {
	return strings.EqualFold(strings.TrimSpace(url), BuiltinVaultURL)
}

// materializeBuiltinVault writes the embedded vault into dir using the same
// "atomic temp + rename swap" contract as syncAtomicReplace: build a fresh
// sibling tmp dir, populate it, then rm -rf dir and rename. A crash mid-way
// leaves either the old snapshot or the new one — never a hybrid — so a
// concurrent reader/scan never sees a half-written tree.
func (u *Usecase) materializeBuiltinVault(dir string) error {
	tmp, err := newCloneTmpDir(dir)
	if err != nil {
		return fmt.Errorf("make tmp dir: %w", err)
	}
	if err := writeEmbeddedVault(tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("remove stale published dir: %w", err)
	}
	if err := os.Rename(tmp, dir); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("atomic rename %s → %s: %w", tmp, dir, err)
	}
	return nil
}

// writeEmbeddedVault copies every embedded file into destRoot, recreating
// the directory tree with the builtinVaultRoot prefix stripped (so
// "builtin_vault/concepts/x.md" lands at "<destRoot>/concepts/x.md" —
// matching what a git clone of the upstream repo would have produced, which
// is what scanRepoFiles expects).
func writeEmbeddedVault(destRoot string) error {
	return fs.WalkDir(builtinVaultFS, builtinVaultRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(builtinVaultRoot, p)
		if err != nil {
			return err
		}
		target := filepath.Join(destRoot, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := builtinVaultFS.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", p, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		return nil
	})
}
