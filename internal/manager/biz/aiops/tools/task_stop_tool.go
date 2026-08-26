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

// TaskStopTool cancels a running worker. The wire name
// "TaskStop" matches claude-code's catalog.
type TaskStopTool struct {
	spawner WorkerSpawner
	log     *slog.Logger
}

// NewTaskStopTool builds the tool.
func NewTaskStopTool(spawner WorkerSpawner, log *slog.Logger) *TaskStopTool {
	if log == nil {
		log = slog.Default()
	}
	return &TaskStopTool{spawner: spawner, log: log}
}

// TaskStopToolName is the wire name.
const TaskStopToolName = "TaskStop"

const taskStopWhenToUse = "Kill a running worker that's gone wrong / off-track. Pass the task_id returned " +
	"by AgentTool. Use when you realize mid-flight that the approach is wrong (worker is looping on the " +
	"same failing tool / chasing the wrong hypothesis / using too much time). " +
	"Stopped workers can still be continued via SendMessage in some flows, but the typical follow-up is " +
	"a fresh AgentTool spawn with a corrected prompt."

const taskStopSchema = `{
  "type": "object",
  "properties": {
    "task_id": {
      "type": "string",
      "description": "Worker task_id from a prior AgentTool call (\"agent-<8 hex>\")."
    }
  },
  "required": ["task_id"]
}`

// taskStopArgs is the parsed shape.
type taskStopArgs struct {
	TaskID string `json:"task_id"`
}

// Info returns the tool metadata.
func (t *TaskStopTool) Info(_ context.Context) (*basetool.ToolInfo, error) {
	return &basetool.ToolInfo{
		Name:         TaskStopToolName,
		Description:  "Stop a previously-spawned sub-agent worker.",
		WhenToUse:    taskStopWhenToUse,
		Parameters:   json.RawMessage(taskStopSchema),
		Class:        "write",
		Confirmation: basetool.ConfirmationNotRequired,
	}, nil
}

// taskStopResult is the JSON shape returned to the LLM.
type taskStopResult struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}

// InvokableRun cancels the worker. Idempotent — stopping an already-
// terminal worker is not an error.
func (t *TaskStopTool) InvokableRun(ctx context.Context, argsJSON string, opts ...basetool.InvokeOption) (string, error) {
	if t.spawner == nil {
		return "", errors.New("TaskStop: runtime not wired")
	}
	var args taskStopArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("TaskStop: parse args: %w", err)
	}
	if strings.TrimSpace(args.TaskID) == "" {
		return "", errors.New("TaskStop: task_id required")
	}

	if err := t.spawner.StopWorker(ctx, args.TaskID); err != nil {
		return "", fmt.Errorf("TaskStop: %w", err)
	}
	w, ok := t.spawner.GetWorker(args.TaskID)
	if !ok || w == nil {
		// Worker is gone — treat as success with synthetic status.
		body, _ := json.Marshal(taskStopResult{TaskID: args.TaskID, Status: "killed"})
		return string(body), nil
	}
	body, err := json.Marshal(taskStopResult{TaskID: w.ID, Status: w.Status})
	if err != nil {
		return "", fmt.Errorf("TaskStop: marshal: %w", err)
	}
	_ = opts
	return string(body), nil
}
