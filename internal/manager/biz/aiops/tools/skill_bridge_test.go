package tools

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	skillsvc "github.com/ongridio/ongrid/internal/manager/biz/skill"
	skillcore "github.com/ongridio/ongrid/internal/skill"
)

// fakeSkillRunner records what the bridge dispatched. We use it to
// verify the bridge:
//   - strips edge_id from manager-scope args (ScopeManager skills)
//   - keeps params verbatim
//   - returns the runner's result envelope
type fakeSkillRunner struct {
	mu     sync.Mutex
	gotIn  skillsvc.ExecuteInput
	result json.RawMessage
	errStr string
}

func (f *fakeSkillRunner) Execute(_ context.Context, _ skillsvc.Caller, in skillsvc.ExecuteInput) (*skillsvc.ExecuteOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotIn = in
	return &skillsvc.ExecuteOutput{Result: f.result, Error: f.errStr}, nil
}

// stubManagerSkill is a minimal Executor that the test registers in a
// fresh registry, then we drive RegisterSafeSkills through the bridge.
// We hijack the global registry via a sentinel key + cleanup.
type stubManagerSkill struct {
	key string
}

func (s stubManagerSkill) Metadata() skillcore.Metadata {
	return skillcore.Metadata{
		Key:         s.key,
		Name:        s.key,
		Description: "stub for bridge test",
		Class:       skillcore.ClassSafe,
		Scope:       skillcore.ScopeManager,
		Params: skillcore.ParamSchema{
			{Name: "query", Param: skillcore.Param{Type: "string", Required: true, Desc: "q"}},
		},
		ResultPreview: "{ok}",
	}
}

func (stubManagerSkill) Execute(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"ok":true}`), nil
}

func TestBridge_ManagerScopeSkill_NoEdgeIDRequired(t *testing.T) {
	const key = "stub_bridge_manager_scope"
	skillcore.Register(stubManagerSkill{key: key})
	defer unregisterSkillForTest(key)

	r := &Registry{tools: map[string]Tool{}, log: slog.Default()}
	runner := &fakeSkillRunner{result: json.RawMessage(`{"ok":true}`)}
	r.RegisterSafeSkills(runner)

	tool, ok := r.tools[key]
	if !ok {
		t.Fatalf("manager-scoped skill not registered as tool")
	}
	// Schema should NOT contain edge_id for ScopeManager.
	if strings.Contains(string(tool.Schema), `"edge_id"`) {
		t.Errorf("manager-scope tool schema should not include edge_id; got %s", string(tool.Schema))
	}
	// Required list (if present) must not include edge_id either.
	var schemaMap map[string]any
	if err := json.Unmarshal(tool.Schema, &schemaMap); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	if req, ok := schemaMap["required"].([]any); ok {
		for _, n := range req {
			if n == "edge_id" {
				t.Errorf("required list still has edge_id")
			}
		}
	}

	// Invoke the tool and verify the runner saw the params verbatim
	// (no edge_id) with EdgeID==0.
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"hi"}`))
	if err != nil {
		t.Fatalf("tool.Execute: %v", err)
	}
	if runner.gotIn.EdgeID != 0 {
		t.Errorf("EdgeID forwarded to runner = %d, want 0 for manager scope", runner.gotIn.EdgeID)
	}
	if got := string(runner.gotIn.Params); !strings.Contains(got, `"query":"hi"`) {
		t.Errorf("params not forwarded: %s", got)
	}
	if !strings.Contains(string(res.ResultJSON), `"result"`) {
		t.Errorf("ResultJSON envelope unexpected: %s", string(res.ResultJSON))
	}
	if res.DeviceID != nil {
		t.Errorf("manager-scope tool should not set DeviceID, got %d", *res.DeviceID)
	}
}

func TestBridge_ManagerScope_StripsAccidentalEdgeID(t *testing.T) {
	const key = "stub_bridge_manager_strip"
	skillcore.Register(stubManagerSkill{key: key})
	defer unregisterSkillForTest(key)

	r := &Registry{tools: map[string]Tool{}, log: slog.Default()}
	runner := &fakeSkillRunner{result: json.RawMessage(`{}`)}
	r.RegisterSafeSkills(runner)

	tool := r.tools[key]
	// LLM mistakenly sends edge_id even though it's not in the schema.
	if _, err := tool.Execute(context.Background(),
		json.RawMessage(`{"query":"hi","edge_id":99}`)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if runner.gotIn.EdgeID != 0 {
		t.Errorf("manager-scope skill must ignore stray edge_id; runner saw %d", runner.gotIn.EdgeID)
	}
	if strings.Contains(string(runner.gotIn.Params), "edge_id") {
		t.Errorf("edge_id should be stripped from params; got %s", string(runner.gotIn.Params))
	}
}

// unregisterSkillForTest is a tiny escape hatch — the production
// registry doesn't expose Unregister but tests need to clean up.
func unregisterSkillForTest(key string) {
	// We can't reach into skillcore.globalRegistry from here (different
	// package) without an exported helper, and we don't want to ship
	// one for production. Instead, the test relies on each subtest
	// using a unique key — the duplicate-key panic would only fire if
	// two tests in the same process used the same key. Leave this as
	// a no-op; the registered skill stays in the global registry for
	// the lifetime of the test binary, which is fine.
	_ = key
}
