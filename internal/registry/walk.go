package registry

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// draft2020SchemaURI is the only value a root "$schema" may have.
const draft2020SchemaURI = "https://json-schema.org/draft/2020-12/schema"

// schemaIssue describes the first rule an args_schema breaks.
type schemaIssue struct {
	// Pointer is the JSON Pointer (RFC 6901) of the offending location inside
	// the args_schema document. The empty string is the root.
	Pointer string
	Msg     string
}

func issue(pointer, msg string) *schemaIssue { return &schemaIssue{Pointer: pointer, Msg: msg} }

// Keywords that must never appear in an args_schema. They would let a schema
// change its own base URI, pull in a vocabulary, or reach another document.
var forbiddenKeywords = []string{
	"$dynamicAnchor", "$dynamicRef", "$id", "$recursiveAnchor", "$recursiveRef", "$vocabulary",
}

// Keywords whose value is ONE subschema.
var singleSchemaKeywords = []string{
	"contains", "contentSchema", "else", "if", "items", "not", "propertyNames",
	"then", "unevaluatedItems", "unevaluatedProperties",
}

// Keywords whose value is an object that maps names to subschemas.
var mapSchemaKeywords = []string{"$defs", "dependentSchemas", "patternProperties", "properties"}

// Keywords whose value is an array of subschemas.
var arraySchemaKeywords = []string{"allOf", "anyOf", "oneOf", "prefixItems"}

// Keywords that make a schema an "object schema" in addition to a "type" of
// (or including) "object". Constraint-only keywords such as "required" or
// "minProperties" are not on this list: on their own they only restrict the
// instance that an enclosing, already closed, object schema describes.
var objectKeywords = []string{
	"additionalProperties", "patternProperties", "properties", "unevaluatedProperties",
}

// checkSchema applies the registry's security rules to a decoded args_schema
// and returns the names of its declared top-level properties, sorted.
//
// It walks only real schema positions (see the keyword lists above), never the
// data held by "const", "enum", "default" or "examples". It rejects anything
// that could make the compiler leave the document, and it requires every
// object schema to be closed with "additionalProperties": false.
//
// The walk is recursive, but its depth is bounded by MaxSchemaDepth, and the
// registry parser has already bounded the nesting of the whole file.
func checkSchema(doc any) ([]string, *schemaIssue) {
	w := &walker{visited: map[string]struct{}{}}
	if is := w.schema(doc, "", 1, true); is != nil {
		return nil, is
	}
	// A "$ref" may only point at a location the walk has inspected as a
	// schema. Otherwise a reference such as "#/default" could smuggle an
	// unchecked object (read as a schema by the compiler) past the rules.
	for _, ref := range w.refs {
		if _, ok := w.visited[ref.target]; !ok {
			return nil, issue(ref.at+"/$ref",
				`"$ref" must point at a schema location in this document (for example under "$defs" or "properties")`)
		}
	}

	root, _ := doc.(map[string]any) // the root was verified to be an object
	props, _ := root["properties"].(map[string]any)
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

type refUse struct {
	at     string // pointer of the schema holding the "$ref"
	target string // normalized pointer the reference resolves to
}

type walker struct {
	visited map[string]struct{} // pointers of every schema position walked
	refs    []refUse
}

// schema checks one schema position. depth is the JSON container nesting
// level of v inside args_schema, with the root object at level 1.
func (w *walker) schema(v any, ptr string, depth int, root bool) *schemaIssue {
	if depth > MaxSchemaDepth {
		return issue(ptr, fmt.Sprintf("schema nesting exceeds %d levels", MaxSchemaDepth))
	}
	w.visited[ptr] = struct{}{}

	switch s := v.(type) {
	case bool:
		if root {
			return issue(ptr, "the root schema must be an object schema, not a boolean")
		}
		return nil
	case map[string]any:
		return w.object(s, ptr, depth, root)
	}
	return issue(ptr, "a schema must be a JSON object or a boolean")
}

func (w *walker) object(s map[string]any, ptr string, depth int, root bool) *schemaIssue {
	for _, kw := range forbiddenKeywords {
		if _, present := s[kw]; present {
			return issue(child(ptr, kw), fmt.Sprintf("keyword %q is not allowed in a tool schema", kw))
		}
	}

	if v, present := s["$schema"]; present {
		str, isString := v.(string)
		switch {
		case !root:
			return issue(child(ptr, "$schema"), `"$schema" is only allowed on the root schema`)
		case !isString || str != draft2020SchemaURI:
			return issue(child(ptr, "$schema"), `"$schema" must be "`+draft2020SchemaURI+`" (JSON Schema Draft 2020-12)`)
		}
	}

	if v, present := s["$ref"]; present {
		target, problem := localRef(v)
		if problem != "" {
			return issue(child(ptr, "$ref"), problem)
		}
		w.refs = append(w.refs, refUse{at: ptr, target: target})
	}

	if root {
		if t, _ := s["type"].(string); t != "object" {
			return issue(child(ptr, "type"), `the root schema must set "type": "object"`)
		}
	}
	if root || isObjectSchema(s) {
		if ap, present := s["additionalProperties"]; !present {
			return issue(ptr, `every object schema must set "additionalProperties": false (it is missing)`)
		} else if ap != false {
			return issue(child(ptr, "additionalProperties"), `every object schema must set "additionalProperties": false`)
		}
	}

	for _, kw := range singleSchemaKeywords {
		if sub, present := s[kw]; present {
			if is := w.schema(sub, child(ptr, kw), depth+1, false); is != nil {
				return is
			}
		}
	}
	for _, kw := range mapSchemaKeywords {
		raw, present := s[kw]
		if !present {
			continue
		}
		m, ok := raw.(map[string]any)
		if !ok {
			return issue(child(ptr, kw), fmt.Sprintf("%q must be an object whose values are schemas", kw))
		}
		names := make([]string, 0, len(m))
		for name := range m {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			// One container level for the keyword's object, one for the schema.
			if is := w.schema(m[name], child(child(ptr, kw), name), depth+2, false); is != nil {
				return is
			}
		}
	}
	for _, kw := range arraySchemaKeywords {
		raw, present := s[kw]
		if !present {
			continue
		}
		arr, ok := raw.([]any)
		if !ok {
			return issue(child(ptr, kw), fmt.Sprintf("%q must be an array of schemas", kw))
		}
		for i, sub := range arr {
			if is := w.schema(sub, child(child(ptr, kw), fmt.Sprint(i)), depth+2, false); is != nil {
				return is
			}
		}
	}
	return nil
}

// isObjectSchema reports whether s describes JSON objects: its "type" is or
// includes "object", or it uses one of objectKeywords.
func isObjectSchema(s map[string]any) bool {
	switch t := s["type"].(type) {
	case string:
		if t == "object" {
			return true
		}
	case []any:
		for _, e := range t {
			if str, _ := e.(string); str == "object" {
				return true
			}
		}
	}
	for _, kw := range objectKeywords {
		if _, present := s[kw]; present {
			return true
		}
	}
	return false
}

// localRef validates the value of a "$ref" and returns the normalized JSON
// Pointer it designates. Only "#" (the root) and "#/..." pointers qualify.
// Anything else, whether an http, https or file URL, a relative or absolute
// path, or an anchor name, is refused. The reference text is never echoed, as
// a URL could carry credentials.
func localRef(v any) (target, problem string) {
	ref, ok := v.(string)
	if !ok {
		return "", `"$ref" must be a string`
	}
	if ref == "#" {
		return "", ""
	}
	if !strings.HasPrefix(ref, "#/") {
		return "", `"$ref" must be a fragment-local JSON Pointer such as "#/$defs/name"; ` +
			`external references (http, https, file or any other URL), relative references and anchors are not allowed`
	}

	// The schema compiler percent-decodes the fragment once and then splits it
	// into JSON Pointer tokens. Do exactly the same so both agree on the target.
	decoded, err := url.PathUnescape(ref[1:])
	if err != nil || !strings.HasPrefix(decoded, "/") {
		return "", `"$ref" is not a valid JSON Pointer fragment`
	}
	var b strings.Builder
	for _, tok := range strings.Split(decoded[1:], "/") {
		key, ok := unescapePointerToken(tok)
		if !ok {
			return "", `"$ref" has an invalid JSON Pointer escape`
		}
		b.WriteByte('/')
		b.WriteString(escapePointerToken(key))
	}
	return b.String(), ""
}

func escapePointerToken(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	return strings.ReplaceAll(s, "/", "~1")
}

// unescapePointerToken reverses escapePointerToken. It reports false for a "~"
// that is not followed by 0 or 1.
func unescapePointerToken(s string) (string, bool) {
	if !strings.Contains(s, "~") {
		return s, true
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '~' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s) {
			return "", false
		}
		switch s[i+1] {
		case '0':
			b.WriteByte('~')
		case '1':
			b.WriteByte('/')
		default:
			return "", false
		}
		i++
	}
	return b.String(), true
}

func child(ptr, token string) string { return ptr + "/" + escapePointerToken(token) }

// printable makes an operator-supplied string safe for an error message:
// control characters and invalid UTF-8 become "?", and long text is cut.
func printable(s string) string {
	const max = 160
	var b strings.Builder
	for i := 0; i < len(s) && b.Len() < max; {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size <= 1 || unicode.IsControl(r) {
			b.WriteByte('?')
		} else {
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	if b.Len() >= max {
		b.WriteString("...")
	}
	return b.String()
}
