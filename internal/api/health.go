package api

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

// readinessCheckTimeout bounds how long all dependency checks may take.
const readinessCheckTimeout = 2 * time.Second

// ReadinessCheck is the extension point for real dependency checks. Later
// milestones register checks (tool registry loaded, policy loaded, store
// reachable) and /readyz reports not ready if any check returns an error.
// A check must respect context cancellation.
type ReadinessCheck func(ctx context.Context) error

// Health serves the liveness and readiness endpoints.
//
// M0 has no dependencies, so readiness means only that startup finished and
// the process is not shutting down. It makes no claim about a registry, a
// policy engine or a database, because none of them exist yet.
type Health struct {
	ready  atomic.Bool
	checks []ReadinessCheck
	logger *slog.Logger
}

// NewHealth returns a Health that starts not ready. Call SetReady(true) once
// startup has completed. Optional checks run on every /readyz request.
func NewHealth(logger *slog.Logger, checks ...ReadinessCheck) *Health {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Health{logger: logger, checks: checks}
}

// SetReady marks the service ready or not ready. It is safe for concurrent use.
func (h *Health) SetReady(ready bool) { h.ready.Store(ready) }

type statusBody struct {
	Status string `json:"status"`
}

// Liveness reports that the process is up and able to serve a request. It
// deliberately checks nothing else.
func (h *Health) Liveness(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, statusBody{Status: "ok"})
}

// Readiness reports whether the service should receive traffic.
func (h *Health) Readiness(w http.ResponseWriter, r *http.Request) {
	if !h.ready.Load() {
		writeJSON(w, http.StatusServiceUnavailable, statusBody{Status: "not_ready"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), readinessCheckTimeout)
	defer cancel()
	for _, check := range h.checks {
		if err := check(ctx); err != nil {
			// The cause is logged for operators; the client sees only the status.
			h.logger.Warn("readiness check failed",
				"request_id", RequestIDFromContext(r.Context()),
				"error", err.Error(),
			)
			writeJSON(w, http.StatusServiceUnavailable, statusBody{Status: "not_ready"})
			return
		}
	}
	writeJSON(w, http.StatusOK, statusBody{Status: "ready"})
}
