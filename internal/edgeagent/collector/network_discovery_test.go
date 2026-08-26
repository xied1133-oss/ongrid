package collector

import (
	"context"
	"errors"
	"testing"
)

func TestParseProcRoutesUsesActualDefaultGateway(t *testing.T) {
	routes := parseProcRoutes("Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT\n" +
		"eth0 00000000 0101A8C0 0003 0 0 100 00000000 0 0 0\n")
	if len(routes) != 1 || routes[0].Gateway != "192.168.1.1" || routes[0].Interface != "eth0" {
		t.Fatalf("routes=%+v", routes)
	}
}

func TestParseProcARPSkipsIncompleteEntries(t *testing.T) {
	arps := parseProcARP("IP address HW type Flags HW address Mask Device\n" +
		"192.168.1.1 0x1 0x2 AA-BB-CC-DD-EE-FF * eth0\n" +
		"192.168.1.2 0x1 0x0 00:00:00:00:00:00 * eth0\n")
	if len(arps) != 1 || arps[0].IPAddress != "192.168.1.1" {
		t.Fatalf("arps=%+v", arps)
	}
}

func TestParseLLDPKeyValueBuildsCandidateAndLink(t *testing.T) {
	neighbors := parseLLDPKeyValue("lldp.eth0.chassis.id=00:11:22:33:44:55\n" +
		"lldp.eth0.chassis.id_subtype=mac address\n" +
		"lldp.eth0.chassis.mgmt-ip=192.0.2.10\n" +
		"lldp.eth0.chassis.mgmt-ip=fe80::211:22ff:fe33:4455\n" +
		"lldp.eth0.port.ifname=Gi1/0/1\n")
	if len(neighbors) != 1 {
		t.Fatalf("neighbors=%+v", neighbors)
	}
	got := neighbors[0]
	if got.Source != "lldp" || got.IPAddress != "192.0.2.10" || got.LLDPChassisID != "00:11:22:33:44:55" || len(got.Links) != 1 {
		t.Fatalf("candidate=%+v", got)
	}
	if got.Links[0].RemoteInterfaceName != "Gi1/0/1" {
		t.Fatalf("link=%+v", got.Links[0])
	}
}

func TestNetworkDiscoveryCollectorContinuesWithoutLLDP(t *testing.T) {
	c := &NetworkDiscoveryCollector{
		procRoot: "/proc",
		linux:    true,
		readFile: func(path string) ([]byte, error) {
			if path == "/proc/net/route" {
				return []byte("Iface Destination Gateway Flags\neth0 00000000 0101A8C0 0003\n"), nil
			}
			return nil, errors.New("not present")
		},
		lldp: func(context.Context) ([]byte, error) { return nil, errors.New("lldpd is not installed") },
	}
	request, err := c.CollectNetworkDiscovery(context.Background())
	if err != nil || len(request.Candidates) != 1 {
		t.Fatalf("request=%+v err=%v", request, err)
	}
	if request.Candidates[0].Source != "gateway" {
		t.Fatalf("candidate=%+v", request.Candidates[0])
	}
}

func TestIsVirtualDiscoveryInterface(t *testing.T) {
	for _, name := range []string{"docker0", "br-abc123", "veth-a", "cni0", "virbr0", "flannel.1", "cali123", "tun0", "tap0", "lo"} {
		if !isVirtualDiscoveryInterface(name) {
			t.Errorf("isVirtualDiscoveryInterface(%q) = false", name)
		}
	}
	for _, name := range []string{"eth0", "ens18", "bond0"} {
		if isVirtualDiscoveryInterface(name) {
			t.Errorf("isVirtualDiscoveryInterface(%q) = true", name)
		}
	}
}

func TestNetworkDiscoveryKeepsLLDPOnVirtualTestInterface(t *testing.T) {
	c := &NetworkDiscoveryCollector{
		linux:    true,
		readFile: func(string) ([]byte, error) { return nil, errors.New("not present") },
		lldp: func(context.Context) ([]byte, error) {
			return []byte("lldp.veth-a.chassis.id=00:11:22:33:44:55\n"), nil
		},
	}
	request, err := c.CollectNetworkDiscovery(context.Background())
	if err != nil || len(request.Candidates) != 1 || request.Candidates[0].Source != "lldp" {
		t.Fatalf("request=%+v err=%v", request, err)
	}
}
