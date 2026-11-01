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
	// Field names the request-envelope field at fault. It is only ever one of
	// the fixed envelope field names, never text taken from the request.
	Field string `json:"field,omitempty"`
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

// writeCode writes the standard error envelope for a registered error code.
// The status and message come from errorSpecs, so a response can never carry
// request content or internal detail.
func writeCode(w http.ResponseWriter, r *http.Request, code string) {
	writeCodeField(w, r, code, "")
}

// writeCodeField is writeCode with an optional envelope field name.
func writeCodeField(w http.ResponseWriter, r *http.Request, code, field string) {
	spec, ok := errorSpecFor(code)
	if !ok {
		// Unreachable for registered codes (a test enforces it); fail safe.
		spec, _ = errorSpecFor(CodeInternalError)
		field = ""
	}
	writeJSON(w, spec.Status, errorBody{
		RequestID: RequestIDFromContext(r.Context()),
		Error:     errorDetail{Code: spec.Code, Message: spec.Message, Field: field},
	})
}
