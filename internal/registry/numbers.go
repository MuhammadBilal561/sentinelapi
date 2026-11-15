package registry

import (
	"encoding/json"
	"strings"
)

// Bounds on the numbers the registry will hand to the schema library.
//
// The strict parser (internal/canonical) keeps a number as its exact text, so
// "1e999999999" is a perfectly valid JSON number to it. The schema library
// compares numbers exactly, using arbitrary-precision rationals, and with an
// absurd exponent that is expensive (about 20 ms and 5 MB for 1e999999, per
// number) or, beyond the limits of the standard library's rational parser,
// makes the library dereference a nil pointer. A request must not be able to
// choose its own cost, so numbers outside these bounds are treated as
// arguments that do not satisfy the schema, and as a load error in a schema.
const (
	// MaxNumberChars is the longest number, in characters of its JSON text.
	MaxNumberChars = 256
	// MaxNumberExponent is the largest absolute value of a number's exponent.
	MaxNumberExponent = 1000

	// maxNumberWalkDepth stops the walk over a value nested deeper than any
	// request or registry the parsers accept.
	maxNumberWalkDepth = 64
)

// numberInRange reports whether the JSON number text s is within the bounds.
// s is assumed to follow the JSON number grammar.
func numberInRange(s string) bool {
	if len(s) > MaxNumberChars {
		return false
	}
	i := strings.IndexAny(s, "eE")
	if i < 0 {
		return true
	}
	exp := s[i+1:]
	if exp != "" && (exp[0] == '+' || exp[0] == '-') {
		exp = exp[1:]
	}
	if exp == "" {
		return false // "1e" is not a number
	}
	exp = strings.TrimLeft(exp, "0")
	if len(exp) > 4 { // more than four significant digits is above 1000
		return false
	}
	n := 0
	for j := 0; j < len(exp); j++ {
		if exp[j] < '0' || exp[j] > '9' {
			return false
		}
		n = n*10 + int(exp[j]-'0')
	}
	return n <= MaxNumberExponent
}

// numbersInRange reports whether every json.Number inside v is in range. A
// value nested deeper than maxNumberWalkDepth is reported as out of range.
func numbersInRange(v any, depth int) bool {
	if depth > maxNumberWalkDepth {
		return false
	}
	switch x := v.(type) {
	case json.Number:
		return numberInRange(string(x))
	case map[string]any:
		for _, e := range x {
			if !numbersInRange(e, depth+1) {
				return false
			}
		}
	case []any:
		for _, e := range x {
			if !numbersInRange(e, depth+1) {
				return false
			}
		}
	}
	return true
}
