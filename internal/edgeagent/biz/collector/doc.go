// Package collector gathers host metrics from /proc and /sys.
// Each file exposes one Collect function returning the matching model type;
// Phase 1 returns zero values so the rest of the agent can be exercised.
package collector
