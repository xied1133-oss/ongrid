// Package errs defines common error sentinels shared across bounded contexts.
//
// Keep this set minimal; BC-specific errors belong in each BC's biz package.
// The HTTPStatus mapping is the single source of truth for HTTP handlers.
package errs

import (
	"errors"
	"net/http"
)

var (
	ErrNotFound       = errors.New("not found")
	ErrUnauthorized   = errors.New("unauthorized")
	ErrForbidden      = errors.New("forbidden")
	ErrConflict       = errors.New("conflict")
	ErrInvalid        = errors.New("invalid argument")
	ErrTenantMismatch = errors.New("tenant mismatch")
	ErrEdgeOffline    = errors.New("edge offline")
	ErrBudgetExceeded = errors.New("budget exceeded")
	ErrNotWiredYet    = errors.New("not wired yet")
	// ErrTooManyAttempts is the 429-mapped sentinel for short-window
	// rate-limited paths (e.g. login). Distinct from ErrBudgetExceeded
	// so loggers / metrics can tell anti-bruteforce from quota throttle.
	ErrTooManyAttempts = errors.New("too many attempts")
)

// HTTPStatus maps known sentinel errors to HTTP status codes.
// Unknown errors map to 500.
func HTTPStatus(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, ErrForbidden), errors.Is(err, ErrTenantMismatch):
		return http.StatusForbidden
	case errors.Is(err, ErrConflict):
		return http.StatusConflict
	case errors.Is(err, ErrInvalid):
		return http.StatusBadRequest
	case errors.Is(err, ErrBudgetExceeded), errors.Is(err, ErrTooManyAttempts):
		return http.StatusTooManyRequests
	case errors.Is(err, ErrEdgeOffline):
		return http.StatusServiceUnavailable
	case errors.Is(err, ErrNotWiredYet):
		return http.StatusNotImplemented
	default:
		return http.StatusInternalServerError
	}
}
