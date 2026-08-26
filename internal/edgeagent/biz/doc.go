// Package biz is the edgeagent BC's run loop. It owns dialing the tunnel,
// reconnect with exponential backoff, heartbeat, periodic metric collection
// + push, and graceful shutdown.
package biz
