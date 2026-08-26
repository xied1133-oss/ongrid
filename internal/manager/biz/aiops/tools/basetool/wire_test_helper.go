//go:build test_helpers

// Package basetool — test_helpers build tag wiring sketch for
// PR-3. This file is a guidance scaffold for downstream PRs that need
// to spin up a fully-decorated query_promql tool in tests; it is NOT
// compiled into production binaries (build tag keeps it inert).
//
// 主参考图 — the canonical chain. Downstream test
// authors copy this when they need a "give me a real tool wired with
// audit + ratelimit + metric on a fresh registry" helper, but PR-3
// itself doesn't have a caller (the closure path runs unmodified;
// individual decorator + tool tests use direct constructors).
package basetool

// (intentionally empty — see file-level doc. The wiring example lives
// alongside the migration PR that introduces the agent loop's BaseTool
// path; this file reserves the import path under the test_helpers build
// tag so future PRs can drop a builder here without churn.)
