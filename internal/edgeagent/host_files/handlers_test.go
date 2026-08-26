package host_files

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

// fakeClient is a tunnel.Client stub that records registered handlers.
// Mirrors the pattern in internal/edgeagent/biz/agent_test.go but kept
// here to avoid depending on biz internals.
type fakeClient struct {
	mu       sync.Mutex
	handlers map[string]tunnel.Handler
}

func newFakeClient() *fakeClient {
	return &fakeClient{handlers: map[string]tunnel.Handler{}}
}

func (f *fakeClient) Dial(_ context.Context) error                     { return nil }
func (f *fakeClient) Call(_ context.Context, _ string, _, _ any) error { return nil }
func (f *fakeClient) OnReconnect(_ func())                             {}
func (f *fakeClient) Close() error                                     { return nil }
func (f *fakeClient) AcceptStream() (tunnel.StreamConn, error)         { return nil, nil }
func (f *fakeClient) RegisterHandler(method string, h tunnel.Handler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[method] = h
}

func (f *fakeClient) handler(method string) tunnel.Handler {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.handlers[method]
}

// sandboxForTempDir builds a SandboxConfig that whitelists the supplied
// temporary directory and preserves the system-discovered binaries. Tests
// use this to exercise real find/du against a known-shape directory tree.
func sandboxForTempDir(t *testing.T, tmp string) *SandboxConfig {
	t.Helper()
	sb := DefaultSandboxConfig()
	// macOS realpaths /var/folders/... → /private/var/folders/... so
	// any tmpdir created via t.TempDir() must be canonicalised before
	// going into the allow-list, otherwise the validator rejects.
	canon, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		canon = tmp
	}
	sb.AllowedReadPaths = []string{canon}
	if err := sb.Validate(); err != nil {
		t.Fatalf("sandbox validate: %v", err)
	}
	return sb
}

// sandboxForTempDirs is the multi-dir variant for batch tests that mix
// allowed and disallowed paths within a single request.
func sandboxForTempDirs(t *testing.T, tmps ...string) *SandboxConfig {
	t.Helper()
	sb := DefaultSandboxConfig()
	allow := make([]string, 0, len(tmps))
	for _, p := range tmps {
		canon, err := filepath.EvalSymlinks(p)
		if err != nil {
			canon = p
		}
		allow = append(allow, canon)
	}
	sb.AllowedReadPaths = allow
	if err := sb.Validate(); err != nil {
		t.Fatalf("sandbox validate: %v", err)
	}
	return sb
}

// canon canonicalises a tmp path the same way sandboxForTempDir does so
// requests sent to handlers match the allow-list shape on macOS.
func canon(t *testing.T, p string) string {
	t.Helper()
	c, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return c
}

// writeFile creates a file of the given size by actually writing bytes
// (NOT Truncate — sparse files report size_bytes via stat correctly but
// du counts allocated blocks, which on APFS / btrfs / xfs with delayed
// allocation will read 0 for a sparse hole. Tests need real data so the
// du parser path is exercised).
func writeFile(t *testing.T, path string, size int64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if size > 0 {
		buf := make([]byte, 32*1024)
		written := int64(0)
		for written < size {
			n := int64(len(buf))
			if size-written < n {
				n = size - written
			}
			m, err := f.Write(buf[:n])
			if err != nil {
				t.Fatal(err)
			}
			written += int64(m)
		}
	}
}

func TestRegister_InstallsAllThreeHandlers(t *testing.T) {
	fc := newFakeClient()
	if err := Register(fc, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	for _, m := range []string{tunnel.MethodFindLargeFiles, tunnel.MethodDuSummary, tunnel.MethodStatFile} {
		if fc.handler(m) == nil {
			t.Errorf("handler for %q not registered", m)
		}
	}
}

// =====================================================================
// find_large_files
// =====================================================================

func TestFindLargeFiles_RealFind(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("find_large_files supports only linux/darwin (have %s)", runtime.GOOS)
	}
	tmp := t.TempDir()
	// Three files: 4 MiB, 2 MiB, 1.5 MiB. min_size_bytes 1 MiB will
	// include all three; top_n=2 should keep only the two largest.
	writeFile(t, filepath.Join(tmp, "biggest.bin"), 4*1024*1024)
	writeFile(t, filepath.Join(tmp, "sub", "middle.bin"), 2*1024*1024)
	writeFile(t, filepath.Join(tmp, "small.bin"), 1024*1024+512*1024) // ~1.5MiB

	sb := sandboxForTempDir(t, tmp)
	h := makeFindLargeFilesHandler(sb, nil)
	body, _ := json.Marshal(tunnel.FindLargeFilesRequest{
		Paths:        []string{canon(t, tmp)},
		TopN:         2,
		MinSizeBytes: 1 << 20,
	})
	out, err := h(context.Background(), tunnel.Session{}, tunnel.MethodFindLargeFiles, body)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var resp tunnel.FindLargeFilesResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("got %d results, want 1", len(resp.Results))
	}
	got := resp.Results[0]
	if got.Error != "" {
		t.Fatalf("entry error: %s", got.Error)
	}
	if len(got.Files) != 2 {
		t.Fatalf("got %d files, want 2 (truncated by top_n)", len(got.Files))
	}
	if got.Files[0].SizeBytes < got.Files[1].SizeBytes {
		t.Errorf("not sorted descending: %d < %d", got.Files[0].SizeBytes, got.Files[1].SizeBytes)
	}
	if got.Files[0].SizeBytes != 4*1024*1024 {
		t.Errorf("biggest = %d, want %d", got.Files[0].SizeBytes, 4*1024*1024)
	}
	if got.Files[0].SizeHuman == "" {
		t.Errorf("SizeHuman empty")
	}
}

// TestFindLargeFiles_BatchPartialSuccess exercises the partial-success
// path: 2 of 3 directories are allow-listed, the third (/etc) gets
// rejected by the sandbox. The handler should return all 3 entries —
// the rejected one with Error set, the others with Files populated.
func TestFindLargeFiles_BatchPartialSuccess(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("find_large_files supports only linux/darwin (have %s)", runtime.GOOS)
	}
	tmpA := t.TempDir()
	tmpB := t.TempDir()
	writeFile(t, filepath.Join(tmpA, "a.bin"), 2*1024*1024)
	writeFile(t, filepath.Join(tmpB, "b.bin"), 3*1024*1024)

	sb := sandboxForTempDirs(t, tmpA, tmpB)
	h := makeFindLargeFilesHandler(sb, nil)

	// /etc is NOT in allow-list — should land in Results[1].Error.
	paths := []string{canon(t, tmpA), "/etc/passwd_dir_does_not_exist", canon(t, tmpB)}
	body, _ := json.Marshal(tunnel.FindLargeFilesRequest{
		Paths:        paths,
		TopN:         5,
		MinSizeBytes: 1 << 20,
	})
	out, err := h(context.Background(), tunnel.Session{}, tunnel.MethodFindLargeFiles, body)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var resp tunnel.FindLargeFilesResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 3 {
		t.Fatalf("len(Results)=%d, want 3", len(resp.Results))
	}
	// Order preserved.
	if resp.Results[0].Path != paths[0] || resp.Results[1].Path != paths[1] || resp.Results[2].Path != paths[2] {
		t.Errorf("path order not preserved: %+v", []string{resp.Results[0].Path, resp.Results[1].Path, resp.Results[2].Path})
	}
	if resp.Results[0].Error != "" {
		t.Errorf("entry 0 (allowed) should succeed, got Error=%q", resp.Results[0].Error)
	}
	if resp.Results[1].Error == "" {
		t.Errorf("entry 1 (sandbox-blocked) should have Error set")
	}
	if resp.Results[2].Error != "" {
		t.Errorf("entry 2 (allowed) should succeed, got Error=%q", resp.Results[2].Error)
	}
	if len(resp.Results[0].Files) == 0 || len(resp.Results[2].Files) == 0 {
		t.Errorf("expected files in entries 0 and 2: %+v", resp.Results)
	}
}

func TestFindLargeFiles_AllPathsBlocked(t *testing.T) {
	sb := DefaultSandboxConfig()
	h := makeFindLargeFilesHandler(sb, nil)
	body, _ := json.Marshal(tunnel.FindLargeFilesRequest{Paths: []string{"/proc/1/maps", "/root/.ssh/id_rsa"}})
	out, err := h(context.Background(), tunnel.Session{}, tunnel.MethodFindLargeFiles, body)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var resp tunnel.FindLargeFilesResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("len = %d, want 2", len(resp.Results))
	}
	for i, r := range resp.Results {
		if r.Error == "" {
			t.Errorf("Results[%d].Error empty for blocked path %q", i, r.Path)
		}
	}
}

func TestFindLargeFiles_EmptyPaths(t *testing.T) {
	sb := DefaultSandboxConfig()
	h := makeFindLargeFilesHandler(sb, nil)
	body, _ := json.Marshal(tunnel.FindLargeFilesRequest{Paths: nil})
	if _, err := h(context.Background(), tunnel.Session{}, tunnel.MethodFindLargeFiles, body); err == nil {
		t.Errorf("expected whole-batch error for empty paths")
	}
}

func TestFindLargeFiles_TooManyPaths(t *testing.T) {
	sb := DefaultSandboxConfig()
	h := makeFindLargeFilesHandler(sb, nil)
	paths := make([]string, hostFilesMaxBatchPaths+1)
	for i := range paths {
		paths[i] = "/var"
	}
	body, _ := json.Marshal(tunnel.FindLargeFilesRequest{Paths: paths})
	if _, err := h(context.Background(), tunnel.Session{}, tunnel.MethodFindLargeFiles, body); err == nil {
		t.Errorf("expected whole-batch error for too many paths")
	}
}

func TestFindLargeFiles_BadBody(t *testing.T) {
	sb := DefaultSandboxConfig()
	h := makeFindLargeFilesHandler(sb, nil)
	if _, err := h(context.Background(), tunnel.Session{}, tunnel.MethodFindLargeFiles, []byte(`not json`)); err == nil {
		t.Errorf("expected error for invalid JSON body")
	}
}

// =====================================================================
// du_summary
// =====================================================================

func TestDuSummary_RealDu(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("du_summary supports only linux/darwin (have %s)", runtime.GOOS)
	}
	tmp := t.TempDir()
	// Build:
	//   tmp/a/big.bin   3 MiB
	//   tmp/b/small.bin 1 MiB
	writeFile(t, filepath.Join(tmp, "a", "big.bin"), 3*1024*1024)
	writeFile(t, filepath.Join(tmp, "b", "small.bin"), 1024*1024)

	sb := sandboxForTempDir(t, tmp)
	h := makeDuSummaryHandler(sb, nil)

	body, _ := json.Marshal(tunnel.DuSummaryRequest{Paths: []string{canon(t, tmp)}, Depth: 1})
	out, err := h(context.Background(), tunnel.Session{}, tunnel.MethodDuSummary, body)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var resp tunnel.DuSummaryResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("len(Results)=%d, want 1", len(resp.Results))
	}
	got := resp.Results[0]
	if got.Error != "" {
		t.Fatalf("entry error: %s", got.Error)
	}
	if len(got.Subpaths) < 2 {
		t.Fatalf("subpaths len = %d, want >= 2 (a and b); resp=%+v", len(got.Subpaths), got)
	}
	if got.TotalSizeBytes <= 0 {
		t.Errorf("total = %d, want > 0", got.TotalSizeBytes)
	}
	if got.Subpaths[0].SizeBytes < got.Subpaths[1].SizeBytes {
		t.Errorf("not sorted descending: %+v", got.Subpaths)
	}
}

// TestDuSummary_BatchPartialSuccess: 2 allowed dirs + 1 blocked path,
// expect entries in input order with Error on the blocked one only.
func TestDuSummary_BatchPartialSuccess(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("du_summary supports only linux/darwin (have %s)", runtime.GOOS)
	}
	tmpA := t.TempDir()
	tmpB := t.TempDir()
	writeFile(t, filepath.Join(tmpA, "a", "f.bin"), 2*1024*1024)
	writeFile(t, filepath.Join(tmpB, "b", "f.bin"), 1024*1024)

	sb := sandboxForTempDirs(t, tmpA, tmpB)
	h := makeDuSummaryHandler(sb, nil)
	paths := []string{canon(t, tmpA), "/proc/1/maps", canon(t, tmpB)}
	body, _ := json.Marshal(tunnel.DuSummaryRequest{Paths: paths, Depth: 1})
	out, err := h(context.Background(), tunnel.Session{}, tunnel.MethodDuSummary, body)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var resp tunnel.DuSummaryResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 3 {
		t.Fatalf("len = %d, want 3", len(resp.Results))
	}
	if resp.Results[0].Error != "" || resp.Results[2].Error != "" {
		t.Errorf("allowed paths should succeed: %+v", resp.Results)
	}
	if resp.Results[1].Error == "" {
		t.Errorf("blocked path should have Error: %+v", resp.Results[1])
	}
	for i, p := range paths {
		if resp.Results[i].Path != p {
			t.Errorf("Results[%d].Path = %q, want %q", i, resp.Results[i].Path, p)
		}
	}
}

func TestDuSummary_RequiresPaths(t *testing.T) {
	sb := DefaultSandboxConfig()
	h := makeDuSummaryHandler(sb, nil)
	if _, err := h(context.Background(), tunnel.Session{}, tunnel.MethodDuSummary, []byte(`{}`)); err == nil {
		t.Errorf("expected error for missing paths")
	}
}

func TestDuSummary_TooManyPaths(t *testing.T) {
	sb := DefaultSandboxConfig()
	h := makeDuSummaryHandler(sb, nil)
	paths := make([]string, hostFilesMaxBatchPaths+1)
	for i := range paths {
		paths[i] = "/var"
	}
	body, _ := json.Marshal(tunnel.DuSummaryRequest{Paths: paths, Depth: 1})
	if _, err := h(context.Background(), tunnel.Session{}, tunnel.MethodDuSummary, body); err == nil {
		t.Errorf("expected whole-batch error for too many paths")
	}
}

func TestDuSummary_AllPathsBlocked(t *testing.T) {
	sb := DefaultSandboxConfig()
	h := makeDuSummaryHandler(sb, nil)
	body, _ := json.Marshal(tunnel.DuSummaryRequest{Paths: []string{"/proc/1/maps", "/root/.ssh/id_rsa"}, Depth: 1})
	out, err := h(context.Background(), tunnel.Session{}, tunnel.MethodDuSummary, body)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var resp tunnel.DuSummaryResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("len = %d, want 2", len(resp.Results))
	}
	for i, r := range resp.Results {
		if r.Error == "" {
			t.Errorf("Results[%d].Error empty for blocked path", i)
		}
	}
}

// =====================================================================
// stat_file
// =====================================================================

func TestStatFile_File(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "data.bin")
	writeFile(t, target, 4096)

	sb := sandboxForTempDir(t, tmp)
	h := makeStatFileHandler(sb, nil)

	canonTarget := canon(t, target)
	body, _ := json.Marshal(tunnel.StatFileRequest{Paths: []string{canonTarget}})
	out, err := h(context.Background(), tunnel.Session{}, tunnel.MethodStatFile, body)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var resp tunnel.StatFileResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("len(Results)=%d, want 1", len(resp.Results))
	}
	got := resp.Results[0]
	if got.Error != "" {
		t.Fatalf("entry error: %s", got.Error)
	}
	if got.Type != "file" {
		t.Errorf("Type = %q, want file", got.Type)
	}
	if got.SizeBytes != 4096 {
		t.Errorf("SizeBytes = %d, want 4096", got.SizeBytes)
	}
	if got.Mode == "" {
		t.Errorf("Mode empty")
	}
	if got.Mtime.IsZero() {
		t.Errorf("Mtime zero")
	}
	if time.Since(got.Mtime) > 5*time.Minute {
		t.Errorf("Mtime stale: %v", got.Mtime)
	}
}

// TestStatFile_BatchMixed exercises multi-path stat with one of each
// type (file / dir / symlink) plus one blocked path.
func TestStatFile_BatchMixed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks unreliable on windows")
	}
	tmp := t.TempDir()
	file := filepath.Join(tmp, "f.bin")
	writeFile(t, file, 100)
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}

	sb := sandboxForTempDir(t, tmp)
	h := makeStatFileHandler(sb, nil)

	paths := []string{
		canon(t, file),
		canon(t, tmp),
		link, // symlink — its parent dir is in allow-list
		"/etc/passwd",
	}
	body, _ := json.Marshal(tunnel.StatFileRequest{Paths: paths})
	out, err := h(context.Background(), tunnel.Session{}, tunnel.MethodStatFile, body)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var resp tunnel.StatFileResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 4 {
		t.Fatalf("len = %d, want 4", len(resp.Results))
	}
	if resp.Results[0].Type != "file" {
		t.Errorf("Results[0].Type = %q, want file", resp.Results[0].Type)
	}
	if resp.Results[1].Type != "dir" {
		t.Errorf("Results[1].Type = %q, want dir", resp.Results[1].Type)
	}
	if resp.Results[2].Type != "symlink" {
		t.Errorf("Results[2].Type = %q, want symlink", resp.Results[2].Type)
	}
	if resp.Results[3].Error == "" {
		t.Errorf("Results[3] (/etc/passwd) should be sandbox-blocked")
	}
	for i, p := range paths {
		if resp.Results[i].Path != p {
			t.Errorf("Results[%d].Path = %q, want %q", i, resp.Results[i].Path, p)
		}
	}
}

func TestStatFile_RequiresPaths(t *testing.T) {
	sb := DefaultSandboxConfig()
	h := makeStatFileHandler(sb, nil)
	if _, err := h(context.Background(), tunnel.Session{}, tunnel.MethodStatFile, []byte(`{}`)); err == nil {
		t.Errorf("expected error for missing paths")
	}
}

func TestStatFile_TooManyPaths(t *testing.T) {
	sb := DefaultSandboxConfig()
	h := makeStatFileHandler(sb, nil)
	paths := make([]string, hostFilesMaxBatchPaths+1)
	for i := range paths {
		paths[i] = "/var"
	}
	body, _ := json.Marshal(tunnel.StatFileRequest{Paths: paths})
	if _, err := h(context.Background(), tunnel.Session{}, tunnel.MethodStatFile, body); err == nil {
		t.Errorf("expected whole-batch error for too many paths")
	}
}

func TestStatFile_AllPathsBlocked(t *testing.T) {
	sb := DefaultSandboxConfig()
	h := makeStatFileHandler(sb, nil)
	body, _ := json.Marshal(tunnel.StatFileRequest{Paths: []string{"/etc/shadow", "/root/.ssh/id_rsa"}})
	out, err := h(context.Background(), tunnel.Session{}, tunnel.MethodStatFile, body)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var resp tunnel.StatFileResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("len = %d, want 2", len(resp.Results))
	}
	for i, r := range resp.Results {
		if r.Error == "" {
			t.Errorf("Results[%d].Error empty for blocked path", i)
		}
	}
}

// =====================================================================
// concurrency / race
// =====================================================================

// TestRunBatch_Concurrency confirms runBatch does honour the limit
// (no more than hostFilesBatchConcurrency goroutines active at once)
// and preserves output order even when the fast paths finish first.
func TestRunBatch_Concurrency(t *testing.T) {
	const N = 12
	paths := make([]string, N)
	for i := range paths {
		paths[i] = "p" + string(rune('a'+i%26))
	}
	out := make([]int, N)
	var inflight, peak int32
	work := func(_ context.Context, idx int, _ string) {
		now := atomic.AddInt32(&inflight, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if now <= p || atomic.CompareAndSwapInt32(&peak, p, now) {
				break
			}
		}
		// Sleep so multiple goroutines pile up and we measure peak.
		time.Sleep(20 * time.Millisecond)
		out[idx] = idx + 1
		atomic.AddInt32(&inflight, -1)
	}
	if err := runBatch(context.Background(), paths, out, work); err != nil {
		t.Fatalf("runBatch: %v", err)
	}
	if peak > hostFilesBatchConcurrency {
		t.Errorf("peak concurrent = %d, exceeds limit %d", peak, hostFilesBatchConcurrency)
	}
	for i, v := range out {
		if v != i+1 {
			t.Errorf("out[%d] = %d, want %d (order corrupted)", i, v, i+1)
		}
	}
}

// =====================================================================
// helper unit tests
// =====================================================================

func TestParseLinuxFindLine(t *testing.T) {
	fi, ok := parseLinuxFindLine("12345|1714000000.0123|alice|/var/log/big.log")
	if !ok {
		t.Fatalf("parse failed")
	}
	if fi.SizeBytes != 12345 || fi.Owner != "alice" || fi.Path != "/var/log/big.log" {
		t.Errorf("unexpected: %+v", fi)
	}
	if fi.Mtime.Unix() != 1714000000 {
		t.Errorf("mtime epoch = %d, want 1714000000", fi.Mtime.Unix())
	}
}

func TestParseLinuxFindLine_PathWithPipe(t *testing.T) {
	// Paths can contain | — splitter must use SplitN(_, _, 4) so the
	// path captures the rest of the line verbatim.
	fi, ok := parseLinuxFindLine("100|1714000000|root|/var/foo|bar.log")
	if !ok {
		t.Fatalf("parse failed")
	}
	if fi.Path != "/var/foo|bar.log" {
		t.Errorf("path = %q, want /var/foo|bar.log", fi.Path)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in  int64
		out string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{int64(1.5 * 1024 * 1024), "1.5 MiB"},
		{1 << 30, "1.0 GiB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.out {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.out)
		}
	}
}
