// Package registry is SentinelAPI's immutable tool registry. It loads a JSON
// registry file once at startup, validates every tool's metadata, compiles
// every tool's argument schema, and afterwards answers two questions: does a
// tool exist, and do these arguments satisfy its schema?
//
// # What this package is, and is not
//
// It validates the SHAPE of tool arguments. It does not decide whether anyone
// may call a tool (authorization, M3), it does not apply policy or approvals,
// and it never calls an executor: the executor field of a tool is an opaque
// label that is stored and returned, nothing more. Passing validation means
// "these arguments are well formed for this tool", never "this call is
// allowed".
//
// # File format
//
// The registry is a JSON file (see docs/decisions/ADR-tool-registry-format.md
// and examples/config/tools.json):
//
//	{
//	  "registry_version": 1,
//	  "tools": [
//	    {
//	      "name": "orders.lookup",
//	      "version": 1,
//	      "risk": "read",
//	      "executor": "mock.orders",
//	      "review_fields": ["order_id"],
//	      "audit_fields": ["tenant_id", "order_id"],
//	      "args_schema": { ...JSON Schema Draft 2020-12... }
//	    }
//	  ]
//	}
//
// It is parsed with internal/canonical.ParseWithLimits, so the file inherits
// strict UTF-8, duplicate-key rejection and bounded nesting. Unknown fields at
// the top level and in every tool entry are rejected. See the Max* constants
// for the bounds.
//
// # Schema security rules
//
// Every args_schema must satisfy all of these, in addition to being a valid
// Draft 2020-12 schema according to the schema library's own compiler:
//
//   - The root is an object schema: "type" is exactly "object" and
//     "additionalProperties" is exactly false.
//   - Every nested object schema also sets "additionalProperties" to false. A
//     subschema counts as an object schema when its "type" is or includes
//     "object", or when it has "properties", "patternProperties" or
//     "additionalProperties". Only real schema positions are inspected: the
//     values of "const", "enum", "default" and "examples" are data, not
//     schemas, and are never walked.
//   - Only fragment-local references are allowed: a "$ref" must start with "#"
//     and must point at a schema location inside the same document. "$id",
//     "$dynamicRef", "$dynamicAnchor", "$recursiveRef", "$vocabulary" and a
//     nested "$schema" are rejected, so a schema cannot change its own base
//     URI or load a vocabulary. A root "$schema" must name Draft 2020-12.
//   - The compiler is given a URL loader that refuses every URL. The standard
//     Draft 2020-12 meta-schemas are embedded in the library and need no
//     loader, so compilation never touches the network or the filesystem.
//
// # Validation semantics
//
// Format assertions are ENABLED: "format" keywords such as "uuid", "date-time"
// or "email" reject non-conforming strings. Content assertions
// ("contentEncoding", "contentMediaType", "contentSchema") stay DISABLED, as
// the specification permits: they are annotations here and are not enforced.
// Regular expressions ("pattern", "patternProperties") use Go's RE2 engine,
// which runs in linear time. "maxLength" counts Unicode code points, as the
// JSON Schema specification requires, not bytes.
//
// Numbers are never converted to float64. Arguments are canonically encoded
// by internal/canonical and re-read with the schema library's UnmarshalJSON,
// which keeps json.Number, and the library compares numbers exactly. As the
// JSON Schema specification defines it, a number with a zero fraction part
// such as 1.0 or 1e2 is a valid "integer".
//
// Numbers whose text is longer than MaxNumberChars characters, or whose
// exponent is larger than MaxNumberExponent in absolute value, are not passed
// to the library: they are invalid arguments (and a load error in a schema).
// Without that bound a request could make the exact arithmetic arbitrarily
// expensive.
//
// A Registry is immutable after loading and safe for concurrent use.
package registry

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"github.com/MuhammadBilal561/sentinelapi/internal/canonical"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Bounds applied to the registry file.
const (
	// MaxFileBytes is the largest registry file that is read.
	MaxFileBytes = 1 << 20 // 1 MiB
	// MaxTools is the largest number of tools in one registry.
	MaxTools = 32
	// MaxSchemaDepth is the deepest JSON container nesting allowed inside one
	// args_schema, where the args_schema object itself is level 1.
	MaxSchemaDepth = 32
	// MaxStringBytes is the longest decoded string, object keys included.
	MaxStringBytes = 8192
	// MaxArrayLen is the longest array anywhere in the file.
	MaxArrayLen = 256

	// SupportedRegistryVersion is the only registry_version this code reads.
	SupportedRegistryVersion = 1

	// maxToolVersion is the largest accepted tool version.
	maxToolVersion = 999_999_999

	// documentOverheadDepth is the container depth above an args_schema:
	// the document object, the tools array and the tool object.
	documentOverheadDepth = 3
)

// Risk classifies what a tool can do to the outside world.
type Risk string

// Risk values.
const (
	RiskRead  Risk = "read"
	RiskWrite Risk = "write"
)

// ToolInfo is the public, read-only description of a registered tool. It is a
// copy: changing its slices does not affect the registry.
type ToolInfo struct {
	Name         string
	Version      int
	Risk         Risk
	Executor     string // metadata only; nothing in this package calls it
	ReviewFields []string
	AuditFields  []string
}

// Registry is an immutable set of tools with compiled argument schemas.
// Obtain one from Load or Parse. The zero value and a nil *Registry are valid
// and contain no tools.
type Registry struct {
	version int
	tools   map[string]*tool // never modified after construction
	names   []string         // sorted
}

type tool struct {
	info   ToolInfo
	schema *jsonschema.Schema
}

// Version returns the registry_version of the loaded file.
func (r *Registry) Version() int {
	if r == nil {
		return 0
	}
	return r.version
}

// Len returns the number of registered tools.
func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.tools)
}

// Names returns the registered tool names in sorted order.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	return append([]string(nil), r.names...)
}

// Has reports whether name is a registered tool.
func (r *Registry) Has(name string) bool {
	if r == nil {
		return false
	}
	_, ok := r.tools[name]
	return ok
}

// Tool returns a copy of the metadata of the named tool.
func (r *Registry) Tool(name string) (ToolInfo, bool) {
	if r == nil {
		return ToolInfo{}, false
	}
	t, ok := r.tools[name]
	if !ok {
		return ToolInfo{}, false
	}
	info := t.info
	info.ReviewFields = append([]string(nil), info.ReviewFields...)
	info.AuditFields = append([]string(nil), info.AuditFields...)
	return info, true
}

// ValidateArguments checks args against the named tool's schema.
//
// It returns nil when the arguments satisfy the schema, ErrUnknownTool when
// the tool is not registered, ErrInvalidArguments when the schema rejects the
// arguments, and ErrInternal when validation could not be carried out. The
// errors never contain argument values or schema detail.
//
// args must be a value produced by the strict parser (canonical.Parse), such
// as the arguments member of a parsed request: nested map[string]any, []any,
// string, json.Number, bool and nil. A nil map is treated as invalid
// arguments, because arguments must be a JSON object.
func (r *Registry) ValidateArguments(name string, args map[string]any) (err error) {
	if r == nil {
		return ErrUnknownTool
	}
	t, ok := r.tools[name]
	if !ok {
		return ErrUnknownTool
	}
	if args == nil {
		return ErrInvalidArguments
	}
	// A request chooses its own numbers, so bound them before the schema
	// library does exact arithmetic on them (see numbers.go).
	if !numbersInRange(args, 0) {
		return ErrInvalidArguments
	}

	// The schema library is third-party code that sees attacker-chosen input.
	// A panic inside it must become an error, never take a request down.
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("%w: schema evaluation failed unexpectedly", ErrInternal)
		}
	}()

	// Canonical encoding keeps json.Number text exactly; the schema library's
	// own JSON reader then yields json.Number again, never float64.
	encoded, err := canonical.Encode(args)
	if err != nil {
		return fmt.Errorf("%w: cannot encode arguments", ErrInternal)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("%w: cannot read encoded arguments", ErrInternal)
	}

	if err := t.schema.Validate(instance); err != nil {
		var invalid *jsonschema.ValidationError
		if errors.As(err, &invalid) {
			return ErrInvalidArguments
		}
		return fmt.Errorf("%w: schema evaluation failed", ErrInternal)
	}
	return nil
}

// newRegistry assembles an immutable Registry from validated tools.
func newRegistry(version int, tools []*tool) *Registry {
	r := &Registry{version: version, tools: make(map[string]*tool, len(tools))}
	for _, t := range tools {
		r.tools[t.info.Name] = t
		r.names = append(r.names, t.info.Name)
	}
	sort.Strings(r.names)
	return r
}

// ValidToolName reports whether s is a valid tool name. It is the single
// definition of the naming rule; the HTTP request envelope delegates to it.
//
// A tool name is 1-64 characters: one or more dot-separated segments, each
// starting with a lowercase ASCII letter followed by lowercase ASCII letters,
// digits or underscores. Examples: orders.lookup, payments.refund.
func ValidToolName(s string) bool { return validIdentifier(s) }

// MaxIdentifierLen is the longest tool name or executor identifier.
const MaxIdentifierLen = 64

// validIdentifier implements the dotted lowercase identifier grammar shared
// by tool names and executor identifiers.
func validIdentifier(s string) bool {
	if len(s) == 0 || len(s) > MaxIdentifierLen {
		return false
	}
	segmentStart := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '.':
			if segmentStart {
				return false // leading dot or empty segment
			}
			segmentStart = true
		case segmentStart:
			if c < 'a' || c > 'z' {
				return false
			}
			segmentStart = false
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
		default:
			return false
		}
	}
	return !segmentStart // must not end with a dot
}
