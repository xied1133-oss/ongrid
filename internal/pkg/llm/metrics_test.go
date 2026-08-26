package llm

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// TestMetricsIncrementOnSuccess — a successful Chat bumps success + tokens.
func TestMetricsIncrementOnSuccess(t *testing.T) {
	_, cfg := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(sampleChatResponse("ok", nil))
	})
	reg := prometheus.NewRegistry()
	client := New(cfg, nil, reg)
	if _, err := client.Chat(context.Background(), ChatReq{
		Messages: []Message{{Role: "user", Content: "hi"}},
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	if got := counterValue(t, reg, "ongrid_llm_requests_total", "gpt-4o", "success"); got != 1 {
		t.Errorf("success counter = %v, want 1", got)
	}
	if got := counterValue(t, reg, "ongrid_llm_tokens_total", "gpt-4o", "prompt"); got != 42 {
		t.Errorf("prompt tokens counter = %v, want 42", got)
	}
	if got := counterValue(t, reg, "ongrid_llm_tokens_total", "gpt-4o", "completion"); got != 8 {
		t.Errorf("completion tokens counter = %v, want 8", got)
	}
}

// TestMetricsReuseOnDoubleRegister — newMetrics on the same registry twice
// must not panic and must return a usable metrics struct.
func TestMetricsReuseOnDoubleRegister(t *testing.T) {
	reg := prometheus.NewRegistry()
	_ = newMetrics(reg, nil)
	// Second call would MustRegister-panic without the downgrade.
	m2 := newMetrics(reg, nil)
	if m2 == nil || m2.requestsTotal == nil {
		t.Fatal("second newMetrics call returned unusable metrics")
	}
}
