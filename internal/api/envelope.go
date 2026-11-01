package api

import (
	"github.com/MuhammadBilal561/sentinelapi/internal/canonical"
)

// Request-envelope field names. These are the only field names the API ever
// echoes back in an error.
const (
	fieldToolName        = "tool_name"
	fieldArguments       = "arguments"
	fieldClientRequestID = "client_request_id"
	fieldApprovalID      = "approval_id"
	fieldIdempotencyKey  = "idempotency_key"
)

// callKind says which tool-call endpoint an envelope was sent to.
type callKind int

const (
	kindEvaluate callKind = iota
	kindExecute
)

// Identifier rules, enforced on the envelope only. They are deliberately
// ASCII-only so that no Unicode confusable can pass.
//
//	tool_name:         1-64 characters; one or more dot-separated segments;
//	                   each segment starts with a lowercase letter and then
//	                   has lowercase letters, digits or underscores.
//	                   Examples: orders.lookup, payments.refund
//	client_request_id: 1-64 characters from A-Z a-z 0-9 . - _
//	idempotency_key:   8-64 characters from the same set
//	approval_id:       "apr_" followed by 16 lowercase hex digits
const (
	maxToolNameLen        = 64
	maxClientRequestIDLen = 64
	minIdempotencyKeyLen  = 8
	maxIdempotencyKeyLen  = 64
	approvalIDLen         = len("apr_") + 16
)

// envelope is a structurally valid tool-call request. Nothing about it has
// been authorized: tool_name has not been looked up, and arguments is only
// known to be a JSON object that passed the parser's limits.
type envelope struct {
	ToolName        string
	Arguments       map[string]any
	ClientRequestID string
	ApprovalID      string
	IdempotencyKey  string
}

// envelopeError describes why an envelope was rejected. Field is a fixed
// envelope field name, or empty when no single known field is at fault (for
// example an unknown field, whose name is attacker-controlled and never echoed).
type envelopeError struct {
	Field string
}

// validateEnvelope checks the shape of a parsed request body. The order of
// checks is fixed so that the reported field is deterministic: root type,
// unknown fields, then tool_name, arguments, client_request_id, approval_id,
// idempotency_key.
func validateEnvelope(kind callKind, root any) (envelope, *envelopeError) {
	var env envelope

	obj, ok := root.(map[string]any)
	if !ok {
		return env, &envelopeError{}
	}
	for key := range obj {
		switch key {
		case fieldToolName, fieldArguments, fieldClientRequestID, fieldApprovalID, fieldIdempotencyKey:
		default:
			return env, &envelopeError{} // unknown field: do not echo its name
		}
	}

	name, ok := obj[fieldToolName].(string)
	if !ok || !validToolName(name) {
		return env, &envelopeError{Field: fieldToolName}
	}
	env.ToolName = name

	args, ok := obj[fieldArguments].(map[string]any)
	if !ok {
		return env, &envelopeError{Field: fieldArguments}
	}
	env.Arguments = args

	if v, present := obj[fieldClientRequestID]; present {
		s, ok := v.(string)
		if !ok || !validSafeID(s, 1, maxClientRequestIDLen) {
			return env, &envelopeError{Field: fieldClientRequestID}
		}
		env.ClientRequestID = s
	}

	if v, present := obj[fieldApprovalID]; present {
		s, ok := v.(string)
		if !ok || !validApprovalID(s) {
			return env, &envelopeError{Field: fieldApprovalID}
		}
		env.ApprovalID = s
	}

	v, present := obj[fieldIdempotencyKey]
	switch kind {
	case kindEvaluate:
		if present {
			return env, &envelopeError{Field: fieldIdempotencyKey} // not allowed here
		}
	case kindExecute:
		if !present {
			return env, &envelopeError{Field: fieldIdempotencyKey} // required
		}
		s, ok := v.(string)
		if !ok || !validSafeID(s, minIdempotencyKeyLen, maxIdempotencyKeyLen) {
			return env, &envelopeError{Field: fieldIdempotencyKey}
		}
		env.IdempotencyKey = s
	}
	return env, nil
}

// validToolName implements the tool_name rule documented above.
func validToolName(s string) bool {
	if len(s) == 0 || len(s) > maxToolNameLen {
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

// validSafeID reports whether s has between min and max characters, all from
// A-Z a-z 0-9 . - _ (so byte length equals character count).
func validSafeID(s string, min, max int) bool {
	if len(s) < min || len(s) > max {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// validApprovalID reports whether s is "apr_" plus 16 lowercase hex digits.
func validApprovalID(s string) bool {
	if len(s) != approvalIDLen || s[:4] != "apr_" {
		return false
	}
	for i := 4; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// codeForParseError maps a canonical parser error to its stable API code.
func codeForParseError(err error) string {
	kind, ok := canonical.KindOf(err)
	if !ok {
		return CodeInternalError
	}
	switch kind {
	case canonical.KindSyntax:
		return CodeInvalidJSON
	case canonical.KindInvalidUTF8:
		return CodeInvalidUTF8
	case canonical.KindDuplicateKey:
		return CodeDuplicateJSONKey
	case canonical.KindTooDeep:
		return CodeJSONTooDeep
	case canonical.KindLimitExceeded:
		return CodeJSONLimitExceeded
	case canonical.KindTooLarge:
		return CodeRequestTooLarge
	}
	return CodeInternalError
}
