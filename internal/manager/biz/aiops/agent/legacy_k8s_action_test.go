package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ongridio/ongrid/internal/manager/biz/aiops/tools"
)

func TestLegacyToolTimeoutAllowsHumanApprovalWait(t *testing.T) {
	defaultTimeout := 15 * time.Second
	tests := []struct {
		name string
		tool string
		args json.RawMessage
		want time.Duration
	}{
		{name: "ordinary tool", tool: "query_devices", args: json.RawMessage(`{}`), want: defaultTimeout},
		{name: "k8s dry-run", tool: tools.ToolNameExecuteK8sAction, args: json.RawMessage(`{"dry_run":true}`), want: legacyK8sDryRunTimeout},
		{name: "k8s drain dry-run default timeout", tool: tools.ToolNameExecuteK8sAction, args: json.RawMessage(`{"action":"drain","dry_run":true}`), want: legacyK8sDefaultDrainTimeout + legacyK8sDryRunDrainTimeoutPadding},
		{name: "k8s drain dry-run custom timeout", tool: tools.ToolNameExecuteK8sAction, args: json.RawMessage(`{"action":"drain","dry_run":true,"drain_timeout_seconds":300}`), want: 330 * time.Second},
		{name: "k8s approval", tool: tools.ToolNameExecuteK8sAction, args: json.RawMessage(`{"dry_run":false}`), want: legacyApprovalToolTimeout},
		{name: "k8s implicit write", tool: tools.ToolNameExecuteK8sAction, args: json.RawMessage(`{}`), want: legacyApprovalToolTimeout},
		{name: "malformed args", tool: tools.ToolNameExecuteK8sAction, args: json.RawMessage(`{`), want: defaultTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := legacyToolTimeout(defaultTimeout, tt.tool, tt.args); got != tt.want {
				t.Fatalf("legacyToolTimeout()=%s want %s", got, tt.want)
			}
		})
	}
}

func TestLegacyEmitIsAvailableToApprovalAdapter(t *testing.T) {
	want := Event{Type: EventApprovalPending, Approval: &ApprovalPendingEvent{ApprovalID: "approval-1"}}
	var got Event
	ctx := withEmit(context.Background(), func(event Event) { got = event })
	emit := EmitFromContext(ctx)
	if emit == nil {
		t.Fatal("EmitFromContext returned nil")
	}
	emit(want)
	if got.Type != EventApprovalPending || got.Approval == nil || got.Approval.ApprovalID != "approval-1" {
		t.Fatalf("unexpected event: %+v", got)
	}
}
