package builtin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ongridio/ongrid/internal/skill"
)

func TestProbeHTTP_Metadata(t *testing.T) {
	m := (ProbeHTTP{}).Metadata()
	if err := m.Validate(); err != nil {
		t.Fatalf("metadata invalid: %v", err)
	}
	if m.EffectiveClass() != skill.ClassSafe {
		t.Fatalf("want ClassSafe, got %v", m.EffectiveClass())
	}
}

func TestProbeHTTP_Execute_HappyPath_HEAD(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "11")
		w.WriteHeader(200)
		if r.Method == "GET" {
			_, _ = w.Write([]byte("hello world"))
		}
	}))
	defer srv.Close()

	params, _ := json.Marshal(map[string]any{
		"url":        srv.URL,
		"method":     "HEAD",
		"timeout_ms": 2000,
	})
	out, err := (ProbeHTTP{}).Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var res probeHTTPResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status=%d", res.StatusCode)
	}
}

func TestProbeHTTP_Execute_HappyPath_GET(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello world"))
	}))
	defer srv.Close()

	params, _ := json.Marshal(map[string]any{
		"url":    srv.URL,
		"method": "GET",
	})
	out, err := (ProbeHTTP{}).Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var res probeHTTPResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status=%d", res.StatusCode)
	}
	if res.ContentLength != 11 {
		t.Fatalf("content_length=%d, want 11", res.ContentLength)
	}
}

func TestProbeHTTP_Execute_InvalidParams(t *testing.T) {
	if _, err := (ProbeHTTP{}).Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected error for missing url")
	}
	if _, err := (ProbeHTTP{}).Execute(context.Background(),
		json.RawMessage(`{"url":"http://x","method":"DELETE"}`)); err == nil {
		t.Fatal("expected error for bad method")
	}
}

func TestProbeHTTP_Execute_BadURL(t *testing.T) {
	params, _ := json.Marshal(map[string]any{
		"url":        "http://127.0.0.1:1/",
		"method":     "HEAD",
		"timeout_ms": 200,
	})
	out, err := (ProbeHTTP{}).Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var res probeHTTPResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Error == "" {
		t.Fatalf("expected error string for unreachable host, got %+v", res)
	}
}
