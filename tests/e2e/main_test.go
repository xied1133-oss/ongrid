//go:build e2e

package e2e

import (
	"os"
	"testing"

	"github.com/ongridio/ongrid/tests/e2e/testenv"
)

// TestMain owns the package-level lifecycle. The only thing it does is
// terminate the shared testcontainers MySQL container on process exit —
// without this, each `go test ./tests/e2e/...` invocation leaks a
// mysql:8.0 container (≈500 MB) into the host because the testcontainers
// reaper (ryuk) is disabled (mac startup is too flaky). On the 3.6 GiB
// test box, ~10 leaked containers exhaust RAM and kill sshd.
func TestMain(m *testing.M) {
	code := m.Run()
	testenv.TerminateSharedMySQL()
	os.Exit(code)
}
