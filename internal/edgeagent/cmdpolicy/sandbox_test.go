package cmdpolicy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakePathValidator is a stub that allows-all by default but can be
// configured to reject specific prefixes.
type fakePathValidator struct {
	rejectPrefixes []string
}

func (f *fakePathValidator) ValidatePath(path string) error {
	for _, p := range f.rejectPrefixes {
		if strings.HasPrefix(path, p) {
			return errors.New("path not allowed: " + path)
		}
	}
	return nil
}

// makeSandbox builds a Sandbox with a Policy that resolves a few
// commonly used binaries via real PATH lookup. Tests that need extra
// fake bins can call installFakeBin.
func makeSandbox(t *testing.T) *Sandbox {
	t.Helper()
	p := DefaultReadOnly()
	return &Sandbox{Policy: p, PathValidator: &fakePathValidator{}}
}

func TestSandbox_DecideRejectsBadCmd(t *testing.T) {
	s := makeSandbox(t)
	d := s.Decide("rm -rf /")
	if d.Allow {
		t.Errorf("expected reject")
	}
}

func TestSandbox_DecidePathValidatorRejects(t *testing.T) {
	p := DefaultReadOnly()
	installFakeBin(t, p, "ls")
	s := &Sandbox{
		Policy:        p,
		PathValidator: &fakePathValidator{rejectPrefixes: []string{"/etc"}},
	}
	if d := s.Decide("ls /etc"); d.Allow {
		t.Errorf("expected path validator to reject /etc")
	}
	if d := s.Decide("ls /var"); !d.Allow {
		t.Errorf("expected /var to pass, got %s", d.Reason)
	}
}

func TestSandbox_NetworkHostAllowlist(t *testing.T) {
	p := DefaultReadOnly()
	installFakeBin(t, p, "ping")
	// Empty allowlist → all outbound denied.
	s := &Sandbox{Policy: p, PathValidator: &fakePathValidator{}}
	if d := s.Decide("ping example.com"); d.Allow {
		t.Errorf("expected reject (empty allowlist)")
	}
	// Add allowed.
	p.NetworkHostAllowlist = []string{"example.com"}
	if d := s.Decide("ping example.com"); !d.Allow {
		t.Errorf("expected allow after permitting host, got %s", d.Reason)
	}
	if d := s.Decide("ping 8.8.8.8"); d.Allow {
		t.Errorf("expected reject for non-listed host")
	}
}

func TestSandbox_NetworkHostAllowlistCIDR(t *testing.T) {
	p := DefaultReadOnly()
	installFakeBin(t, p, "ping")
	p.NetworkHostAllowlist = []string{"10.0.0.0/8"}
	s := &Sandbox{Policy: p, PathValidator: &fakePathValidator{}}
	if d := s.Decide("ping 10.5.5.5"); !d.Allow {
		t.Errorf("CIDR match: expected allow, got %s", d.Reason)
	}
	if d := s.Decide("ping 8.8.8.8"); d.Allow {
		t.Errorf("CIDR mismatch: expected reject")
	}
}

func TestSandbox_NetworkHostSuffix(t *testing.T) {
	p := DefaultReadOnly()
	installFakeBin(t, p, "curl")
	p.NetworkHostAllowlist = []string{".internal"}
	s := &Sandbox{Policy: p, PathValidator: &fakePathValidator{}}
	if d := s.Decide("curl --head http://svc.internal/health"); !d.Allow {
		t.Errorf("expected allow, got %s", d.Reason)
	}
	if d := s.Decide("curl --head http://example.com"); d.Allow {
		t.Errorf("expected reject")
	}
}

func TestSandbox_ExecRealEcho(t *testing.T) {
	// Build a tiny sandbox with /bin/echo as a ClassReadFS bin so we can
	// actually run something portable. echo is not in DefaultReadOnly
	// (we don't recommend it) so we add it here.
	p := DefaultReadOnly()
	echoPath, err := exec_LookPath("echo")
	if err != nil {
		t.Skipf("echo not in PATH: %v", err)
	}
	p.bins["echo"] = &BinaryPolicy{Bin: "echo", AbsPath: echoPath, Class: ClassReadFS}
	s := &Sandbox{Policy: p, PathValidator: &fakePathValidator{}}
	res, err := s.Exec(context.Background(), `echo hello`)
	if err != nil {
		t.Fatalf("Exec err: %v", err)
	}
	if !res.Allowed {
		t.Fatalf("expected allowed, got reason=%s", res.Reason)
	}
	if !strings.Contains(res.Stdout, "hello") {
		t.Errorf("stdout = %q, want hello", res.Stdout)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit = %d", res.ExitCode)
	}
}

func TestSandbox_ExecPipeline(t *testing.T) {
	p := DefaultReadOnly()
	for _, b := range []string{"echo", "wc"} {
		abs, err := exec_LookPath(b)
		if err != nil {
			t.Skipf("%s not in PATH: %v", b, err)
		}
		class := ClassReadFS
		if b == "wc" {
			class = ClassReadFS
		}
		p.bins[b] = &BinaryPolicy{Bin: b, AbsPath: abs, Class: class}
	}
	s := &Sandbox{Policy: p, PathValidator: &fakePathValidator{}}
	res, err := s.Exec(context.Background(), "echo hello world | wc -w")
	if err != nil {
		t.Fatalf("Exec err: %v", err)
	}
	if !res.Allowed {
		t.Fatalf("rejected: %s", res.Reason)
	}
	if strings.TrimSpace(res.Stdout) != "2" {
		t.Errorf("stdout = %q, want '2'", res.Stdout)
	}
}

func TestSandbox_ExecOutputCap(t *testing.T) {
	p := DefaultReadOnly()
	yes, err := exec_LookPath("yes")
	if err != nil {
		t.Skipf("yes not in PATH: %v", err)
	}
	head, err := exec_LookPath("head")
	if err != nil {
		t.Skipf("head not in PATH: %v", err)
	}
	p.bins["yes"] = &BinaryPolicy{Bin: "yes", AbsPath: yes, Class: ClassReadFS}
	p.bins["head"] = &BinaryPolicy{Bin: "head", AbsPath: head, Class: ClassReadFS}
	p.StdoutCap = 100
	p.Timeout = 2 * time.Second
	s := &Sandbox{Policy: p, PathValidator: &fakePathValidator{}}
	// `yes | head -n 10000` would print 20000 bytes — well past 100.
	res, err := s.Exec(context.Background(), "yes | head -n 10000")
	if err != nil {
		t.Fatalf("Exec err: %v", err)
	}
	if !res.Allowed {
		t.Fatalf("rejected: %s", res.Reason)
	}
	if !res.Truncated {
		t.Errorf("expected Truncated=true, got %+v", res)
	}
	if len(res.Stdout) > 100 {
		t.Errorf("stdout len = %d, want ≤ 100", len(res.Stdout))
	}
}

func TestSandbox_ExecTimeout(t *testing.T) {
	p := DefaultReadOnly()
	sleep, err := exec_LookPath("sleep")
	if err != nil {
		t.Skipf("sleep not in PATH: %v", err)
	}
	p.bins["sleep"] = &BinaryPolicy{Bin: "sleep", AbsPath: sleep, Class: ClassReadSystem}
	p.Timeout = 200 * time.Millisecond
	s := &Sandbox{Policy: p, PathValidator: &fakePathValidator{}}
	start := time.Now()
	res, _ := s.Exec(context.Background(), "sleep 5")
	dur := time.Since(start)
	if dur > 2*time.Second {
		t.Errorf("did not honour timeout (took %v)", dur)
	}
	if res.ExitCode == 0 {
		t.Errorf("expected non-zero exit on timeout")
	}
}

func TestSandbox_ExecPolicyRejection(t *testing.T) {
	s := makeSandbox(t)
	res, err := s.Exec(context.Background(), "rm -rf /")
	if err != nil {
		t.Fatalf("Exec returned error: %v", err)
	}
	if res.Allowed {
		t.Errorf("expected reject")
	}
	if res.Reason == "" {
		t.Errorf("expected reason")
	}
}

func TestSandbox_PathValidatorReusedForAbsArgs(t *testing.T) {
	p := DefaultReadOnly()
	installFakeBin(t, p, "cat")
	pv := &fakePathValidator{rejectPrefixes: []string{"/etc"}}
	s := &Sandbox{Policy: p, PathValidator: pv}
	// First arg is an abs path — must go through validator.
	if d := s.Decide("cat /etc/shadow"); d.Allow {
		t.Errorf("expected reject for /etc/shadow")
	}
}

// helpers

// exec_LookPath defers to os/exec.LookPath. Wrapped so the test file
// doesn't introduce a top-level os/exec import (which could collide
// with a future vendor restriction). Plus a clean place to skip
// missing-binary cases.
func exec_LookPath(bin string) (string, error) {
	// Try LookPath, then conventional fallbacks (mirrors discoverBin).
	abs := discoverBin(bin)
	if abs == "" {
		return "", os.ErrNotExist
	}
	if _, err := os.Stat(abs); err != nil {
		// Verify it really exists.
		return "", err
	}
	return filepath.Clean(abs), nil
}
