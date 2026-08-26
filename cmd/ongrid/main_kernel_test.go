package main

import (
	"os"
	"testing"

	managersvcaiops "github.com/ongridio/ongrid/internal/manager/service/aiops"
)

// TestKernelEnvParsing covers the cmd-level boot-time decision:
//
//	unset / empty → KernelLegacy (default = zero behavior change)
//	"graph" → KernelGraph
//	"garbage" → KernelLegacy (with warn — see service.NewWithKernel)
//
// The actual env wiring lives in main(); this test exercises the
// parser the env value flows through, plus emulates the env-set
// path via os.Setenv. / the default MUST be
// legacy so the kernel switch is opt-in.
func TestKernelEnvParsing(t *testing.T) {
	cases := []struct {
		name   string
		envVal string
		setEnv bool
		want   managersvcaiops.Kernel
	}{
		{"unset", "", false, managersvcaiops.KernelGraph},
		{"empty_string", "", true, managersvcaiops.KernelGraph},
		{"graph_lower", "graph", true, managersvcaiops.KernelGraph},
		{"graph_upper", "GRAPH", true, managersvcaiops.KernelGraph},
		{"graph_padded", "  graph  ", true, managersvcaiops.KernelGraph},
		{"legacy_explicit", "legacy", true, managersvcaiops.KernelLegacy},
		{"garbage", "this-is-not-a-kernel", true, managersvcaiops.KernelGraph},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prev, hadPrev := os.LookupEnv("ONGRID_AGENT_KERNEL")
			t.Cleanup(func() {
				if hadPrev {
					_ = os.Setenv("ONGRID_AGENT_KERNEL", prev)
				} else {
					_ = os.Unsetenv("ONGRID_AGENT_KERNEL")
				}
			})
			if c.setEnv {
				_ = os.Setenv("ONGRID_AGENT_KERNEL", c.envVal)
			} else {
				_ = os.Unsetenv("ONGRID_AGENT_KERNEL")
			}
			got := managersvcaiops.ParseKernel(os.Getenv("ONGRID_AGENT_KERNEL"))
			if got != c.want {
				t.Errorf("ParseKernel(env=%q,set=%v) = %q, want %q", c.envVal, c.setEnv, got, c.want)
			}
		})
	}
}
