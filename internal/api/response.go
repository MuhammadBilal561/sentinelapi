package api

import (
	"encoding/json"
	"net/http"
)

// errorBody is the stable error envelope used for every API error.
type errorBody struct {
	RequestID string      `json:"request_id"`
	Error     errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeJSON writes v as a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	// The status line is already sent, so an encoding failure of these small
	// fixed bodies can only be a closed connection; there is nothing to do.
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes the standard error envelope. The message must be a fixed,
// client-safe string and never contain internal details.
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, errorBody{
		RequestID: RequestIDFromContext(r.Context()),
		Error:     errorDetail{Code: code, Message: message},
	})
}
