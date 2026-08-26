package biz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

// handleAgentUpgrade implements MethodAgentUpgrade. It downloads the
// artifact at req.URL into the configured stage dir, verifies the
// declared SHA256, atomically renames it to `pending` (alongside a
// `pending.sha256` companion the ExecStartPre script reads), and
// signals Run() to exit so systemd can swap the binary on restart.
//
// Failure is sticky: any error along the way leaves no `pending` file,
// so a subsequent restart starts the existing binary as before. We
// don't try to rollback if the swap fails post-restart — that's the
// installer script's responsibility (it can fall back to `previous`
// on its own; out of scope for the agent).
//
// Sized for production over flaky links (45 min timeout, 4 KB stream
// chunks). The URL is fetched without auth; the artifact server lives
// behind the same nginx the manager already trusts, and the SHA256 is
// the gate against MITM / corruption either way.
func (a *Agent) handleAgentUpgrade(ctx context.Context, req tunnel.AgentUpgradeRequest) (tunnel.AgentUpgradeResponse, error) {
	dir := strings.TrimSpace(a.cfg.UpgradeStageDir)
	if dir == "" {
		return tunnel.AgentUpgradeResponse{}, fmt.Errorf("agent_upgrade: stage dir not configured")
	}

	expected := strings.ToLower(strings.TrimSpace(req.SHA256))
	if len(expected) != 64 {
		return tunnel.AgentUpgradeResponse{}, fmt.Errorf("agent_upgrade: sha256 must be 64 hex chars (got %d)", len(expected))
	}
	for _, c := range expected {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return tunnel.AgentUpgradeResponse{}, fmt.Errorf("agent_upgrade: sha256 not lower-hex")
		}
	}
	url := strings.TrimSpace(req.URL)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return tunnel.AgentUpgradeResponse{}, fmt.Errorf("agent_upgrade: url must be http(s)")
	}

	// Pre-create the stage dir; agent runs as a non-root user so this
	// only succeeds if the install script set perms (mode 0750 owned
	// by ongrid-edge). MkdirAll is idempotent and benign if it exists.
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return tunnel.AgentUpgradeResponse{}, fmt.Errorf("agent_upgrade: mkdir stage: %w", err)
	}

	tmp := filepath.Join(dir, "pending.tmp")
	final := filepath.Join(dir, "pending")
	checksum := filepath.Join(dir, "pending.sha256")

	// Clean up any stale leftovers from a previous failed attempt — we
	// own this dir exclusively, so unconditionally removing is safe.
	for _, p := range []string{tmp, final, checksum} {
		_ = os.Remove(p)
	}

	a.log.Info("agent_upgrade: starting download",
		slog.String("url", url),
		slog.String("sha256", expected),
		slog.String("stage_dir", dir),
	)

	// Long timeout so a slow network doesn't kill mid-download. ctx
	// override caps it for tests / RPC deadlines that come in shorter.
	dlCtx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(dlCtx, http.MethodGet, url, nil)
	if err != nil {
		return tunnel.AgentUpgradeResponse{}, fmt.Errorf("agent_upgrade: build req: %w", err)
	}
	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return tunnel.AgentUpgradeResponse{}, fmt.Errorf("agent_upgrade: get: %w", err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		return tunnel.AgentUpgradeResponse{}, fmt.Errorf("agent_upgrade: get: status %d", httpResp.StatusCode)
	}

	// Stream into pending.tmp while computing sha256 inline. Writing
	// then verifying decouples download throughput from sha cost; for
	// a 20 MB binary the cost is negligible either way.
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return tunnel.AgentUpgradeResponse{}, fmt.Errorf("agent_upgrade: open tmp: %w", err)
	}
	hasher := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hasher), httpResp.Body)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return tunnel.AgentUpgradeResponse{}, fmt.Errorf("agent_upgrade: stream: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return tunnel.AgentUpgradeResponse{}, fmt.Errorf("agent_upgrade: sync: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return tunnel.AgentUpgradeResponse{}, fmt.Errorf("agent_upgrade: close: %w", err)
	}

	got := hex.EncodeToString(hasher.Sum(nil))
	if got != expected {
		_ = os.Remove(tmp)
		return tunnel.AgentUpgradeResponse{}, fmt.Errorf("agent_upgrade: sha256 mismatch (got %s, want %s)", got, expected)
	}

	// Write the checksum companion BEFORE renaming pending.tmp to
	// pending — order matters because the ExecStartPre script keys off
	// pending.sha256's existence and re-verifies before applying.
	// pending.sha256 alone is benign without pending; the script
	// no-ops if either is missing.
	if err := os.WriteFile(checksum, []byte(expected+"\n"), 0o640); err != nil {
		_ = os.Remove(tmp)
		return tunnel.AgentUpgradeResponse{}, fmt.Errorf("agent_upgrade: write checksum: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		_ = os.Remove(checksum)
		return tunnel.AgentUpgradeResponse{}, fmt.Errorf("agent_upgrade: rename: %w", err)
	}

	a.log.Info("agent_upgrade: staged",
		slog.String("path", final),
		slog.Int64("bytes", n),
	)

	// Signal Run() to exit. Non-blocking — channel is buffered 1, and
	// repeated signals are harmless (extra send goes into the buffer
	// and is consumed by the same select case).
	select {
	case a.upgradeRequested <- struct{}{}:
	default:
	}

	return tunnel.AgentUpgradeResponse{StagedPath: final, Bytes: n}, nil
}
