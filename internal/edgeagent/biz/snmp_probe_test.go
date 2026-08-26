package biz

import (
	"context"
	"strings"
	"testing"

	"github.com/gosnmp/gosnmp"
	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

func TestProbeNetworkSNMPValidatesCredentialsBeforeNetworkCall(t *testing.T) {
	tests := []struct {
		name string
		req  tunnel.ProbeNetworkSNMPRequest
		want string
	}{
		{name: "missing address", req: tunnel.ProbeNetworkSNMPRequest{Version: "v2c", Community: "public"}, want: "address"},
		{name: "missing community", req: tunnel.ProbeNetworkSNMPRequest{Address: "192.0.2.1", Version: "v2c"}, want: "community"},
		{name: "missing version defaults to v2c", req: tunnel.ProbeNetworkSNMPRequest{Address: "192.0.2.1"}, want: "community"},
		{name: "invalid version", req: tunnel.ProbeNetworkSNMPRequest{Address: "192.0.2.1", Version: "v1", Community: "public"}, want: "version"},
		{name: "missing v3 username", req: tunnel.ProbeNetworkSNMPRequest{Address: "192.0.2.1", Version: "v3"}, want: "username"},
		{name: "missing v3 auth secret", req: tunnel.ProbeNetworkSNMPRequest{Address: "192.0.2.1", Version: "v3", Username: "operator", AuthProtocol: "sha256"}, want: "auth secret"},
		{name: "privacy without auth", req: tunnel.ProbeNetworkSNMPRequest{Address: "192.0.2.1", Version: "v3", Username: "operator", PrivacyProtocol: "aes256", PrivacySecret: "private"}, want: "requires authentication"},
		{name: "missing privacy secret", req: tunnel.ProbeNetworkSNMPRequest{Address: "192.0.2.1", Version: "v3", Username: "operator", AuthProtocol: "sha256", AuthSecret: "authenticate", PrivacyProtocol: "aes256"}, want: "privacy secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ProbeNetworkSNMP(context.Background(), tt.req)
			if got.OK || !strings.Contains(strings.ToLower(got.Error), tt.want) {
				t.Fatalf("response=%+v, want error containing %q", got, tt.want)
			}
		})
	}
}

func TestSNMPEngineID(t *testing.T) {
	params := &gosnmp.GoSNMP{
		SecurityParameters: &gosnmp.UsmSecurityParameters{
			AuthoritativeEngineID: string([]byte{0x80, 0x00, 0x13, 0x70, 0x01}),
		},
	}
	if got := snmpEngineID(params); got != "8000137001" {
		t.Fatalf("snmpEngineID() = %q", got)
	}
	if got := snmpEngineID(&gosnmp.GoSNMP{}); got != "" {
		t.Fatalf("snmpEngineID() without USM = %q", got)
	}
}

func TestProbeNetworkSNMPCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := ProbeNetworkSNMP(ctx, tunnel.ProbeNetworkSNMPRequest{
		Address: "192.0.2.1", Version: "v2c", Community: "public",
	})
	if got.OK || !strings.Contains(got.Error, "canceled") {
		t.Fatalf("response=%+v, want cancelled error", got)
	}
}

func TestSNMPInterfaceHelpers(t *testing.T) {
	index, ok := oidIndex(".1.3.6.1.2.1.2.2.1.2.17", oidIfDescr)
	if !ok || index != 17 {
		t.Fatalf("index=%d ok=%v", index, ok)
	}
	if _, ok := oidIndex(".1.3.6.1.2.1.2.2.1.3.17.1", oidIfType); ok {
		t.Fatal("multi-part suffix must not be treated as an interface index")
	}
	if got := snmpInterfaceKind(6); got != "ethernet" {
		t.Fatalf("kind=%q", got)
	}
	if got := snmpInterfaceStatus(2); got != "down" {
		t.Fatalf("status=%q", got)
	}
	address, ok := oidIPv4Address(".1.3.6.1.2.1.4.20.1.2.10.20.30.40", oidIPAdEntIfIndex)
	if !ok || address != "10.20.30.40" {
		t.Fatalf("address=%q ok=%v", address, ok)
	}
	if _, ok := oidIPv4Address(".1.3.6.1.2.1.4.20.1.2.10.20.300.40", oidIPAdEntIfIndex); ok {
		t.Fatal("invalid IPv4 suffix must be rejected")
	}
}

func TestUSMSecuritySupportsModernProtocols(t *testing.T) {
	security, flags, err := usmSecurity(tunnel.ProbeNetworkSNMPRequest{
		Username: "operator", AuthProtocol: "SHA-256", AuthSecret: "authenticate",
		PrivacyProtocol: "AES-256-C", PrivacySecret: "private",
	})
	if err != nil {
		t.Fatalf("usmSecurity() error = %v", err)
	}
	if security.AuthenticationProtocol != gosnmp.SHA256 {
		t.Fatalf("auth protocol = %v", security.AuthenticationProtocol)
	}
	if security.PrivacyProtocol != gosnmp.AES256C {
		t.Fatalf("privacy protocol = %v", security.PrivacyProtocol)
	}
	if flags != gosnmp.AuthPriv {
		t.Fatalf("flags = %v", flags)
	}
}
