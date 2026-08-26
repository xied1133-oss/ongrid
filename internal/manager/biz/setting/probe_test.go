package setting

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTempoURLProbeUsesOTLPHTTPExportEndpoint(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/v1/traces" {
			t.Errorf("path = %q, want /v1/traces", r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "application/x-protobuf" {
			t.Errorf("Content-Type = %q, want application/x-protobuf", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	resolver := NewTempoResolver(New(newFakeRepo(), nil), server.URL+"/v1/traces")
	if err := NewTempoURLProbe(resolver).Probe(context.Background()); err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
}

func TestTempoURLProbeUsesReadyForQueryAPIURL(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if r.URL.Path != "/ready" {
			t.Errorf("path = %q, want /ready", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	resolver := NewTempoResolver(New(newFakeRepo(), nil), server.URL)
	if err := NewTempoURLProbe(resolver).Probe(context.Background()); err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
}

func TestTempoReadinessProbeUsesQueryAPIURL(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if r.URL.Path != "/ready" {
			t.Errorf("path = %q, want /ready", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	if err := NewTempoReadinessProbe(server.URL).Probe(context.Background()); err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
}
