package api

import "net/http"

// Stable API error codes. Codes are append-only: removing or renaming one is a
// breaking change for clients, and a golden test (codes_test.go) fails if it
// happens. Adding a code requires updating testdata/error_codes.golden.
const (
	CodeInternalError        = "INTERNAL_ERROR"
	CodeInvalidJSON          = "INVALID_JSON"
	CodeInvalidUTF8          = "INVALID_UTF8"
	CodeDuplicateJSONKey     = "DUPLICATE_JSON_KEY"
	CodeJSONTooDeep          = "JSON_TOO_DEEP"
	CodeJSONLimitExceeded    = "JSON_LIMIT_EXCEEDED"
	CodeRequestTooLarge      = "REQUEST_TOO_LARGE"
	CodeUnsupportedMediaType = "UNSUPPORTED_MEDIA_TYPE"
	CodeInvalidRequest       = "INVALID_REQUEST"
	CodeToolUnknown          = "TOOL_UNKNOWN"
	CodeArgsSchemaInvalid    = "ARGS_SCHEMA_INVALID"
	CodeMethodNotAllowed     = "METHOD_NOT_ALLOWED"
	CodeNotFound             = "NOT_FOUND"
	CodeNotImplemented       = "NOT_IMPLEMENTED"
)

// errorSpec binds a code to its HTTP status and its fixed, client-safe
// message. Messages never contain request content or parser internals.
type errorSpec struct {
	Code    string
	Status  int
	Message string
}

// errorSpecs is the single source of truth for error responses.
//
// Mapping of input problems:
//   - 400 INVALID_JSON: syntax errors, empty body, trailing data.
//   - 400 INVALID_UTF8: invalid UTF-8 or invalid \u surrogate escapes.
//   - 400 DUPLICATE_JSON_KEY: an object repeats a key at any depth.
//   - 400 JSON_TOO_DEEP: nesting beyond the depth limit.
//   - 400 JSON_LIMIT_EXCEEDED: a string or array beyond its length limit.
//   - 413 REQUEST_TOO_LARGE: body beyond the byte limit.
//   - 415 UNSUPPORTED_MEDIA_TYPE: Content-Type is not application/json.
//   - 400 INVALID_REQUEST: valid JSON whose envelope is wrong.
//   - 404 TOOL_UNKNOWN: the envelope is valid but tool_name is not registered.
//   - 400 ARGS_SCHEMA_INVALID: the tool is registered but its arguments do not
//     satisfy the tool's JSON Schema.
var errorSpecs = []errorSpec{
	{CodeInternalError, http.StatusInternalServerError, "An internal error occurred."},
	{CodeInvalidJSON, http.StatusBadRequest, "The request body is not valid JSON."},
	{CodeInvalidUTF8, http.StatusBadRequest, "The request body is not valid UTF-8."},
	{CodeDuplicateJSONKey, http.StatusBadRequest, "The request body contains a duplicate object key."},
	{CodeJSONTooDeep, http.StatusBadRequest, "The request body is nested too deeply."},
	{CodeJSONLimitExceeded, http.StatusBadRequest, "The request body exceeds a JSON structure limit."},
	{CodeRequestTooLarge, http.StatusRequestEntityTooLarge, "The request body is too large."},
	{CodeUnsupportedMediaType, http.StatusUnsupportedMediaType, "Content-Type must be application/json."},
	{CodeInvalidRequest, http.StatusBadRequest, "The request is invalid."},
	{CodeToolUnknown, http.StatusNotFound, "The requested tool is not registered."},
	{CodeArgsSchemaInvalid, http.StatusBadRequest, "The tool arguments do not satisfy the tool's schema."},
	{CodeMethodNotAllowed, http.StatusMethodNotAllowed, "The method is not allowed for this endpoint."},
	{CodeNotFound, http.StatusNotFound, "The requested resource was not found."},
	{CodeNotImplemented, http.StatusNotImplemented, "Tool-call decisions are not implemented yet; this endpoint only validates the request."},
}

func errorSpecFor(code string) (errorSpec, bool) {
	for _, s := range errorSpecs {
		if s.Code == code {
			return s, true
		}
	}
	return errorSpec{}, false
}
