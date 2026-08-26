package knowledge

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// embeddedVaultMDCount walks the embedded FS and counts .md files. Used to
// guard against an empty/broken go:embed (e.g. someone deletes the vendored
// dir and the embed silently captures nothing).
func embeddedVaultMDCount(t *testing.T) int {
	t.Helper()
	n := 0
	err := fs.WalkDir(builtinVaultFS, builtinVaultRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded vault: %v", err)
	}
	return n
}

func TestBuiltinVault_Embedded(t *testing.T) {
	if got := embeddedVaultMDCount(t); got == 0 {
		t.Fatal("embedded vault has 0 markdown files — go:embed captured nothing")
	}
}

func TestIsBuiltinVaultURL(t *testing.T) {
	cases := map[string]bool{
		BuiltinVaultURL:                         true,
		"  builtin://vault  ":                   true,
		"BUILTIN://VAULT":                       true,
		"https://github.com/ongridio/vault.git": false,
		"":                                      false,
	}
	for in, want := range cases {
		if got := IsBuiltinVaultURL(in); got != want {
			t.Errorf("IsBuiltinVaultURL(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestMaterializeBuiltinVault is the end-to-end proof for the local-source
// path: materialize the embedded vault into a repo dir, then scanRepoFiles
// (the exact downstream the qdrant pipeline feeds from) must see every
// markdown file — no git, no network involved.
func TestMaterializeBuiltinVault(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "7") // mimics repoDir(id=7)

	u := &Usecase{}
	if err := u.materializeBuiltinVault(dir); err != nil {
		t.Fatalf("materializeBuiltinVault: %v", err)
	}

	files, err := scanRepoFiles(dir)
	if err != nil {
		t.Fatalf("scanRepoFiles: %v", err)
	}
	want := embeddedVaultMDCount(t)
	if len(files) != want {
		t.Fatalf("scanRepoFiles found %d files, want %d (embedded .md count)", len(files), want)
	}

	// The prefix must be stripped: a clone of the upstream repo would put
	// concepts/observability.md at <dir>/concepts/observability.md, never
	// <dir>/builtin_vault/concepts/observability.md.
	if _, err := os.Stat(filepath.Join(dir, builtinVaultRoot)); !os.IsNotExist(err) {
		t.Errorf("embed root prefix %q leaked into materialized tree", builtinVaultRoot)
	}

	// Every scanned doc must carry a non-empty title + body so the
	// downstream embed step has something to index.
	for _, f := range files {
		if strings.TrimSpace(f.Title) == "" {
			t.Errorf("file %q has empty title", f.URL)
		}
		if strings.TrimSpace(f.Content) == "" {
			t.Errorf("file %q has empty content", f.URL)
		}
	}
}

// TestMaterializeBuiltinVault_Idempotent verifies a re-sync (materialize
// over an existing populated dir) atomically replaces rather than erroring
// on "destination exists" — the same guarantee the git atomic-swap gives.
func TestMaterializeBuiltinVault_Idempotent(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "7")
	u := &Usecase{}

	for i := 0; i < 2; i++ {
		if err := u.materializeBuiltinVault(dir); err != nil {
			t.Fatalf("materializeBuiltinVault pass %d: %v", i, err)
		}
	}
	// No stale .tmp-clone-* siblings should linger after a successful swap.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-clone-") {
			t.Errorf("leftover tmp dir after materialize: %s", e.Name())
		}
	}
}
