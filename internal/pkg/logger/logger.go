// Package logger wraps log/slog with ongrid conventions.
//
// gospec red line: structured logs only, JSON handler, never log raw user
// content (chat messages, request bodies, secrets). Injecting trace_id /
// org_id is handled at call sites via slog attributes.
package logger

import (
	"log/slog"
	"os"
)

// New returns a *slog.Logger that writes JSON lines to stderr at the given
// minimum level.
func New(level slog.Level) *slog.Logger {
	h := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	})
	return slog.New(h)
}

// WithService returns a logger decorated with a "service" attribute.
// Used by cmd/ongrid and cmd/ongrid-edge at startup.
func WithService(l *slog.Logger, name string) *slog.Logger {
	return l.With(slog.String("service", name))
}
