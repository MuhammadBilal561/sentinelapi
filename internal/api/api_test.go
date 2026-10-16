package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var requestIDPattern = regexp.MustCompile(`^req_[0-9a-f]{16}$`)

func newTestHandler(t *testing.T, health *Health) http.Handler {
	t.Helper()
	return NewHandler(Options{Health: health})
}

func readyHealth() *Health {
	h := NewHealth(nil)
	h.SetReady(true)
	return h
}

func do(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func decodeStatus(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not a JSON object of strings: %v", rec.Body.String(), err)
	}
	if len(body) != 1 {
		t.Fatalf("body %q should contain exactly one field", rec.Body.String())
	}
	return body["status"]
}

func TestHealthz(t *testing.T) {
	// Liveness must not depend on readiness.
	rec := do(newTestHandler(t, NewHealth(nil)), http.MethodGet, "/healthz")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if got := decodeStatus(t, rec); got != "ok" {
		t.Fatalf("status field = %q, want ok", got)
	}
}

func TestReadyzWhenReady(t *testing.T) {
	rec := do(newTestHandler(t, readyHealth()), http.MethodGet, "/readyz")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if got := decodeStatus(t, rec); got != "ready" {
		t.Fatalf("status field = %q, want ready", got)
	}
}

func TestReadyzBeforeStartupCompletes(t *testing.T) {
	rec := do(newTestHandler(t, NewHealth(nil)), http.MethodGet, "/readyz")

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got := decodeStatus(t, rec); got != "not_ready" {
		t.Fatalf("status field = %q, want not_ready", got)
	}
}

func TestReadyzDuringShutdown(t *testing.T) {
	health := readyHealth()
	h := newTestHandler(t, health)
	health.SetReady(false)

	if rec := do(h, http.MethodGet, "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz status = %d, want 503", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200 while draining", rec.Code)
	}
}

func TestReadyzFailingCheckIsNotReadyAndNotLeaked(t *testing.T) {
	const secretCause = "dial tcp 10.0.0.5:5432: password authentication failed"
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	health := NewHealth(logger, func(context.Context) error { return errors.New(secretCause) })
	health.SetReady(true)
	rec := do(NewHandler(Options{Logger: logger, Health: health}), http.MethodGet, "/readyz")

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got := decodeStatus(t, rec); got != "not_ready" {
		t.Fatalf("status field = %q, want not_ready", got)
	}
	if strings.Contains(rec.Body.String(), "password") {
		t.Fatalf("response leaked check error: %q", rec.Body.String())
	}
	if !strings.Contains(logs.String(), secretCause) {
		t.Fatalf("cause should be logged for operators, logs: %s", logs.String())
	}
}

func TestReadyzPassingChecksAreReady(t *testing.T) {
	calls := 0
	health := NewHealth(nil, func(context.Context) error { calls++; return nil })
	health.SetReady(true)
	rec := do(newTestHandler(t, health), http.MethodGet, "/readyz")

	if rec.Code != http.StatusOK || calls != 1 {
		t.Fatalf("status = %d, check calls = %d, want 200 and 1", rec.Code, calls)
	}
}

func TestMethodAndPathRestrictions(t *testing.T) {
	h := newTestHandler(t, readyHealth())

	if rec := do(h, http.MethodPost, "/healthz"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /healthz status = %d, want 405", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/v1/tool-calls/evaluate"); rec.Code != http.StatusNotFound {
		t.Errorf("unimplemented route status = %d, want 404", rec.Code)
	}
}

func TestRequestIDHeaderFormatAndUniqueness(t *testing.T) {
	h := newTestHandler(t, readyHealth())
	seen := map[string]bool{}

	for range 50 {
		id := do(h, http.MethodGet, "/healthz").Header().Get(RequestIDHeader)
		if !requestIDPattern.MatchString(id) {
			t.Fatalf("request ID %q does not match %s", id, requestIDPattern)
		}
		if seen[id] {
			t.Fatalf("request ID %q repeated", id)
		}
		seen[id] = true
	}
}

func TestRequestIDPresentOnErrorsToo(t *testing.T) {
	h := newTestHandler(t, readyHealth())
	for _, tc := range []struct{ method, target string }{
		{http.MethodGet, "/nope"},
		{http.MethodPost, "/healthz"},
	} {
		id := do(h, tc.method, tc.target).Header().Get(RequestIDHeader)
		if !requestIDPattern.MatchString(id) {
			t.Errorf("%s %s: request ID %q invalid", tc.method, tc.target, id)
		}
	}
}

func TestClientSuppliedRequestIDIsIgnored(t *testing.T) {
	h := newTestHandler(t, readyHealth())
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set(RequestIDHeader, "attacker-chosen-id\nforged log line")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	got := rec.Header().Get(RequestIDHeader)
	if !requestIDPattern.MatchString(got) {
		t.Fatalf("response request ID = %q, want a server-generated ID", got)
	}
}

const secretPanicText = "secret internal panic detail: db password hunter2"

func TestPanicRecovery(t *testing.T) {
	testPanicRecovery(t, secretPanicText, secretPanicText, "string")
}

func TestPanicRecoveryWithErrorValue(t *testing.T) {
	testPanicRecovery(t, secretPanicText, errors.New(secretPanicText), "*errors.errorString")
}

// testPanicRecovery panics with panicValue (whose text is panicText) inside a
// handler and checks the client response and the server logs.
func testPanicRecovery(t *testing.T, panicText string, panicValue any, wantType string) {
	t.Helper()

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	panicking := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(panicValue) })

	h := RequestID(AccessLog(logger)(Recover(logger)(panicking)))

	rec := do(h, http.MethodGet, "/boom")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	body := rec.Body.String()
	for _, leak := range []string{panicText, "hunter2", "goroutine", ".go:"} {
		if strings.Contains(body, leak) {
			t.Fatalf("response leaked %q: %s", leak, body)
		}
	}

	var parsed errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("body is not the error envelope: %v", err)
	}
	if parsed.Error.Code != "INTERNAL_ERROR" {
		t.Fatalf("error code = %q, want INTERNAL_ERROR", parsed.Error.Code)
	}
	if id := rec.Header().Get(RequestIDHeader); parsed.RequestID != id || !requestIDPattern.MatchString(id) {
		t.Fatalf("body request_id %q should equal header %q", parsed.RequestID, id)
	}

	// Data minimization: neither the panic value nor a stack trace may be logged.
	out := logs.String()
	for _, leak := range []string{panicText, "hunter2", "goroutine", ".go:", `"stack"`} {
		if strings.Contains(out, leak) {
			t.Errorf("server log leaked %q: %s", leak, out)
		}
	}

	// Recovery is still observable: a generic event with the request ID and the
	// panic type, plus an access-log line recording the 500.
	for _, want := range []string{
		`"msg":"panic recovered"`,
		`"request_id":"` + parsed.RequestID + `"`,
		`"panic_type":"` + wantType + `"`,
		`"status":500`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("server log missing %s: %s", want, out)
		}
	}
}

func TestServerKeepsServingAfterPanic(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	mux.HandleFunc("GET /ok", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	srv := httptest.NewServer(RequestID(AccessLog(logger)(Recover(logger)(mux))))
	defer srv.Close()

	for _, tc := range []struct {
		path string
		want int
	}{{"/boom", 500}, {"/ok", 204}, {"/boom", 500}, {"/ok", 204}} {
		resp, err := http.Get(srv.URL + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Fatalf("GET %s status = %d, want %d", tc.path, resp.StatusCode, tc.want)
		}
	}
}

func TestAccessLogDoesNotRecordSensitiveRequestData(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	h := NewHandler(Options{Logger: logger, Health: readyHealth()})

	req := httptest.NewRequest(http.MethodPost, "/healthz?token=QUERYCANARY", strings.NewReader("BODYCANARY"))
	req.Header.Set("Authorization", "Bearer HEADERCANARY")
	h.ServeHTTP(httptest.NewRecorder(), req)

	out := logs.String()
	for _, canary := range []string{"QUERYCANARY", "BODYCANARY", "HEADERCANARY"} {
		if strings.Contains(out, canary) {
			t.Errorf("access log leaked %s: %s", canary, out)
		}
	}
	for _, field := range []string{`"request_id":"req_`, `"method":"POST"`, `"path":"/healthz"`, `"status":405`} {
		if !strings.Contains(out, field) {
			t.Errorf("access log missing %s: %s", field, out)
		}
	}
}
