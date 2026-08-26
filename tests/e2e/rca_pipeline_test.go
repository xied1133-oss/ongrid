//go:build e2e

// Catalog: F1 — RCA Investigator pipeline. Builds on E1: fire an
// incident through the alert evaluator, then assert the investigator
// usecase picks it up, calls the LLM, and persists a populated
// investigation_reports row. Flow:
//
//   - Start manager with ONGRID_ALERT_EVAL_INTERVAL=2s (E1 pattern) +
//     ONGRID_INVESTIGATOR_ENABLED=true (default OFF — gates the entire
//     investigator usecase, see cmd/ongrid/main.go:1424).
//   - Login admin, create a metric_raw global-scope rule + push a
//     matching FakeProm instant series, exactly as in E1.
//   - Override the FakeLLM canned reply BEFORE fire so the worker's
//     final answer carries our distinctive substring. The investigator
//     stores the final answer verbatim into findings_md when the
//     structured-extraction Pass-2 can't parse JSON out of it (which
//     is exactly what happens here — the fake LLM returns the same
//     plain text for the extractor call too, the JSON parse fails,
//     and the fallback path preserves finalAnswer in findings_md —
//     see report_extractor.go fallback).
//   - Poll /v1/alerts/incidents (≤45s, E1 cadence) for our rule.
//   - POST /v1/alerts/incidents/{id}/investigation to trigger a
//     ForceEnqueueWith — kills any auto-spawned worker, deletes the
//     prior row, re-enqueues fresh. Picked over relying on auto-fire
//     because (a) F1 should isolate the investigator from notification
//     timing, (b) the manual path's response shape (202 + report stub)
//     is the SPA's actual contract, and (c) auto-fire still runs in
//     parallel — both paths produce a final ready row.
//   - Poll GET /v1/alerts/incidents/{id}/investigation up to 60s for
//     status=ready. Async is real here: the run() goroutine spawns a
//     chatruntime worker, blocks on the eino ReAct loop (one LLM call
//     with our fake), then extractStructured does one more LLM call,
//     then MarkReady. With the fake LLM that's ~sub-second, but on a
//     cold mac the box can stall for tens of seconds — 60s is a safe
//     ceiling.
//   - Assert findings_md (DTO field `findings_md`) contains the
//     distinctive substring + FakeLLM CallCount() >= 1 (proves the
//     investigator actually went through the LLM path, not the
//     spawner-nil / spawn-error short-circuits).
package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/ongridio/ongrid/tests/e2e/testenv"
)

func TestRCA_InvestigationPipeline_F1(t *testing.T) {
	env := testenv.Start(t,
		testenv.WithEnv("ONGRID_ALERT_EVAL_INTERVAL", "2s"),
		testenv.WithEnv("ONGRID_ALERT_ENABLED", "true"),
		// Gates the structured-RCA investigator wiring in main.go.
		// Without this the POST endpoint returns 503 feature_disabled
		// and no auto-spawn happens.
		testenv.WithEnv("ONGRID_INVESTIGATOR_ENABLED", "true"),
	)
	pair := env.LoginAdmin()

	const (
		ruleKey   = "e2e_rca_pipeline_test"
		expr      = "fake_e2e_rca_metric > 50"
		ruleName  = "E2E RCA pipeline rule"
		llmCanned = "E2E investigation report: root cause is the test."
	)

	// Pre-stage the LLM reply BEFORE the incident can auto-fire — the
	// alert pipeline's notify hook calls InvestigateAsync on the
	// is-new transition, which spawns the worker immediately. Setting
	// the reply after the incident lands risks a race where the auto-
	// fire worker sees the default canned reply and we lose the
	// substring guarantee. Manual ForceEnqueue below will overwrite
	// the row anyway, but pre-staging keeps both paths aligned.
	env.FakeLLM().SetLLMReply(llmCanned)

	// Create a global-scope metric_raw rule. Severity warning meets
	// the investigator's default MinSeverity floor (also "warning").
	createStatus, body, err := env.DoJSON("POST", "/api/v1/alert-rules", map[string]any{
		"rule_key":   ruleKey,
		"kind":       "metric_raw",
		"name":       ruleName,
		"scope_type": "global",
		"join_mode":  "all",
		"severity":   "warning",
		"enabled":    true,
		"spec":       map[string]any{"expr": expr},
	}, pair.AccessToken)
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}
	if createStatus != 200 && createStatus != 201 {
		t.Fatalf("create rule: status=%d body=%v", createStatus, body)
	}

	// FakeProm: any instant query of `expr` returns one firing series.
	// E1 pattern — global scope means an empty label set is fine.
	env.FakeProm().SetInstant(expr, []testenv.InstantEntry{
		{Labels: map[string]string{}, Value: 95.0},
	})

	// Step 1: wait for the incident. Same 45s window as E1.
	var incidentID uint64
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		status, list, err := env.DoJSON("GET", "/api/v1/alerts/incidents", nil, pair.AccessToken)
		if err != nil {
			t.Fatalf("list incidents: %v", err)
		}
		if status != 200 {
			t.Fatalf("list incidents: status=%d body=%v", status, list)
		}
		if id, ok := findIncidentID(list, ruleKey); ok {
			incidentID = id
			break
		}
		time.Sleep(1 * time.Second)
	}
	if incidentID == 0 {
		_, list, _ := env.DoJSON("GET", "/api/v1/alerts/incidents", nil, pair.AccessToken)
		_, ruleList, _ := env.DoJSON("GET", "/api/v1/alert-rules", nil, pair.AccessToken)
		t.Logf("rule list (sanity — confirms our POST landed): %v", ruleList)
		t.Logf("manager logs filtered:\n%s", filterRelevant(env.ManagerLogs()))
		t.Fatalf("no incident with rule_key=%q within 45s; list=%v", ruleKey, list)
	}

	// Step 2: trigger investigation via the manual endpoint. The auto
	// path may also have fired (and may even have a ready row by now),
	// but ForceEnqueueWith deletes the prior row and re-enqueues, so
	// the assertions below see a deterministic single-pass result.
	postPath := "/api/v1/alerts/incidents/" + utoa(incidentID) + "/investigation"
	getPath := postPath
	postStatus, postBody, err := env.DoJSON("POST", postPath, map[string]any{}, pair.AccessToken)
	if err != nil {
		t.Fatalf("trigger investigation: %v", err)
	}
	// Per http.go:400 the handler returns 202 Accepted on success, with
	// a {status:"pending"} (or echoed row) body. 503 means the gate
	// rejected (investigator not wired) — fatal here.
	if postStatus != 202 {
		t.Logf("manager logs filtered:\n%s", filterRelevant(env.ManagerLogs()))
		t.Fatalf("trigger investigation: status=%d body=%v", postStatus, postBody)
	}

	// Step 3: poll the GET endpoint until status flips to "ready".
	// Async wait: run() is a goroutine; with the fake LLM it usually
	// completes in <1s, but cold-mac scheduling has been seen at 10s+.
	// 60s is a generous ceiling consistent with the F1 task spec.
	var got map[string]any
	rcaDeadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(rcaDeadline) {
		status, body, err := env.DoJSON("GET", getPath, nil, pair.AccessToken)
		if err != nil {
			t.Fatalf("get investigation: %v", err)
		}
		if status != 200 {
			t.Fatalf("get investigation: status=%d body=%v", status, body)
		}
		s, _ := body["status"].(string)
		switch s {
		case "ready":
			got = body
		case "failed", "skipped", "feature_disabled":
			// Terminal but not what we want — fail with the row so
			// the diagnostic is the actual failure reason from the
			// investigator (e.g. "spawner not wired", "spawn: ...").
			t.Fatalf("investigation terminated unhealthily: status=%q body=%v", s, body)
		}
		if got != nil {
			break
		}
		time.Sleep(1 * time.Second)
	}
	if got == nil {
		_, body, _ := env.DoJSON("GET", getPath, nil, pair.AccessToken)
		t.Fatalf("investigation did not reach status=ready within 60s; last=%v", body)
	}

	// Step 4: assert the canned LLM reply survived into findings_md.
	// extractStructured's fallback path (report_extractor.go ~L98)
	// stuffs finalAnswer into FindingsMD when JSON parse fails — which
	// is exactly the path we hit with a plain-text fake reply. The
	// wire DTO field is `findings_md` (see http.go InvestigationReport).
	findings, _ := got["findings_md"].(string)
	if !strings.Contains(findings, llmCanned) {
		t.Fatalf("findings_md missing canned LLM reply.\n  want substring: %q\n  got: %q",
			llmCanned, findings)
	}

	// Step 5: assert the investigator actually invoked the LLM. The
	// worker is one call, the extractor is a second; auto-fire (if it
	// also ran) adds more. >= 1 is the floor that distinguishes the
	// healthy path from a spawner-nil / spawn-error short-circuit
	// where findings_md would never be populated.
	if n := env.FakeLLM().CallCount(); n < 1 {
		t.Fatalf("FakeLLM call count = %d, want >= 1 (investigator should have called the LLM)", n)
	}
}

// findIncidentID scans the incidents list for one whose rule_key
// matches and returns its uint64 id. JSON numbers decode into
// float64 through map[string]any so we cast through that.
func findIncidentID(list map[string]any, ruleKey string) (uint64, bool) {
	items, _ := list["items"].([]any)
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m == nil {
			continue
		}
		if r, _ := m["rule_key"].(string); r != ruleKey {
			continue
		}
		idF, ok := m["id"].(float64)
		if !ok {
			continue
		}
		return uint64(idF), true
	}
	return 0, false
}

// utoa renders a uint64 as decimal — strconv.FormatUint would do but
// pulling in strconv just for this would noise up the imports.
func utoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

// filterRelevant keeps only lines mentioning RCA / chatruntime / aiops /
// investigator / provider / runtime / LLM so a 503-gate failure surfaces
// the actual wiring decision without dumping 5k lines.
func filterRelevant(s string) string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		l := strings.ToLower(ln)
		if strings.Contains(l, "aiops") ||
			strings.Contains(l, "chatruntime") ||
			strings.Contains(l, "investigator") ||
			strings.Contains(l, "provider") ||
			strings.Contains(l, "runtime") ||
			strings.Contains(l, "llm") ||
			strings.Contains(l, "rca") {
			out = append(out, ln)
		}
	}
	return strings.Join(out, "\n")
}
