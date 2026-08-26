package collector

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

// NetworkDiscoveryCollector reads passive local neighbor state. It never
// scans a CIDR and never sends SNMP credentials; SNMP enrichment is a later
// manager-controlled action after a candidate is visible.
type NetworkDiscoveryCollector struct {
	procRoot string
	readFile func(string) ([]byte, error)
	lldp     func(context.Context) ([]byte, error)
	linux    bool
}

func NewNetworkDiscovery() *NetworkDiscoveryCollector {
	return &NetworkDiscoveryCollector{
		procRoot: "/proc",
		readFile: os.ReadFile,
		linux:    runtime.GOOS == "linux",
		lldp: func(ctx context.Context) ([]byte, error) {
			if runtime.GOOS != "linux" {
				return nil, nil
			}
			cmd := exec.CommandContext(ctx, "lldpctl", "-f", "keyvalue")
			return cmd.Output()
		},
	}
}

// CollectNetworkDiscovery returns gateway/ARP/LLDP candidates. Missing
// /proc files or lldpctl are treated as an empty signal, not as a fatal Edge
// error, because the same binary also runs on non-Linux development hosts.
func (c *NetworkDiscoveryCollector) CollectNetworkDiscovery(ctx context.Context) (tunnel.NetworkDiscoveryRequest, error) {
	if c == nil {
		return tunnel.NetworkDiscoveryRequest{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	request := tunnel.NetworkDiscoveryRequest{ObservedAt: nowUnix()}
	seen := make(map[string]struct{})
	add := func(candidate tunnel.NetworkDiscoveryCandidateReport) {
		candidate.IPAddress = strings.TrimSpace(candidate.IPAddress)
		candidate.MAC = normalizeMAC(candidate.MAC)
		candidate.InterfaceName = strings.TrimSpace(candidate.InterfaceName)
		if !strings.EqualFold(candidate.Source, "lldp") && isVirtualDiscoveryInterface(candidate.InterfaceName) {
			return
		}
		key := candidate.IPAddress + "|" + candidate.MAC + "|" + candidate.InterfaceName
		if key == "||" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		request.Candidates = append(request.Candidates, candidate)
	}

	if c.linux {
		gateways := make(map[string]string)
		if raw, err := c.readFile(c.procRoot + "/net/route"); err == nil {
			for _, route := range parseProcRoutes(string(raw)) {
				if !isVirtualDiscoveryInterface(route.Interface) {
					gateways[route.Gateway] = route.Interface
				}
			}
		}
		arpByGateway := make(map[string]procNeighbor)
		if raw, err := c.readFile(c.procRoot + "/net/arp"); err == nil {
			for _, neighbor := range parseProcARP(string(raw)) {
				if iface, ok := gateways[neighbor.IPAddress]; ok && iface == neighbor.Interface {
					arpByGateway[neighbor.IPAddress] = neighbor
				}
			}
		}
		for gateway, iface := range gateways {
			if neighbor, ok := arpByGateway[gateway]; ok {
				add(tunnel.NetworkDiscoveryCandidateReport{
					IPAddress: neighbor.IPAddress, MAC: neighbor.MAC,
					InterfaceName: neighbor.Interface, Source: "arp",
				})
				continue
			}
			add(tunnel.NetworkDiscoveryCandidateReport{
				IPAddress: gateway, InterfaceName: iface, Source: "gateway",
			})
		}
	}
	if c.lldp != nil {
		if raw, err := c.lldp(ctx); err == nil {
			for _, neighbor := range parseLLDPKeyValue(string(raw)) {
				add(neighbor)
			}
		} else if !errors.Is(err, exec.ErrNotFound) && !errors.Is(err, context.Canceled) {
			// lldpctl is optional; ARP/gateway candidates remain useful.
		}
	}
	return request, nil
}

func isVirtualDiscoveryInterface(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, prefix := range []string{
		"docker", "br-", "veth", "cni", "virbr", "flannel", "cali", "tun", "tap",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return name == "lo" || name == "loopback"
}

func normalizeMAC(value string) string {
	value = strings.ToLower(strings.NewReplacer(":", "", "-", "", ".", "", " ", "").Replace(strings.TrimSpace(value)))
	if len(value) != 12 {
		return ""
	}
	for _, r := range value {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return ""
		}
	}
	parts := make([]string, 0, 6)
	for i := 0; i < len(value); i += 2 {
		parts = append(parts, value[i:i+2])
	}
	return strings.Join(parts, ":")
}

type procRoute struct {
	Interface string
	Gateway   string
}

func parseProcRoutes(input string) []procRoute {
	var out []procRoute
	scanner := bufio.NewScanner(strings.NewReader(input))
	if scanner.Scan() {
		// Header: Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT
	}
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 || fields[1] != "00000000" {
			continue
		}
		gateway, ok := littleEndianIPv4(fields[2])
		if !ok || gateway == "0.0.0.0" {
			continue
		}
		out = append(out, procRoute{Interface: fields[0], Gateway: gateway})
	}
	return out
}

type procNeighbor struct {
	IPAddress string
	MAC       string
	Interface string
}

func parseProcARP(input string) []procNeighbor {
	var out []procNeighbor
	scanner := bufio.NewScanner(strings.NewReader(input))
	if scanner.Scan() {
		// Header: IP address HW type Flags HW address Mask Device
	}
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 6 || fields[2] == "0x0" || strings.EqualFold(fields[3], "00:00:00:00:00:00") {
			continue
		}
		if net.ParseIP(fields[0]) == nil {
			continue
		}
		out = append(out, procNeighbor{IPAddress: fields[0], MAC: fields[3], Interface: fields[5]})
	}
	return out
}

func littleEndianIPv4(value string) (string, bool) {
	if len(value) != 8 {
		return "", false
	}
	raw, err := hex.DecodeString(value)
	if err != nil {
		return "", false
	}
	return net.IPv4(raw[3], raw[2], raw[1], raw[0]).String(), true
}

// parseLLDPKeyValue handles lldpctl's stable key-value output without
// depending on a vendor-specific JSON schema. Keys commonly look like
// lldp.eth0.chassis.id, lldp.eth0.chassis.name and lldp.eth0.port.ifname.
func parseLLDPKeyValue(input string) []tunnel.NetworkDiscoveryCandidateReport {
	type neighbor struct {
		local, chassisID, chassisSubtype, managementAddress, remotePort string
	}
	groups := make(map[string]*neighbor)
	for _, line := range strings.Split(input, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || !strings.HasPrefix(key, "lldp.") {
			continue
		}
		parts := strings.Split(key, ".")
		if len(parts) < 4 {
			continue
		}
		local := parts[1]
		g := groups[local]
		if g == nil {
			g = &neighbor{local: local}
			groups[local] = g
		}
		switch strings.Join(parts[2:], ".") {
		case "chassis.id", "chassis.mac":
			g.chassisID = strings.TrimSpace(value)
		case "chassis.id_subtype":
			g.chassisSubtype = strings.TrimSpace(value)
		case "chassis.mgmt-ip", "chassis.mgmt.ip", "chassis.management-ip":
			g.managementAddress = preferredLLDPManagementAddress(g.managementAddress, value)
		case "port.ifname", "port.descr":
			g.remotePort = strings.TrimSpace(value)
		}
	}
	out := make([]tunnel.NetworkDiscoveryCandidateReport, 0, len(groups))
	for _, g := range groups {
		if g.chassisID == "" {
			continue
		}
		out = append(out, tunnel.NetworkDiscoveryCandidateReport{
			IPAddress: g.managementAddress, InterfaceName: g.local, Source: "lldp", LLDPChassisID: g.chassisID,
			LLDPChassisSubtype: g.chassisSubtype,
			Links: []tunnel.NetworkLinkReport{{
				LocalInterfaceName: g.local, RemoteInterfaceName: g.remotePort,
			}},
		})
	}
	return out
}

func preferredLLDPManagementAddress(current, candidate string) string {
	candidateIP := net.ParseIP(strings.TrimSpace(candidate))
	if candidateIP == nil {
		return current
	}
	currentIP := net.ParseIP(strings.TrimSpace(current))
	if currentIP == nil || lldpManagementAddressPriority(candidateIP) > lldpManagementAddressPriority(currentIP) {
		return candidateIP.String()
	}
	return currentIP.String()
}

func lldpManagementAddressPriority(ip net.IP) int {
	if ip.To4() != nil {
		return 3
	}
	if ip.IsGlobalUnicast() && !ip.IsLinkLocalUnicast() {
		return 2
	}
	return 1
}

func nowUnix() int64 { return time.Now().Unix() }

var _ interface {
	CollectNetworkDiscovery(context.Context) (tunnel.NetworkDiscoveryRequest, error)
} = (*NetworkDiscoveryCollector)(nil)
