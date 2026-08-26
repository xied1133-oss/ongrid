package biz

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// makeBundle writes a fake bundle to disk and returns the tarball path,
// its outer sha256, and the manifest contents.
func makeBundle(t *testing.T, dir string) (tarballPath, outerSHA string) {
	t.Helper()
	type entry struct {
		name string
		body []byte
		mode int
		dest string
	}
	entries := []entry{
		{name: "ongrid-edge", body: []byte("agent-v1"), mode: 0o755, dest: "/usr/local/bin/ongrid-edge"},
		{name: "process_exporter", body: []byte("pe-v1"), mode: 0o755, dest: "/usr/local/lib/ongrid-edge/process_exporter"},
	}

	var manifest bytes.Buffer
	fmt.Fprintln(&manifest, "# generated for test")
	for _, e := range entries {
		s := sha256.Sum256(e.body)
		fmt.Fprintf(&manifest, "%s  %04o  %s  %s\n", hex.EncodeToString(s[:]), e.mode, e.name, e.dest)
	}

	tarballPath = filepath.Join(dir, "bundle.tar.gz")
	out, err := os.Create(tarballPath)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	hasher := sha256.New()
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)

	// manifest first so extract can find it without ordering assumptions
	if err := tw.WriteHeader(&tar.Header{
		Name: "MANIFEST.txt",
		Mode: 0o644,
		Size: int64(manifest.Len()),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(manifest.Bytes()); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name: e.name,
			Mode: int64(e.mode),
			Size: int64(len(e.body)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	// Hash the on-disk tarball.
	raw, err := os.ReadFile(tarballPath)
	if err != nil {
		t.Fatal(err)
	}
	hasher.Write(raw)
	outerSHA = hex.EncodeToString(hasher.Sum(nil))
	return
}

func TestExtractTarGz_HappyPath(t *testing.T) {
	root := t.TempDir()
	tarballPath, _ := makeBundle(t, root)
	stage := filepath.Join(root, "stage")
	if err := extractTarGz(tarballPath, stage); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"MANIFEST.txt", "ongrid-edge", "process_exporter"} {
		if _, err := os.Stat(filepath.Join(stage, name)); err != nil {
			t.Errorf("expected %s to exist after extract: %v", name, err)
		}
	}
}

func TestExtractTarGz_RejectsPathEscape(t *testing.T) {
	root := t.TempDir()
	tarballPath := filepath.Join(root, "evil.tar.gz")
	out, err := os.Create(tarballPath)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: "../escape",
		Mode: 0o644,
		Size: 1,
	}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	_ = gz.Close()
	_ = out.Close()
	if err := extractTarGz(tarballPath, filepath.Join(root, "stage")); err == nil {
		t.Fatal("expected path-escape rejection")
	}
}

func TestVerifyManifest_HappyPath(t *testing.T) {
	root := t.TempDir()
	tarballPath, _ := makeBundle(t, root)
	stage := filepath.Join(root, "stage")
	if err := extractTarGz(tarballPath, stage); err != nil {
		t.Fatal(err)
	}
	count, err := verifyManifest(filepath.Join(stage, "MANIFEST.txt"), stage)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("expected 2 files verified, got %d", count)
	}
}

func TestVerifyManifest_DetectsTamper(t *testing.T) {
	root := t.TempDir()
	tarballPath, _ := makeBundle(t, root)
	stage := filepath.Join(root, "stage")
	if err := extractTarGz(tarballPath, stage); err != nil {
		t.Fatal(err)
	}
	// Mutate one file's contents post-extract; sha should mismatch.
	if err := os.WriteFile(filepath.Join(stage, "ongrid-edge"), []byte("TAMPERED"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyManifest(filepath.Join(stage, "MANIFEST.txt"), stage); err == nil {
		t.Fatal("expected sha mismatch")
	}
}
