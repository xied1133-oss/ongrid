package edge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileBundleResolverCurrentBundlesReportsEachArchitecture(t *testing.T) {
	dir := t.TempDir()
	amd64Name := "edge-bundle-linux-amd64-v1.2.3.tar.gz"
	if err := os.WriteFile(filepath.Join(dir, amd64Name), []byte("bundle"), 0o600); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	sha := strings.Repeat("a", 64)
	if err := os.WriteFile(filepath.Join(dir, amd64Name+".sha256"), []byte(sha+"  "+amd64Name+"\n"), 0o600); err != nil {
		t.Fatalf("write checksum: %v", err)
	}

	resolver := NewFileBundleResolver(dir, "v1.2.3", "https://manager.example")
	catalog, err := resolver.CurrentBundles()
	if err != nil {
		t.Fatalf("CurrentBundles: %v", err)
	}
	if catalog.ManagerVersion != "v1.2.3" || len(catalog.Items) != 2 {
		t.Fatalf("catalog = %+v, want current version and two architectures", catalog)
	}
	amd64 := catalog.Items[0]
	if amd64.Arch != "linux-amd64" || !amd64.Available || amd64.SHA256 != sha || amd64.Bytes != 6 || amd64.ModifiedAt == nil {
		t.Errorf("amd64 bundle = %+v, want available metadata", amd64)
	}
	arm64 := catalog.Items[1]
	if arm64.Arch != "linux-arm64" || arm64.Available || arm64.Error == "" {
		t.Errorf("arm64 bundle = %+v, want explicit missing state", arm64)
	}
}

func TestFileBundleResolverCurrentBundlesRejectsInvalidChecksum(t *testing.T) {
	dir := t.TempDir()
	name := "edge-bundle-linux-amd64-v1.tar.gz"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("bundle"), 0o600); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".sha256"), []byte(strings.Repeat("z", 64)), 0o600); err != nil {
		t.Fatalf("write checksum: %v", err)
	}

	catalog, err := NewFileBundleResolver(dir, "v1", "https://manager.example").CurrentBundles()
	if err != nil {
		t.Fatalf("CurrentBundles: %v", err)
	}
	if catalog.Items[0].Available || catalog.Items[0].Error == "" {
		t.Fatalf("invalid checksum should be unavailable: %+v", catalog.Items[0])
	}
}

func TestFileBundleResolverCurrentBundlesRequiresPublicURL(t *testing.T) {
	dir := t.TempDir()
	name := "edge-bundle-linux-amd64-v1.tar.gz"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("bundle"), 0o600); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".sha256"), []byte(strings.Repeat("b", 64)), 0o600); err != nil {
		t.Fatalf("write checksum: %v", err)
	}

	catalog, err := NewFileBundleResolver(dir, "v1", "").CurrentBundles()
	if err != nil {
		t.Fatalf("CurrentBundles: %v", err)
	}
	if catalog.Items[0].Available || !strings.Contains(catalog.Items[0].Error, "public") {
		t.Fatalf("missing public URL should be unavailable: %+v", catalog.Items[0])
	}
}

func TestFileBundleResolverReportsInaccessibleBundle(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "restricted")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir restricted dir: %v", err)
	}
	name := "edge-bundle-linux-amd64-v1.tar.gz"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("bundle"), 0o600); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("restrict bundle dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Errorf("restore bundle dir permissions: %v", err)
		}
	})

	resolver := NewFileBundleResolver(dir, "v1", "https://manager.example")
	_, _, _, err := resolver.ResolveBundle("linux-amd64", "v1")
	if err == nil || !strings.Contains(err.Error(), "bundle inaccessible") || strings.Contains(err.Error(), "bundle missing") {
		t.Fatalf("ResolveBundle error = %v, want inaccessible rather than missing", err)
	}

	catalog, err := resolver.CurrentBundles()
	if err != nil {
		t.Fatalf("CurrentBundles: %v", err)
	}
	if catalog.Items[0].Available || !strings.Contains(catalog.Items[0].Error, "permission denied") {
		t.Fatalf("catalog item = %+v, want permission diagnostic", catalog.Items[0])
	}
}
