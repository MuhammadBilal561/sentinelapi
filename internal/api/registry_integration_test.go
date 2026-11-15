package api

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	refundArgs = `{"tenant_id":"t_acme","order_id":"ord_1042","amount_cents":12000,"reason":"duplicate charge"}`
	lookupArgs = `{"tenant_id":"t_acme","order_id":"ord_1042"}`
	closeArgs  = `{"tenant_id":"t_acme","account_id":"acct_77","reason":"customer request"}`
)

// call builds a request body for tool with arguments, adding an idempotency
// key for the execute endpoint.
func call(path, tool, arguments string) string {
	body := `{"tool_name":"` + tool + `","arguments":` + arguments
	if path == executePath {
		body += `,"idempotency_key":"key-0001-abcd"`
	}
	return body + `}`
}

var bothEndpoints = []string{evaluatePath, executePath}

// ---------------------------------------------------------------------------
// The three outcomes after envelope validation

func TestKnownToolWithValidArgumentsIsNotImplemented(t *testing.T) {
	h := newToolHandler()
	tools := map[string]string{
		"orders.lookup":   lookupArgs,
		"payments.refund": refundArgs,
		"accounts.close":  closeArgs,
	}
	for _, path := range bothEndpoints {
		for tool, a := range tools {
			t.Run(path+" "+tool, func(t *testing.T) {
				rec := postJSON(h, path, call(path, tool, a))
				expectError(t, rec, http.StatusNotImplemented, CodeNotImplemented, "")

				// Registered and schema-valid is still not approved and not run:
				// no outcome value anywhere, and the body is exactly the error
				// envelope (decodeError rejects any extra member).
				out := rec.Body.String()
				for _, forbidden := range []string{"APPROVED", "BLOCKED", "PENDING_APPROVAL", "SUCCEEDED", "NOT_ATTEMPTED"} {
					if strings.Contains(out, forbidden) {
						t.Fatalf("the response mentions %q: %s", forbidden, out)
					}
				}
				decodeError(t, rec)
			})
		}
	}
}

func TestHighRiskToolIsStillNotApprovedWithoutAnyPolicy(t *testing.T) {
	// accounts.close is registered although no authorization exists. The
	// registry says it exists; it does not say anyone may call it.
	h := newToolHandler()
	for _, path := range bothEndpoints {
		rec := postJSON(h, path, call(path, "accounts.close", closeArgs))
		expectError(t, rec, http.StatusNotImplemented, CodeNotImplemented, "")
	}
}

func TestUnknownToolReturns404(t *testing.T) {
	h := newToolHandler()
	for _, path := range bothEndpoints {
		for _, tool := range []string{"orders.refund", "orders", "orders.lookup.v2", "payments", "x", "a.b.c", "accounts.delete"} {
			t.Run(path+" "+tool, func(t *testing.T) {
				rec := postJSON(h, path, call(path, tool, lookupArgs))
				expectError(t, rec, http.StatusNotFound, CodeToolUnknown, "tool_name")
			})
		}
	}
}

func TestMalformedToolNamesAreEnvelopeErrorsNotUnknownTools(t *testing.T) {
	// A name outside the naming rule never reaches the registry lookup.
	h := newToolHandler()
	for _, tool := range []string{"Orders.lookup", "orders.Lookup", "orders-lookup", "orders..lookup", ".orders", "orders.", "", " orders.lookup"} {
		t.Run(fmt.Sprintf("%q", tool), func(t *testing.T) {
			rec := postJSON(h, evaluatePath, call(evaluatePath, tool, lookupArgs))
			expectError(t, rec, http.StatusBadRequest, CodeInvalidRequest, "tool_name")
		})
	}
}

func TestInvalidArgumentsReturn400(t *testing.T) {
	h := newToolHandler()
	tests := []struct {
		name, tool, args string
	}{
		{"missing tenant", "orders.lookup", `{"order_id":"ord_1042"}`},
		{"missing order", "orders.lookup", `{"tenant_id":"t_acme"}`},
		{"empty arguments", "orders.lookup", `{}`},
		{"extra argument", "orders.lookup", `{"tenant_id":"t_acme","order_id":"ord_1042","extra":true}`},
		{"identity-looking argument", "orders.lookup", `{"tenant_id":"t_acme","order_id":"ord_1042","agent_id":"admin"}`},
		{"wrong type for tenant", "orders.lookup", `{"tenant_id":7,"order_id":"ord_1042"}`},
		{"null for order", "orders.lookup", `{"tenant_id":"t_acme","order_id":null}`},
		{"tenant pattern", "orders.lookup", `{"tenant_id":"acme","order_id":"ord_1042"}`},
		{"order pattern", "orders.lookup", `{"tenant_id":"t_acme","order_id":"1042"}`},
		{"trailing newline in identifier", "orders.lookup", `{"tenant_id":"t_acme\n","order_id":"ord_1042"}`},
		{"amount zero", "payments.refund", `{"tenant_id":"t_acme","order_id":"ord_1","amount_cents":0,"reason":"x"}`},
		{"amount over maximum", "payments.refund", `{"tenant_id":"t_acme","order_id":"ord_1","amount_cents":1000001,"reason":"x"}`},
		{"amount fraction", "payments.refund", `{"tenant_id":"t_acme","order_id":"ord_1","amount_cents":10.5,"reason":"x"}`},
		{"amount as string", "payments.refund", `{"tenant_id":"t_acme","order_id":"ord_1","amount_cents":"10","reason":"x"}`},
		{"amount beyond float64 precision", "payments.refund", `{"tenant_id":"t_acme","order_id":"ord_1","amount_cents":99999999999999999999,"reason":"x"}`},
		{"reason too long", "payments.refund", `{"tenant_id":"t_acme","order_id":"ord_1","amount_cents":5,"reason":"` + strings.Repeat("r", 201) + `"}`},
		{"missing reason", "payments.refund", `{"tenant_id":"t_acme","order_id":"ord_1","amount_cents":5}`},
		{"empty close reason", "accounts.close", `{"tenant_id":"t_acme","account_id":"acct_1","reason":""}`},
		{"close account pattern", "accounts.close", `{"tenant_id":"t_acme","account_id":"1","reason":"x"}`},
		{"arguments for another tool", "accounts.close", lookupArgs},
		{"nested object where a string belongs", "orders.lookup", `{"tenant_id":{"v":"t_acme"},"order_id":"ord_1"}`},
	}
	for _, path := range bothEndpoints {
		for _, tt := range tests {
			t.Run(path+" "+tt.name, func(t *testing.T) {
				rec := postJSON(h, path, call(path, tt.tool, tt.args))
				expectError(t, rec, http.StatusBadRequest, CodeArgsSchemaInvalid, "arguments")
			})
		}
	}
}

func TestBoundaryAmountsOverHTTP(t *testing.T) {
	h := newToolHandler()
	body := func(amount string) string {
		return call(evaluatePath, "payments.refund",
			`{"tenant_id":"t_acme","order_id":"ord_1","amount_cents":`+amount+`,"reason":"x"}`)
	}
	for amount, status := range map[string]int{
		"1": http.StatusNotImplemented, "1000000": http.StatusNotImplemented, "1000000.0": http.StatusNotImplemented,
		"0": http.StatusBadRequest, "1000001": http.StatusBadRequest, "-5": http.StatusBadRequest,
	} {
		rec := postJSON(h, evaluatePath, body(amount))
		if rec.Code != status {
			t.Errorf("amount %s: status = %d, want %d: %s", amount, rec.Code, status, rec.Body.String())
		}
	}
}

func TestExtremeNumbersAreClientErrorsNotServerErrors(t *testing.T) {
	// "1e1000001" used to make the schema library dereference a nil pointer,
	// which the recovery middleware would have turned into a 500.
	h := newToolHandler()
	for _, amount := range []string{"1e1000001", "1e999999", "1e999999999", "-1e999999999", "1e-999999999", strings.Repeat("9", 300)} {
		start := time.Now()
		rec := postJSON(h, evaluatePath, call(evaluatePath, "payments.refund",
			`{"tenant_id":"t_acme","order_id":"ord_1","amount_cents":`+amount+`,"reason":"x"}`))
		expectError(t, rec, http.StatusBadRequest, CodeArgsSchemaInvalid, "arguments")
		if d := time.Since(start); d > time.Second {
			t.Errorf("amount %.12s...: took %v", amount, d)
		}
	}
}

// ---------------------------------------------------------------------------
// Ordering of the checks

func TestChecksRunInOrderEnvelopeThenToolThenArguments(t *testing.T) {
	h := newToolHandler()

	// An unknown tool with invalid arguments is an unknown tool.
	rec := postJSON(h, evaluatePath, `{"tool_name":"orders.refund","arguments":{"bad":1}}`)
	expectError(t, rec, http.StatusNotFound, CodeToolUnknown, "tool_name")

	// A bad envelope wins over an unknown tool.
	rec = postJSON(h, executePath, `{"tool_name":"orders.refund","arguments":{}}`) // missing idempotency_key
	expectError(t, rec, http.StatusBadRequest, CodeInvalidRequest, "idempotency_key")
	rec = postJSON(h, evaluatePath, `{"tool_name":"orders.refund","arguments":{},"agent_id":"x"}`)
	expectError(t, rec, http.StatusBadRequest, CodeInvalidRequest, "")

	// A parse error wins over everything.
	rec = postJSON(h, evaluatePath, `{"tool_name":"orders.lookup","arguments":{"order_id":"a","order_id":"b"}}`)
	expectError(t, rec, http.StatusBadRequest, CodeDuplicateJSONKey, "")

	// Arguments must be an object: the envelope says so before any schema runs.
	rec = postJSON(h, evaluatePath, `{"tool_name":"orders.lookup","arguments":[]}`)
	expectError(t, rec, http.StatusBadRequest, CodeInvalidRequest, "arguments")
}

func TestEvaluateRejectsAndExecuteRequiresIdempotencyKeyForRegisteredTools(t *testing.T) {
	h := newToolHandler()

	// evaluate: not allowed, even with a valid tool and valid arguments.
	rec := postJSON(h, evaluatePath, `{"tool_name":"orders.lookup","arguments":`+lookupArgs+`,"idempotency_key":"key-0001-abcd"}`)
	expectError(t, rec, http.StatusBadRequest, CodeInvalidRequest, "idempotency_key")

	// execute: required.
	rec = postJSON(h, executePath, `{"tool_name":"orders.lookup","arguments":`+lookupArgs+`}`)
	expectError(t, rec, http.StatusBadRequest, CodeInvalidRequest, "idempotency_key")

	// Present and valid: schema validation then decides.
	rec = postJSON(h, executePath, `{"tool_name":"orders.lookup","arguments":`+lookupArgs+`,"idempotency_key":"key-0001-abcd"}`)
	expectError(t, rec, http.StatusNotImplemented, CodeNotImplemented, "")
	rec = postJSON(h, executePath, `{"tool_name":"orders.lookup","arguments":{},"idempotency_key":"key-0001-abcd"}`)
	expectError(t, rec, http.StatusBadRequest, CodeArgsSchemaInvalid, "arguments")
}

func TestEvaluateAndExecuteValidateSchemasIdentically(t *testing.T) {
	h := newToolHandler()
	cases := []struct {
		tool, args string
		status     int
	}{
		{"orders.lookup", lookupArgs, http.StatusNotImplemented},
		{"orders.lookup", `{"tenant_id":"t_acme"}`, http.StatusBadRequest},
		{"payments.refund", refundArgs, http.StatusNotImplemented},
		{"payments.refund", `{"tenant_id":"t_acme","order_id":"ord_1","amount_cents":2000000,"reason":"x"}`, http.StatusBadRequest},
		{"nope.nope", `{}`, http.StatusNotFound},
	}
	for _, c := range cases {
		e := postJSON(h, evaluatePath, call(evaluatePath, c.tool, c.args))
		x := postJSON(h, executePath, call(executePath, c.tool, c.args))
		if e.Code != c.status || x.Code != c.status {
			t.Errorf("%s %s: evaluate=%d execute=%d, want both %d", c.tool, c.args, e.Code, x.Code, c.status)
		}
		if decodeError(t, e).Error.Code != decodeError(t, x).Error.Code {
			t.Errorf("%s %s: evaluate and execute disagree on the code", c.tool, c.args)
		}
	}
}

// ---------------------------------------------------------------------------
// Fail closed without a registry

func TestHandlerWithoutRegistryKnowsNoTools(t *testing.T) {
	health := NewHealth(nil)
	health.SetReady(true)
	h := NewHandler(Options{Health: health}) // Registry deliberately nil
	for _, path := range bothEndpoints {
		rec := postJSON(h, path, call(path, "orders.lookup", lookupArgs))
		expectError(t, rec, http.StatusNotFound, CodeToolUnknown, "tool_name")
	}
	// Request-boundary errors are unaffected.
	rec := postJSON(h, evaluatePath, `{`)
	expectError(t, rec, http.StatusBadRequest, CodeInvalidJSON, "")
}

// ---------------------------------------------------------------------------
// No leakage

func TestRegistryErrorsLeakNothing(t *testing.T) {
	const canary = "CANARY-5521-secret"
	var logs bytes.Buffer
	health := NewHealth(nil)
	health.SetReady(true)
	h := NewHandler(Options{
		Health:   health,
		Registry: testRegistry(),
		Logger:   slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})

	requests := []struct {
		path, body string
		status     int
	}{
		{evaluatePath, `{"tool_name":"orders.lookup","arguments":{"tenant_id":"` + canary + `","order_id":"ord_1"}}`, 400},
		{evaluatePath, `{"tool_name":"orders.lookup","arguments":{"tenant_id":"t_a","order_id":"ord_1","` + canary + `":"` + canary + `"}}`, 400},
		{executePath, `{"tool_name":"payments.refund","arguments":{"tenant_id":"t_a","order_id":"ord_1","amount_cents":"` + canary + `","reason":"x"},"idempotency_key":"key-0001-abcd"}`, 400},
		{evaluatePath, `{"tool_name":"` + strings.ToLower(strings.ReplaceAll(canary, "-", "")) + `","arguments":{"v":"` + canary + `"}}`, 404},
	}
	for _, rq := range requests {
		rec := postJSON(h, rq.path, rq.body)
		if rec.Code != rq.status {
			t.Fatalf("status = %d, want %d: %s", rec.Code, rq.status, rec.Body.String())
		}
		body := rec.Body.String()
		for _, leak := range []string{
			canary, strings.ToLower(strings.ReplaceAll(canary, "-", "")),
			"pattern", "minimum", "maximum", "maxLength", "additionalProperties", "required", "properties",
			"$ref", "$defs", "schema", "^t_", "[a-z0-9]", "ord_", "t_a", "jsonschema",
			// The response must not enumerate the registry either.
			"orders.lookup", "payments.refund", "accounts.close", "mock.",
		} {
			if strings.Contains(body, leak) && !(leak == "schema" && strings.Contains(body, "tool's schema")) {
				t.Errorf("response leaks %q: %s", leak, body)
			}
		}
		// The response is exactly the standard envelope: no extra members.
		decodeError(t, rec)
	}

	logged := logs.String()
	for _, leak := range []string{canary, strings.ToLower(strings.ReplaceAll(canary, "-", "")), "ord_1", "t_a", "pattern", "additionalProperties", "payments.refund", "orders.lookup"} {
		if strings.Contains(logged, leak) {
			t.Errorf("log output leaks %q:\n%s", leak, logged)
		}
	}
}

func TestUnknownToolResponseIsIdenticalForEveryUnknownName(t *testing.T) {
	// The response must not depend on the name asked for, so it cannot be used
	// to learn the registry's naming scheme beyond whether the tool exists.
	h := newToolHandler()
	var first errorBody
	for i, tool := range []string{"zzz.one", "orders.lookups", "payments.refunds", "a.b.c.d.e"} {
		rec := postJSON(h, evaluatePath, call(evaluatePath, tool, `{}`))
		eb := decodeError(t, rec)
		eb.RequestID = ""
		if i == 0 {
			first = eb
		} else if eb != first {
			t.Errorf("response for %s differs: %+v vs %+v", tool, eb, first)
		}
	}
}

// ---------------------------------------------------------------------------
// Request IDs, content negotiation and M0/M1 behavior alongside a registry

func TestEveryRegistryResponseCarriesARequestID(t *testing.T) {
	h := newToolHandler()
	bodies := []string{
		call(evaluatePath, "orders.lookup", lookupArgs),  // 501
		call(evaluatePath, "orders.refund", lookupArgs),  // 404
		call(evaluatePath, "orders.lookup", `{}`),        // 400 schema
		`{"tool_name":"orders.lookup"}`,                  // 400 envelope
		`{"tool_name":"orders.lookup","arguments":{`,     // 400 syntax
		`{"tool_name":"orders.lookup","arguments":{}} x`, // 400 trailing
	}
	seen := map[string]bool{}
	for _, b := range bodies {
		rec := postJSON(h, evaluatePath, b)
		id := rec.Header().Get(RequestIDHeader)
		eb := decodeError(t, rec)
		if !requestIDPattern.MatchString(id) || eb.RequestID != id {
			t.Errorf("body %s: request ID header %q, body %q", b, id, eb.RequestID)
		}
		if seen[id] {
			t.Errorf("request ID %q was reused", id)
		}
		seen[id] = true
	}
}

func TestM1BoundariesStillApplyBeforeTheRegistry(t *testing.T) {
	h := newToolHandler()
	rec := post(h, evaluatePath, "text/plain", validEval)
	expectError(t, rec, http.StatusUnsupportedMediaType, CodeUnsupportedMediaType, "")
	rec = post(h, evaluatePath, "application/json; charset=utf-8", validEval)
	expectError(t, rec, http.StatusNotImplemented, CodeNotImplemented, "")
	rec = post(h, evaluatePath, "application/json", strings.Repeat(" ", MaxRequestBodyBytes)+"x")
	expectError(t, rec, http.StatusRequestEntityTooLarge, CodeRequestTooLarge, "")
}

func TestHealthEndpointsUnchangedWithARegistry(t *testing.T) {
	notReady := NewHealth(nil)
	h := NewHandler(Options{Health: notReady, Registry: testRegistry()})

	if rec := do(h, http.MethodGet, "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("GET /healthz = %d, want 200", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET /readyz before ready = %d, want 503", rec.Code)
	}
	notReady.SetReady(true)
	if rec := do(h, http.MethodGet, "/readyz"); rec.Code != http.StatusOK {
		t.Errorf("GET /readyz when ready = %d, want 200", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/healthz"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /healthz = %d, want 405", rec.Code)
	}
}

func TestToolCallRoutesStillRejectOtherMethods(t *testing.T) {
	h := newToolHandler()
	for _, path := range bothEndpoints {
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch} {
			rec := do(h, method, path)
			expectError(t, rec, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "")
		}
	}
	// There is no endpoint that lists the registry.
	for _, path := range []string{"/v1/tools", "/v1/tools/orders.lookup", "/v1/registry", "/tools.json", "/examples/config/tools.json"} {
		rec := do(h, http.MethodGet, path)
		expectError(t, rec, http.StatusNotFound, CodeNotFound, "")
	}
}

// ---------------------------------------------------------------------------
// Concurrency

func TestConcurrentToolCalls(t *testing.T) {
	h := newToolHandler()
	type job struct {
		path, body string
		status     int
	}
	jobs := []job{
		{evaluatePath, call(evaluatePath, "orders.lookup", lookupArgs), http.StatusNotImplemented},
		{executePath, call(executePath, "payments.refund", refundArgs), http.StatusNotImplemented},
		{evaluatePath, call(evaluatePath, "orders.lookup", `{}`), http.StatusBadRequest},
		{executePath, call(executePath, "payments.refund", `{"amount_cents":1}`), http.StatusBadRequest},
		{evaluatePath, call(evaluatePath, "nope.nope", `{}`), http.StatusNotFound},
	}
	var wg sync.WaitGroup
	fail := make(chan string, 16)
	for w := 0; w < 24; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 60; i++ {
				j := jobs[(w+i)%len(jobs)]
				req := httptest.NewRequest(http.MethodPost, j.path, strings.NewReader(j.body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code != j.status {
					select {
					case fail <- fmt.Sprintf("%s: status %d, want %d", j.body, rec.Code, j.status):
					default:
					}
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(fail)
	for msg := range fail {
		t.Error(msg)
	}
}
