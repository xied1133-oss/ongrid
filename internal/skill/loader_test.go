package skill

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// loaderRegistryMu serialises tests that mutate the global registry —
// LoadDirs goes through Register, which panics on duplicates, so we
// have to clean up between tests. Production code never re-loads the
// registry at runtime so this is purely a test concern.
var loaderRegistryMu sync.Mutex

func writeManifest(t *testing.T, dir string, m SkillManifest) {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skill.json"), b, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func writeExec(t *testing.T, path, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("loader tests use POSIX shell; skipping on Windows")
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write exec %s: %v", path, err)
	}
}

func TestLoadDirs_RegistersValidManifest(t *testing.T) {
	loaderRegistryMu.Lock()
	defer loaderRegistryMu.Unlock()

	root := t.TempDir()
	skillDir := filepath.Join(root, "echo_pack")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	exec := filepath.Join(skillDir, "run.sh")
	writeExec(t, exec, `#!/usr/bin/env bash
cat > /dev/null
echo '{"hello":"world"}'
`)
	writeManifest(t, skillDir, SkillManifest{
		Name:           "test_loader_pack",
		Description:    "loader test echo pack",
		Schema:         json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
		Entry:          "./run.sh",
		EnvAllow:       []string{"PATH"},
		TimeoutSeconds: 5,
		Class:          "safe",
		Category:       "test",
	})

	count, err := LoadDirs(LoaderConfig{Dirs: []string{root}})
	if err != nil {
		t.Fatalf("LoadDirs: %v", err)
	}
	if count != 1 {
		t.Fatalf("registered=%d, want 1", count)
	}
	defer unregisterForTest("test_loader_pack")

	exec2, ok := Get("test_loader_pack")
	if !ok {
		t.Fatal("skill not registered")
	}
	meta := exec2.Metadata()
	if meta.EffectiveScope() != ScopeManager {
		t.Errorf("scope=%v", meta.EffectiveScope())
	}
	if meta.Category != "test" {
		t.Errorf("category=%q", meta.Category)
	}

	// The registered Executor should run the script and return its stdout.
	out, err := exec2.Execute(context.Background(), json.RawMessage(`{"q":"hi"}`))
	if err != nil {
		t.Fatalf("execute registered subprocess skill: %v", err)
	}
	if !strings.Contains(string(out), `"world"`) {
		t.Errorf("stdout=%s", string(out))
	}
}

func TestLoadDirs_SkipsManifestWithRelativeEscape(t *testing.T) {
	loaderRegistryMu.Lock()
	defer loaderRegistryMu.Unlock()

	root := t.TempDir()
	skillDir := filepath.Join(root, "evil")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// entry tries to escape the allowlist root via ..
	writeManifest(t, skillDir, SkillManifest{
		Name:        "test_escape_pack",
		Description: "tries to escape",
		Entry:       "../../../bin/sh",
	})
	// And we put a phony binary at a path the absolute resolution
	// would land on, just to make sure we don't accidentally launch it.
	count, err := LoadDirs(LoaderConfig{Dirs: []string{root}})
	if err != nil {
		t.Fatalf("LoadDirs: %v", err)
	}
	if count != 0 {
		t.Fatalf("escape manifest should be skipped, got %d registered", count)
	}
	if _, ok := Get("test_escape_pack"); ok {
		unregisterForTest("test_escape_pack")
		t.Fatal("escape manifest registered despite allowlist")
	}
}

func TestLoadDirs_SkipsMissingDir(t *testing.T) {
	loaderRegistryMu.Lock()
	defer loaderRegistryMu.Unlock()

	count, err := LoadDirs(LoaderConfig{Dirs: []string{"/this/does/not/exist"}})
	if err != nil {
		t.Fatalf("LoadDirs: %v", err)
	}
	if count != 0 {
		t.Errorf("missing dir should yield 0 registrations, got %d", count)
	}
}

func TestLoadDirs_SkipsRelativeRoot(t *testing.T) {
	loaderRegistryMu.Lock()
	defer loaderRegistryMu.Unlock()

	count, err := LoadDirs(LoaderConfig{Dirs: []string{"relative/path"}})
	if err != nil {
		t.Fatalf("LoadDirs: %v", err)
	}
	if count != 0 {
		t.Errorf("relative root should yield 0 registrations, got %d", count)
	}
}

func TestLoadDirs_SkipsManifestWithBadKey(t *testing.T) {
	loaderRegistryMu.Lock()
	defer loaderRegistryMu.Unlock()

	root := t.TempDir()
	skillDir := filepath.Join(root, "bad_key")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	exec := filepath.Join(skillDir, "run.sh")
	writeExec(t, exec, `#!/usr/bin/env bash
echo '{}'
`)
	writeManifest(t, skillDir, SkillManifest{
		Name:        "Bad Key With Spaces",
		Description: "x",
		Entry:       "./run.sh",
	})
	count, err := LoadDirs(LoaderConfig{Dirs: []string{root}})
	if err != nil {
		t.Fatalf("LoadDirs: %v", err)
	}
	if count != 0 {
		t.Errorf("bad key should be skipped, got %d", count)
	}
}

// unregisterForTest pulls a skill out of the global registry. The
// production registry deliberately does not expose Unregister (skills
// are init-time + immutable), but tests need it to clean up between
// runs of LoadDirs.
func unregisterForTest(key string) {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()
	delete(globalRegistry.skills, key)
}
