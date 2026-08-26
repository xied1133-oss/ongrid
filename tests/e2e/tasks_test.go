//go:build e2e

// Catalog: T1 — unified tasks (HLD-022). Covers the v0.9.0 headline:
//   - POST /v1/tasks/oneoff   → creates a one-shot task + generates a report
//     attributed to it (task_id = "oneoff:<uuid>")
//   - GET  /v1/tasks          → unified list = recurring schedules ∪ oneoff
//   - GET  /v1/tasks/{id}     → resolves both kinds (colon id, url-encoded)
//   - DELETE /v1/tasks/{id}   → removes a oneoff
//   - report-schedule run-now → report carries task_id "report-schedule:<n>"
//     even though schedule_id stays NULL (dedup-key avoidance)
package e2e

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ongridio/ongrid/tests/e2e/testenv"
)

func TestTasks_OneoffAndRecurringUnion_T1(t *testing.T) {
	env := testenv.Start(t)
	tok := env.LoginAdmin().AccessToken
	// A report needs an LLM reply; the row is created synchronously regardless,
	// so attribution assertions don't depend on generation finishing.
	env.FakeLLM().SetLLMReply("# 巡检报告\n资源水位正常。")

	// --- oneoff task: create + immediate generate -----------------------------
	status, body, err := env.DoJSON("POST", "/api/v1/tasks/oneoff", map[string]any{
		"kind":  "weekly",
		"title": "e2e-oneoff-task",
	}, tok)
	if err != nil || status != 201 {
		t.Fatalf("create oneoff: status=%d err=%v body=%v", status, err, body)
	}
	oneoffID, _ := body["id"].(string)
	if !strings.HasPrefix(oneoffID, "oneoff:") {
		t.Fatalf("oneoff id should start with oneoff:, got %q", oneoffID)
	}
	if k, _ := body["kind"].(string); k != "oneoff" {
		t.Fatalf("kind = %q, want oneoff", k)
	}

	// the generated report must be attributed to this task (task_id == oneoffID)
	if !pollReportForTask(t, env, tok, oneoffID, 20*time.Second) {
		t.Fatalf("no report attributed to oneoff task %s", oneoffID)
	}

	// --- recurring task (report schedule) appears in the union ----------------
	status, schedBody, _ := env.DoJSON("POST", "/api/v1/report-schedules", map[string]any{
		"name":     "e2e-weekly-sched",
		"kind":     "weekly",
		"timezone": "UTC",
	}, tok)
	if status != 201 {
		t.Fatalf("create schedule: status=%d body=%v", status, schedBody)
	}
	schedID := numToStr(schedBody["id"])
	recurringTaskID := "report-schedule:" + schedID

	// run-now → report attributed to the schedule's task (schedule_id stays NULL)
	status, _, _ = env.DoJSON("POST", fmt.Sprintf("/api/v1/report-schedules/%s/run-now", schedID), map[string]any{}, tok)
	if status != 200 && status != 201 && status != 202 {
		t.Fatalf("run-now: status=%d", status)
	}
	if !pollReportForTask(t, env, tok, recurringTaskID, 20*time.Second) {
		t.Fatalf("run-now report not attributed to %s", recurringTaskID)
	}

	// --- GET /v1/tasks: union has BOTH, with the right kinds ------------------
	status, listBody, _ := env.DoJSON("GET", "/api/v1/tasks", nil, tok)
	if status != 200 {
		t.Fatalf("list tasks: status=%d", status)
	}
	tasks, _ := listBody["tasks"].([]any)
	oneoff := taskByID(tasks, oneoffID)
	recurring := taskByID(tasks, recurringTaskID)
	if oneoff == nil || recurring == nil {
		t.Fatalf("union missing a task: oneoff=%v recurring=%v (got %d tasks)", oneoff != nil, recurring != nil, len(tasks))
	}
	if k, _ := oneoff["kind"].(string); k != "oneoff" {
		t.Errorf("oneoff kind = %q", k)
	}
	if k, _ := recurring["kind"].(string); k != "recurring_report" {
		t.Errorf("recurring kind = %q, want recurring_report", k)
	}

	// --- GET /v1/tasks/{id} resolves both (colon id is url-encoded) -----------
	for _, id := range []string{oneoffID, recurringTaskID} {
		status, dBody, _ := env.DoJSON("GET", "/api/v1/tasks/"+url.QueryEscape(id), nil, tok)
		if status != 200 {
			t.Fatalf("get task %s: status=%d", id, status)
		}
		if got, _ := dBody["id"].(string); got != id {
			t.Errorf("get task id = %q, want %q", got, id)
		}
	}

	// --- DELETE oneoff: gone from the union; recurring untouched --------------
	status, _, _ = env.DoJSON("DELETE", "/api/v1/tasks/"+url.QueryEscape(oneoffID), nil, tok)
	if status != 200 && status != 204 {
		t.Fatalf("delete oneoff: status=%d", status)
	}
	status, listBody, _ = env.DoJSON("GET", "/api/v1/tasks", nil, tok)
	tasks, _ = listBody["tasks"].([]any)
	if taskByID(tasks, oneoffID) != nil {
		t.Errorf("oneoff still present after delete")
	}
	if taskByID(tasks, recurringTaskID) == nil {
		t.Errorf("recurring task wrongly removed by oneoff delete")
	}
}

// pollReportForTask waits until at least one report carries task_id == taskID.
func pollReportForTask(t *testing.T, env *testenv.Env, tok, taskID string, d time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		status, body, _ := env.DoJSON("GET", "/api/v1/reports?task_id="+url.QueryEscape(taskID), nil, tok)
		if status == 200 {
			for _, it := range firstList(body, "reports", "items") {
				if m, ok := it.(map[string]any); ok {
					if tid, _ := m["task_id"].(string); tid == taskID {
						return true
					}
				}
			}
		}
		time.Sleep(400 * time.Millisecond)
	}
	return false
}

func taskByID(tasks []any, id string) map[string]any {
	for _, it := range tasks {
		if m, ok := it.(map[string]any); ok {
			if s, _ := m["id"].(string); s == id {
				return m
			}
		}
	}
	return nil
}

// firstList returns the first present []any under the given keys.
func firstList(body map[string]any, keys ...string) []any {
	for _, k := range keys {
		if v, ok := body[k].([]any); ok {
			return v
		}
	}
	return nil
}

// numToStr renders a JSON number/string id as a string.
func numToStr(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case float64:
		return fmt.Sprintf("%d", int64(n))
	default:
		return fmt.Sprintf("%v", v)
	}
}
