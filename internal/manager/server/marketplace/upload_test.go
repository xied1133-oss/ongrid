package marketplace

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// TestExtractTar_PreservesExecutableBit guards the regression where a skill
// shipping a binary (e.g. terraform) lost its executable bit on upload extract
// because writeFile used os.Create (0644), leaving the binary non-runnable.
// Executable archive entries must land 0755; plain files stay 0644.
func TestExtractTar_PreservesExecutableBit(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, e := range []struct {
		name string
		mode int64
		body string
	}{
		{"bin/tool", 0o755, "#!/bin/sh\n"},
		{"README.md", 0o644, "hi\n"},
	} {
		if err := tw.WriteHeader(&tar.Header{
			Name: e.name, Mode: e.mode, Size: int64(len(e.body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gw.Close()

	dest := t.TempDir()
	gr, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := extractTar(gr, dest); err != nil {
		t.Fatalf("extractTar: %v", err)
	}

	if got := statPerm(t, filepath.Join(dest, "bin/tool")); got != 0o755 {
		t.Errorf("bin/tool perm = %o, want 755 (executable bit lost)", got)
	}
	if got := statPerm(t, filepath.Join(dest, "README.md")); got != 0o644 {
		t.Errorf("README.md perm = %o, want 644", got)
	}
}

func statPerm(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}
