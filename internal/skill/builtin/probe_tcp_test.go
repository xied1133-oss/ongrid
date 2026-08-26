package builtin

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	"github.com/ongridio/ongrid/internal/skill"
)

func TestProbeTCP_Metadata(t *testing.T) {
	m := (ProbeTCP{}).Metadata()
	if err := m.Validate(); err != nil {
		t.Fatalf("metadata invalid: %v", err)
	}
	if m.EffectiveClass() != skill.ClassSafe {
		t.Fatalf("want ClassSafe, got %v", m.EffectiveClass())
	}
	if m.Key != "host_probe_tcp" {
		t.Fatalf("unexpected Key %q", m.Key)
	}
}

func TestProbeTCP_Execute_HappyPath(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	params, _ := json.Marshal(map[string]any{
		"target":     ln.Addr().String(),
		"timeout_ms": 1000,
	})
	out, err := (ProbeTCP{}).Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var res probeTCPResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected OK=true, got %+v", res)
	}
	if res.Error != "" {
		t.Fatalf("unexpected error: %q", res.Error)
	}
}

func TestProbeTCP_Execute_InvalidParams(t *testing.T) {
	if _, err := (ProbeTCP{}).Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected error for missing target")
	}
	if _, err := (ProbeTCP{}).Execute(context.Background(), json.RawMessage(`{"target":123}`)); err == nil {
		t.Fatal("expected error for wrong type")
	}
}

func TestProbeTCP_Execute_Unreachable(t *testing.T) {
	// 127.0.0.1:1 is virtually guaranteed to refuse on dev hosts.
	params, _ := json.Marshal(map[string]any{
		"target":     "127.0.0.1:1",
		"timeout_ms": 200,
	})
	out, err := (ProbeTCP{}).Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var res probeTCPResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res.OK {
		t.Fatalf("expected OK=false for refused port, got %+v", res)
	}
	if res.Error == "" {
		t.Fatalf("expected error message, got empty")
	}
}
