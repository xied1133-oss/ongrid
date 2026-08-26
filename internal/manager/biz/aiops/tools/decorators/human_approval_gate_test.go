package decorators

import (
	"context"
	"errors"
	"testing"

	"github.com/ongridio/ongrid/internal/manager/biz/aiops/tools/basetool"
)

type fakeHumanApprovalProposer struct {
	request HumanApprovalRequest
	calls   int
	execute bool
	err     error
}

func (f *fakeHumanApprovalProposer) ProposeAndAwait(ctx context.Context, request HumanApprovalRequest, execute HumanApprovalExecutor) (string, error) {
	f.calls++
	f.request = request
	if f.err != nil {
		return "", f.err
	}
	if !f.execute {
		return `{"status":"rejected"}`, nil
	}
	return execute(ctx)
}

func TestHumanApprovalGate_ReadPassesThrough(t *testing.T) {
	inner := &fakeTool{name: "query_devices", class: "read", result: `{"ok":true}`}
	proposer := &fakeHumanApprovalProposer{execute: true}
	out, err := WithHumanApprovalGate(inner, proposer).InvokableRun(context.Background(), `{}`)
	if err != nil || out != `{"ok":true}` {
		t.Fatalf("read invoke = %q, %v", out, err)
	}
	if proposer.calls != 0 || inner.calls != 1 {
		t.Fatalf("calls proposer=%d inner=%d, want 0/1", proposer.calls, inner.calls)
	}
}

func TestHumanApprovalGate_WriteFreezesProposalAndExecutesAfterApproval(t *testing.T) {
	inner := &fakeTool{name: "restart_service", class: "write", result: `{"restarted":true}`}
	proposer := &fakeHumanApprovalProposer{execute: true}
	args := `{"device_id":1,"service":"nginx"}`
	ctx := basetool.WithSessionID(context.Background(), "session-1")
	out, err := WithHumanApprovalGate(inner, proposer).InvokableRun(
		ctx, args, basetool.WithUserID(42),
	)
	if err != nil || out != `{"restarted":true}` {
		t.Fatalf("approved invoke = %q, %v", out, err)
	}
	if proposer.calls != 1 || inner.calls != 1 {
		t.Fatalf("calls proposer=%d inner=%d, want 1/1", proposer.calls, inner.calls)
	}
	if proposer.request.ToolName != "restart_service" || proposer.request.ArgsJSON != args || proposer.request.SessionID != "session-1" || proposer.request.UserID != 42 {
		t.Fatalf("proposal = %+v", proposer.request)
	}
	if !inner.gotOpts.HumanApproved {
		t.Fatal("inner tool did not receive the single-use human-approved marker")
	}
}

func TestHumanApprovalGate_RequiredReadUsesSameProposalFlow(t *testing.T) {
	inner := &fakeTool{name: "capture_pcap", class: "read", confirmation: basetool.ConfirmationRequired, result: `{"operation_id":"op-1"}`}
	proposer := &fakeHumanApprovalProposer{execute: false}
	out, err := WithHumanApprovalGate(inner, proposer).InvokableRun(context.Background(), `{"device_id":1}`)
	if err != nil || out != `{"status":"rejected"}` {
		t.Fatalf("rejected invoke = %q, %v", out, err)
	}
	if proposer.calls != 1 || inner.calls != 0 {
		t.Fatalf("calls proposer=%d inner=%d, want 1/0", proposer.calls, inner.calls)
	}
}

func TestHumanApprovalGate_SelfManagedToolIsNotDoubleGated(t *testing.T) {
	inner := &fakeTool{name: "cloud_bash", class: "destructive", confirmation: basetool.ConfirmationSelfManaged, result: `{"ok":true}`}
	proposer := &fakeHumanApprovalProposer{execute: true}
	_, err := WithHumanApprovalGate(inner, proposer).InvokableRun(context.Background(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if proposer.calls != 0 || inner.calls != 1 {
		t.Fatalf("calls proposer=%d inner=%d, want 0/1", proposer.calls, inner.calls)
	}
}

func TestHumanApprovalGate_ExplicitBenignWriteIsNotGated(t *testing.T) {
	inner := &fakeTool{name: "AgentTool", class: "write", confirmation: basetool.ConfirmationNotRequired, result: `{"ok":true}`}
	proposer := &fakeHumanApprovalProposer{execute: true}
	_, err := WithHumanApprovalGate(inner, proposer).InvokableRun(context.Background(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if proposer.calls != 0 || inner.calls != 1 {
		t.Fatalf("calls proposer=%d inner=%d, want 0/1", proposer.calls, inner.calls)
	}
}

func TestHumanApprovalGate_FailsClosedWithoutBroker(t *testing.T) {
	inner := &fakeTool{name: "restart_service", class: "write"}
	_, err := WithHumanApprovalGate(inner, nil).InvokableRun(context.Background(), `{}`)
	if !errors.Is(err, ErrHumanApprovalUnavailable) || inner.calls != 0 {
		t.Fatalf("err=%v inner=%d; want unavailable and no execution", err, inner.calls)
	}
}
