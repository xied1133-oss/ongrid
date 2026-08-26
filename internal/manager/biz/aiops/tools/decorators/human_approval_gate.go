package decorators

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/compose"
	"github.com/ongridio/ongrid/internal/manager/biz/aiops/tools/basetool"
)

// ErrHumanApprovalUnavailable fails closed when a tool requires confirmation
// but the durable approval broker has not been wired.
var ErrHumanApprovalUnavailable = errors.New("human approval unavailable")

// HumanApprovalRequest is the immutable proposal created from an LLM tool
// candidate. The broker persists it before surfacing the confirmation card.
type HumanApprovalRequest struct {
	ToolName   string
	ToolClass  string
	ArgsJSON   string
	Summary    string
	SessionID  string
	ToolCallID string
	UserID     uint64
}

// HumanApprovalExecutor is invoked exactly once by the approval broker after
// a human approves the durable proposal.
type HumanApprovalExecutor func(context.Context) (string, error)

// HumanApprovalProposer bridges the tool decorator to the approval domain.
// Implementations block until approve/reject and return the approved tool's
// real result so the ReAct loop continues normally.
type HumanApprovalProposer interface {
	ProposeAndAwait(context.Context, HumanApprovalRequest, HumanApprovalExecutor) (string, error)
}

type HumanApprovalGate struct {
	inner    basetool.BaseTool
	proposer HumanApprovalProposer
}

func WithHumanApprovalGate(inner basetool.BaseTool, proposer HumanApprovalProposer) basetool.BaseTool {
	if inner == nil {
		return nil
	}
	return &HumanApprovalGate{inner: inner, proposer: proposer}
}

func (g *HumanApprovalGate) Info(ctx context.Context) (*basetool.ToolInfo, error) {
	return g.inner.Info(ctx)
}

func (g *HumanApprovalGate) InvokableRun(ctx context.Context, argsJSON string, opts ...basetool.InvokeOption) (string, error) {
	info, err := g.inner.Info(ctx)
	if err != nil || info == nil {
		return g.inner.InvokableRun(ctx, argsJSON, opts...)
	}
	resolved := basetool.ResolveOptions(opts)
	if resolved.HumanApproved || !requiresHumanApproval(info) {
		return g.inner.InvokableRun(ctx, argsJSON, opts...)
	}
	if g.proposer == nil {
		return "", fmt.Errorf("%w: tool %q", ErrHumanApprovalUnavailable, info.Name)
	}
	request := HumanApprovalRequest{
		ToolName:   info.Name,
		ToolClass:  info.Class,
		ArgsJSON:   argsJSON,
		Summary:    approvalSummary(info.Name, argsJSON),
		SessionID:  proposalSessionID(ctx, resolved),
		ToolCallID: compose.GetToolCallID(ctx),
		UserID:     resolved.UserID,
	}
	execute := func(context.Context) (string, error) {
		approvedOpts := append([]basetool.InvokeOption(nil), opts...)
		approvedOpts = append(approvedOpts, basetool.WithHumanApproval(true))
		return g.inner.InvokableRun(ctx, argsJSON, approvedOpts...)
	}
	return g.proposer.ProposeAndAwait(ctx, request, execute)
}

func requiresHumanApproval(info *basetool.ToolInfo) bool {
	if info == nil || info.Confirmation == basetool.ConfirmationSelfManaged || info.Confirmation == basetool.ConfirmationNotRequired {
		return false
	}
	if info.Confirmation == basetool.ConfirmationRequired {
		return true
	}
	return isMutatingClass(info.Class)
}

func approvalSummary(toolName, argsJSON string) string {
	args := strings.TrimSpace(argsJSON)
	if len(args) > 500 {
		args = args[:500] + "…"
	}
	if args == "" {
		args = "{}"
	}
	return toolName + " " + args
}

var _ basetool.BaseTool = (*HumanApprovalGate)(nil)
