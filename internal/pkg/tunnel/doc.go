// Package tunnel is the edge-side cloud channel abstraction.
//
// Responsibilities: establish and maintain a multiplexed, mutually-initiated
// RPC channel from ongrid-edge up to the cloud-side broker (frontier),
// dispatch incoming reverse-call RPCs to registered handlers, and provide
// the JSON message shapes (messages.go) shared with the manager-side
// frontierbound handlers.
//
// the cloud-side listening end is no longer in this repo; the
// upstream github.com/singchia/frontier broker terminates geminio for us
// and the manager dials it via internal/manager/service/frontierbound.
// Only the edge keeps a NewClient here.
package tunnel
