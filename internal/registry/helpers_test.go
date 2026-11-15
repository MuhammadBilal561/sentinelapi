package registry

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MuhammadBilal561/sentinelapi/internal/canonical"
)

// obj is a JSON object under construction in a test.
type obj = map[string]any

// repoFile returns the absolute path of a file in the repository, found from
// this source file's location, so tests do not depend on the working directory.
func repoFile(t testing.TB, rel ...string) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source file")
	}
	parts := append([]string{filepath.Dir(here), "..", ".."}, rel...)
	return filepath.Join(parts...)
}

// closedObject returns an object schema with additionalProperties false.
func closedObject(properties obj, required ...string) obj {
	s := obj{"type": "object", "additionalProperties": false, "properties": properties}
	if len(required) > 0 {
		req := make([]any, len(required))
		for i, r := range required {
			req[i] = r
		}
		s["required"] = req
	}
	return s
}

// baseTool returns a valid tool entry named name with a small closed schema.
func baseTool(name string) obj {
	return obj{
		"name":          name,
		"version":       1,
		"risk":          "read",
		"executor":      "mock.test",
		"review_fields": []any{"a"},
		"audit_fields":  []any{"a"},
		"args_schema":   closedObject(obj{"a": obj{"type": "string"}}, "a"),
	}
}

// withSchema returns a copy of baseTool whose args_schema is schema and whose
// review_fields and audit_fields are empty.
func withSchema(name string, schema any) obj {
	t := baseTool(name)
	t["args_schema"] = schema
	t["review_fields"] = []any{}
	t["audit_fields"] = []any{}
	return t
}

// document wraps tools in a registry document.
func document(tools ...obj) obj {
	list := make([]any, len(tools))
	for i, tl := range tools {
		list[i] = tl
	}
	return obj{"registry_version": 1, "tools": list}
}

// marshal encodes v as JSON, failing the test on error.
func marshal(t testing.TB, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// parseDoc encodes v as JSON and runs Parse on it.
func parseDoc(t testing.TB, v any) (*Registry, error) {
	t.Helper()
	return Parse(marshal(t, v))
}

// mustParseDoc parses v and fails the test if the registry is rejected.
func mustParseDoc(t testing.TB, v any) *Registry {
	t.Helper()
	r, err := parseDoc(t, v)
	if err != nil {
		t.Fatalf("registry rejected: %v", err)
	}
	if r == nil {
		t.Fatal("Parse returned a nil registry and a nil error")
	}
	return r
}

// mustSingleTool builds a registry with one tool that has the given schema.
func mustSingleTool(t testing.TB, name string, schema any) *Registry {
	t.Helper()
	return mustParseDoc(t, document(withSchema(name, schema)))
}

// expectLoadError asserts that err is a *LoadError of the given kind whose
// text contains every want (case-insensitively), and that r is nil: a failed
// load never yields a registry.
func expectLoadError(t testing.TB, r *Registry, err error, kind LoadKind, want ...string) {
	t.Helper()
	if r != nil {
		t.Fatal("a failed load must return a nil registry")
	}
	if err == nil {
		t.Fatalf("expected a %v error, got nil", kind)
	}
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("error is %T, want *LoadError: %v", err, err)
	}
	if le.Kind != kind {
		t.Fatalf("error kind = %v, want %v: %v", le.Kind, kind, err)
	}
	for _, w := range want {
		if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(w)) {
			t.Fatalf("error %q does not mention %q", err.Error(), w)
		}
	}
	if strings.ContainsAny(err.Error(), "\n\r") {
		t.Fatalf("error must be a single line: %q", err.Error())
	}
}

// args parses a JSON object with the strict parser, as the HTTP layer does,
// so numbers arrive as json.Number.
func args(t testing.TB, text string) map[string]any {
	t.Helper()
	v, err := canonical.Parse([]byte(text))
	if err != nil {
		t.Fatalf("test arguments are not strictly valid JSON: %v\n%s", err, text)
	}
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("test arguments are not an object: %s", text)
	}
	return m
}

// expectArgs asserts the outcome of validating text against the named tool.
func expectArgs(t testing.TB, r *Registry, tool, text string, wantErr error) {
	t.Helper()
	err := r.ValidateArguments(tool, args(t, text))
	if wantErr == nil {
		if err != nil {
			t.Errorf("%s %s: unexpected error %v", tool, text, err)
		}
		return
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("%s %s: error = %v, want %v", tool, text, err, wantErr)
	}
}
