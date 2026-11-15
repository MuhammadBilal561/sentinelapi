package api

import (
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	"github.com/MuhammadBilal561/sentinelapi/internal/canonical"
	"github.com/MuhammadBilal561/sentinelapi/internal/registry"
)

// MaxRequestBodyBytes is the largest request body the tool-call endpoints
// read, whitespace included. It equals the parser's default input limit.
const MaxRequestBodyBytes = canonical.DefaultMaxInputBytes

// toolCallHandler returns the handler for one tool-call endpoint.
//
// Scope (M2): it establishes the input boundary and checks requests against
// the tool registry, and nothing else. It checks the media type, bounds and
// reads the body, parses it strictly, validates the request envelope, looks up
// the tool, validates the arguments against the tool's JSON Schema, and then
// answers 501 NOT_IMPLEMENTED. It never authenticates, authorizes, makes a
// decision or executes anything, and it does not reach any package that could.
//
// A nil registry behaves like an empty one: every well-formed request names an
// unknown tool. The handler fails closed rather than skipping validation.
//
// There is no authentication yet: run it only on loopback.
func toolCallHandler(kind callKind, reg *registry.Registry, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1. Media type, before a single body byte is read.
		if !isJSONContentType(r.Header.Values("Content-Type")) {
			writeCode(w, r, CodeUnsupportedMediaType)
			return
		}

		// 2. Refuse a body that declares itself too large without reading it.
		if r.ContentLength > MaxRequestBodyBytes {
			writeCode(w, r, CodeRequestTooLarge)
			return
		}

		// 3. Read at most MaxRequestBodyBytes. MaxBytesReader fails the read
		// as soon as the limit is crossed, which also covers chunked bodies
		// and bodies that lie about their Content-Length.
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRequestBodyBytes))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeCode(w, r, CodeRequestTooLarge)
				return
			}
			writeCode(w, r, CodeInvalidRequest) // the client went away or timed out mid-body
			return
		}

		// 4. Strict parse under the default limits.
		root, err := canonical.ParseWithLimits(body, canonical.DefaultLimits())
		if err != nil {
			writeCode(w, r, codeForParseError(err))
			return
		}

		// 5. Envelope shape.
		env, bad := validateEnvelope(kind, root)
		if bad != nil {
			writeCodeField(w, r, CodeInvalidRequest, bad.Field)
			return
		}

		// 6. The tool must be registered. The name has already passed the
		// envelope's strict ASCII rule.
		if !reg.Has(env.ToolName) {
			writeCodeField(w, r, CodeToolUnknown, fieldToolName)
			return
		}

		// 7. The arguments must satisfy the tool's JSON Schema. The error is
		// opaque on purpose: nothing about the schema or the argument values
		// goes into the response or the log.
		if err := reg.ValidateArguments(env.ToolName, env.Arguments); err != nil {
			switch {
			case errors.Is(err, registry.ErrInvalidArguments):
				writeCodeField(w, r, CodeArgsSchemaInvalid, fieldArguments)
			case errors.Is(err, registry.ErrUnknownTool):
				writeCodeField(w, r, CodeToolUnknown, fieldToolName)
			default:
				logger.Error("tool argument validation could not be performed",
					"request_id", RequestIDFromContext(r.Context()), "error", err.Error())
				writeCode(w, r, CodeInternalError)
			}
			return
		}

		// 8. Structurally valid and schema valid. Decisions are not implemented
		// yet (M3), so nothing is approved, blocked or executed.
		writeCode(w, r, CodeNotImplemented)
	}
}

// isJSONContentType reports whether the Content-Type header values describe
// application/json. Exactly one header is accepted. The only permitted
// parameter is charset, and only utf-8.
func isJSONContentType(values []string) bool {
	if len(values) != 1 {
		return false
	}
	mediaType, params, err := mime.ParseMediaType(values[0])
	if err != nil || mediaType != "application/json" {
		return false
	}
	for name, value := range params {
		if name != "charset" || !strings.EqualFold(value, "utf-8") {
			return false
		}
	}
	return true
}
