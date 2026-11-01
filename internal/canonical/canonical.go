// Package canonical is SentinelAPI's strict JSON input boundary. It parses
// untrusted JSON under hard limits, encodes accepted values in a deterministic
// canonical form, and fingerprints that form with SHA-256.
//
// # Parser guarantees
//
// Parse and ParseWithLimits accept an input only if all of the following hold.
// Every other input is rejected with an *Error whose Kind says why.
//
//   - The input is at most Limits.MaxInputBytes long.
//   - The input is valid UTF-8, and every \u escape inside a string is a valid
//     Unicode scalar value: lone or mispaired surrogate escapes are rejected
//     instead of being silently replaced with U+FFFD.
//   - The input is exactly one well-formed JSON value (RFC 8259) with nothing
//     but whitespace after it. Empty input is rejected.
//   - No object has two members whose keys are equal after JSON escapes are
//     decoded, at any nesting depth. Keys are compared byte for byte; no
//     Unicode normalization is applied, so "é" written as one code point and
//     as e plus a combining accent are different keys.
//   - Container nesting is at most Limits.MaxDepth levels, where the root
//     container is level 1. Nesting is checked as tokens are read, so a deeply
//     nested input is rejected after MaxDepth+1 levels without recursion.
//   - Every decoded string, object keys included, is at most
//     Limits.MaxStringBytes bytes of UTF-8. The limit applies to the decoded
//     text, not to the escaped source text.
//   - Every array has at most Limits.MaxArrayLen elements, at every depth.
//   - Numbers follow the JSON number grammar and are kept as json.Number, the
//     exact source text, so no precision is lost to float64.
//
// The parser does not panic on any input; this is enforced by tests and by
// FuzzParseStrict rather than by recover, so a bug cannot be silently hidden.
//
// # What the parser does not do
//
// It does not validate meaning. It does not check numeric ranges (1e400 is
// accepted as text), it does not limit the number of members in an object
// beyond the input size limit, and it accepts the NUL character (\u0000)
// inside strings. Request shape (required fields, identifier formats) is the
// job of the HTTP envelope validator in internal/api, and argument schemas
// belong to a later milestone.
//
// # Result types
//
// A successful parse returns one of: map[string]any, []any, string,
// json.Number, bool or nil. Empty containers are non-nil.
//
// # Canonical form
//
// Encode writes compact JSON with no insignificant whitespace, object members
// sorted by key in UTF-8 byte order, and strings escaped in one fixed way
// (see Encode). Two inputs that differ only in whitespace, object member
// order, or string escape spelling have identical canonical bytes.
//
// This is NOT RFC 8785 (JCS). Numbers are written exactly as they appeared in
// the input, and keys are ordered by UTF-8 bytes, not UTF-16 code units, so
// canonical output can differ from a JCS implementation. In particular two
// numerically equal values written differently (1, 1.0, 1e0, and -0 versus 0)
// have different canonical bytes and fingerprints. That errs on the side of
// treating inputs as different, which is the safe direction for binding an
// approval to exact arguments.
//
// # Fingerprint
//
// Fingerprint returns "sha256:" followed by 64 lowercase hex digits. It is a
// generic JSON fingerprint primitive. The full action fingerprint, which also
// covers the authenticated agent, tool version and policy version, is built on
// top of it in a later milestone.
package canonical

// Default limits for untrusted input.
const (
	DefaultMaxDepth       = 8
	DefaultMaxStringBytes = 4096
	DefaultMaxArrayLen    = 100
	DefaultMaxInputBytes  = 64 << 10
)

// Limits bounds what the parser accepts. A zero or negative field means the
// corresponding default.
type Limits struct {
	MaxDepth       int // container levels; the root container is level 1
	MaxStringBytes int // decoded UTF-8 bytes, object keys included
	MaxArrayLen    int // elements per array
	MaxInputBytes  int // raw input length, whitespace included
}

// DefaultLimits returns the default limits.
func DefaultLimits() Limits {
	return Limits{
		MaxDepth:       DefaultMaxDepth,
		MaxStringBytes: DefaultMaxStringBytes,
		MaxArrayLen:    DefaultMaxArrayLen,
		MaxInputBytes:  DefaultMaxInputBytes,
	}
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxDepth <= 0 {
		l.MaxDepth = d.MaxDepth
	}
	if l.MaxStringBytes <= 0 {
		l.MaxStringBytes = d.MaxStringBytes
	}
	if l.MaxArrayLen <= 0 {
		l.MaxArrayLen = d.MaxArrayLen
	}
	if l.MaxInputBytes <= 0 {
		l.MaxInputBytes = d.MaxInputBytes
	}
	return l
}
