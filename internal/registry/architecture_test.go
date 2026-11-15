package registry

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestRegistryImportsStayNarrow guards the M2 promise that loading and using
// the registry can neither reach the network or the shell, nor reach any
// package that decides, approves, audits, stores or executes.
//
// Allowed imports of non-test code: the standard library (minus the packages
// listed in bannedStd), internal/canonical, and the one schema library.
func TestRegistryImportsStayNarrow(t *testing.T) {
	const module = "github.com/MuhammadBilal561/sentinelapi/"
	allowedModule := map[string]bool{module + "internal/canonical": true}
	allowedThirdParty := map[string]bool{"github.com/santhosh-tekuri/jsonschema/v6": true}
	// Anything that could fetch a schema, run a program or load code.
	bannedStd := map[string]bool{
		"net": true, "net/http": true, "net/rpc": true, "net/smtp": true, "os/exec": true, "os/signal": true,
		"plugin": true, "unsafe": true, "syscall": true, "debug/elf": true, "reflect": true,
	}
	bannedNames := []string{"api", "executor", "policy", "approval", "authz", "audit", "store", "gateway", "config"}

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
			first := strings.SplitN(path, "/", 2)[0]
			switch {
			case strings.HasPrefix(path, module):
				if !allowedModule[path] {
					t.Errorf("%s imports %s; the registry may only use internal/canonical from this module", name, path)
				}
				for _, banned := range bannedNames {
					if strings.Contains(path, "/"+banned) {
						t.Errorf("%s imports %s, which suggests a decision, storage or execution path", name, path)
					}
				}
			case strings.Contains(first, "."):
				if !allowedThirdParty[path] {
					t.Errorf("%s imports third-party package %s; only the JSON Schema library is allowed", name, path)
				}
			default:
				if bannedStd[path] {
					t.Errorf("%s imports %s; the registry must not reach the network, a shell or unsafe memory", name, path)
				}
			}
		}
	}
}

// TestToolNameRuleMatchesTheDocumentedGrammar pins ValidToolName to the grammar
// the README and the API envelope document.
func TestToolNameRuleMatchesTheDocumentedGrammar(t *testing.T) {
	if MaxIdentifierLen != 64 {
		t.Fatalf("MaxIdentifierLen = %d; the documented limit is 64", MaxIdentifierLen)
	}
	for name, want := range map[string]bool{
		"orders.lookup": true, "payments.refund": true, "a": true, "a1.b2_c3": true,
		"Orders.lookup": false, "orders..lookup": false, ".a": false, "a.": false, "a-b": false, "1a": false,
	} {
		if got := ValidToolName(name); got != want {
			t.Errorf("ValidToolName(%q) = %v, want %v", name, got, want)
		}
	}
}
