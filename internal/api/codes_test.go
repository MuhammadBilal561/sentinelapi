package api

import (
	"bufio"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const goldenPath = "testdata/error_codes.golden"

// parseGolden reads "CODE STATUS" lines, ignoring blank lines and # comments.
func parseGolden(t *testing.T) map[string]int {
	t.Helper()
	f, err := os.Open(goldenPath)
	if err != nil {
		t.Fatalf("cannot open golden file: %v", err)
	}
	defer f.Close()

	golden := map[string]int{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("%s:%d: want \"CODE STATUS\", got %q", goldenPath, n, line)
		}
		status, err := strconv.Atoi(fields[1])
		if err != nil {
			t.Fatalf("%s:%d: bad status %q", goldenPath, n, fields[1])
		}
		if _, dup := golden[fields[0]]; dup {
			t.Fatalf("%s:%d: duplicate code %s", goldenPath, n, fields[0])
		}
		golden[fields[0]] = status
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return golden
}

// TestErrorCodesMatchGolden freezes the public error-code list. Removing or
// renaming a code, or changing its HTTP status, fails the first loop. Adding a
// code fails the second loop until the golden file is updated on purpose.
func TestErrorCodesMatchGolden(t *testing.T) {
	golden := parseGolden(t)
	current := map[string]int{}
	for _, s := range errorSpecs {
		current[s.Code] = s.Status
	}

	for code, status := range golden {
		got, ok := current[code]
		switch {
		case !ok:
			t.Errorf("error code %s was removed or renamed; this breaks clients. "+
				"Codes are append-only", code)
		case got != status:
			t.Errorf("error code %s changed HTTP status from %d to %d; this breaks clients", code, status, got)
		}
	}
	var added []string
	for code := range current {
		if _, ok := golden[code]; !ok {
			added = append(added, code)
		}
	}
	sort.Strings(added)
	for _, code := range added {
		t.Errorf("error code %s (HTTP %d) is new: add the line %q to %s to confirm it is intentional",
			code, current[code], code+" "+strconv.Itoa(current[code]), goldenPath)
	}
}

func TestRequiredErrorCodesExist(t *testing.T) {
	required := map[string]int{
		"INVALID_JSON": 400, "INVALID_UTF8": 400, "DUPLICATE_JSON_KEY": 400, "JSON_TOO_DEEP": 400,
		"JSON_LIMIT_EXCEEDED": 400, "REQUEST_TOO_LARGE": 413, "UNSUPPORTED_MEDIA_TYPE": 415,
		"INVALID_REQUEST": 400, "METHOD_NOT_ALLOWED": 405, "NOT_FOUND": 404, "NOT_IMPLEMENTED": 501,
		"INTERNAL_ERROR": 500, "TOOL_UNKNOWN": 404, "ARGS_SCHEMA_INVALID": 400,
	}
	for code, status := range required {
		spec, ok := errorSpecFor(code)
		if !ok || spec.Status != status {
			t.Errorf("code %s: registered=%v status=%d, want %d", code, ok, spec.Status, status)
		}
	}
}

func TestErrorSpecsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range errorSpecs {
		if seen[s.Code] {
			t.Errorf("duplicate code %s", s.Code)
		}
		seen[s.Code] = true
		if s.Code != strings.ToUpper(s.Code) || strings.ContainsAny(s.Code, " -.") {
			t.Errorf("code %q must be UPPER_SNAKE_CASE", s.Code)
		}
		if s.Status < 400 || s.Status > 599 {
			t.Errorf("code %s has non-error status %d", s.Code, s.Status)
		}
		if s.Message == "" || !strings.HasSuffix(s.Message, ".") {
			t.Errorf("code %s needs a sentence-style message, got %q", s.Code, s.Message)
		}
	}
	if _, ok := errorSpecFor("NO_SUCH_CODE"); ok {
		t.Error("errorSpecFor must report unknown codes")
	}
}

// TestParserEndpointsReachNoDecisionOrExecutionCode guards the promise that
// these handlers only validate. Non-test code in the api package may import the
// standard library and exactly two packages of this module, internal/canonical
// and internal/registry. It may not import any third-party package directly
// (the JSON Schema library stays behind the registry), and no package whose
// name suggests authorization, policy, approval, audit, storage, a gateway or
// an executor, so it cannot reach a decision or an execution path.
func TestParserEndpointsReachNoDecisionOrExecutionCode(t *testing.T) {
	const module = "github.com/MuhammadBilal561/sentinelapi/"
	allowed := map[string]bool{
		module + "internal/canonical": true,
		module + "internal/registry":  true,
	}
	banned := []string{"executor", "policy", "approval", "authz", "audit", "store", "gateway", "config"}

	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("cannot list package files: %v", err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if strings.HasPrefix(path, module) && !allowed[path] {
				t.Errorf("%s imports %s; M2 handlers may only use internal/canonical and internal/registry", name, path)
			}
			if !strings.HasPrefix(path, module) && strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
				t.Errorf("%s imports third-party package %s; the api package may only import the standard library and this module", name, path)
			}
			for _, b := range banned {
				if strings.Contains(path, "/"+b) {
					t.Errorf("%s imports %s, which suggests a decision, storage or execution path", name, path)
				}
			}
		}
	}
}
