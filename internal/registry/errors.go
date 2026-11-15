package registry

import (
	"errors"
	"strconv"
	"strings"
)

// Errors returned by Registry.ValidateArguments. They are deliberately
// detail-free: they carry no schema text, no argument name and no argument
// value, so a caller can map them to a client response without leaking
// anything. Test them with errors.Is.
var (
	// ErrUnknownTool means the tool name is not registered.
	ErrUnknownTool = errors.New("registry: unknown tool")

	// ErrInvalidArguments means the tool is registered and the arguments do
	// not satisfy its JSON Schema.
	ErrInvalidArguments = errors.New("registry: arguments do not satisfy the tool schema")

	// ErrInternal means validation could not be carried out at all, for
	// example because the arguments contain a value the canonical encoder
	// does not support. It is never caused by a well-formed client request
	// that came through the strict parser.
	ErrInternal = errors.New("registry: validation could not be performed")
)

// LoadKind classifies why a registry failed to load.
type LoadKind int

const (
	// KindFile: the registry file could not be opened or read, is not a
	// regular file, or is larger than MaxFileBytes.
	KindFile LoadKind = iota + 1
	// KindSyntax: the file is not acceptable strict JSON (malformed, duplicate
	// keys, over a parser limit, bad UTF-8, and so on).
	KindSyntax
	// KindStructure: the JSON is valid but the registry layout is wrong:
	// unknown or missing fields, wrong types, too many tools.
	KindStructure
	// KindMetadata: a tool's name, version, risk, executor, review_fields or
	// audit_fields is invalid, or tool names collide.
	KindMetadata
	// KindSchema: a tool's args_schema breaks a security rule or does not
	// compile as a JSON Schema Draft 2020-12 document.
	KindSchema
)

// String returns a stable lowercase name for the kind.
func (k LoadKind) String() string {
	switch k {
	case KindFile:
		return "file"
	case KindSyntax:
		return "syntax"
	case KindStructure:
		return "structure"
	case KindMetadata:
		return "metadata"
	case KindSchema:
		return "schema"
	}
	return "unknown"
}

// LoadError is returned by Load and Parse for every failure. It is meant for
// the operator who owns the registry file: it names the tool and the location
// inside the document that is wrong. It never contains request data, because
// the registry is loaded before any request exists.
type LoadError struct {
	Kind LoadKind
	// Tool is the tool name when it is known and well formed, else empty.
	Tool string
	// Where is a short location such as tools[1].args_schema.properties.amount_cents.
	Where string
	// Msg is a fixed description of the problem.
	Msg string
	// Err is the underlying cause, if any.
	Err error
}

// Error implements error.
func (e *LoadError) Error() string {
	var b strings.Builder
	b.WriteString("registry: ")
	if e.Tool != "" {
		b.WriteString("tool ")
		b.WriteString(e.Tool)
		b.WriteString(": ")
	}
	if e.Where != "" {
		b.WriteString(e.Where)
		b.WriteString(": ")
	}
	b.WriteString(e.Msg)
	if e.Err != nil {
		b.WriteString(": ")
		b.WriteString(e.Err.Error())
	}
	return b.String()
}

// Unwrap returns the underlying cause.
func (e *LoadError) Unwrap() error { return e.Err }

func loadErr(kind LoadKind, tool, where, msg string) *LoadError {
	return &LoadError{Kind: kind, Tool: tool, Where: where, Msg: msg}
}

// causeError wraps an underlying error but exposes a single-line, length
// bounded message, so a multi-line library error cannot break a log line.
type causeError struct {
	msg string
	err error
}

func (c *causeError) Error() string { return c.msg }
func (c *causeError) Unwrap() error { return c.err }

// oneLine flattens err's text onto one line and truncates it.
func oneLine(err error) error {
	const max = 400
	s := strings.Join(strings.Fields(strings.ReplaceAll(err.Error(), "\n", " ; ")), " ")
	if len(s) > max {
		s = s[:max] + "..."
	}
	return &causeError{msg: s, err: err}
}

// quoteKey renders an object key or other operator-supplied token for an error
// location. Plain identifiers are shown as they are; anything else is quoted
// and truncated so odd bytes cannot reach a log line unescaped.
func quoteKey(k string) string {
	if k != "" && len(k) <= 64 {
		plain := true
		for i := 0; i < len(k); i++ {
			c := k[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
				c == '_' || c == '-' || c == '$' || c == '.') {
				plain = false
				break
			}
		}
		if plain {
			return k
		}
	}
	if len(k) > 64 {
		k = k[:64] + "..."
	}
	return strconv.Quote(k)
}
