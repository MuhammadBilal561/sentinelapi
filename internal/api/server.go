// Package api contains the HTTP layer: routing, middleware and handlers.
// It holds no business logic.
package api

import (
	"log/slog"
	"net/http"
)

// Options configures NewHandler.
type Options struct {
	// Logger receives access and panic logs. If nil, logs are discarded.
	Logger *slog.Logger
	// Health serves /healthz and /readyz. If nil, a Health that is never
	// ready is used.
	Health *Health
}

// NewHandler returns the service's root handler with routing and middleware.
//
// Middleware order, outermost first: request ID, access log, panic recovery.
// Recovery sits inside the access log so a recovered panic is logged as a 500.
func NewHandler(opts Options) http.Handler {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	health := opts.Health
	if health == nil {
		health = NewHealth(logger)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health.Liveness)
	mux.HandleFunc("GET /readyz", health.Readiness)

	var h http.Handler = mux
	h = Recover(logger)(h)
	h = AccessLog(logger)(h)
	h = RequestID(h)
	return h
}
