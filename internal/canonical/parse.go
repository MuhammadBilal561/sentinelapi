package canonical

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

// Parse parses data under DefaultLimits. See the package documentation for
// exactly what is guaranteed about an accepted value.
func Parse(data []byte) (any, error) {
	return ParseWithLimits(data, DefaultLimits())
}

// ParseWithLimits parses data under the given limits (zero fields take their
// defaults). On failure it returns a nil value and an *Error.
//
// The checks run in this order, which decides the Kind when an input has more
// than one problem: size, raw UTF-8, \u escapes, then structure (syntax,
// duplicate keys, depth and limits as tokens are read), then trailing data.
func ParseWithLimits(data []byte, lim Limits) (any, error) {
	lim = lim.withDefaults()

	if len(data) > lim.MaxInputBytes {
		return nil, newError(KindTooLarge, "input exceeds size limit")
	}
	// encoding/json silently replaces invalid UTF-8 and lone surrogate escapes
	// with U+FFFD, which would make distinct inputs look identical. Reject
	// both before the decoder sees them.
	if !utf8.Valid(data) {
		return nil, newError(KindInvalidUTF8, "input is not valid UTF-8")
	}
	if !validUnicodeEscapes(data) {
		return nil, newError(KindInvalidUTF8, "input contains an invalid unicode escape")
	}
	if len(bytes.TrimLeft(data, " \t\r\n")) == 0 {
		return nil, newError(KindSyntax, "empty input")
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	p := &parser{dec: dec, lim: lim}

	v, err := p.value(0)
	if err != nil {
		return nil, err
	}

	// Anything after the first value, even another valid value, is an error.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, newError(KindSyntax, "unexpected data after top-level value")
	}

	// Belt and braces: an accepted input must also satisfy the standard
	// library's full validator. This runs only on input that already passed
	// the depth limit, so it cannot be used to exhaust resources.
	if !json.Valid(data) {
		return nil, newError(KindSyntax, "malformed JSON")
	}
	return v, nil
}

type parser struct {
	dec *json.Decoder
	lim Limits
}

// value reads one JSON value. depth is the number of containers currently open.
func (p *parser) value(depth int) (any, error) {
	tok, err := p.dec.Token()
	if err != nil {
		return nil, newError(KindSyntax, "malformed JSON")
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			return p.object(depth + 1)
		case '[':
			return p.array(depth + 1)
		}
		return nil, newError(KindSyntax, "unexpected delimiter")
	case string:
		if len(t) > p.lim.MaxStringBytes {
			return nil, newError(KindLimitExceeded, "string exceeds length limit")
		}
		return t, nil
	case json.Number:
		return t, nil
	case bool:
		return t, nil
	case nil:
		return nil, nil
	}
	return nil, newError(KindSyntax, "unexpected token")
}

// object reads the members of an object whose opening brace has been consumed.
// depth is this object's own level.
func (p *parser) object(depth int) (any, error) {
	if depth > p.lim.MaxDepth {
		return nil, newError(KindTooDeep, "nesting exceeds depth limit")
	}
	obj := make(map[string]any)
	for p.dec.More() {
		keyTok, err := p.dec.Token()
		if err != nil {
			return nil, newError(KindSyntax, "malformed JSON")
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, newError(KindSyntax, "object key is not a string")
		}
		if len(key) > p.lim.MaxStringBytes {
			return nil, newError(KindLimitExceeded, "object key exceeds length limit")
		}
		// The decoder has already decoded JSON escapes, so a key written as
		// an escape sequence and the same key written literally arrive here
		// as the same string.
		if _, dup := obj[key]; dup {
			return nil, newError(KindDuplicateKey, "duplicate object key")
		}
		val, err := p.value(depth)
		if err != nil {
			return nil, err
		}
		obj[key] = val
	}
	if err := p.closing('}'); err != nil {
		return nil, err
	}
	return obj, nil
}

// array reads the elements of an array whose opening bracket has been
// consumed. depth is this array's own level.
func (p *parser) array(depth int) (any, error) {
	if depth > p.lim.MaxDepth {
		return nil, newError(KindTooDeep, "nesting exceeds depth limit")
	}
	arr := make([]any, 0, 4)
	for p.dec.More() {
		if len(arr) >= p.lim.MaxArrayLen {
			return nil, newError(KindLimitExceeded, "array exceeds length limit")
		}
		val, err := p.value(depth)
		if err != nil {
			return nil, err
		}
		arr = append(arr, val)
	}
	if err := p.closing(']'); err != nil {
		return nil, err
	}
	return arr, nil
}

// closing consumes the closing delimiter of the current container.
func (p *parser) closing(want json.Delim) error {
	tok, err := p.dec.Token()
	if err != nil {
		return newError(KindSyntax, "malformed JSON")
	}
	if d, ok := tok.(json.Delim); !ok || d != want {
		return newError(KindSyntax, "malformed JSON")
	}
	return nil
}

// validUnicodeEscapes reports whether every \uXXXX escape inside a string is a
// valid Unicode scalar value: no lone high or low surrogate, and every high
// surrogate immediately followed by a low surrogate escape. It only tracks
// string boundaries and backslashes, so it is safe on malformed input; real
// syntax errors are reported by the decoder afterwards.
func validUnicodeEscapes(data []byte) bool {
	inString := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			continue
		}
		switch c {
		case '"':
			inString = false
		case '\\':
			if i+1 >= len(data) {
				return true
			}
			if data[i+1] != 'u' {
				i++ // skip the escaped character, which may be a quote or backslash
				continue
			}
			r, ok := hex4(data, i+2)
			if !ok {
				return true // malformed escape; the decoder will reject it
			}
			switch {
			case r >= 0xDC00 && r <= 0xDFFF:
				return false // lone low surrogate
			case r >= 0xD800 && r <= 0xDBFF:
				if i+11 < len(data) && data[i+6] == '\\' && data[i+7] == 'u' {
					if lo, ok := hex4(data, i+8); ok && lo >= 0xDC00 && lo <= 0xDFFF {
						i += 11 // consumed \uXXXX\uXXXX
						continue
					}
				}
				return false // high surrogate without a low surrogate
			}
			i += 5 // consumed \uXXXX
		}
	}
	return true
}

// hex4 parses four hexadecimal digits at data[at:at+4].
func hex4(data []byte, at int) (rune, bool) {
	if at < 0 || at+4 > len(data) {
		return 0, false
	}
	var r rune
	for _, b := range data[at : at+4] {
		switch {
		case b >= '0' && b <= '9':
			r = r<<4 | rune(b-'0')
		case b >= 'a' && b <= 'f':
			r = r<<4 | rune(b-'a'+10)
		case b >= 'A' && b <= 'F':
			r = r<<4 | rune(b-'A'+10)
		default:
			return 0, false
		}
	}
	return r, true
}
