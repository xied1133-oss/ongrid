// Package promquery is a tiny client for Prometheus's HTTP query API
// (/api/v1/query and /api/v1/query_range). It is consumed by the manager
// AI tool registry; the response is passed back to the LLM verbatim, so we
// preserve the raw JSON shape.
//
// Cross-BC: lives under internal/pkg/ and has no manager/* import.
package promquery
