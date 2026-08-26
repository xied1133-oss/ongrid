// Package tracequery is a tiny client for Tempo's HTTP query API
// (/api/search and /api/traces/<id>). It is consumed by the manager AI
// tool registry; the response is passed back to the LLM verbatim, so we
// preserve the raw JSON shape.
//
// Backend-decoupled name: the package is `tracequery`, not `tempoquery`,
// so that swapping the backend (e.g. to VictoriaTraces; F)
// is a single import-site change rather than a rename ripple. See
// for the same convention applied to logquery.
//
// Cross-BC: lives under internal/pkg/ and has no manager/* import.
package tracequery
