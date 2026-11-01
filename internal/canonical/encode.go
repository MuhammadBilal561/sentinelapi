package canonical

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"unicode/utf8"
)

// maxEncodeDepth bounds recursion in Encode for values that did not come from
// the parser (the parser's own MaxDepth is far smaller).
const maxEncodeDepth = 64

// ErrUnsupportedValue is returned by Encode for a value that the strict parser
// could not have produced.
var ErrUnsupportedValue = errors.New("canonical: unsupported value")

// Encode returns the canonical JSON encoding of v, which must consist only of
// the types Parse returns: map[string]any, []any, string, json.Number, bool
// and nil.
//
// The encoding is compact (no whitespace). Object members are sorted by key
// in UTF-8 byte order. A json.Number is written exactly as given after being
// checked against the JSON number grammar; it is never converted to float64.
// Strings are written as follows: the quote and backslash are escaped as \"
// and \\; the control characters U+0008, U+000C, U+000A, U+000D and U+0009 as
// \b \f \n \r \t; every other control character below U+0020 as \u00xx with
// lowercase hex digits; all other characters, including U+007F, U+2028 and
// <, > and &, are written as literal UTF-8. A string that is not valid UTF-8
// is an error, never silently repaired.
//
// See the package documentation for how this differs from RFC 8785.
func Encode(v any) ([]byte, error) {
	return appendValue(make([]byte, 0, 256), v, 0)
}

// Canonicalize parses data under DefaultLimits and returns its canonical
// encoding.
func Canonicalize(data []byte) ([]byte, error) {
	v, err := Parse(data)
	if err != nil {
		return nil, err
	}
	return Encode(v)
}

func appendValue(buf []byte, v any, depth int) ([]byte, error) {
	switch x := v.(type) {
	case nil:
		return append(buf, "null"...), nil
	case bool:
		if x {
			return append(buf, "true"...), nil
		}
		return append(buf, "false"...), nil
	case string:
		if !utf8.ValidString(x) {
			return nil, fmt.Errorf("%w: string is not valid UTF-8", ErrUnsupportedValue)
		}
		return appendString(buf, x), nil
	case json.Number:
		if !validNumber(string(x)) {
			return nil, fmt.Errorf("%w: invalid number", ErrUnsupportedValue)
		}
		return append(buf, x...), nil
	case []any:
		if depth+1 > maxEncodeDepth {
			return nil, newError(KindTooDeep, "value nesting exceeds encoder limit")
		}
		buf = append(buf, '[')
		for i, e := range x {
			if i > 0 {
				buf = append(buf, ',')
			}
			var err error
			if buf, err = appendValue(buf, e, depth+1); err != nil {
				return nil, err
			}
		}
		return append(buf, ']'), nil
	case map[string]any:
		if depth+1 > maxEncodeDepth {
			return nil, newError(KindTooDeep, "value nesting exceeds encoder limit")
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys) // byte-wise, which is UTF-8 code point order
		buf = append(buf, '{')
		for i, k := range keys {
			if i > 0 {
				buf = append(buf, ',')
			}
			if !utf8.ValidString(k) {
				return nil, fmt.Errorf("%w: key is not valid UTF-8", ErrUnsupportedValue)
			}
			buf = appendString(buf, k)
			buf = append(buf, ':')
			var err error
			if buf, err = appendValue(buf, x[k], depth+1); err != nil {
				return nil, err
			}
		}
		return append(buf, '}'), nil
	}
	return nil, fmt.Errorf("%w: type %T", ErrUnsupportedValue, v)
}

const hexDigits = "0123456789abcdef"

// appendString writes s, which must be valid UTF-8, as a JSON string.
func appendString(buf []byte, s string) []byte {
	buf = append(buf, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			buf = append(buf, '\\', '"')
		case c == '\\':
			buf = append(buf, '\\', '\\')
		case c == '\b':
			buf = append(buf, '\\', 'b')
		case c == '\f':
			buf = append(buf, '\\', 'f')
		case c == '\n':
			buf = append(buf, '\\', 'n')
		case c == '\r':
			buf = append(buf, '\\', 'r')
		case c == '\t':
			buf = append(buf, '\\', 't')
		case c < 0x20:
			buf = append(buf, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0x0f])
		default:
			buf = append(buf, c) // bytes >= 0x80 are part of valid UTF-8 sequences
		}
	}
	return append(buf, '"')
}

// validNumber reports whether s matches the JSON number grammar:
// -?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?
func validNumber(s string) bool {
	i := 0
	if i < len(s) && s[i] == '-' {
		i++
	}
	if i >= len(s) {
		return false
	}
	switch {
	case s[i] == '0':
		i++
	case s[i] >= '1' && s[i] <= '9':
		for i < len(s) && isDigit(s[i]) {
			i++
		}
	default:
		return false
	}
	if i < len(s) && s[i] == '.' {
		i++
		start := i
		for i < len(s) && isDigit(s[i]) {
			i++
		}
		if i == start {
			return false
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		start := i
		for i < len(s) && isDigit(s[i]) {
			i++
		}
		if i == start {
			return false
		}
	}
	return i == len(s)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
