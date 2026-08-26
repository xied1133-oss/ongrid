//go:build linux

package packetcapture

import (
	"reflect"
	"testing"
	"time"
)

func TestNormalizeRequestAppliesBoundedDefaults(t *testing.T) {
	got, err := normalizeRequest(Request{CaptureID: "capture-123", Interface: "eth0"})
	if err != nil {
		t.Fatalf("normalizeRequest: %v", err)
	}
	if got.Duration != defaultDuration || got.MaxBytes != defaultMaxBytes || got.MaxPackets != defaultMaxPackets || got.Snaplen != defaultSnaplen {
		t.Fatalf("defaults = %+v", got)
	}
}

func TestNormalizeRequestRejectsUnsafeInput(t *testing.T) {
	tests := []Request{
		{CaptureID: "../escape", Interface: "eth0"},
		{CaptureID: "capture-123", Interface: "../eth0"},
		{CaptureID: "capture-123", Interface: "eth0", NetworkNamespace: "../host"},
		{CaptureID: "capture-123", Interface: "eth0", Duration: maxDuration + time.Second},
		{CaptureID: "capture-123", Interface: "eth0", Filter: "tcp or udp"},
	}
	for _, in := range tests {
		if _, err := normalizeRequest(in); err == nil {
			t.Fatalf("normalizeRequest(%+v) error = nil", in)
		}
	}
}

func TestNormalizeRequestKeepsNamedNetworkNamespace(t *testing.T) {
	got, err := normalizeRequest(Request{CaptureID: "capture-123", Interface: "eth0", NetworkNamespace: "ongrid-netdev-a"})
	if err != nil {
		t.Fatalf("normalizeRequest: %v", err)
	}
	if got.NetworkNamespace != "ongrid-netdev-a" {
		t.Fatalf("network namespace = %q", got.NetworkNamespace)
	}
}

func TestNormalizeFilterAndTCPDumpArgs(t *testing.T) {
	filter, err := normalizeFilter("tcp port 443")
	if err != nil {
		t.Fatalf("normalizeFilter: %v", err)
	}
	if filter != "tcp and port 443" {
		t.Fatalf("filter = %q", filter)
	}
	got := tcpdumpWriteArgs(Request{Interface: "eth0", Snaplen: 1514, MaxPackets: 100, Filter: filter})
	want := []string{"-U", "-n", "-i", "eth0", "-s", "1514", "-c", "100", "-w", "-", "-p", "tcp and port 443"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestTCPDumpArgsUsePromiscuousModeWhenRequested(t *testing.T) {
	got := tcpdumpWriteArgs(Request{Interface: "eth0", Snaplen: 96, MaxPackets: 5, Promiscuous: true})
	for _, arg := range got {
		if arg == "-p" {
			t.Fatalf("args should not disable promiscuous mode: %#v", got)
		}
	}
}
