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
// Every route has a method-less fallback registered next to its method-specific
// pattern. The mux prefers the specific pattern, so the fallback only sees
// other methods and answers 405 in the JSON error format. The final "/"
// catch-all answers unknown paths with a JSON 404.
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
	mux.HandleFunc("/healthz", methodNotAllowed("GET, HEAD"))
	mux.HandleFunc("GET /readyz", health.Readiness)
	mux.HandleFunc("/readyz", methodNotAllowed("GET, HEAD"))

	mux.HandleFunc("POST /v1/tool-calls/evaluate", toolCallHandler(kindEvaluate))
	mux.HandleFunc("/v1/tool-calls/evaluate", methodNotAllowed("POST"))
	mux.HandleFunc("POST /v1/tool-calls/execute", toolCallHandler(kindExecute))
	mux.HandleFunc("/v1/tool-calls/execute", methodNotAllowed("POST"))

	mux.HandleFunc("/", notFound)

	var h http.Handler = mux
	h = Recover(logger)(h)
	h = AccessLog(logger)(h)
	h = RequestID(h)
	return h

}

// methodNotAllowed answers 405 with an Allow header and the JSON error format.
func methodNotAllowed(allow string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allow)
		writeCode(w, r, CodeMethodNotAllowed)
	}
}

// notFound answers 404 in the JSON error format.
func notFound(w http.ResponseWriter, r *http.Request) {
	writeCode(w, r, CodeNotFound)
}
