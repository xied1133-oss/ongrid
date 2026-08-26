package flow

import (
	"strings"
	"testing"
)

func TestGenSystemPrompt_WhenMessagingRequested_UsesSharedToolNodes(t *testing.T) {
	prompt := genSystemPrompt([]ToolMeta{
		{Name: "query_promql", Description: "query metrics"},
		{Name: "send_notification", Description: "assistant notification sender"},
		{Name: "send_im_message", Description: "explicit IM group sender"},
	})
	for _, want := range []string{
		"send_notification",
		"send_im_message",
		"设置 → 通知",
		"im_app_id 和 group_id",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("generation prompt missing workflow-notification guidance %q", want)
		}
	}
	if !strings.Contains(prompt, "- send_notification") || !strings.Contains(prompt, "- send_im_message") {
		t.Fatal("messaging tools must be listed as workflow tools")
	}
}
