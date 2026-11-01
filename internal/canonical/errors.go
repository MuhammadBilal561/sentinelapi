package canonical

import "errors"

// Kind classifies why input was rejected.
// The HTTP layer maps each Kind to a stable API error code.
type Kind int

const (
	KindSyntax Kind = iota + 1
	KindInvalidUTF8
	KindDuplicateKey
	KindTooDeep
	KindLimitExceeded
	KindTooLarge
)

// String returns a stable lowercase name for the kind.
func (k Kind) String() string {
	switch k {
	case KindSyntax:
		return "syntax"
	case KindInvalidUTF8:
		return "invalid_utf8"
	case KindDuplicateKey:
		return "duplicate_key"
	case KindTooDeep:
		return "too_deep"
	case KindLimitExceeded:
		return "limit_exceeded"
	case KindTooLarge:
		return "too_large"
	}
	return "unknown"
}

// Error is returned for every rejected input.
// It never contains any part of the input.
type Error struct {
	Kind   Kind
	reason string
}

// Error implements the error interface.
func (e *Error) Error() string {
	return "canonical: " + e.reason
}

// Is reports whether target is an Error of the same Kind.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Kind == e.Kind
}

// Sentinel errors for use with errors.Is.
var (
	ErrSyntax        = &Error{Kind: KindSyntax, reason: "invalid JSON"}
	ErrInvalidUTF8   = &Error{Kind: KindInvalidUTF8, reason: "invalid UTF-8"}
	ErrDuplicateKey  = &Error{Kind: KindDuplicateKey, reason: "duplicate object key"}
	ErrTooDeep       = &Error{Kind: KindTooDeep, reason: "nesting too deep"}
	ErrLimitExceeded = &Error{Kind: KindLimitExceeded, reason: "limit exceeded"}
	ErrTooLarge      = &Error{Kind: KindTooLarge, reason: "input too large"}
)

// KindOf returns the Kind of an error produced by this package.
func KindOf(err error) (Kind, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind, true
	}
	return 0, false
}

func newError(kind Kind, reason string) *Error {
	return &Error{Kind: kind, reason: reason}
}
