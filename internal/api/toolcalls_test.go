package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	evaluatePath = "/v1/tool-calls/evaluate"
	executePath  = "/v1/tool-calls/execute"
	validEval    = `{"tool_name":"orders.lookup","arguments":{"order_id":"ord_1042"}}`
	validExec    = `{"tool_name":"payments.refund","arguments":{"amount_cents":12000},"idempotency_key":"run7731-step4"}`
)

// esc turns "~u" into a JSON "\u" introducer so escapes stay explicit in test
// source and cannot be decoded by an editor before the test runs.
func esc(s string) string { return strings.ReplaceAll(s, "~u", `\u`) }

type result struct {
	rec  *httptest.ResponseRecorder
	body errorBody
}

func newToolHandler() http.Handler {
	h := NewHealth(nil)
	h.SetReady(true)
	return NewHandler(Options{Health: h})
}

func post(h http.Handler, path, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func postJSON(h http.Handler, path, body string) *httptest.ResponseRecorder {
	return post(h, path, "application/json", esc(body))
}

// decodeError strictly decodes the standard error envelope.
func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorBody {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	dec.DisallowUnknownFields()
	var eb errorBody
	if err := dec.Decode(&eb); err != nil {
		t.Fatalf("response is not the error envelope: %v\nbody: %s", err, rec.Body.String())
	}
	return eb
}

// expectError asserts status, code and field, and checks the invariants every
// error response must satisfy.
func expectError(t *testing.T, rec *httptest.ResponseRecorder, status int, code, field string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, status, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	eb := decodeError(t, rec)
	if eb.Error.Code != code {
		t.Fatalf("error code = %q, want %q; body: %s", eb.Error.Code, code, rec.Body.String())
	}
	if eb.Error.Field != field {
		t.Fatalf("error field = %q, want %q; body: %s", eb.Error.Field, field, rec.Body.String())
	}
	id := rec.Header().Get(RequestIDHeader)
	if !requestIDPattern.MatchString(id) || eb.RequestID != id {
		t.Fatalf("request ID header %q and body %q must match and be well-formed", id, eb.RequestID)
	}
	spec, ok := errorSpecFor(code)
	if !ok || eb.Error.Message != spec.Message || spec.Status != status {
		t.Fatalf("message/status do not match the registered spec for %s: %+v", code, eb)
	}
}

func TestValidEnvelopesReturnNotImplemented(t *testing.T) {
	h := newToolHandler()
	tests := []struct {
		name, path, body string
	}{
		{"evaluate minimal", evaluatePath, validEval},
		{"execute minimal", executePath, validExec},
		{"evaluate with optional ids", evaluatePath,
			`{"tool_name":"orders.lookup","arguments":{},"client_request_id":"run-7731.step_4","approval_id":"apr_2c7d91e0a4b85f13"}`},
		{"execute with optional ids", executePath,
			`{"tool_name":"payments.refund","arguments":{"a":[1,2,{"b":null}]},"idempotency_key":"abcdefgh","client_request_id":"x","approval_id":"apr_0123456789abcdef"}`},
		{"empty arguments object", evaluatePath, `{"tool_name":"a","arguments":{}}`},
		{"any argument value types", evaluatePath,
			`{"tool_name":"a.b","arguments":{"s":"x","n":-1.5e3,"t":true,"f":false,"z":null,"arr":[1,"2"],"obj":{"k":"v"}}}`},
		{"key order and whitespace", evaluatePath, "{ \"arguments\" : { } ,\n \"tool_name\" : \"orders.lookup\" }\n"},
		{"unicode in argument values", evaluatePath, `{"tool_name":"a","arguments":{"note":"héllo ~ud83d~ude00"}}`},
		{"idempotency key at 64", executePath,
			`{"tool_name":"a","arguments":{},"idempotency_key":"` + strings.Repeat("k", 64) + `"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := postJSON(h, tt.path, tt.body)
			expectError(t, rec, http.StatusNotImplemented, CodeNotImplemented, "")

			// M1 must never produce a decision or an execution outcome: no such
			// fields, and no decision or outcome values anywhere in the body.
			out := rec.Body.String()
			for _, forbidden := range []string{"APPROVED", "BLOCKED", "PENDING_APPROVAL", "SUCCEEDED", "NOT_ATTEMPTED"} {
				if strings.Contains(out, forbidden) {
					t.Fatalf("M1 response mentions %q: %s", forbidden, out)
				}
			}
			var generic map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &generic); err != nil {
				t.Fatalf("response is not a JSON object: %v", err)
			}
			for _, field := range []string{"decision", "execution", "reason_code", "approval", "action_fingerprint", "policy_version"} {
				if _, present := generic[field]; present {
					t.Fatalf("M1 response has a %q field: %s", field, out)
				}
			}
			if len(generic) != 2 {
				t.Fatalf("M1 response should have only request_id and error: %s", out)
			}
		})
	}
}

func TestContentTypeHandling(t *testing.T) {
	h := newToolHandler()

	accepted := []string{
		"application/json",
		"application/json; charset=utf-8",
		"application/json;charset=UTF-8",
		`application/json; charset="utf-8"`,
		"APPLICATION/JSON",
		"Application/Json; Charset=Utf-8",
	}
	for _, ct := range accepted {
		t.Run("accept "+ct, func(t *testing.T) {
			expectError(t, post(h, evaluatePath, ct, validEval), http.StatusNotImplemented, CodeNotImplemented, "")
		})
	}

	rejected := []string{
		"",
		"text/plain",
		"text/json",
		"application/x-www-form-urlencoded",
		"multipart/form-data; boundary=x",
		"application/xml",
		"application/jsonp",
		"application/json-seq",
		"application/vnd.api+json",
		"application/ld+json",
		"application/json; charset=latin1",
		"application/json; charset=utf-16",
		"application/json; charset=",
		"application/json; boundary=x",
		"application/json; version=1",
		"application/json; charset=utf-8; extra=1",
		"application/json;;",
		"application/",
		"json",
		"*/*",
		"application/json, text/plain",
		"not a media type",
	}
	for _, ct := range rejected {
		t.Run("reject "+ct, func(t *testing.T) {
			expectError(t, post(h, evaluatePath, ct, validEval), http.StatusUnsupportedMediaType, CodeUnsupportedMediaType, "")
		})
	}

	t.Run("reject on execute too", func(t *testing.T) {
		expectError(t, post(h, executePath, "text/plain", validExec), http.StatusUnsupportedMediaType, CodeUnsupportedMediaType, "")
	})

	t.Run("reject duplicate Content-Type headers", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, evaluatePath, strings.NewReader(validEval))
		req.Header.Add("Content-Type", "application/json")
		req.Header.Add("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		expectError(t, rec, http.StatusUnsupportedMediaType, CodeUnsupportedMediaType, "")
	})
}

// countingReader serves n copies of b and records how many bytes were read.
type countingReader struct {
	b    []byte
	left int // remaining bytes; negative means endless
	read int
}

func (c *countingReader) Read(p []byte) (int, error) {
	if c.left == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if c.left > 0 && n > c.left {
		n = c.left
	}
	for i := 0; i < n; i++ {
		p[i] = c.b[(c.read+i)%len(c.b)]
	}
	c.read += n
	if c.left > 0 {
		c.left -= n
	}
	return n, nil
}

func (c *countingReader) Close() error { return nil }

func TestContentTypeIsCheckedBeforeTheBodyIsRead(t *testing.T) {
	h := newToolHandler()
	body := &countingReader{b: []byte(" "), left: -1}
	req := httptest.NewRequest(http.MethodPost, evaluatePath, body)
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	expectError(t, rec, http.StatusUnsupportedMediaType, CodeUnsupportedMediaType, "")
	if body.read != 0 {
		t.Fatalf("handler read %d body bytes before checking Content-Type", body.read)
	}
}

// padded returns a valid evaluate envelope followed by spaces, exactly size
// bytes long.
func padded(size int) string {
	return validEval + strings.Repeat(" ", size-len(validEval))
}

func TestBodySizeBoundaries(t *testing.T) {
	h := newToolHandler()
	const limit = 64 << 10
	if MaxRequestBodyBytes != limit {
		t.Fatalf("MaxRequestBodyBytes = %d, want %d", MaxRequestBodyBytes, limit)
	}

	t.Run("64 KiB minus one is accepted", func(t *testing.T) {
		expectError(t, postJSON(h, evaluatePath, padded(limit-1)), http.StatusNotImplemented, CodeNotImplemented, "")
	})
	t.Run("exactly 64 KiB is accepted", func(t *testing.T) {
		body := padded(limit)
		if len(body) != limit {
			t.Fatalf("test body is %d bytes", len(body))
		}
		expectError(t, postJSON(h, evaluatePath, body), http.StatusNotImplemented, CodeNotImplemented, "")
	})
	t.Run("64 KiB plus one is rejected", func(t *testing.T) {
		expectError(t, postJSON(h, evaluatePath, padded(limit+1)), http.StatusRequestEntityTooLarge, CodeRequestTooLarge, "")
	})
	t.Run("far over the limit is rejected", func(t *testing.T) {
		expectError(t, postJSON(h, evaluatePath, padded(limit*4)), http.StatusRequestEntityTooLarge, CodeRequestTooLarge, "")
	})
	t.Run("execute endpoint has the same limit", func(t *testing.T) {
		big := validExec + strings.Repeat(" ", limit+1-len(validExec))
		expectError(t, postJSON(h, executePath, big), http.StatusRequestEntityTooLarge, CodeRequestTooLarge, "")
	})

	t.Run("oversized body of garbage is 413, not a parse error", func(t *testing.T) {
		expectError(t, postJSON(h, evaluatePath, strings.Repeat("x", limit+1)), http.StatusRequestEntityTooLarge, CodeRequestTooLarge, "")
	})
}

func TestBodyLimitDoesNotDependOnContentLength(t *testing.T) {
	h := newToolHandler()
	const limit = 64 << 10

	t.Run("unknown length (chunked) over the limit", func(t *testing.T) {
		body := &countingReader{b: []byte(" "), left: limit + 10}
		req := httptest.NewRequest(http.MethodPost, evaluatePath, body)
		req.Header.Set("Content-Type", "application/json")
		if req.ContentLength != -1 {
			t.Fatalf("test setup: ContentLength = %d, want -1", req.ContentLength)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		expectError(t, rec, http.StatusRequestEntityTooLarge, CodeRequestTooLarge, "")
	})

	t.Run("unknown length at the limit is accepted", func(t *testing.T) {
		raw := padded(limit)
		req := httptest.NewRequest(http.MethodPost, evaluatePath, io.NopCloser(strings.NewReader(raw)))
		req.Header.Set("Content-Type", "application/json")
		if req.ContentLength != -1 {
			t.Fatalf("test setup: ContentLength = %d, want -1", req.ContentLength)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		expectError(t, rec, http.StatusNotImplemented, CodeNotImplemented, "")
	})

	t.Run("Content-Length that understates the body", func(t *testing.T) {
		raw := padded(limit + 1)
		req := httptest.NewRequest(http.MethodPost, evaluatePath, strings.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.ContentLength = 100 // a lie
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		expectError(t, rec, http.StatusRequestEntityTooLarge, CodeRequestTooLarge, "")
	})
}

func TestEndlessBodyIsNeverReadUnbounded(t *testing.T) {
	h := newToolHandler()
	body := &countingReader{b: []byte(" "), left: -1}
	req := httptest.NewRequest(http.MethodPost, evaluatePath, body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	expectError(t, rec, http.StatusRequestEntityTooLarge, CodeRequestTooLarge, "")
	if body.read > MaxRequestBodyBytes+1 {
		t.Fatalf("handler consumed %d bytes of an endless body, want at most %d", body.read, MaxRequestBodyBytes+1)
	}
}

func TestDeclaredOversizeBodyIsRefusedWithoutReading(t *testing.T) {
	h := newToolHandler()
	body := &countingReader{b: []byte(" "), left: -1}
	req := httptest.NewRequest(http.MethodPost, evaluatePath, body)
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = 10 << 30 // 10 GiB declared
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	expectError(t, rec, http.StatusRequestEntityTooLarge, CodeRequestTooLarge, "")
	if body.read != 0 {
		t.Fatalf("handler read %d bytes of a body that declared itself oversized", body.read)
	}
}

func TestMalformedAndUnsafeJSONMapsToStableCodes(t *testing.T) {
	h := newToolHandler()
	// nested(n) builds an arguments value that is n objects deep, where the
	// outermost of those is the arguments object itself. The envelope is level
	// 1, so the total depth is n+1: nested(7) is exactly the limit of 8.
	nested := func(n int) string {
		return `{"tool_name":"a","arguments":` + strings.Repeat(`{"k":`, n) + `1` + strings.Repeat(`}`, n) + `}`
	}
	longString := strings.Repeat("a", 4097)
	manyItems := "[" + strings.TrimSuffix(strings.Repeat("0,", 101), ",") + "]"

	tests := []struct {
		name string
		body string
		code string
	}{
		// INVALID_JSON
		{"empty body", ``, CodeInvalidJSON},
		{"whitespace only", "  \n ", CodeInvalidJSON},
		{"not json", `hello`, CodeInvalidJSON},
		{"truncated object", `{"tool_name":"a","arguments":{`, CodeInvalidJSON},
		{"trailing comma", `{"tool_name":"a","arguments":{},}`, CodeInvalidJSON},
		{"single quotes", `{'tool_name':'a'}`, CodeInvalidJSON},
		{"trailing second object", validEval + ` {}`, CodeInvalidJSON},
		{"trailing garbage", validEval + `x`, CodeInvalidJSON},
		{"two envelopes", validEval + "\n" + validEval, CodeInvalidJSON},
		{"leading zero number", `{"tool_name":"a","arguments":{"n":01}}`, CodeInvalidJSON},
		{"NaN", `{"tool_name":"a","arguments":{"n":NaN}}`, CodeInvalidJSON},
		{"comment", `{"tool_name":"a","arguments":{}} // x`, CodeInvalidJSON},
		{"byte order mark", "\xef\xbb\xbf" + validEval, CodeInvalidJSON},
		{"raw newline in string", "{\"tool_name\":\"a\nb\",\"arguments\":{}}", CodeInvalidJSON},

		// INVALID_UTF8
		{"invalid raw utf-8", "{\"tool_name\":\"a\",\"arguments\":{\"s\":\"\xff\"}}", CodeInvalidUTF8},
		{"invalid utf-8 outside strings", "\xff" + validEval, CodeInvalidUTF8},
		{"lone surrogate escape", `{"tool_name":"a","arguments":{"s":"~ud800"}}`, CodeInvalidUTF8},
		{"lone low surrogate in key", `{"tool_name":"a","arguments":{"~udc00":1}}`, CodeInvalidUTF8},

		// DUPLICATE_JSON_KEY
		{"duplicate envelope key", `{"tool_name":"a","tool_name":"b","arguments":{}}`, CodeDuplicateJSONKey},
		{"duplicate arguments key", `{"tool_name":"a","arguments":{"x":1,"x":2}}`, CodeDuplicateJSONKey},
		{"duplicate nested key", `{"tool_name":"a","arguments":{"o":{"x":1,"x":2}}}`, CodeDuplicateJSONKey},
		{"duplicate via escape", `{"tool_name":"a","arguments":{"a":1,"~u0061":2}}`, CodeDuplicateJSONKey},
		{"duplicate envelope key via escape", `{"tool_name":"a","~u0074ool_name":"b","arguments":{}}`, CodeDuplicateJSONKey},
		{"duplicate in array element", `{"tool_name":"a","arguments":{"l":[{"x":1,"x":2}]}}`, CodeDuplicateJSONKey},

		// JSON_TOO_DEEP: the envelope is level 1, arguments level 2.
		{"nesting over the limit", nested(8), CodeJSONTooDeep},
		{"nesting far over the limit", nested(40), CodeJSONTooDeep},

		// JSON_LIMIT_EXCEEDED
		{"string over the limit", `{"tool_name":"a","arguments":{"s":"` + longString + `"}}`, CodeJSONLimitExceeded},
		{"key over the limit", `{"tool_name":"a","arguments":{"` + longString + `":1}}`, CodeJSONLimitExceeded},
		{"array over the limit", `{"tool_name":"a","arguments":{"l":` + manyItems + `}}`, CodeJSONLimitExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, path := range []string{evaluatePath, executePath} {
				expectError(t, postJSON(h, path, tt.body), http.StatusBadRequest, tt.code, "")
			}
		})
	}

	t.Run("nesting at the limit is accepted", func(t *testing.T) {
		expectError(t, postJSON(h, evaluatePath, nested(7)), http.StatusNotImplemented, CodeNotImplemented, "")
	})
	t.Run("string at the limit is accepted", func(t *testing.T) {
		body := `{"tool_name":"a","arguments":{"s":"` + strings.Repeat("a", 4096) + `"}}`
		expectError(t, postJSON(h, evaluatePath, body), http.StatusNotImplemented, CodeNotImplemented, "")
	})
	t.Run("array at the limit is accepted", func(t *testing.T) {
		body := `{"tool_name":"a","arguments":{"l":[` + strings.TrimSuffix(strings.Repeat("0,", 100), ",") + `]}}`
		expectError(t, postJSON(h, evaluatePath, body), http.StatusNotImplemented, CodeNotImplemented, "")
	})
}

func TestEnvelopeValidation(t *testing.T) {
	h := newToolHandler()
	const bad = http.StatusBadRequest
	long65 := strings.Repeat("a", 65)

	tests := []struct {
		name  string
		path  string
		body  string
		field string // "" when no single known field is at fault
	}{
		// Root shape.
		{"root array", evaluatePath, `[]`, ""},
		{"root string", evaluatePath, `"x"`, ""},
		{"root number", evaluatePath, `1`, ""},
		{"root null", evaluatePath, `null`, ""},
		{"root true", evaluatePath, `true`, ""},
		{"empty object", evaluatePath, `{}`, fieldToolName},

		// Unknown fields are rejected and their names are never echoed.
		{"unknown field", evaluatePath, `{"tool_name":"a","arguments":{},"extra":1}`, ""},
		{"agent_id is not trusted identity", evaluatePath, `{"tool_name":"a","arguments":{},"agent_id":"admin"}`, ""},
		{"roles is not trusted identity", evaluatePath, `{"tool_name":"a","arguments":{},"roles":["admin"]}`, ""},
		{"tenant attributes are not trusted", executePath, `{"tool_name":"a","arguments":{},"idempotency_key":"abcdefgh","attributes":{"tenant_id":"x"}}`, ""},
		{"wrong-case field name", evaluatePath, `{"Tool_Name":"a","arguments":{}}`, ""},
		{"unknown field beats missing field", evaluatePath, `{"agent_id":"x"}`, ""},

		// tool_name.
		{"missing tool_name", evaluatePath, `{"arguments":{}}`, fieldToolName},
		{"null tool_name", evaluatePath, `{"tool_name":null,"arguments":{}}`, fieldToolName},
		{"number tool_name", evaluatePath, `{"tool_name":7,"arguments":{}}`, fieldToolName},
		{"object tool_name", evaluatePath, `{"tool_name":{},"arguments":{}}`, fieldToolName},
		{"array tool_name", evaluatePath, `{"tool_name":["a"],"arguments":{}}`, fieldToolName},
		{"empty tool_name", evaluatePath, `{"tool_name":"","arguments":{}}`, fieldToolName},
		{"uppercase tool_name", evaluatePath, `{"tool_name":"Orders.lookup","arguments":{}}`, fieldToolName},
		{"tool_name with space", evaluatePath, `{"tool_name":"orders lookup","arguments":{}}`, fieldToolName},
		{"tool_name with dash", evaluatePath, `{"tool_name":"orders-lookup","arguments":{}}`, fieldToolName},
		{"tool_name with slash", evaluatePath, `{"tool_name":"orders/lookup","arguments":{}}`, fieldToolName},
		{"tool_name with traversal", evaluatePath, `{"tool_name":"../etc/passwd","arguments":{}}`, fieldToolName},
		{"tool_name leading dot", evaluatePath, `{"tool_name":".orders","arguments":{}}`, fieldToolName},
		{"tool_name trailing dot", evaluatePath, `{"tool_name":"orders.","arguments":{}}`, fieldToolName},
		{"tool_name empty segment", evaluatePath, `{"tool_name":"orders..lookup","arguments":{}}`, fieldToolName},
		{"tool_name segment starts with digit", evaluatePath, `{"tool_name":"orders.1lookup","arguments":{}}`, fieldToolName},
		{"tool_name starts with underscore", evaluatePath, `{"tool_name":"_orders","arguments":{}}`, fieldToolName},
		{"tool_name non-ASCII", evaluatePath, `{"tool_name":"ordérs.lookup","arguments":{}}`, fieldToolName},
		{"tool_name fullwidth letters", evaluatePath, `{"tool_name":"ｏｒｄｅｒｓ","arguments":{}}`, fieldToolName},
		{"tool_name with newline", evaluatePath, `{"tool_name":"orders\nlookup","arguments":{}}`, fieldToolName},
		{"tool_name with NUL", evaluatePath, `{"tool_name":"orders~u0000","arguments":{}}`, fieldToolName},
		{"tool_name with trailing space", evaluatePath, `{"tool_name":"orders.lookup ","arguments":{}}`, fieldToolName},
		{"tool_name over 64 characters", evaluatePath, `{"tool_name":"` + long65 + `","arguments":{}}`, fieldToolName},

		// arguments.
		{"missing arguments", evaluatePath, `{"tool_name":"a"}`, fieldArguments},
		{"null arguments", evaluatePath, `{"tool_name":"a","arguments":null}`, fieldArguments},
		{"array arguments", evaluatePath, `{"tool_name":"a","arguments":[]}`, fieldArguments},
		{"array of objects arguments", evaluatePath, `{"tool_name":"a","arguments":[{}]}`, fieldArguments},
		{"string arguments", evaluatePath, `{"tool_name":"a","arguments":"{}"}`, fieldArguments},
		{"number arguments", evaluatePath, `{"tool_name":"a","arguments":1}`, fieldArguments},
		{"boolean arguments", evaluatePath, `{"tool_name":"a","arguments":true}`, fieldArguments},

		// client_request_id.
		{"empty client_request_id", evaluatePath, `{"tool_name":"a","arguments":{},"client_request_id":""}`, fieldClientRequestID},
		{"null client_request_id", evaluatePath, `{"tool_name":"a","arguments":{},"client_request_id":null}`, fieldClientRequestID},
		{"number client_request_id", evaluatePath, `{"tool_name":"a","arguments":{},"client_request_id":5}`, fieldClientRequestID},
		{"client_request_id with space", evaluatePath, `{"tool_name":"a","arguments":{},"client_request_id":"a b"}`, fieldClientRequestID},
		{"client_request_id with slash", evaluatePath, `{"tool_name":"a","arguments":{},"client_request_id":"a/b"}`, fieldClientRequestID},
		{"client_request_id with newline", evaluatePath, `{"tool_name":"a","arguments":{},"client_request_id":"a\nb"}`, fieldClientRequestID},
		{"client_request_id non-ASCII", evaluatePath, `{"tool_name":"a","arguments":{},"client_request_id":"é"}`, fieldClientRequestID},
		{"client_request_id over 64", evaluatePath, `{"tool_name":"a","arguments":{},"client_request_id":"` + long65 + `"}`, fieldClientRequestID},

		// approval_id.
		{"malformed approval_id", evaluatePath, `{"tool_name":"a","arguments":{},"approval_id":"apr_123"}`, fieldApprovalID},
		{"approval_id without prefix", evaluatePath, `{"tool_name":"a","arguments":{},"approval_id":"2c7d91e0a4b85f13"}`, fieldApprovalID},
		{"approval_id uppercase hex", evaluatePath, `{"tool_name":"a","arguments":{},"approval_id":"apr_2C7D91E0A4B85F13"}`, fieldApprovalID},
		{"approval_id non-hex", evaluatePath, `{"tool_name":"a","arguments":{},"approval_id":"apr_zzzzzzzzzzzzzzzz"}`, fieldApprovalID},
		{"approval_id too long", evaluatePath, `{"tool_name":"a","arguments":{},"approval_id":"apr_2c7d91e0a4b85f130"}`, fieldApprovalID},
		{"null approval_id", evaluatePath, `{"tool_name":"a","arguments":{},"approval_id":null}`, fieldApprovalID},
		{"number approval_id", evaluatePath, `{"tool_name":"a","arguments":{},"approval_id":1}`, fieldApprovalID},
		{"approval_id on execute is validated too", executePath, `{"tool_name":"a","arguments":{},"idempotency_key":"abcdefgh","approval_id":"nope"}`, fieldApprovalID},

		// idempotency_key: not allowed on evaluate, required on execute.
		{"evaluate rejects idempotency_key", evaluatePath, `{"tool_name":"a","arguments":{},"idempotency_key":"abcdefgh"}`, fieldIdempotencyKey},
		{"evaluate rejects even a null idempotency_key", evaluatePath, `{"tool_name":"a","arguments":{},"idempotency_key":null}`, fieldIdempotencyKey},
		{"evaluate rejects an invalid idempotency_key", evaluatePath, `{"tool_name":"a","arguments":{},"idempotency_key":""}`, fieldIdempotencyKey},
		{"execute requires idempotency_key", executePath, `{"tool_name":"a","arguments":{}}`, fieldIdempotencyKey},
		{"execute null idempotency_key", executePath, `{"tool_name":"a","arguments":{},"idempotency_key":null}`, fieldIdempotencyKey},
		{"execute number idempotency_key", executePath, `{"tool_name":"a","arguments":{},"idempotency_key":12345678}`, fieldIdempotencyKey},
		{"execute short idempotency_key (7)", executePath, `{"tool_name":"a","arguments":{},"idempotency_key":"abcdefg"}`, fieldIdempotencyKey},
		{"execute empty idempotency_key", executePath, `{"tool_name":"a","arguments":{},"idempotency_key":""}`, fieldIdempotencyKey},
		{"execute long idempotency_key (65)", executePath, `{"tool_name":"a","arguments":{},"idempotency_key":"` + long65 + `"}`, fieldIdempotencyKey},
		{"execute idempotency_key with space", executePath, `{"tool_name":"a","arguments":{},"idempotency_key":"abcd efgh"}`, fieldIdempotencyKey},
		{"execute idempotency_key with slash", executePath, `{"tool_name":"a","arguments":{},"idempotency_key":"abcd/efgh"}`, fieldIdempotencyKey},
		{"execute idempotency_key non-ASCII", executePath, `{"tool_name":"a","arguments":{},"idempotency_key":"abcdefgé"}`, fieldIdempotencyKey},

		// Check order: tool_name is reported before later problems.
		{"first failing field wins", executePath, `{"tool_name":"BAD","arguments":1,"idempotency_key":"x"}`, fieldToolName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectError(t, postJSON(h, tt.path, tt.body), bad, CodeInvalidRequest, tt.field)
		})
	}
}

func TestEnvelopeErrorsNeverEchoRequestContent(t *testing.T) {
	const canary = "CANARY-8841-secret"
	h := newToolHandler()
	bodies := []string{
		`{"tool_name":"a","arguments":{},"` + canary + `":1}`,                              // unknown field name
		`{"tool_name":"` + canary + `","arguments":{}}`,                                    // invalid tool name
		`{"tool_name":"a","arguments":"` + canary + `"}`,                                   // wrong type
		`{"tool_name":"a","arguments":{},"client_request_id":"` + canary + `~u0020"}`,      // invalid id
		`{"tool_name":"a","arguments":{"` + canary + `":1,"` + canary + `":2}}`,            // duplicate key
		`{"tool_name":"a","arguments":{"s":"` + canary + `"`,                               // truncated
		`{"tool_name":"a","arguments":{"s":"` + canary + `~ud800"}}`,                       // bad escape
		`{"tool_name":"a","arguments":{"s":"` + canary + strings.Repeat("x", 5000) + `"}}`, // too long
	}
	for i, body := range bodies {
		rec := postJSON(h, evaluatePath, body)
		if rec.Code < 400 {
			t.Fatalf("case %d: expected an error status, got %d", i, rec.Code)
		}
		if strings.Contains(rec.Body.String(), canary) {
			t.Errorf("case %d: response echoes request content: %s", i, rec.Body.String())
		}
		for _, k := range []string{"canonical:", "json:", "offset", "character", "invalid character", "goroutine", ".go"} {
			if strings.Contains(rec.Body.String(), k) {
				t.Errorf("case %d: response leaks internals (%q): %s", i, k, rec.Body.String())
			}
		}
	}
}

func TestNotFoundAndMethodNotAllowedUseTheJSONErrorFormat(t *testing.T) {
	h := newToolHandler()
	do := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec
	}

	notFound := []string{"/", "/nope", "/v1", "/v1/", "/v1/tool-calls", "/v1/tool-calls/", "/v1/tool-calls/other", "/healthz/extra", "/admin"}
	for _, p := range notFound {
		expectError(t, do(http.MethodGet, p), http.StatusNotFound, CodeNotFound, "")
	}

	routes := map[string]string{
		evaluatePath: "POST", executePath: "POST", "/healthz": "GET, HEAD", "/readyz": "GET, HEAD",
	}
	for path, allow := range routes {
		for _, method := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch} {
			rec := do(method, path)
			expectError(t, rec, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "")
			if got := rec.Header().Get("Allow"); got != allow {
				t.Errorf("%s %s: Allow = %q, want %q", method, path, got, allow)
			}
		}
	}
	for _, path := range []string{evaluatePath, executePath} {
		expectError(t, do(http.MethodGet, path), http.StatusMethodNotAllowed, CodeMethodNotAllowed, "")
	}
	for _, path := range []string{"/healthz", "/readyz"} {
		expectError(t, do(http.MethodPost, path), http.StatusMethodNotAllowed, CodeMethodNotAllowed, "")
	}
}

func TestHealthEndpointsAreUnchangedByM1(t *testing.T) {
	h := newToolHandler()
	for _, tc := range []struct{ path, want string }{
		{"/healthz", `{"status":"ok"}`},
		{"/readyz", `{"status":"ready"}`},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != tc.want {
			t.Errorf("GET %s = %d %q, want 200 %q", tc.path, rec.Code, rec.Body.String(), tc.want)
		}
		if !requestIDPattern.MatchString(rec.Header().Get(RequestIDHeader)) {
			t.Errorf("GET %s lacks a request ID", tc.path)
		}
	}
	// HEAD on health endpoints still works through the GET pattern.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("HEAD /healthz = %d, want 200", rec.Code)
	}
}

func TestRequestIDIsServerGeneratedOnToolCallErrors(t *testing.T) {
	h := newToolHandler()
	seen := map[string]bool{}
	for i := 0; i < 30; i++ {
		req := httptest.NewRequest(http.MethodPost, evaluatePath, strings.NewReader(`{`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(RequestIDHeader, "client-chosen-id")
		req.Header.Set("X-Request-Id", "client-chosen-id")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		id := rec.Header().Get(RequestIDHeader)
		if !requestIDPattern.MatchString(id) || seen[id] {
			t.Fatalf("request ID %q is malformed or repeated", id)
		}
		seen[id] = true
		if decodeError(t, rec).RequestID != id {
			t.Fatalf("body request_id does not match the header")
		}
	}
}

func TestToolCallLogsContainNoRequestData(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	health := NewHealth(logger)
	health.SetReady(true)
	h := NewHandler(Options{Logger: logger, Health: health})

	req := httptest.NewRequest(http.MethodPost, evaluatePath+"?token=QUERYCANARY",
		strings.NewReader(`{"tool_name":"a","arguments":{"secret":"BODYCANARY"},"agent_id":"AGENTCANARY"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer HEADERCANARY")
	h.ServeHTTP(httptest.NewRecorder(), req)

	out := logs.String()
	for _, canary := range []string{"QUERYCANARY", "BODYCANARY", "AGENTCANARY", "HEADERCANARY"} {
		if strings.Contains(out, canary) {
			t.Errorf("log leaked %s: %s", canary, out)
		}
	}
	for _, want := range []string{`"path":"/v1/tool-calls/evaluate"`, `"status":400`, `"method":"POST"`} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %s: %s", want, out)
		}
	}
}

func TestServerKeepsServingAfterRejectedRequests(t *testing.T) {
	srv := httptest.NewServer(newToolHandler())
	defer srv.Close()
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

	send := func(body, contentType string) int {
		req, err := http.NewRequest(http.MethodPost, srv.URL+evaluatePath, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", contentType)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		if err := resp.Body.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		return resp.StatusCode
	}

	oversize := validEval + strings.Repeat(" ", MaxRequestBodyBytes)
	for _, tc := range []struct {
		body, ct string
		want     int
	}{
		{oversize, "application/json", http.StatusRequestEntityTooLarge},
		{validEval, "application/json", http.StatusNotImplemented},
		{`{`, "application/json", http.StatusBadRequest},
		{validEval, "text/plain", http.StatusUnsupportedMediaType},
		{validEval, "application/json", http.StatusNotImplemented},
	} {
		if got := send(tc.body, tc.ct); got != tc.want {
			t.Fatalf("status = %d, want %d", got, tc.want)
		}
	}
}

func TestValidatorHelpers(t *testing.T) {
	validNames := []string{"a", "orders.lookup", "payments.refund", "a1", "a_b", "a.b.c", "a1_b2.c3_d4", strings.Repeat("a", 64)}
	for _, s := range validNames {
		if !validToolName(s) {
			t.Errorf("validToolName(%q) = false, want true", s)
		}
	}
	invalidNames := []string{"", "A", "1a", "_a", ".a", "a.", "a..b", "a-b", "a b", "a.B", "a.1", "é", strings.Repeat("a", 65), "a\x00"}
	for _, s := range invalidNames {
		if validToolName(s) {
			t.Errorf("validToolName(%q) = true, want false", s)
		}
	}

	if !validSafeID("Az09._-", 1, 64) || validSafeID("a b", 1, 64) || validSafeID("", 1, 64) || validSafeID("abc", 4, 64) || validSafeID("abcde", 1, 4) {
		t.Error("validSafeID rules are wrong")
	}
	if !validApprovalID("apr_0123456789abcdef") || validApprovalID("apr_0123456789ABCDEF") || validApprovalID("apr_0123") || validApprovalID("xyz_0123456789abcdef") {
		t.Error("validApprovalID rules are wrong")
	}
}

func TestValidateEnvelopeReturnsParsedFields(t *testing.T) {
	root := map[string]any{
		"tool_name":         "payments.refund",
		"arguments":         map[string]any{"a": "b"},
		"client_request_id": "cid-1",
		"approval_id":       "apr_0123456789abcdef",
		"idempotency_key":   "idem-key-1",
	}
	env, bad := validateEnvelope(kindExecute, root)
	if bad != nil {
		t.Fatalf("unexpected error %+v", bad)
	}
	if env.ToolName != "payments.refund" || env.ClientRequestID != "cid-1" || env.ApprovalID != "apr_0123456789abcdef" ||
		env.IdempotencyKey != "idem-key-1" || env.Arguments["a"] != "b" {
		t.Fatalf("unexpected envelope %+v", env)
	}
}
