// Package httpserver wraps net/http.Server with graceful shutdown honoring
// a parent context. Used by cmd/* for the API, metrics and debug listeners.
package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// Server composes *http.Server with the logger used for lifecycle events.
type Server struct {
	*http.Server
	log *slog.Logger
}

// New constructs a Server. The returned *Server is ready to be Start'd.
func New(addr string, h http.Handler, log *slog.Logger) *Server {
	return &Server{
		Server: &http.Server{
			Addr:              addr,
			Handler:           h,
			ReadHeaderTimeout: 10 * time.Second,
		},
		log: log,
	}
}

// Start blocks until either ListenAndServe returns an error or ctx is
// cancelled. On ctx cancel it triggers Shutdown with a 10s deadline.
// Returns nil on clean shutdown.
func (s *Server) Start(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		s.log.Info("http server listening", slog.String("addr", s.Addr))
		err := s.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.Shutdown(shutdownCtx); err != nil {
			s.log.Error("http server shutdown error", slog.String("addr", s.Addr), slog.Any("err", err))
			return err
		}
		s.log.Info("http server shutdown complete", slog.String("addr", s.Addr))
		return nil
	case err := <-errCh:
		return err
	}
}
