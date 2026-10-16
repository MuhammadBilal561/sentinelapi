package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// RequestIDHeader is the response header carrying the server-generated ID.
const RequestIDHeader = "X-Request-ID"

type requestIDKey struct{}

// RequestIDFromContext returns the request ID stored by the RequestID
// middleware, or an empty string if there is none.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// newRequestID returns "req_" followed by 16 random hex characters.
func newRequestID() string {
	var b [8]byte
	// crypto/rand.Read does not return an error on supported Go versions; it
	// terminates the program if the system random source fails.
	_, _ = rand.Read(b[:])
	return "req_" + hex.EncodeToString(b[:])
}

// RequestID assigns a server-generated request ID to every request, stores it
// in the request context and returns it in the X-Request-ID response header.
// An ID supplied by the client is deliberately ignored.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set(RequestIDHeader, id)
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// statusRecorder remembers the response status for logging and tells the
// panic handler whether a response has already started.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	started bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.started {
		s.status = code
		s.started = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.started {
		s.status = http.StatusOK
		s.started = true
	}
	return s.ResponseWriter.Write(b)
}

// Started reports whether the response status has been sent.
func (s *statusRecorder) Started() bool { return s.started }

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// AccessLog logs one structured line per request: request ID, method, path,
// status and duration. It never logs request bodies, headers, query strings
// or credentials.
func AccessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			logger.Info("request",
				"request_id", RequestIDFromContext(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// Recover turns a handler panic into a generic 500 response. The client gets
// no panic details. The server log gets only a generic event with the request
// ID and the panic's Go type: the panic value and stack trace are deliberately
// not logged because they can contain sensitive data. If the response has
// already started, the connection is aborted instead so the client does not
// mistake a truncated body for a complete one.

func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if rec == http.ErrAbortHandler {
					panic(rec)
				}

				// Never log the panic value or a stack trace: either can carry
				// secrets or request data. The type is enough to tell operators
				// what kind of failure happened.

				logger.Error("panic recovered",
					"request_id", RequestIDFromContext(r.Context()),
					"panic_type", fmt.Sprintf("%T", rec),
				)
				if s, ok := w.(interface{ Started() bool }); ok && s.Started() {
					panic(http.ErrAbortHandler)
				}
				writeError(w, r, http.StatusInternalServerError,
					"INTERNAL_ERROR", "An internal error occurred.")
			}()
			next.ServeHTTP(w, r)
		})
	}
}
