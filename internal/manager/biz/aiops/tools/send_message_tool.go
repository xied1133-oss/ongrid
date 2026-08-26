package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/ongridio/ongrid/internal/manager/biz/aiops/tools/basetool"
)

// SendMessageTool continues a worker by sending a follow-up message
// The wire name "SendMessage" matches claude-code's
// catalog so the LLM picks the right tool by name.
type SendMessageTool struct {
	spawner WorkerSpawner
	log     *slog.Logger
}

// NewSendMessageTool builds the tool.
func NewSendMessageTool(spawner WorkerSpawner, log *slog.Logger) *SendMessageTool {
	if log == nil {
		log = slog.Default()
	}
	return &SendMessageTool{spawner: spawner, log: log}
}

// SendMessageToolName is the wire name.
const SendMessageToolName = "SendMessage"

const sendMessageWhenToUse = "Continue a running or completed worker by sending a follow-up message. " +
	"Use 'to' = the task_id returned by AgentTool. Useful when the initial result is close but needs a " +
	"refinement (\"focus on the last 30 min\", \"also check disk\"). " +
	"DO NOT use for fresh tasks (use AgentTool). DO NOT use for killed workers — spawn a new one."

const sendMessageSchema = `{
  "type": "object",
  "properties": {
    "to": {
      "type": "string",
      "description": "Worker task_id from a prior AgentTool call (\"agent-<8 hex>\")."
    },
    "message": {
      "type": "string",
      "description": "Follow-up message body. Treated as a fresh user turn for the worker."
    }
  },
  "required": ["to", "message"]
}`

// sendMessageArgs is the parsed shape.
type sendMessageArgs struct {
	To      string `json:"to"`
	Message string `json:"message"`
}

// Info returns the tool metadata.
func (t *SendMessageTool) Info(_ context.Context) (*basetool.ToolInfo, error) {
	return &basetool.ToolInfo{
		Name:         SendMessageToolName,
		Description:  "Send a follow-up message to a previously-spawned sub-agent worker.",
		WhenToUse:    sendMessageWhenToUse,
		Parameters:   json.RawMessage(sendMessageSchema),
		Class:        "write",
		Confirmation: basetool.ConfirmationNotRequired,
	}, nil
}

// sendMessageResult is the JSON shape returned to the LLM.
type sendMessageResult struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
	Result string `json:"result,omitempty"`
	Err    string `json:"error,omitempty"`
}

// InvokableRun continues the worker and returns the new terminal state.
func (t *SendMessageTool) InvokableRun(ctx context.Context, argsJSON string, opts ...basetool.InvokeOption) (string, error) {
	if t.spawner == nil {
		return "", errors.New("SendMessage: runtime not wired")
	}
	var args sendMessageArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("SendMessage: parse args: %w", err)
	}
	if strings.TrimSpace(args.To) == "" {
		return "", errors.New("SendMessage: to required")
	}
	if strings.TrimSpace(args.Message) == "" {
		return "", errors.New("SendMessage: message required")
	}

	if err := t.spawner.SendToWorker(ctx, args.To, args.Message); err != nil {
		return "", fmt.Errorf("SendMessage: %w", err)
	}
	w, ok := t.spawner.GetWorker(args.To)
	if !ok || w == nil {
		return "", fmt.Errorf("SendMessage: worker %q vanished after send", args.To)
	}
	res := sendMessageResult{
		TaskID: w.ID,
		Status: w.Status,
		Result: w.Result,
		Err:    w.Err,
	}
	body, err := json.Marshal(res)
	if err != nil {
		return "", fmt.Errorf("SendMessage: marshal: %w", err)
	}
	_ = opts
	return string(body), nil
}
