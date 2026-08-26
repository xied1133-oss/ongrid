package tunnel

const (
	MethodOperatorExec      = "operator.exec"
	MethodOperatorExecStart = "operator.exec_start"
	MethodOperatorPushEvent = "operator.push_event"
	StreamKindOperatorExec  = "operator_exec"
)

type OperatorExecRequest struct {
	Command   string         `json:"command"`
	Args      map[string]any `json:"args,omitempty"`
	TimeoutMs int            `json:"timeout_ms,omitempty"`
}

type OperatorStreamMeta struct {
	Kind string              `json:"kind"`
	Req  OperatorExecRequest `json:"req"`
}

type OperatorStreamEvent struct {
	Type       string `json:"type"`
	Stream     string `json:"stream,omitempty"`
	Message    string `json:"message,omitempty"`
	Status     string `json:"status,omitempty"`
	ExitCode   int    `json:"exit_code,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Allowed    bool   `json:"allowed,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
}

type OperatorExecStartRequest struct {
	RunID string              `json:"run_id"`
	Req   OperatorExecRequest `json:"req"`
}

type OperatorExecStartResponse struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason,omitempty"`
}

type OperatorPushEventRequest struct {
	RunID string              `json:"run_id"`
	Event OperatorStreamEvent `json:"event"`
}

type OperatorPushEventResponse struct {
	OK bool `json:"ok"`
}

type OperatorExecResponse struct {
	Allowed    bool   `json:"allowed"`
	Reason     string `json:"reason,omitempty"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	ExitCode   int    `json:"exit_code"`
	Truncated  bool   `json:"truncated,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
}
