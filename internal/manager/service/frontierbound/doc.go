// Package frontierbound is the manager-side wrapper around the upstream
// github.com/singchia/frontier service-end SDK
// (api/dataplane/v1/service.Service).
//
// Responsibilities: open a long-lived geminio service connection to the
// frontier broker, register the lifecycle callbacks frontier needs
// (GetEdgeID, EdgeOnline, EdgeOffline), and expose a small Caller surface
// (Call, Register) so manager biz code can talk to edges without learning
// geminio.Request / geminio.Response.
//
// The wire-level message names + JSON shapes live in
// internal/pkg/tunnel/messages.go and are shared with the edge agent;
// they are intentionally NOT re-declared here. Lifecycle Meta is the same
// JSON {access_key, secret_key} the edge already sends.
package frontierbound
