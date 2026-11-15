package registry

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// parseSchema runs a schema through the full registry loader as the only tool.
func parseSchema(t *testing.T, schema any) (*Registry, error) {
	t.Helper()
	return parseDoc(t, document(withSchema("a.b", schema)))
}

func expectSchemaOK(t *testing.T, schema any) *Registry {
	t.Helper()
	r, err := parseSchema(t, schema)
	if err != nil {
		t.Fatalf("schema rejected: %v\n%s", err, marshal(t, schema))
	}
	return r
}

func expectSchemaRejected(t *testing.T, schema any, want ...string) {
	t.Helper()
	r, err := parseSchema(t, schema)
	expectLoadError(t, r, err, KindSchema, want...)
}

// ---------------------------------------------------------------------------
// Root rules

func TestRootSchemaRules(t *testing.T) {
	t.Run("a closed object schema is accepted", func(t *testing.T) {
		expectSchemaOK(t, closedObject(obj{"a": obj{"type": "string"}}, "a"))
	})
	t.Run("no properties at all is allowed", func(t *testing.T) {
		expectSchemaOK(t, obj{"type": "object", "additionalProperties": false})
	})
	t.Run("missing additionalProperties", func(t *testing.T) {
		expectSchemaRejected(t, obj{"type": "object", "properties": obj{}}, `"additionalProperties": false`, "missing")
	})
	t.Run("additionalProperties true", func(t *testing.T) {
		expectSchemaRejected(t, obj{"type": "object", "additionalProperties": true}, "args_schema#/additionalProperties")
	})
	t.Run("additionalProperties as a schema", func(t *testing.T) {
		expectSchemaRejected(t, obj{"type": "object", "additionalProperties": obj{"type": "string"}}, `"additionalProperties": false`)
	})
	t.Run("additionalProperties as a string or null", func(t *testing.T) {
		expectSchemaRejected(t, obj{"type": "object", "additionalProperties": "false"}, `"additionalProperties": false`)
		expectSchemaRejected(t, obj{"type": "object", "additionalProperties": nil}, `"additionalProperties": false`)
	})
	t.Run("missing type", func(t *testing.T) {
		expectSchemaRejected(t, obj{"additionalProperties": false}, `"type": "object"`)
	})
	t.Run("type is not object", func(t *testing.T) {
		for _, typ := range []any{"string", "array", "integer", "null", "boolean", "number"} {
			expectSchemaRejected(t, obj{"type": typ, "additionalProperties": false}, `"type": "object"`)
		}
	})
	t.Run("type given as an array", func(t *testing.T) {
		expectSchemaRejected(t, obj{"type": []any{"object"}, "additionalProperties": false}, `"type": "object"`)
		expectSchemaRejected(t, obj{"type": []any{"object", "null"}, "additionalProperties": false}, `"type": "object"`)
	})
	t.Run("boolean root", func(t *testing.T) {
		r, err := parseSchema(t, true)
		expectLoadError(t, r, err, KindStructure, "must be a JSON object")
	})
}

func TestRootSchemaKeywordDoesNotGrantArgumentsAnyType(t *testing.T) {
	// Even a schema that is otherwise fine is refused when it opens the root.
	expectSchemaRejected(t, obj{"type": "object", "additionalProperties": true, "properties": obj{"a": obj{"type": "string"}}})
}

func TestSchemaDeclarationKeyword(t *testing.T) {
	good := "https://json-schema.org/draft/2020-12/schema"
	t.Run("Draft 2020-12 is accepted", func(t *testing.T) {
		s := closedObject(obj{})
		s["$schema"] = good
		expectSchemaOK(t, s)
	})
	t.Run("no $schema means Draft 2020-12", func(t *testing.T) {
		expectSchemaOK(t, closedObject(obj{}))
	})
	for _, other := range []any{
		"http://json-schema.org/draft-07/schema#",
		"http://json-schema.org/draft-07/schema",
		"https://json-schema.org/draft/2019-09/schema",
		"http://json-schema.org/draft-04/schema#",
		good + "#",
		"http://json-schema.org/draft/2020-12/schema",
		"https://json-schema.org/draft/2020-12/schema ",
		"https://example.com/my-metaschema",
		"",
		5,
		nil,
	} {
		t.Run(fmt.Sprintf("rejected %v", other), func(t *testing.T) {
			s := closedObject(obj{})
			s["$schema"] = other
			expectSchemaRejected(t, s, "$schema")
		})
	}
	t.Run("nested $schema is rejected", func(t *testing.T) {
		inner := obj{"type": "string", "$schema": good}
		expectSchemaRejected(t, closedObject(obj{"a": inner}), "only allowed on the root", "#/properties/a/$schema")
	})
}

func TestForbiddenKeywords(t *testing.T) {
	for _, kw := range forbiddenKeywords {
		t.Run("root "+kw, func(t *testing.T) {
			s := closedObject(obj{})
			s[kw] = "https://example.com/x"
			expectSchemaRejected(t, s, kw, "not allowed")
		})
		t.Run("nested "+kw, func(t *testing.T) {
			s := closedObject(obj{"a": obj{"type": "string", kw: "x"}})
			expectSchemaRejected(t, s, kw, "#/properties/a")
		})
	}
	t.Run("a property merely named like a keyword is fine", func(t *testing.T) {
		props := obj{}
		for _, name := range append([]string{"$ref", "$schema", "type", "items", "properties", "additionalProperties", "enum", "const"}, forbiddenKeywords...) {
			props[name] = obj{"type": "string"}
		}
		expectSchemaOK(t, closedObject(props))
	})
	t.Run("$anchor and $comment are harmless", func(t *testing.T) {
		s := closedObject(obj{"a": obj{"type": "string", "$anchor": "a1", "$comment": "note"}})
		s["$comment"] = "root note"
		expectSchemaOK(t, s)
	})
}

// ---------------------------------------------------------------------------
// additionalProperties:false on nested object schemas, at every schema position

// schemaPositions describes every keyword position that holds a subschema, and
// the JSON Pointer of that subschema under the root.
var schemaPositions = []struct {
	name    string
	pointer string
	place   func(root obj, sub any)
}{
	{"properties", "/properties/p", func(r obj, s any) { r["properties"] = obj{"p": s} }},
	{"patternProperties", "/patternProperties/^x_", func(r obj, s any) { r["patternProperties"] = obj{"^x_": s} }},
	{"$defs", "/$defs/d", func(r obj, s any) { r["$defs"] = obj{"d": s} }},
	{"dependentSchemas", "/dependentSchemas/a", func(r obj, s any) { r["dependentSchemas"] = obj{"a": s} }},
	{"items", "/items", func(r obj, s any) { r["items"] = s }},
	{"prefixItems", "/prefixItems/0", func(r obj, s any) { r["prefixItems"] = []any{s} }},
	{"allOf", "/allOf/0", func(r obj, s any) { r["allOf"] = []any{s} }},
	{"anyOf", "/anyOf/0", func(r obj, s any) { r["anyOf"] = []any{s} }},
	{"oneOf", "/oneOf/0", func(r obj, s any) { r["oneOf"] = []any{s} }},
	{"not", "/not", func(r obj, s any) { r["not"] = s }},
	{"if", "/if", func(r obj, s any) { r["if"] = s }},
	{"then", "/then", func(r obj, s any) { r["then"] = s }},
	{"else", "/else", func(r obj, s any) { r["else"] = s }},
	{"contains", "/contains", func(r obj, s any) { r["contains"] = s }},
	{"propertyNames", "/propertyNames", func(r obj, s any) { r["propertyNames"] = s }},
	{"unevaluatedItems", "/unevaluatedItems", func(r obj, s any) { r["unevaluatedItems"] = s }},
	{"unevaluatedProperties", "/unevaluatedProperties", func(r obj, s any) { r["unevaluatedProperties"] = s }},
	{"contentSchema", "/contentSchema", func(r obj, s any) { r["contentSchema"] = s }},
}

func TestNestedObjectSchemasMustBeClosedAtEveryPosition(t *testing.T) {
	for _, pos := range schemaPositions {
		t.Run(pos.name, func(t *testing.T) {
			open := obj{"type": "object"}
			root := obj{"type": "object", "additionalProperties": false}
			pos.place(root, open)
			expectSchemaRejected(t, root, `"additionalProperties": false`, "args_schema#"+pos.pointer)

			closed := obj{"type": "object", "additionalProperties": false}
			okRoot := obj{"type": "object", "additionalProperties": false}
			pos.place(okRoot, closed)
			expectSchemaOK(t, okRoot)

			// additionalProperties set to anything but false is refused too.
			loose := obj{"type": "object", "additionalProperties": true}
			badRoot := obj{"type": "object", "additionalProperties": false}
			pos.place(badRoot, loose)
			expectSchemaRejected(t, badRoot, "args_schema#"+pos.pointer+"/additionalProperties")
		})
	}
}

func TestWhichSchemasCountAsObjectSchemas(t *testing.T) {
	closedRoot := func(p obj) obj { return closedObject(obj{"p": p}) }

	needsClosing := map[string]obj{
		"type object":            {"type": "object"},
		"type array with object": {"type": []any{"object", "null"}},
		"properties only":        {"properties": obj{"x": obj{"type": "string"}}},
		"patternProperties only": {"patternProperties": obj{"^a": obj{"type": "string"}}},
		"additionalProperties":   {"additionalProperties": obj{"type": "string"}},
		"unevaluatedProperties":  {"unevaluatedProperties": false},
	}
	for name, s := range needsClosing {
		t.Run("must close: "+name, func(t *testing.T) {
			expectSchemaRejected(t, closedRoot(s), "additionalProperties")
		})
	}

	fine := map[string]obj{
		"string":                {"type": "string"},
		"integer with bounds":   {"type": "integer", "minimum": 1},
		"array of strings":      {"type": "array", "items": obj{"type": "string"}},
		"nullable string":       {"type": []any{"string", "null"}},
		"enum":                  {"enum": []any{"a", "b"}},
		"const":                 {"const": "x"},
		"anyOf of scalars":      {"anyOf": []any{obj{"type": "string"}, obj{"type": "null"}}},
		"required only":         {"required": []any{"a"}},
		"minProperties only":    {"minProperties": 1},
		"dependentRequired":     {"dependentRequired": obj{"a": []any{"b"}}},
		"propertyNames pattern": {"propertyNames": obj{"pattern": "^[a-z]+$"}},
		"description only":      {"description": "anything"},
	}
	for name, s := range fine {
		t.Run("no closing needed: "+name, func(t *testing.T) {
			expectSchemaOK(t, closedRoot(s))
		})
	}

	t.Run("an anyOf branch that is a constraint fragment is fine", func(t *testing.T) {
		root := closedObject(obj{"a": obj{"type": "string"}, "b": obj{"type": "string"}})
		root["anyOf"] = []any{obj{"required": []any{"a"}}, obj{"required": []any{"b"}}}
		expectSchemaOK(t, root)
	})
	t.Run("boolean subschemas are accepted", func(t *testing.T) {
		expectSchemaOK(t, closedObject(obj{"never": false, "anything": true}))
	})
}

func TestDeeplyNestedObjectsAreAllChecked(t *testing.T) {
	leaf := obj{"type": "object"} // open
	deep := closedObject(obj{
		"l1": closedObject(obj{
			"l2": obj{"type": "array", "items": obj{"anyOf": []any{obj{"type": "string"}, leaf}}},
		}),
	})
	expectSchemaRejected(t, deep, "#/properties/l1/properties/l2/items/anyOf/1")

	leaf["additionalProperties"] = false
	expectSchemaOK(t, deep)
}

func TestNonSchemaPositionsAreNotWalked(t *testing.T) {
	// The values of const, enum, default and examples are data. An object in
	// there is not a subschema, and a "$ref" or "$id" inside it is not a
	// reference or an identifier.
	open := obj{"type": "object", "$ref": "https://example.com/x.json", "$id": "https://evil.example/x", "properties": obj{}}
	s := closedObject(obj{
		"a": obj{"type": "object", "additionalProperties": false, "default": open, "examples": []any{open, open}},
		"b": obj{"const": open},
		"c": obj{"enum": []any{open, "x", 1, nil, []any{open}}},
	})
	s["default"] = open
	s["examples"] = []any{open}
	s["x-vendor-extension"] = open
	s["x-notes"] = obj{"$ref": "file:///etc/passwd", "additionalProperties": true}
	expectSchemaOK(t, s)
}

func TestSchemaPositionsMustHaveTheRightShape(t *testing.T) {
	cases := map[string]obj{
		"properties as array":        {"properties": []any{}},
		"properties as string":       {"properties": "x"},
		"properties value is string": {"properties": obj{"a": "string"}},
		"properties value is number": {"properties": obj{"a": 5}},
		"properties value is null":   {"properties": obj{"a": nil}},
		"$defs as array":             {"$defs": []any{}},
		"allOf as object":            {"allOf": obj{}},
		"anyOf element is a string":  {"anyOf": []any{"x"}},
		"oneOf as string":            {"oneOf": "x"},
		"items as array (old form)":  {"items": []any{obj{"type": "string"}}},
		"not as string":              {"not": "x"},
		"if as number":               {"if": 1},
		"prefixItems as object":      {"prefixItems": obj{}},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			root := obj{"type": "object", "additionalProperties": false}
			for k, v := range extra {
				root[k] = v
			}
			r, err := parseSchema(t, root)
			expectLoadError(t, r, err, KindSchema)
		})
	}
}

// ---------------------------------------------------------------------------
// References

func refSchema(ref any) obj {
	root := closedObject(obj{"p": obj{"$ref": ref}})
	root["$defs"] = obj{
		"id":     obj{"type": "string", "pattern": "^[a-z]+$"},
		"a/b":    obj{"type": "string"},
		"a~b":    obj{"type": "string"},
		"a b":    obj{"type": "string"},
		"nested": obj{"type": "object", "additionalProperties": false, "properties": obj{"x": obj{"type": "string"}}},
	}
	root["allOf"] = []any{obj{"type": "object"}}
	root["allOf"] = []any{obj{"type": "object", "additionalProperties": false}}
	root["default"] = obj{"type": "object"}
	root["const"] = obj{"type": "object"}
	return root
}

func TestFragmentLocalReferencesAreAllowed(t *testing.T) {
	allowed := []string{
		"#/$defs/id",
		"#/$defs/nested",
		"#/$defs/nested/properties/x",
		"#/$defs/a~1b",
		"#/$defs/a~0b",
		"#/$defs/a%20b",
		"#/allOf/0",
		"#/properties/p",
		"#",
	}
	for _, ref := range allowed {
		t.Run(ref, func(t *testing.T) {
			expectSchemaOK(t, refSchema(ref))
		})
	}
}

func TestRecursiveLocalReferenceCompiles(t *testing.T) {
	root := closedObject(obj{"child": obj{"anyOf": []any{obj{"type": "integer"}, obj{"$ref": "#"}}}})
	r := expectSchemaOK(t, root)
	expectArgs(t, r, "a.b", `{"child":{"child":{"child":1}}}`, nil)
	expectArgs(t, r, "a.b", `{"child":{"child":{"child":"x"}}}`, ErrInvalidArguments)
}

func TestReferencesMustPointAtSchemas(t *testing.T) {
	// Each of these is a syntactically fine pointer, but its target is data,
	// a container of schemas, or does not exist. The compiler would read a
	// data target as a schema without any of the registry's checks, so the
	// registry refuses them.
	for _, ref := range []string{
		"#/default",
		"#/const",
		"#/required",
		"#/properties",
		"#/$defs",
		"#/allOf",
		"#/properties/p/$ref",
		"#/$defs/id/pattern",
		"#/$defs/missing",
		"#/missing",
		"#/allOf/1",
		"#/allOf/00",
		"#/allOf/+0",
		"#/$defs/nested/required",
		"#/",
	} {
		t.Run(ref, func(t *testing.T) {
			expectSchemaRejected(t, refSchema(ref), "$ref", "schema location", "#/properties/p/$ref")
		})
	}
}

func TestExternalReferencesAreRejected(t *testing.T) {
	external := []string{
		"http://example.com/schema.json",
		"https://example.com/schema.json",
		"https://example.com/schema.json#/$defs/x",
		"HTTPS://EXAMPLE.COM/S.JSON",
		"https://json-schema.org/draft/2020-12/schema", // even a meta-schema URL
		"http://json-schema.org/draft-07/schema#",
		"file:///etc/passwd",
		"file://localhost/etc/passwd",
		"FILE:///c:/windows/win.ini",
		"ftp://example.com/s.json",
		"gopher://example.com/",
		"data:application/json,{}",
		"urn:uuid:3d7b3a52-0c3f-4a74-8bb4-2f6f6c9d9e11",
		"jar:file:///x.jar!/s.json",
		"other.json",
		"./other.json",
		"../other.json",
		"/etc/passwd",
		"//example.com/s.json",
		`C:\windows\win.ini`,
		"other.json#/$defs/x",
		"#anchor",
		"#Foo",
		"",
		" #/$defs/id",
		"#/$defs/id ",
	}
	for _, ref := range external {
		t.Run(fmt.Sprintf("%q", ref), func(t *testing.T) {
			r, err := parseSchema(t, refSchema(ref))
			if err == nil {
				t.Fatalf("reference %q was accepted", ref)
			}
			expectLoadError(t, r, err, KindSchema, "$ref")
			// The reference text must not be echoed: it may hold credentials.
			if ref != "" && strings.Contains(err.Error(), ref) {
				t.Fatalf("the error repeats the reference: %v", err)
			}
		})
	}
}

func TestMalformedReferences(t *testing.T) {
	for _, ref := range []any{"#/%zz", "#/$defs/a~2b", "#/$defs/a~", "#/$defs/%", 5, nil, true, obj{}, []any{"#/$defs/id"}} {
		t.Run(fmt.Sprintf("%v", ref), func(t *testing.T) {
			expectSchemaRejected(t, refSchema(ref), "$ref")
		})
	}
}

func TestReferenceInsideEveryPositionIsChecked(t *testing.T) {
	for _, pos := range schemaPositions {
		t.Run(pos.name, func(t *testing.T) {
			root := obj{"type": "object", "additionalProperties": false}
			pos.place(root, obj{"$ref": "https://example.com/s.json"})
			expectSchemaRejected(t, root, "external references", "args_schema#"+pos.pointer+"/$ref")
		})
	}
}

// ---------------------------------------------------------------------------
// The compiler is never allowed to load anything

// recordingLoader refuses every URL like the production loader and remembers
// what was asked for.
type recordingLoader struct {
	mu   sync.Mutex
	urls []string
}

func (l *recordingLoader) Load(url string) (any, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.urls = append(l.urls, url)
	return nil, errExternalLoad
}

func (l *recordingLoader) asked() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.urls...)
}

func TestCompilerNeedsNoLoaderForMetaSchemas(t *testing.T) {
	// The Draft 2020-12 meta-schema is embedded in the library. Compiling a
	// schema that names it must not ask the loader for anything, which is what
	// lets the service start with no network at all.
	for _, withDeclaration := range []bool{true, false} {
		t.Run(fmt.Sprintf("explicit $schema=%v", withDeclaration), func(t *testing.T) {
			s := closedObject(obj{"a": obj{"type": "string", "format": "email"}})
			if withDeclaration {
				s["$schema"] = "https://json-schema.org/draft/2020-12/schema"
			}
			loader := &recordingLoader{}
			if _, err := compileSchema("a.b", 1, s, loader); err != nil {
				t.Fatalf("compile: %v", err)
			}
			if got := loader.asked(); len(got) != 0 {
				t.Fatalf("the compiler asked the loader for %v", got)
			}
		})
	}
}

func TestProductionLoaderRefusesEverything(t *testing.T) {
	var l refusingLoader
	for _, url := range []string{
		"https://example.com/x.json", "http://example.com/", "file:///etc/passwd", "/etc/passwd", "relative.json", "", "ftp://x",
	} {
		doc, err := l.Load(url)
		if err == nil || doc != nil {
			t.Errorf("Load(%q) = %v, %v; want a refusal", url, doc, err)
		}
	}
}

func TestCompilerReachesTheLoaderOnlyToBeRefused(t *testing.T) {
	// The walker rejects these schemas first. To prove the second line of
	// defense, hand them to the compiler directly and watch what it asks for.
	tests := []struct {
		name, ref, wantURL string
	}{
		{"https", "https://example.com/x.json", "https://example.com/x.json"},
		{"http", "http://example.com/x.json", "http://example.com/x.json"},
		{"file", "file:///etc/passwd", "file:///etc/passwd"},
		{"other scheme", "ftp://example.com/x.json", "ftp://example.com/x.json"},
		{"relative", "other.json", "https://sentinelapi.invalid/tools/a.b/v1/other.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := closedObject(obj{"p": obj{"$ref": tt.ref}})
			loader := &recordingLoader{}
			sch, err := compileSchema("a.b", 1, s, loader)
			if err == nil || sch != nil {
				t.Fatalf("an external reference compiled: %v", err)
			}
			if !strings.Contains(err.Error(), errExternalLoad.Error()) {
				t.Errorf("the failure should come from the refusing loader: %v", err)
			}
			got := loader.asked()
			if len(got) == 0 || !strings.HasPrefix(got[0], strings.SplitN(tt.wantURL, "#", 2)[0]) {
				t.Errorf("loader was asked for %v, want a request for %s", got, tt.wantURL)
			}
		})
	}
}

func TestResourceURLCannotResolve(t *testing.T) {
	u := resourceURL("orders.lookup", 3)
	if u != "https://sentinelapi.invalid/tools/orders.lookup/v3/args.json" {
		t.Errorf("resourceURL = %q", u)
	}
}

func TestCompileSurvivesUnencodableSchemas(t *testing.T) {
	_, err := compileSchema("a.b", 1, map[string]any{"type": "object", "x": func() {}}, refusingLoader{})
	if err == nil {
		t.Fatal("a schema with an unsupported value compiled")
	}
	_, err = compileSchema("a.b", 1, map[string]any{"type": "object", "x": make(chan int)}, refusingLoader{})
	if err == nil {
		t.Fatal("a schema with an unsupported value compiled")
	}
}

// ---------------------------------------------------------------------------
// The compiler validates the schema itself

func TestMalformedSchemasFailAtLoad(t *testing.T) {
	cases := map[string]obj{
		"type is a number":          {"type": "object", "additionalProperties": false, "properties": obj{"a": obj{"type": 5}}},
		"unknown type name":         {"type": "object", "additionalProperties": false, "properties": obj{"a": obj{"type": "strin"}}},
		"type array with duplicate": {"type": "object", "additionalProperties": false, "properties": obj{"a": obj{"type": []any{"string", "string"}}}},
		"invalid regular expression": {"type": "object", "additionalProperties": false,
			"properties": obj{"a": obj{"type": "string", "pattern": "("}}},
		"minimum is a string":        {"type": "object", "additionalProperties": false, "properties": obj{"a": obj{"type": "integer", "minimum": "1"}}},
		"maxLength is negative":      {"type": "object", "additionalProperties": false, "properties": obj{"a": obj{"type": "string", "maxLength": -1}}},
		"maxLength is a fraction":    {"type": "object", "additionalProperties": false, "properties": obj{"a": obj{"type": "string", "maxLength": 1.5}}},
		"required is a string":       {"type": "object", "additionalProperties": false, "required": "a"},
		"required has duplicates":    {"type": "object", "additionalProperties": false, "required": []any{"a", "a"}},
		"enum is not an array":       {"type": "object", "additionalProperties": false, "properties": obj{"a": obj{"enum": "x"}}},
		"format is a number":         {"type": "object", "additionalProperties": false, "properties": obj{"a": obj{"type": "string", "format": 5}}},
		"multipleOf is zero":         {"type": "object", "additionalProperties": false, "properties": obj{"a": obj{"type": "integer", "multipleOf": 0}}},
		"pattern is a number":        {"type": "object", "additionalProperties": false, "properties": obj{"a": obj{"type": "string", "pattern": 5}}},
		"minItems is a string":       {"type": "object", "additionalProperties": false, "properties": obj{"a": obj{"type": "array", "minItems": "1"}}},
		"dependentRequired is wrong": {"type": "object", "additionalProperties": false, "dependentRequired": obj{"a": "b"}},
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			r, err := parseSchema(t, s)
			expectLoadError(t, r, err, KindSchema, "a.b")
		})
	}
}

func TestSchemaDepthLimit(t *testing.T) {
	// nestedItems builds a closed root whose "items" chain makes the schema
	// exactly n JSON container levels deep.
	nestedItems := func(n int) obj {
		leaf := obj{"type": "string"}
		for i := 2; i < n; i++ {
			leaf = obj{"type": "array", "items": leaf}
		}
		if n == 1 {
			return obj{"type": "object", "additionalProperties": false}
		}
		return obj{"type": "object", "additionalProperties": false, "items": leaf}
	}

	t.Run("32 levels is accepted", func(t *testing.T) {
		expectSchemaOK(t, nestedItems(MaxSchemaDepth))
	})
	t.Run("33 levels is rejected by the parser limit", func(t *testing.T) {
		r, err := parseSchema(t, nestedItems(MaxSchemaDepth+1))
		expectLoadError(t, r, err, KindSyntax, "deeper than allowed")
	})
	t.Run("far deeper is rejected cheaply", func(t *testing.T) {
		r, err := parseSchema(t, nestedItems(5000))
		expectLoadError(t, r, err, KindSyntax, "deeper than allowed")
	})
	t.Run("the walker has its own bound", func(t *testing.T) {
		// checkSchema can be called on documents that did not come through the
		// registry parser; it must not recurse without limit.
		_, is := checkSchema(nestedItems(MaxSchemaDepth + 10))
		if is == nil || !strings.Contains(is.Msg, "nesting exceeds") {
			t.Fatalf("issue = %+v, want a nesting issue", is)
		}
	})
	t.Run("deep properties chain counts both containers per level", func(t *testing.T) {
		chain := func(levels int) obj {
			leaf := obj{"type": "string"}
			for i := 0; i < levels; i++ {
				leaf = closedObject(obj{"p": leaf})
			}
			return leaf
		}
		// 15 wrappers: root(1) + 15*2 levels = the leaf at level 31, fits.
		expectSchemaOK(t, chain(15))
		// 16 wrappers put the leaf at level 33.
		r, err := parseSchema(t, chain(16))
		expectLoadError(t, r, err, KindSyntax, "deeper than allowed")
	})
}

func TestCheckSchemaReturnsSortedTopLevelProperties(t *testing.T) {
	s := closedObject(obj{"zeta": obj{"type": "string"}, "alpha": obj{"type": "string"}, "mid": closedObject(obj{"inner": obj{"type": "string"}})})
	names, is := checkSchema(decode(t, s))
	if is != nil {
		t.Fatalf("issue: %+v", is)
	}
	want := []string{"alpha", "mid", "zeta"}
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
}

// decode round-trips v through the strict parser so the walker sees the types
// it sees in production.
func decode(t *testing.T, v any) any {
	t.Helper()
	return args(t, string(marshal(t, v)))
}
