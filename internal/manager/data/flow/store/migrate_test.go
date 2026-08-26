package store

import (
	"strings"
	"testing"
)

func TestRewriteLegacyIMNotificationGraph_RewritesOnlyLegacyToolNodes(t *testing.T) {
	raw := `{"nodes":[{"id":"old","type":"tool","config":{"tool":"send_im_message","channel":"ops"}},{"id":"new","type":"tool","config":{"tool":"send_notification"}},{"id":"note","type":"notify","config":{}}]}`
	got, changed, err := rewriteLegacyIMNotificationGraph(raw)
	if err != nil {
		t.Fatalf("rewrite graph: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if strings.Contains(got, `"tool":"send_im_message"`) {
		t.Fatalf("rewritten graph still contains legacy tool: %s", got)
	}
	if !strings.Contains(got, `"tool":"send_notification"`) {
		t.Fatalf("rewritten graph missing notification tool: %s", got)
	}
}

func TestRewriteLegacyIMNotificationGraph_LeavesUnrelatedGraphUntouched(t *testing.T) {
	raw := `{"nodes":[{"id":"a","type":"tool","config":{"tool":"query_promql"}}]}`
	got, changed, err := rewriteLegacyIMNotificationGraph(raw)
	if err != nil {
		t.Fatalf("rewrite graph: %v", err)
	}
	if changed || got != raw {
		t.Fatalf("got changed=%v graph=%s, want unchanged", changed, got)
	}
}
