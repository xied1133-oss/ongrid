package logquery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type mutableLokiEndpointResolver struct {
	mu       sync.Mutex
	endpoint LokiEndpoint
}

func (r *mutableLokiEndpointResolver) ResolveLokiEndpoint(context.Context) (LokiEndpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.endpoint, nil
}

func (r *mutableLokiEndpointResolver) set(endpoint LokiEndpoint) {
	r.mu.Lock()
	r.endpoint = endpoint
	r.mu.Unlock()
}

func TestRuntimeClientFollowsEndpointAndBasicAuthChanges(t *testing.T) {
	newServer := func(user, password, label string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotUser, gotPassword, ok := r.BasicAuth()
			if !ok || gotUser != user || gotPassword != password {
				t.Errorf("BasicAuth() = %q, %q, %v, want %q, %q, true", gotUser, gotPassword, ok, user, password)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": []string{label}})
		}))
	}
	first := newServer("first-user", " first-password ", "first")
	t.Cleanup(first.Close)
	second := newServer("second-user", "second-password", "second")
	t.Cleanup(second.Close)

	resolver := &mutableLokiEndpointResolver{endpoint: LokiEndpoint{
		URL: first.URL, BasicUser: "first-user", BasicPassword: " first-password ",
	}}
	client := NewRuntimeClient(resolver, nil)
	start, end := time.Now().Add(-time.Minute), time.Now()
	values, err := client.LabelNames(t.Context(), start, end)
	if err != nil || len(values) != 1 || values[0] != "first" {
		t.Fatalf("first LabelNames() = %#v, %v", values, err)
	}

	resolver.set(LokiEndpoint{
		URL: second.URL, BasicUser: "second-user", BasicPassword: "second-password",
	})
	values, err = client.LabelNames(t.Context(), start, end)
	if err != nil || len(values) != 1 || values[0] != "second" {
		t.Fatalf("second LabelNames() = %#v, %v", values, err)
	}
}

func TestRuntimeClientAppliesRuntimeTLSSetting(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": []string{"level"}})
	}))
	t.Cleanup(server.Close)

	resolver := &mutableLokiEndpointResolver{endpoint: LokiEndpoint{URL: server.URL}}
	client := NewRuntimeClient(resolver, nil)
	start, end := time.Now().Add(-time.Minute), time.Now()
	if _, err := client.LabelNames(t.Context(), start, end); err == nil {
		t.Fatal("LabelNames() succeeded before TLS verification was disabled")
	}

	resolver.set(LokiEndpoint{URL: server.URL, TLSInsecure: true})
	values, err := client.LabelNames(t.Context(), start, end)
	if err != nil || len(values) != 1 || values[0] != "level" {
		t.Fatalf("LabelNames() with TLSInsecure = %#v, %v", values, err)
	}
}

func TestRuntimeClientRejectsPasswordWithoutUser(t *testing.T) {
	client := NewRuntimeClient(&mutableLokiEndpointResolver{endpoint: LokiEndpoint{
		URL: "https://loki.example.com", BasicPassword: "secret",
	}}, nil)
	_, err := client.LabelNames(t.Context(), time.Now().Add(-time.Minute), time.Now())
	if err == nil || !strings.Contains(err.Error(), "user is required") {
		t.Fatalf("LabelNames() error = %v", err)
	}
}
