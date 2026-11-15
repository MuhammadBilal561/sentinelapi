package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// logMessages returns the "msg" of every JSON log line, in order.
func logMessages(t *testing.T, logs string) []string {
	t.Helper()
	var msgs []string
	for _, line := range strings.Split(logs, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e struct {
			Msg string `json:"msg"`
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		msgs = append(msgs, e.Msg)
	}
	return msgs
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

func writeRegistry(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tools.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// freeAddr returns a loopback address that was free a moment ago.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

const goodTool = `{"name":"a.good","version":1,"risk":"read","executor":"mock.a","review_fields":["x"],"audit_fields":["x"],
 "args_schema":{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false,
 "properties":{"x":{"type":"string","maxLength":10}},"required":["x"]}}`

func registryWith(tools ...string) string {
	return `{"registry_version":1,"tools":[` + strings.Join(tools, ",") + `]}`
}

func mustFailStartup(t *testing.T, registryPath string) (stdout, stderr string, addr string) {
	t.Helper()
	addr = freeAddr(t)
	var out, errOut syncBuffer
	done := make(chan int, 1)
	go func() {
		done <- run(context.Background(), envLookup(map[string]string{
			"HTTP_ADDR": addr, "TOOL_REGISTRY_PATH": registryPath,
		}), &out, &errOut)
	}()
	select {
	case code := <-done:
		if code != exitConfig {
			t.Fatalf("exit code = %d, want %d (exitConfig); logs: %s", code, exitConfig, out.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run kept running with an unusable registry")
	}
	return out.String(), errOut.String(), addr
}

func TestStartupWithValidRegistryServesAllThreeOutcomes(t *testing.T) {
	addr, cancel, done, out := startServer(t, "127.0.0.1:0", 3*time.Second)
	defer func() { cancel(); <-done }()
	client := newTestClient(t)

	post := func(body string) (int, string) {
		t.Helper()
		resp, err := client.Post("http://"+addr+"/v1/tool-calls/evaluate", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		return resp.StatusCode, b.String()
	}
	const ok = `{"tool_name":"orders.lookup","arguments":{"tenant_id":"t_acme","order_id":"ord_1042"}}`
	if code, body := post(ok); code != http.StatusNotImplemented || !strings.Contains(body, "NOT_IMPLEMENTED") {
		t.Errorf("valid call: %d %s", code, body)
	}
	if code, body := post(`{"tool_name":"no.such","arguments":{}}`); code != http.StatusNotFound || !strings.Contains(body, "TOOL_UNKNOWN") {
		t.Errorf("unknown tool: %d %s", code, body)
	}
	if code, body := post(`{"tool_name":"orders.lookup","arguments":{"tenant_id":"bad","order_id":"ord_1"}}`); code != http.StatusBadRequest || !strings.Contains(body, "ARGS_SCHEMA_INVALID") {
		t.Errorf("invalid args: %d %s", code, body)
	}
	if !strings.Contains(out.String(), "tool registry loaded") {
		t.Errorf("startup log lacks the registry summary: %s", out.String())
	}
}

func TestRegistryIsLoadedBeforeListeningAndReadiness(t *testing.T) {
	_, cancel, done, out := startServer(t, "127.0.0.1:0", 3*time.Second)
	defer func() { cancel(); <-done }()
	msgs := logMessages(t, out.String())
	loaded, listening := indexOf(msgs, "tool registry loaded"), indexOf(msgs, "server listening")
	if loaded < 0 || listening < 0 || loaded > listening {
		t.Fatalf("registry must be loaded before the server reports listening: %v", msgs)
	}
}

func TestStartupFailsAndNeverListensWithUnusableRegistry(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.json")
	cases := map[string]string{
		"missing file":     missing,
		"directory":        t.TempDir(),
		"malformed JSON":   writeRegistry(t, `{"registry_version":1,"tools":[`),
		"empty file":       writeRegistry(t, ``),
		"duplicate key":    writeRegistry(t, `{"registry_version":1,"registry_version":1,"tools":[`+goodTool+`]}`),
		"unknown field":    writeRegistry(t, `{"registry_version":1,"tools":[`+goodTool+`],"extra":1}`),
		"no tools":         writeRegistry(t, registryWith()),
		"open schema":      writeRegistry(t, registryWith(strings.Replace(goodTool, `"additionalProperties":false,`, ``, 1))),
		"external $ref":    writeRegistry(t, registryWith(strings.Replace(goodTool, `{"type":"string","maxLength":10}`, `{"$ref":"https://example.com/x.json"}`, 1))),
		"file $ref":        writeRegistry(t, registryWith(strings.Replace(goodTool, `{"type":"string","maxLength":10}`, `{"$ref":"file:///etc/passwd"}`, 1))),
		"malformed schema": writeRegistry(t, registryWith(strings.Replace(goodTool, `"maxLength":10`, `"maxLength":"ten"`, 1))),
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, addr := mustFailStartup(t, path)
			msgs := logMessages(t, stdout)
			if indexOf(msgs, "cannot load tool registry") < 0 {
				t.Errorf("missing the startup error in logs: %v", msgs)
			}
			for _, m := range []string{"server listening", "tool registry loaded"} {
				if indexOf(msgs, m) >= 0 {
					t.Errorf("logged %q although the registry is unusable", m)
				}
			}
			if stderr != "" {
				t.Errorf("unexpected stderr: %q", stderr)
			}
			if c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond); err == nil {
				c.Close()
				t.Errorf("something is listening on %s after a failed startup", addr)
			}
		})
	}
}

// One good tool next to one bad tool must not result in a half-loaded service.
func TestPartialRegistryIsNeverServed(t *testing.T) {
	bad := strings.Replace(strings.Replace(goodTool, `"a.good"`, `"a.bad"`, 1), `"additionalProperties":false,`, ``, 1)
	_, _, addr := mustFailStartup(t, writeRegistry(t, registryWith(goodTool, bad)))
	if c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond); err == nil {
		c.Close()
		t.Fatalf("a server is listening on %s", addr)
	}
}

func TestStartupErrorHasNoSecretsOrFileContents(t *testing.T) {
	const canary = "CANARY-SECRET-9f3a"
	bad := strings.Replace(goodTool, `"executor":"mock.a"`, `"executor":"`+canary+` bad"`, 1)
	stdout, stderr, _ := mustFailStartup(t, writeRegistry(t, registryWith(bad)))
	if strings.Contains(stdout+stderr, canary) {
		t.Errorf("the startup error repeats registry content: %s", stdout)
	}
}

func TestDefaultRegistryPathIsRelativeToWorkingDirectory(t *testing.T) {
	addr := freeAddr(t)
	env := map[string]string{"HTTP_ADDR": addr}

	// From a directory without the example file, the default path fails closed.
	t.Chdir(t.TempDir())
	var out, errOut syncBuffer
	if code := run(context.Background(), envLookup(env), &out, &errOut); code != exitConfig {
		t.Fatalf("exit code = %d, want %d; %s", code, exitConfig, out.String())
	}

	// From the repository root, the default works.
	t.Chdir(repoRoot())
	ctx, cancel := context.WithCancel(context.Background())
	out = syncBuffer{}
	done := make(chan int, 1)
	go func() { done <- run(ctx, envLookup(env), &out, &errOut) }()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), "server listening") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if code := <-done; code != exitOK {
		t.Fatalf("exit code = %d; logs: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "tool registry loaded") {
		t.Errorf("default registry not loaded: %s", out.String())
	}
}

func TestInvalidRegistryPathValueIsAConfigurationError(t *testing.T) {
	var out, errOut syncBuffer
	code := run(context.Background(), envLookup(map[string]string{
		"HTTP_ADDR": "127.0.0.1:0", "TOOL_REGISTRY_PATH": "tools\x00.json",
	}), &out, &errOut)
	if code != exitConfig || !strings.Contains(errOut.String(), "TOOL_REGISTRY_PATH") || out.String() != "" {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, errOut.String(), out.String())
	}
}
