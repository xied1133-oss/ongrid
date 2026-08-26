package skill

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// writeShellScript drops a small bash script into a temp dir and chmods
// it executable. Returns the absolute path. Tests skip on Windows since
// the shebang line and chmod won't work.
func writeShellScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("subprocess skill tests use POSIX shell; skipping on Windows")
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestSubprocessSkill_HappyPath(t *testing.T) {
	dir := t.TempDir()
	entry := writeShellScript(t, dir, "echo.sh", `#!/usr/bin/env bash
cat > /dev/null   # ignore stdin
echo '{"ok":true,"msg":"hello"}'
`)
	ss := &SubprocessSkill{
		Meta: Metadata{
			Key:         "subproc_echo",
			Name:        "echo",
			Description: "test echo",
			Class:       ClassSafe,
			Scope:       ScopeManager,
		},
		Entry:   entry,
		Timeout: 3 * time.Second,
	}
	out, err := ss.Execute(context.Background(), json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode stdout: %v (raw: %s)", err, string(out))
	}
	if got["ok"] != true || got["msg"] != "hello" {
		t.Errorf("payload mismatch: %+v", got)
	}
}

func TestSubprocessSkill_PassesParamsViaStdin(t *testing.T) {
	dir := t.TempDir()
	entry := writeShellScript(t, dir, "passthrough.sh", `#!/usr/bin/env bash
read line
echo "{\"received\":${line}}"
`)
	ss := &SubprocessSkill{
		Meta:    Metadata{Key: "subproc_pt", Name: "pt", Description: "pt"},
		Entry:   entry,
		Timeout: 3 * time.Second,
	}
	out, err := ss.Execute(context.Background(), json.RawMessage(`{"a":42}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(string(out), `"a":42`) {
		t.Errorf("stdin not forwarded; got %s", string(out))
	}
}

func TestSubprocessSkill_NonZeroExitReturnsError(t *testing.T) {
	dir := t.TempDir()
	entry := writeShellScript(t, dir, "fail.sh", `#!/usr/bin/env bash
echo "boom" >&2
exit 7
`)
	ss := &SubprocessSkill{
		Meta:    Metadata{Key: "subproc_fail", Name: "f", Description: "f"},
		Entry:   entry,
		Timeout: 3 * time.Second,
	}
	_, err := ss.Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error on non-zero exit")
	}
	if !strings.Contains(err.Error(), "exit 7") {
		t.Errorf("error should mention exit code: %v", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error should include stderr tail: %v", err)
	}
}

func TestSubprocessSkill_RejectsRelativePath(t *testing.T) {
	ss := &SubprocessSkill{
		Meta:  Metadata{Key: "subproc_rel", Name: "r", Description: "r"},
		Entry: "relative/path.sh",
	}
	if _, err := ss.Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected error for relative entry path")
	}
}

func TestSubprocessSkill_RejectsMissingBinary(t *testing.T) {
	ss := &SubprocessSkill{
		Meta:  Metadata{Key: "subproc_missing", Name: "m", Description: "m"},
		Entry: "/this/does/not/exist",
	}
	if _, err := ss.Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected error for missing entry")
	}
}

func TestSubprocessSkill_TimeoutFiresAndReturnsError(t *testing.T) {
	dir := t.TempDir()
	entry := writeShellScript(t, dir, "sleep.sh", `#!/usr/bin/env bash
sleep 2
`)
	ss := &SubprocessSkill{
		Meta:    Metadata{Key: "subproc_slow", Name: "s", Description: "s"},
		Entry:   entry,
		Timeout: 200 * time.Millisecond,
	}
	start := time.Now()
	_, err := ss.Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Errorf("timeout did not fire promptly; elapsed %v", elapsed)
	}
}

func TestSubprocessSkill_EnvAllowFiltersEnv(t *testing.T) {
	dir := t.TempDir()
	entry := writeShellScript(t, dir, "env.sh", `#!/usr/bin/env bash
cat > /dev/null
echo "{\"have_secret\":\"${MY_SECRET:-}\",\"have_other\":\"${SHOULD_NOT_LEAK:-}\"}"
`)
	t.Setenv("MY_SECRET", "shhh")
	t.Setenv("SHOULD_NOT_LEAK", "leaked")
	ss := &SubprocessSkill{
		Meta:     Metadata{Key: "subproc_env", Name: "e", Description: "e"},
		Entry:    entry,
		EnvAllow: []string{"MY_SECRET"},
		Timeout:  3 * time.Second,
	}
	out, err := ss.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode: %v (raw: %s)", err, string(out))
	}
	if got["have_secret"] != "shhh" {
		t.Errorf("MY_SECRET not forwarded: %v", got)
	}
	if got["have_other"] != "" {
		t.Errorf("SHOULD_NOT_LEAK leaked: %v", got)
	}
}

func TestSubprocessSkill_RejectsNonJSONStdout(t *testing.T) {
	dir := t.TempDir()
	entry := writeShellScript(t, dir, "garbage.sh", `#!/usr/bin/env bash
echo "this is definitely not JSON"
`)
	ss := &SubprocessSkill{
		Meta:    Metadata{Key: "subproc_garbage", Name: "g", Description: "g"},
		Entry:   entry,
		Timeout: 3 * time.Second,
	}
	if _, err := ss.Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected error on non-JSON stdout")
	}
}

func TestSubprocessSkill_MetadataForcesScopeManager(t *testing.T) {
	ss := &SubprocessSkill{
		Meta: Metadata{
			Key:         "subproc_force_scope",
			Name:        "f",
			Description: "f",
			Scope:       ScopeHost, // author lied; runtime overrides
		},
		Entry: "/tmp/anything",
	}
	if got := ss.Metadata().EffectiveScope(); got != ScopeManager {
		t.Errorf("scope override failed: got %v", got)
	}
}
