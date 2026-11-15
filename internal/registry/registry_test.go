package registry

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// The shipped example registry

func TestLoadExampleRegistry(t *testing.T) {
	r, err := Load(repoFile(t, "examples", "config", "tools.json"))
	if err != nil {
		t.Fatalf("the shipped example registry does not load: %v", err)
	}
	if r.Version() != 1 {
		t.Errorf("Version() = %d, want 1", r.Version())
	}
	if got, want := r.Names(), []string{"accounts.close", "orders.lookup", "payments.refund"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Names() = %v, want exactly %v", got, want)
	}
	if r.Len() != 3 {
		t.Errorf("Len() = %d, want 3", r.Len())
	}

	want := map[string]ToolInfo{
		"orders.lookup": {
			Name: "orders.lookup", Version: 1, Risk: RiskRead, Executor: "mock.orders",
			ReviewFields: []string{"order_id"}, AuditFields: []string{"tenant_id", "order_id"},
		},
		"payments.refund": {
			Name: "payments.refund", Version: 1, Risk: RiskWrite, Executor: "mock.payments",
			ReviewFields: []string{"order_id", "amount_cents", "reason"},
			AuditFields:  []string{"tenant_id", "order_id", "amount_cents"},
		},
		"accounts.close": {
			Name: "accounts.close", Version: 1, Risk: RiskWrite, Executor: "mock.accounts",
			ReviewFields: []string{"account_id", "reason"}, AuditFields: []string{"tenant_id", "account_id"},
		},
	}
	for name, w := range want {
		got, ok := r.Tool(name)
		if !ok {
			t.Errorf("tool %s is not registered", name)
			continue
		}
		if !reflect.DeepEqual(got, w) {
			t.Errorf("Tool(%s) = %+v, want %+v", name, got, w)
		}
		if !r.Has(name) {
			t.Errorf("Has(%s) = false", name)
		}
	}
}

func TestExampleRegistryValidatesRepresentativeCalls(t *testing.T) {
	r, err := Load(repoFile(t, "examples", "config", "tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	expectArgs(t, r, "orders.lookup", `{"tenant_id":"t_acme","order_id":"ord_1042"}`, nil)
	expectArgs(t, r, "payments.refund",
		`{"tenant_id":"t_acme","order_id":"ord_1042","amount_cents":12000,"reason":"duplicate charge"}`, nil)
	expectArgs(t, r, "accounts.close", `{"tenant_id":"t_acme","account_id":"acct_77","reason":"customer request"}`, nil)

	// accounts.close is registered even though nobody may be authorized to
	// call it yet: registration is not authorization.
	if !r.Has("accounts.close") {
		t.Fatal("accounts.close must be registered")
	}
}

func TestExampleRegistryReadsTheSameFromAnyDirectory(t *testing.T) {
	// The path is absolute (built from this source file), so changing the
	// working directory must not matter.
	path := repoFile(t, "examples", "config", "tools.json")
	t.Chdir(t.TempDir())
	if _, err := Load(path); err != nil {
		t.Fatalf("Load with an absolute path failed after changing directory: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Immutability and the nil registry

func TestRegistryExposesOnlyCopies(t *testing.T) {
	r := mustParseDoc(t, document(baseTool("a.b"), baseTool("c.d")))

	names := r.Names()
	names[0] = "tampered"
	if got := r.Names(); got[0] != "a.b" {
		t.Errorf("mutating Names() changed the registry: %v", got)
	}

	info, _ := r.Tool("a.b")
	info.ReviewFields[0] = "tampered"
	info.AuditFields = append(info.AuditFields, "extra")
	info.Name = "tampered"
	again, _ := r.Tool("a.b")
	if again.ReviewFields[0] != "a" || len(again.AuditFields) != 1 || again.Name != "a.b" {
		t.Errorf("mutating a ToolInfo changed the registry: %+v", again)
	}
}

func TestNilRegistryIsEmpty(t *testing.T) {
	var r *Registry
	if r.Has("orders.lookup") || r.Len() != 0 || r.Version() != 0 || r.Names() != nil {
		t.Error("a nil registry must be empty")
	}
	if _, ok := r.Tool("orders.lookup"); ok {
		t.Error("a nil registry has no tools")
	}
	if err := r.ValidateArguments("orders.lookup", map[string]any{}); !errors.Is(err, ErrUnknownTool) {
		t.Errorf("ValidateArguments on nil registry = %v, want ErrUnknownTool", err)
	}
	var zero Registry
	if zero.Has("x") || zero.Len() != 0 {
		t.Error("the zero Registry must be empty")
	}
}

// ---------------------------------------------------------------------------
// Loading from a file

func writeFile(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tools.json")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFromFile(t *testing.T) {
	path := writeFile(t, marshal(t, document(baseTool("a.b"))))
	r, err := Load(path)
	if err != nil || !r.Has("a.b") {
		t.Fatalf("Load = %v, %v", r, err)
	}
}

func TestLoadFileProblems(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		r, err := Load(filepath.Join(t.TempDir(), "nope.json"))
		expectLoadError(t, r, err, KindFile, "cannot open")
	})
	t.Run("a directory is not a registry", func(t *testing.T) {
		r, err := Load(t.TempDir())
		expectLoadError(t, r, err, KindFile, "regular file")
	})
	t.Run("empty file", func(t *testing.T) {
		r, err := Load(writeFile(t, nil))
		expectLoadError(t, r, err, KindSyntax, "not valid JSON")
	})
	t.Run("file larger than 1 MiB", func(t *testing.T) {
		big := append([]byte(`{"registry_version":1,"tools":[]}`), bytes.Repeat([]byte(" "), MaxFileBytes)...)
		r, err := Load(writeFile(t, big))
		expectLoadError(t, r, err, KindFile, "larger than")
	})
	t.Run("file of exactly 1 MiB is read (and then judged on its content)", func(t *testing.T) {
		doc := marshal(t, document(baseTool("a.b")))
		padded := append(doc, bytes.Repeat([]byte(" "), MaxFileBytes-len(doc))...)
		if len(padded) != MaxFileBytes {
			t.Fatalf("test file is %d bytes", len(padded))
		}
		r, err := Load(writeFile(t, padded))
		if err != nil || !r.Has("a.b") {
			t.Fatalf("a file of exactly the limit must load: %v", err)
		}
	})
	t.Run("one byte over the limit is rejected by Parse as well", func(t *testing.T) {
		doc := marshal(t, document(baseTool("a.b")))
		padded := append(doc, bytes.Repeat([]byte(" "), MaxFileBytes+1-len(doc))...)
		r, err := Parse(padded)
		expectLoadError(t, r, err, KindFile, "larger than")
	})
}

func TestLoadReadsAtMostTheLimit(t *testing.T) {
	// A 200 MiB sparse file must be refused from its size alone, without the
	// loader trying to read it.
	path := filepath.Join(t.TempDir(), "huge.json")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(200 << 20); err != nil {
		f.Close()
		t.Skipf("cannot create a sparse file here: %v", err)
	}
	f.Close()
	r, err := Load(path)
	expectLoadError(t, r, err, KindFile, "larger than")
}

// ---------------------------------------------------------------------------
// Syntax: strict JSON rules inherited from internal/canonical

func TestParseRejectsBadJSON(t *testing.T) {
	tool := string(marshal(t, baseTool("a.b")))
	tests := []struct {
		name string
		text string
		kind LoadKind
		want string
	}{
		{"empty", ``, KindSyntax, "not valid JSON"},
		{"only whitespace", " \n\t ", KindSyntax, "not valid JSON"},
		{"not JSON", `tools`, KindSyntax, "not valid JSON"},
		{"truncated", `{"registry_version":1,"tools":[`, KindSyntax, "not valid JSON"},
		{"trailing comma", `{"registry_version":1,"tools":[],}`, KindSyntax, "not valid JSON"},
		{"comment", `{"registry_version":1,"tools":[]} // c`, KindSyntax, "not valid JSON"},
		{"two documents", `{"registry_version":1,"tools":[` + tool + `]}{}`, KindSyntax, "not valid JSON"},
		{"duplicate top-level key", `{"registry_version":1,"registry_version":1,"tools":[` + tool + `]}`, KindSyntax, "duplicate"},
		{"duplicate key inside a tool", `{"registry_version":1,"tools":[{"name":"a.b","name":"c.d"}]}`, KindSyntax, "duplicate"},
		{"duplicate key inside a schema", `{"registry_version":1,"tools":[{"name":"a.b","version":1,"risk":"read","executor":"x","review_fields":[],"audit_fields":[],` +
			`"args_schema":{"type":"object","type":"object"}}]}`, KindSyntax, "duplicate"},
		{"invalid UTF-8", "{\"registry_version\":1,\"tools\":[],\"x\":\"\xff\"}", KindSyntax, "UTF-8"},
		{"UTF-8 byte-order mark", "\xef\xbb\xbf" + `{"registry_version":1,"tools":[` + tool + `]}`, KindSyntax, "byte-order mark"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := Parse([]byte(tt.text))
			expectLoadError(t, r, err, tt.kind, tt.want)
		})
	}
}

func TestParseDuplicateKeysAreFoundAfterDecodingEscapes(t *testing.T) {
	// "tools" spelled with a JSON escape is the same key as "tools".
	text := strings.ReplaceAll(`{"tools":[],"~u0074ools":[],"registry_version":1}`, "~u", `\u`)
	r, err := Parse([]byte(text))
	expectLoadError(t, r, err, KindSyntax, "duplicate")
}

func TestParseEnforcesParserLimits(t *testing.T) {
	t.Run("string over 8192 bytes", func(t *testing.T) {
		tl := baseTool("a.b")
		tl["args_schema"].(obj)["description"] = strings.Repeat("x", MaxStringBytes+1)
		r, err := parseDoc(t, document(tl))
		expectLoadError(t, r, err, KindSyntax, "8192")
	})
	t.Run("string of exactly 8192 bytes", func(t *testing.T) {
		tl := baseTool("a.b")
		tl["args_schema"].(obj)["description"] = strings.Repeat("x", MaxStringBytes)
		mustParseDoc(t, document(tl))
	})
	t.Run("array over 256 elements", func(t *testing.T) {
		tl := baseTool("a.b")
		many := make([]any, MaxArrayLen+1)
		for i := range many {
			many[i] = fmt.Sprintf("v%d", i)
		}
		tl["args_schema"].(obj)["properties"].(obj)["a"].(obj)["enum"] = many
		r, err := parseDoc(t, document(tl))
		expectLoadError(t, r, err, KindSyntax, "256")
	})
	t.Run("array of exactly 256 elements", func(t *testing.T) {
		tl := baseTool("a.b")
		many := make([]any, MaxArrayLen)
		for i := range many {
			many[i] = fmt.Sprintf("v%d", i)
		}
		tl["args_schema"].(obj)["properties"].(obj)["a"].(obj)["enum"] = many
		mustParseDoc(t, document(tl))
	})
	t.Run("tools array over 256 hits the array limit first", func(t *testing.T) {
		tools := make([]obj, MaxArrayLen+1)
		for i := range tools {
			tools[i] = baseTool(fmt.Sprintf("t.n%d", i))
		}
		r, err := parseDoc(t, document(tools...))
		expectLoadError(t, r, err, KindSyntax, "256")
	})
}

// ---------------------------------------------------------------------------
// Structure

func TestParseStructureErrors(t *testing.T) {
	good := baseTool("a.b")
	tests := []struct {
		name string
		doc  any
		want string
	}{
		{"root is an array", []any{}, "must be a JSON object"},
		{"root is a string", "x", "must be a JSON object"},
		{"unknown top-level field", obj{"registry_version": 1, "tools": []any{good}, "extra": 1}, `unknown field extra`},
		{"missing registry_version", obj{"tools": []any{good}}, "registry_version"},
		{"missing tools", obj{"registry_version": 1}, `"tools"`},
		{"registry_version is a string", obj{"registry_version": "1", "tools": []any{good}}, "registry_version"},
		{"registry_version 2", obj{"registry_version": 2, "tools": []any{good}}, "registry_version"},
		{"registry_version 0", obj{"registry_version": 0, "tools": []any{good}}, "registry_version"},
		{"registry_version 1.0", obj{"registry_version": 1.5, "tools": []any{good}}, "registry_version"},
		{"tools is an object", obj{"registry_version": 1, "tools": obj{}}, "must be an array"},
		{"tools is empty", obj{"registry_version": 1, "tools": []any{}}, "at least one tool"},
		{"tool entry is a string", obj{"registry_version": 1, "tools": []any{"orders.lookup"}}, "must be a JSON object"},
		{"tool entry is null", obj{"registry_version": 1, "tools": []any{nil}}, "must be a JSON object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := parseDoc(t, tt.doc)
			expectLoadError(t, r, err, KindStructure, tt.want)
		})
	}
}

func TestParseRegistryVersionMustBeExactlyOne(t *testing.T) {
	// Number spellings of one that are not the plain integer 1.
	for _, text := range []string{"1.0", "1e0", "01", "+1", "-1", "10", "true", "null", `"1"`} {
		t.Run(text, func(t *testing.T) {
			doc := `{"registry_version":` + text + `,"tools":[` + string(marshal(t, baseTool("a.b"))) + `]}`
			r, err := Parse([]byte(doc))
			if err == nil {
				t.Fatalf("registry_version %s was accepted", text)
			}
			if r != nil {
				t.Fatal("a failed load returned a registry")
			}
		})
	}
}

func TestParseToolEntryStructure(t *testing.T) {
	for _, field := range toolFields {
		t.Run("missing "+field, func(t *testing.T) {
			tl := baseTool("a.b")
			delete(tl, field)
			r, err := parseDoc(t, document(tl))
			expectLoadError(t, r, err, KindStructure, "missing required field", field, "tools[0]")
		})
	}
	t.Run("unknown tool field", func(t *testing.T) {
		tl := baseTool("a.b")
		tl["owner"] = "me"
		r, err := parseDoc(t, document(tl))
		expectLoadError(t, r, err, KindStructure, `unknown field owner`, "tools[0]")
	})
	t.Run("unknown field is named in the right tool", func(t *testing.T) {
		second := baseTool("c.d")
		second["timeout_ms"] = 5
		r, err := parseDoc(t, document(baseTool("a.b"), second))
		expectLoadError(t, r, err, KindStructure, "tools[1]", "timeout_ms")
	})
	t.Run("agent permissions are not registry fields", func(t *testing.T) {
		for _, f := range []string{"allowed_agents", "roles", "approval_required", "policy"} {
			tl := baseTool("a.b")
			tl[f] = []any{"x"}
			r, err := parseDoc(t, document(tl))
			expectLoadError(t, r, err, KindStructure, "unknown field")
		}
	})
	wrongTypes := []struct {
		field string
		value any
		kind  LoadKind
	}{
		{"name", 7, KindStructure},
		{"name", nil, KindStructure},
		{"risk", 7, KindStructure},
		{"executor", []any{}, KindStructure},
		{"review_fields", "a", KindStructure},
		{"review_fields", obj{}, KindStructure},
		{"audit_fields", nil, KindStructure},
		{"review_fields", []any{1}, KindStructure},
		{"audit_fields", []any{obj{}}, KindStructure},
		{"args_schema", "{}", KindStructure},
		{"args_schema", []any{}, KindStructure},
		{"args_schema", true, KindStructure},
		{"args_schema", nil, KindStructure},
	}
	for _, w := range wrongTypes {
		t.Run(fmt.Sprintf("%s=%v", w.field, w.value), func(t *testing.T) {
			tl := baseTool("a.b")
			tl[w.field] = w.value
			r, err := parseDoc(t, document(tl))
			expectLoadError(t, r, err, w.kind, w.field)
		})
	}
}

// ---------------------------------------------------------------------------
// Metadata

func TestParseToolNames(t *testing.T) {
	valid := []string{
		"a", "orders.lookup", "payments.refund", "accounts.close", "a1", "a_b", "a.b.c.d",
		"x9.y_z", strings.Repeat("a", 64),
	}
	for _, name := range valid {
		t.Run("valid "+name, func(t *testing.T) {
			if !ValidToolName(name) {
				t.Fatalf("ValidToolName(%q) = false", name)
			}
			r := mustParseDoc(t, document(baseTool(name)))
			if !r.Has(name) {
				t.Fatalf("%q was not registered", name)
			}
		})
	}

	invalid := []string{
		"", "Orders.lookup", "orders.Lookup", "orders lookup", "orders-lookup", "orders/lookup",
		".orders", "orders.", "orders..lookup", "1orders", "orders.1lookup", "_orders", "orders.lookup ",
		" orders.lookup", "orders\nlookup", "orders\x00", "ordérs.lookup", "ｏｒｄｅｒｓ", "../etc/passwd",
		"orders:lookup", "orders*", strings.Repeat("a", 65),
	}
	for _, name := range invalid {
		t.Run(fmt.Sprintf("invalid %q", name), func(t *testing.T) {
			if ValidToolName(name) {
				t.Fatalf("ValidToolName(%q) = true", name)
			}
			r, err := parseDoc(t, document(baseTool(name)))
			expectLoadError(t, r, err, KindMetadata, "invalid tool name", "tools[0].name")
		})
	}
}

func TestParseDuplicateToolNames(t *testing.T) {
	r, err := parseDoc(t, document(baseTool("a.b"), baseTool("c.d"), baseTool("a.b")))
	expectLoadError(t, r, err, KindMetadata, "duplicate tool name", "tools[2]", "tools[0]")

	// Names differ only by version: still a duplicate. One name, one tool.
	second := baseTool("a.b")
	second["version"] = 2
	r, err = parseDoc(t, document(baseTool("a.b"), second))
	expectLoadError(t, r, err, KindMetadata, "duplicate tool name")
}

func TestParseToolVersions(t *testing.T) {
	for _, v := range []string{"1", "2", "42", "999999999"} {
		t.Run("valid "+v, func(t *testing.T) {
			text := `{"registry_version":1,"tools":[` + strings.Replace(string(marshal(t, baseTool("a.b"))), `"version":1`, `"version":`+v, 1) + `]}`
			r, err := Parse([]byte(text))
			if err != nil {
				t.Fatalf("version %s rejected: %v", v, err)
			}
			if info, _ := r.Tool("a.b"); fmt.Sprint(info.Version) != v {
				t.Fatalf("version = %d, want %s", info.Version, v)
			}
		})
	}
	for _, v := range []string{"0", "-1", "1.0", "1.5", "1e0", "01", "+1", "1000000000", "99999999999999999999", `"1"`, "true", "null", "[]", "{}"} {
		t.Run("invalid "+v, func(t *testing.T) {
			text := `{"registry_version":1,"tools":[` + strings.Replace(string(marshal(t, baseTool("a.b"))), `"version":1`, `"version":`+v, 1) + `]}`
			r, err := Parse([]byte(text))
			if err == nil {
				t.Fatalf("version %s was accepted", v)
			}
			if r != nil {
				t.Fatal("a failed load returned a registry")
			}
			var le *LoadError
			if !errors.As(err, &le) {
				t.Fatalf("error is %T, want *LoadError", err)
			}
			// "01" and "+1" are not JSON numbers at all, so the strict parser
			// refuses the whole file; every other spelling reaches the field.
			if v == "01" || v == "+1" {
				if le.Kind != KindSyntax {
					t.Fatalf("kind = %v, want syntax", le.Kind)
				}
			} else if !strings.Contains(le.Where, "version") {
				t.Fatalf("error should point at the version field: %v", err)
			}
		})
	}
}

func TestParseRisk(t *testing.T) {
	for _, risk := range []string{"read", "write"} {
		tl := baseTool("a.b")
		tl["risk"] = risk
		r := mustParseDoc(t, document(tl))
		if info, _ := r.Tool("a.b"); string(info.Risk) != risk {
			t.Errorf("risk = %q, want %q", info.Risk, risk)
		}
	}
	for _, risk := range []string{"", "READ", "Write", "admin", "readwrite", "read ", "none", "high", "delete"} {
		t.Run(fmt.Sprintf("invalid %q", risk), func(t *testing.T) {
			tl := baseTool("a.b")
			tl["risk"] = risk
			r, err := parseDoc(t, document(tl))
			expectLoadError(t, r, err, KindMetadata, `"read" or "write"`, "tools[0].risk")
		})
	}
}

func TestParseExecutorIdentifier(t *testing.T) {
	for _, ex := range []string{"mock.orders", "orders", "svc_1.x", strings.Repeat("a", 64)} {
		tl := baseTool("a.b")
		tl["executor"] = ex
		mustParseDoc(t, document(tl))
	}
	for _, ex := range []string{"", "Mock.orders", "mock orders", "mock-orders", "http://evil", "/bin/sh", "a/b", ".x", "x.", "1x",
		strings.Repeat("a", 65), "exec\x00", "mock.orders;rm"} {
		t.Run(fmt.Sprintf("invalid %q", ex), func(t *testing.T) {
			tl := baseTool("a.b")
			tl["executor"] = ex
			r, err := parseDoc(t, document(tl))
			expectLoadError(t, r, err, KindMetadata, "executor", "metadata only")
		})
	}
}

func TestExecutorIsMetadataOnly(t *testing.T) {
	// The executor field is stored and returned and nothing more. Registering
	// a tool whose executor names something that does not exist, or something
	// dangerous-looking but grammatical, must not run or resolve anything.
	tl := baseTool("a.b")
	tl["executor"] = "mock.does_not_exist"
	r := mustParseDoc(t, document(tl))
	info, _ := r.Tool("a.b")
	if info.Executor != "mock.does_not_exist" {
		t.Fatalf("executor = %q", info.Executor)
	}
}

func TestParseReviewAndAuditFields(t *testing.T) {
	schema := closedObject(obj{"a": obj{"type": "string"}, "b": obj{"type": "integer"}}, "a", "b")

	build := func(review, audit []any) obj {
		tl := baseTool("a.b")
		tl["args_schema"] = schema
		tl["review_fields"] = review
		tl["audit_fields"] = audit
		return tl
	}

	t.Run("valid, including empty lists and any order", func(t *testing.T) {
		mustParseDoc(t, document(build([]any{"b", "a"}, []any{"a"})))
		mustParseDoc(t, document(build([]any{}, []any{})))
	})
	t.Run("review field that is not declared", func(t *testing.T) {
		r, err := parseDoc(t, document(build([]any{"a", "zzz"}, []any{"a"})))
		expectLoadError(t, r, err, KindMetadata, "review_fields[1]", "zzz is not a top-level property")
	})
	t.Run("audit field that is not declared", func(t *testing.T) {
		r, err := parseDoc(t, document(build([]any{"a"}, []any{"nope"})))
		expectLoadError(t, r, err, KindMetadata, "audit_fields[0]", "nope is not a top-level property")
	})
	t.Run("a nested property is not a top-level property", func(t *testing.T) {
		nested := closedObject(obj{"outer": closedObject(obj{"inner": obj{"type": "string"}})})
		tl := baseTool("a.b")
		tl["args_schema"] = nested
		tl["review_fields"] = []any{"inner"}
		r, err := parseDoc(t, document(tl))
		expectLoadError(t, r, err, KindMetadata, "review_fields[0]")
	})
	t.Run("a path is not a field name", func(t *testing.T) {
		nested := closedObject(obj{"outer": closedObject(obj{"inner": obj{"type": "string"}})})
		tl := baseTool("a.b")
		tl["args_schema"] = nested
		tl["review_fields"] = []any{"outer.inner"}
		r, err := parseDoc(t, document(tl))
		expectLoadError(t, r, err, KindMetadata, "review_fields[0]")
	})
	t.Run("duplicate entries", func(t *testing.T) {
		r, err := parseDoc(t, document(build([]any{"a", "a"}, []any{"a"})))
		expectLoadError(t, r, err, KindMetadata, "listed twice")
	})
	t.Run("empty entry", func(t *testing.T) {
		r, err := parseDoc(t, document(build([]any{""}, []any{})))
		expectLoadError(t, r, err, KindMetadata, "must not be empty")
	})
	t.Run("case matters", func(t *testing.T) {
		r, err := parseDoc(t, document(build([]any{"A"}, []any{})))
		expectLoadError(t, r, err, KindMetadata, "review_fields[0]")
	})
	t.Run("any tool without declared properties has no fields", func(t *testing.T) {
		tl := baseTool("a.b")
		tl["args_schema"] = obj{"type": "object", "additionalProperties": false}
		tl["review_fields"] = []any{"a"}
		r, err := parseDoc(t, document(tl))
		expectLoadError(t, r, err, KindMetadata, "review_fields[0]")
	})
}

func TestParseMaxTools(t *testing.T) {
	build := func(n int) []obj {
		tools := make([]obj, n)
		for i := range tools {
			tools[i] = baseTool(fmt.Sprintf("t.n%d", i))
		}
		return tools
	}
	r := mustParseDoc(t, document(build(MaxTools)...))
	if r.Len() != MaxTools {
		t.Fatalf("Len() = %d, want %d", r.Len(), MaxTools)
	}
	got, err := parseDoc(t, document(build(MaxTools+1)...))
	expectLoadError(t, got, err, KindStructure, "more than the maximum of 32")
}

func TestNoPartialRegistryOnAnyFailure(t *testing.T) {
	// Three good tools followed by one bad tool: nothing at all is returned.
	bad := baseTool("e.f")
	bad["args_schema"] = obj{"type": "object"} // not closed
	r, err := parseDoc(t, document(baseTool("a.b"), baseTool("c.d"), baseTool("g.h"), bad))
	expectLoadError(t, r, err, KindSchema, "additionalProperties")

	// Same for a bad tool first.
	r, err = parseDoc(t, document(bad, baseTool("a.b")))
	expectLoadError(t, r, err, KindSchema, "additionalProperties")
}

func TestErrorsNameTheToolAndLocation(t *testing.T) {
	bad := baseTool("payments.refund")
	bad["args_schema"] = closedObject(obj{"n": obj{"type": "object"}})
	r, err := parseDoc(t, document(baseTool("a.b"), bad))
	expectLoadError(t, r, err, KindSchema, "payments.refund", "tools[1].args_schema#/properties/n", "additionalProperties")
}

func TestLoadErrorMessagesDoNotEchoOperatorSecretsInReferences(t *testing.T) {
	// A $ref URL can carry credentials; its text must never reach the error.
	canary := "SECRETTOKEN123"
	schema := closedObject(obj{"a": obj{"$ref": "https://user:" + canary + "@example.com/x.json?k=" + canary}})
	r, err := parseDoc(t, document(withSchema("a.b", schema)))
	expectLoadError(t, r, err, KindSchema, "external references")
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("the error echoes the reference: %v", err)
	}
}

func TestLoadErrorUnwraps(t *testing.T) {
	r, err := parseDoc(t, document(withSchema("a.b", closedObject(obj{"a": obj{"type": 5}}))))
	if r != nil || err == nil {
		t.Fatalf("expected a failure, got %v %v", r, err)
	}
	var le *LoadError
	if !errors.As(err, &le) || le.Unwrap() == nil {
		t.Fatalf("a compile failure should carry its cause: %#v", err)
	}
	if LoadKind(0).String() != "unknown" || KindFile.String() != "file" || KindSyntax.String() != "syntax" ||
		KindStructure.String() != "structure" || KindMetadata.String() != "metadata" || KindSchema.String() != "schema" {
		t.Error("LoadKind.String values changed")
	}
}
