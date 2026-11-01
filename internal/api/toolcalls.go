package api

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/MuhammadBilal561/sentinelapi/internal/canonical"
)

// MaxRequestBodyBytes is the largest request body the tool-call endpoints
// read, whitespace included. It equals the parser's default input limit.
const MaxRequestBodyBytes = canonical.DefaultMaxInputBytes

// toolCallHandler returns the parser-only handler for one tool-call endpoint.
//
// M1 scope: it establishes the input boundary and nothing else. It checks the
// media type, bounds and reads the body, parses it strictly, validates the
// request envelope, and then answers 501 NOT_IMPLEMENTED. It never
// authenticates, authorizes, makes a decision or executes anything, and it
// does not reach any package that could.
//
// There is no authentication yet: run it only on loopback.
func toolCallHandler(kind callKind) http.HandlerFunc {
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
		if _, bad := validateEnvelope(kind, root); bad != nil {
			writeCodeField(w, r, CodeInvalidRequest, bad.Field)
			return
		}

		// 6. Structurally valid. Decisions are not implemented in M1.
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
