package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewCloneTmpDir_IsSiblingAndUnique(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	a, err := newCloneTmpDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(a)
	b, err := newCloneTmpDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(b)

	if a == b {
		t.Fatal("tmp dirs collided")
	}
	if filepath.Dir(a) != filepath.Dir(dir) || filepath.Dir(b) != filepath.Dir(dir) {
		t.Fatalf("tmp dirs not siblings of %s: %s / %s", dir, a, b)
	}
	for _, p := range []string{a, b} {
		base := filepath.Base(p)
		if !strings.HasPrefix(base, ".tmp-clone-1-") {
			t.Errorf("unexpected tmp dir name %q", base)
		}
	}
}

func TestPurgeStaleCloneTmps(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "5")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two siblings that look like stale leftovers + one that should
	// survive (different prefix → different repo id).
	for _, sib := range []string{".tmp-clone-5-aaa", ".tmp-clone-5-bbb"} {
		if err := os.MkdirAll(filepath.Join(root, sib), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, ".tmp-clone-7-zzz"), 0o755); err != nil {
		t.Fatal(err)
	}

	purgeStaleCloneTmps(dir)

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name()] = true
	}
	if names[".tmp-clone-5-aaa"] || names[".tmp-clone-5-bbb"] {
		t.Errorf("stale dir for our id survived sweep: %v", names)
	}
	if !names[".tmp-clone-7-zzz"] {
		t.Errorf("sweep incorrectly removed a different repo's tmp: %v", names)
	}
	if !names["5"] {
		t.Errorf("the actual dir was nuked: %v", names)
	}
}
