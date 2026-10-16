package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a bytes.Buffer safe for use by the server goroutine and the test.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func envLookup(env map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := env[k]; return v, ok }
}

func TestRunRejectsInvalidConfig(t *testing.T) {
	var stdout, stderr syncBuffer
	code := run(context.Background(), envLookup(map[string]string{
		"HTTP_ADDR": "not-an-address",
		"LOG_LEVEL": "loud",
	}), &stdout, &stderr)

	if code != exitConfig {
		t.Fatalf("exit code = %d, want %d", code, exitConfig)
	}
	for _, want := range []string{"HTTP_ADDR", "LOG_LEVEL"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr missing %s: %q", want, stderr.String())
		}
	}
	if stdout.String() != "" {
		t.Errorf("nothing should be logged before config is valid, got %q", stdout.String())
	}
}

func TestRunFailsWhenAddressInUse(t *testing.T) {
	first, firstCancel, firstDone, _ := startServer(t, "127.0.0.1:0", 3*time.Second)
	defer func() { firstCancel(); <-firstDone }()

	var stdout, stderr syncBuffer
	code := run(context.Background(), envLookup(map[string]string{"HTTP_ADDR": first}), &stdout, &stderr)
	if code != exitRuntime {
		t.Fatalf("exit code = %d, want %d; logs: %s", code, exitRuntime, stdout.String())
	}
	if !strings.Contains(stdout.String(), "cannot listen") {
		t.Errorf("expected a clear listen error in logs, got %q", stdout.String())
	}
}

func TestRunServesAndShutsDownGracefully(t *testing.T) {
	addr, cancel, done, out := startServer(t, "127.0.0.1:0", 3*time.Second)
	client := newTestClient(t)

	for _, tc := range []struct{ path, wantBody string }{
		{"/healthz", `{"status":"ok"}`},
		{"/readyz", `{"status":"ready"}`},
	} {
		resp, body := get(t, client, "http://"+addr+tc.path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", tc.path, resp.StatusCode)
		}
		if got := strings.TrimSpace(string(body)); got != tc.wantBody {
			t.Errorf("GET %s body = %q, want %q", tc.path, got, tc.wantBody)
		}
		if resp.Header.Get("X-Request-ID") == "" {
			t.Errorf("GET %s missing X-Request-ID", tc.path)
		}
	}

	cancel()
	select {
	case code := <-done:
		if code != exitOK {
			t.Fatalf("exit code = %d, want 0; logs: %s", code, out.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after shutdown signal")
	}
	if !strings.Contains(out.String(), "shutdown complete") {
		t.Errorf("logs missing shutdown complete: %s", out.String())
	}
}

// A genuine shutdown failure must be reported, not hidden. http.Server.Shutdown
// treats a connection that has not yet sent a request header as busy for its
// first 5 seconds, so an idle raw connection keeps the drain open past a short
// shutdown timeout. run must then give up, close the connections and exit
// non-zero.
func TestRunReportsGracefulShutdownFailure(t *testing.T) {
	addr, cancel, done, out := startServer(t, "127.0.0.1:0", 200*time.Millisecond)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Connections are accepted in order, so once this request is served the
	// server has also accepted the silent connection above.
	resp, _ := get(t, newTestClient(t), "http://"+addr+"/healthz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200", resp.StatusCode)
	}

	cancel()
	select {
	case code := <-done:
		if code != exitRuntime {
			t.Fatalf("exit code = %d, want %d; logs: %s", code, exitRuntime, out.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after shutdown signal")
	}
	if !strings.Contains(out.String(), "graceful shutdown failed") {
		t.Errorf("logs missing graceful shutdown failure: %s", out.String())
	}
}

// newTestClient returns an HTTP client that never reuses connections, so no
// keep-alive or spare connection can outlive a request and affect shutdown.
func newTestClient(t *testing.T) *http.Client {
	t.Helper()
	tr := &http.Transport{DisableKeepAlives: true}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 5 * time.Second}
}

// get performs a GET, reads the whole body to EOF and closes it, failing the
// test on any error. The returned response has an already-closed body.
func get(t *testing.T, client *http.Client, url string) (*http.Response, []byte) {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	body, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if readErr != nil {
		t.Fatalf("GET %s: reading body: %v", url, readErr)
	}
	if closeErr != nil {
		t.Fatalf("GET %s: closing body: %v", url, closeErr)
	}
	return resp, body
}

// startServer runs the service on addr and returns the address it bound, a
// function that triggers shutdown, a channel that yields the exit code, and
// the log buffer.
func startServer(t *testing.T, addr string, shutdownTimeout time.Duration) (string, context.CancelFunc, <-chan int, *syncBuffer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	out, errOut := &syncBuffer{}, &syncBuffer{}
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, envLookup(map[string]string{
			"HTTP_ADDR": addr, "SHUTDOWN_TIMEOUT": shutdownTimeout.String(),
		}), out, errOut)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range strings.Split(out.String(), "\n") {
			var entry struct {
				Msg  string `json:"msg"`
				Addr string `json:"addr"`
			}
			if json.Unmarshal([]byte(line), &entry) == nil && entry.Msg == "server listening" {
				return entry.Addr, cancel, done, out
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	t.Fatalf("server did not report listening; logs: %s %s", out.String(), errOut.String())
	return "", nil, nil, nil
}
