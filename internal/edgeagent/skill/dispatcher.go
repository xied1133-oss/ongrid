// Package skill on the edge side wires the tunnel-level execute_skill
// handler into the shared skill registry. The agent imports this package
// (and any builtin skill packages) at startup; once the tunnel handler
// is registered, every cloud->edge MethodExecuteSkill RPC dispatches by
// key to the corresponding Executor.
package skill

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ongridio/ongrid/internal/skill"
)

// Dispatch is the body of the edge-side execute_skill handler. It
// unmarshals the wire request, looks up the executor, runs Execute with
// the param blob, and packages the response (result or error string).
//
// Errors from the executor land in the response Error field — the RPC
// itself doesn't fail (so the manager can render the error to the
// operator); that keeps the audit trail intact and avoids the caller
// guessing whether a transport error or a skill error occurred.
func Dispatch(ctx context.Context, body []byte) ([]byte, error) {
	var req struct {
		Key    string          `json:"key"`
		Params json.RawMessage `json:"params,omitempty"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("decode execute_skill body: %w", err)
	}
	if req.Key == "" {
		return marshalResp(nil, "execute_skill: key required")
	}
	exec, ok := skill.Get(req.Key)
	if !ok {
		return marshalResp(nil, fmt.Sprintf("execute_skill: unknown skill %q", req.Key))
	}
	result, err := exec.Execute(ctx, req.Params)
	if err != nil {
		return marshalResp(nil, err.Error())
	}
	return marshalResp(result, "")
}

func marshalResp(result json.RawMessage, errMsg string) ([]byte, error) {
	body, err := json.Marshal(struct {
		Result json.RawMessage `json:"result,omitempty"`
		Error  string          `json:"error,omitempty"`
	}{Result: result, Error: errMsg})
	if err != nil {
		return nil, fmt.Errorf("marshal execute_skill resp: %w", err)
	}
	return body, nil
}
