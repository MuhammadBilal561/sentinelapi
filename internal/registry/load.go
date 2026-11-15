package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/MuhammadBilal561/sentinelapi/internal/canonical"
)

// Field names of the registry file.
const (
	fieldRegistryVersion = "registry_version"
	fieldTools           = "tools"

	fieldName         = "name"
	fieldVersion      = "version"
	fieldRisk         = "risk"
	fieldExecutor     = "executor"
	fieldReviewFields = "review_fields"
	fieldAuditFields  = "audit_fields"
	fieldArgsSchema   = "args_schema"
)

// toolFields lists the fields every tool entry must have, in the order they
// are checked. No other field is accepted.
var toolFields = []string{
	fieldName, fieldVersion, fieldRisk, fieldExecutor,
	fieldReviewFields, fieldAuditFields, fieldArgsSchema,
}

// parseLimits are the canonical parser limits for a registry file. The
// nesting limit covers the three container levels above an args_schema (the
// document object, the tools array and the tool object) plus MaxSchemaDepth
// levels inside it.
func parseLimits() canonical.Limits {
	return canonical.Limits{
		MaxDepth:       MaxSchemaDepth + documentOverheadDepth,
		MaxStringBytes: MaxStringBytes,
		MaxArrayLen:    MaxArrayLen,
		MaxInputBytes:  MaxFileBytes,
	}
}

// Load reads, validates and compiles the registry file at path.
//
// At most MaxFileBytes are read: a larger file, a directory, or any other
// file that is not a regular file is rejected. On any error Load returns a nil
// Registry, so a partly loaded registry can never be used. Errors are
// *LoadError values meant for the operator.
func Load(path string) (*Registry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, &LoadError{Kind: KindFile, Msg: "cannot open the registry file", Err: oneLine(err)}
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, &LoadError{Kind: KindFile, Msg: "cannot inspect the registry file", Err: oneLine(err)}
	}
	if !info.Mode().IsRegular() {
		return nil, loadErr(KindFile, "", "", "the registry path is not a regular file")
	}
	if info.Size() > MaxFileBytes {
		return nil, loadErr(KindFile, "", "", fmt.Sprintf("the registry file is larger than %d bytes", MaxFileBytes))
	}

	// Read one byte past the limit so a file that grows after the Stat, or
	// reports a wrong size, is still caught.
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return nil, &LoadError{Kind: KindFile, Msg: "cannot read the registry file", Err: oneLine(err)}
	}
	if len(data) > MaxFileBytes {
		return nil, loadErr(KindFile, "", "", fmt.Sprintf("the registry file is larger than %d bytes", MaxFileBytes))
	}
	return Parse(data)
}

// Parse validates and compiles a registry document held in memory. It applies
// the same rules as Load, including the size limit.
func Parse(data []byte) (*Registry, error) {
	root, err := canonical.ParseWithLimits(data, parseLimits())
	if err != nil {
		return nil, syntaxError(err, data)
	}

	doc, ok := root.(map[string]any)
	if !ok {
		return nil, loadErr(KindStructure, "", "", "the registry must be a JSON object")
	}
	if le := rejectUnknown(doc, []string{fieldRegistryVersion, fieldTools}, ""); le != nil {
		return nil, le
	}

	rawVersion, present := doc[fieldRegistryVersion]
	if !present {
		return nil, loadErr(KindStructure, "", "", `missing required field "registry_version"`)
	}
	version, ok := positiveInt(rawVersion)
	if !ok || version != SupportedRegistryVersion {
		return nil, loadErr(KindStructure, "", fieldRegistryVersion,
			fmt.Sprintf("must be the integer %d, the only supported registry version", SupportedRegistryVersion))
	}

	rawTools, present := doc[fieldTools]
	if !present {
		return nil, loadErr(KindStructure, "", "", `missing required field "tools"`)
	}
	list, ok := rawTools.([]any)
	if !ok {
		return nil, loadErr(KindStructure, "", fieldTools, "must be an array")
	}
	if len(list) == 0 {
		return nil, loadErr(KindStructure, "", fieldTools, "must contain at least one tool")
	}
	if len(list) > MaxTools {
		return nil, loadErr(KindStructure, "", fieldTools, fmt.Sprintf("has more than the maximum of %d tools", MaxTools))
	}

	// Pass 1: structure and metadata of every tool, so that an operator is told
	// about a bad name or a duplicate before any schema is compiled.
	parsed := make([]*parsedTool, 0, len(list))
	seen := make(map[string]int, len(list))
	for i, raw := range list {
		pt, le := parseTool(i, raw)
		if le != nil {
			return nil, le
		}
		if first, dup := seen[pt.info.Name]; dup {
			return nil, loadErr(KindMetadata, pt.info.Name, toolWhere(i, fieldName),
				fmt.Sprintf("duplicate tool name (already used by tools[%d])", first))
		}
		seen[pt.info.Name] = i
		parsed = append(parsed, pt)
	}

	// Pass 2: security rules, field references and compilation. Nothing is
	// kept until every tool has passed.
	tools := make([]*tool, 0, len(parsed))
	for _, pt := range parsed {
		t, le := buildTool(pt)
		if le != nil {
			return nil, le
		}
		tools = append(tools, t)
	}
	return newRegistry(version, tools), nil
}

// parsedTool is a tool entry whose structure and metadata have been checked
// but whose schema has not been compiled yet.
type parsedTool struct {
	index  int
	info   ToolInfo
	schema any // decoded args_schema, a map[string]any
}

func toolWhere(i int, field string) string {
	if field == "" {
		return fmt.Sprintf("tools[%d]", i)
	}
	return fmt.Sprintf("tools[%d].%s", i, field)
}

// parseTool checks the structure and metadata of tools[i].
func parseTool(i int, raw any) (*parsedTool, *LoadError) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, loadErr(KindStructure, "", toolWhere(i, ""), "a tool entry must be a JSON object")
	}
	if le := rejectUnknown(obj, toolFields, toolWhere(i, "")); le != nil {
		return nil, le
	}
	for _, f := range toolFields {
		if _, present := obj[f]; !present {
			return nil, loadErr(KindStructure, "", toolWhere(i, ""), fmt.Sprintf("missing required field %q", f))
		}
	}

	pt := &parsedTool{index: i}

	name, ok := obj[fieldName].(string)
	if !ok {
		return nil, loadErr(KindStructure, "", toolWhere(i, fieldName), "must be a string")
	}
	if !ValidToolName(name) {
		return nil, loadErr(KindMetadata, "", toolWhere(i, fieldName),
			fmt.Sprintf("invalid tool name %s: use 1-%d lowercase dot-separated segments such as orders.lookup",
				quoteKey(name), MaxIdentifierLen))
	}
	pt.info.Name = name

	version, ok := positiveInt(obj[fieldVersion])
	if !ok {
		return nil, loadErr(KindMetadata, name, toolWhere(i, fieldVersion),
			fmt.Sprintf("must be a positive integer written without a sign, fraction or exponent, at most %d", maxToolVersion))
	}
	pt.info.Version = version

	risk, ok := obj[fieldRisk].(string)
	if !ok {
		return nil, loadErr(KindStructure, name, toolWhere(i, fieldRisk), "must be a string")
	}
	switch Risk(risk) {
	case RiskRead, RiskWrite:
		pt.info.Risk = Risk(risk)
	default:
		return nil, loadErr(KindMetadata, name, toolWhere(i, fieldRisk), `must be "read" or "write"`)
	}

	executor, ok := obj[fieldExecutor].(string)
	if !ok {
		return nil, loadErr(KindStructure, name, toolWhere(i, fieldExecutor), "must be a string")
	}
	if !validIdentifier(executor) {
		return nil, loadErr(KindMetadata, name, toolWhere(i, fieldExecutor),
			"must be a lowercase dot-separated identifier such as mock.orders (metadata only; nothing is called)")
	}
	pt.info.Executor = executor

	var le *LoadError
	if pt.info.ReviewFields, le = stringList(obj[fieldReviewFields], name, toolWhere(i, fieldReviewFields)); le != nil {
		return nil, le
	}
	if pt.info.AuditFields, le = stringList(obj[fieldAuditFields], name, toolWhere(i, fieldAuditFields)); le != nil {
		return nil, le
	}

	pt.schema = obj[fieldArgsSchema]
	if _, ok := pt.schema.(map[string]any); !ok {
		return nil, loadErr(KindStructure, name, toolWhere(i, fieldArgsSchema), "must be a JSON object")
	}
	return pt, nil
}

// buildTool applies the schema security rules, checks review_fields and
// audit_fields against the declared properties, and compiles the schema.
func buildTool(pt *parsedTool) (*tool, *LoadError) {
	name := pt.info.Name

	props, is := checkSchema(pt.schema)
	if is != nil {
		where := toolWhere(pt.index, fieldArgsSchema)
		if is.Pointer != "" {
			where += "#" + printable(is.Pointer)
		}
		return nil, loadErr(KindSchema, name, where, is.Msg)
	}

	if !numbersInRange(pt.schema, 0) {
		return nil, loadErr(KindSchema, name, toolWhere(pt.index, fieldArgsSchema),
			fmt.Sprintf("a number in the schema is longer than %d characters or has an exponent beyond +/-%d",
				MaxNumberChars, MaxNumberExponent))
	}

	declared := make(map[string]struct{}, len(props))
	for _, p := range props {
		declared[p] = struct{}{}
	}
	for _, list := range []struct {
		field  string
		values []string
	}{
		{fieldReviewFields, pt.info.ReviewFields},
		{fieldAuditFields, pt.info.AuditFields},
	} {
		for j, v := range list.values {
			if _, ok := declared[v]; !ok {
				return nil, loadErr(KindMetadata, name, fmt.Sprintf("%s[%d]", toolWhere(pt.index, list.field), j),
					fmt.Sprintf("%s is not a top-level property declared in args_schema", quoteKey(v)))
			}
		}
	}

	sch, err := compileSchema(name, pt.info.Version, pt.schema, refusingLoader{})
	if err != nil {
		return nil, &LoadError{
			Kind: KindSchema, Tool: name, Where: toolWhere(pt.index, fieldArgsSchema),
			Msg: "args_schema is not a valid JSON Schema (Draft 2020-12)", Err: oneLine(err),
		}
	}
	return &tool{info: pt.info, schema: sch}, nil
}

// stringList checks that v is an array of unique, non-empty strings.
func stringList(v any, tool, where string) ([]string, *LoadError) {
	arr, ok := v.([]any)
	if !ok {
		return nil, loadErr(KindStructure, tool, where, "must be an array of strings")
	}
	out := make([]string, 0, len(arr))
	seen := make(map[string]struct{}, len(arr))
	for j, e := range arr {
		s, ok := e.(string)
		if !ok {
			return nil, loadErr(KindStructure, tool, fmt.Sprintf("%s[%d]", where, j), "must be a string")
		}
		if s == "" {
			return nil, loadErr(KindMetadata, tool, fmt.Sprintf("%s[%d]", where, j), "must not be empty")
		}
		if _, dup := seen[s]; dup {
			return nil, loadErr(KindMetadata, tool, fmt.Sprintf("%s[%d]", where, j), fmt.Sprintf("%s is listed twice", quoteKey(s)))
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out, nil
}

// rejectUnknown returns an error for the first (in sorted order) key of obj
// that is not in allowed.
func rejectUnknown(obj map[string]any, allowed []string, where string) *LoadError {
	ok := make(map[string]struct{}, len(allowed))
	for _, a := range allowed {
		ok[a] = struct{}{}
	}
	var unknown []string
	for k := range obj {
		if _, fine := ok[k]; !fine {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return loadErr(KindStructure, "", where, fmt.Sprintf("unknown field %s", quoteKey(unknown[0])))
}

// positiveInt reads a JSON number that is a plain positive decimal integer of
// at most maxToolVersion: digits only, no sign, fraction or exponent.
func positiveInt(v any) (int, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	s := n.String()
	if len(s) == 0 || len(s) > 9 || s[0] == '0' {
		return 0, false
	}
	out := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		out = out*10 + int(s[i]-'0')
	}
	if out < 1 || out > maxToolVersion {
		return 0, false
	}
	return out, true
}

// utf8BOM is what some Windows editors put at the start of a "UTF-8" file.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// syntaxError converts a canonical parser error into a LoadError.
func syntaxError(err error, data []byte) *LoadError {
	kind, _ := canonical.KindOf(err)
	le := &LoadError{Kind: KindSyntax, Err: err}
	switch kind {
	case canonical.KindTooLarge:
		le.Kind = KindFile
		le.Msg = fmt.Sprintf("the registry is larger than %d bytes", MaxFileBytes)
	case canonical.KindInvalidUTF8:
		le.Msg = "the registry is not valid UTF-8"
	case canonical.KindDuplicateKey:
		le.Msg = "the registry has a duplicate object key"
	case canonical.KindTooDeep:
		le.Msg = fmt.Sprintf("the registry nests JSON deeper than allowed (an args_schema may nest %d levels)", MaxSchemaDepth)
	case canonical.KindLimitExceeded:
		le.Msg = fmt.Sprintf("a string is longer than %d bytes or an array has more than %d elements", MaxStringBytes, MaxArrayLen)
	default:
		le.Msg = "the registry is not valid JSON"
		if bytes.HasPrefix(data, utf8BOM) {
			le.Msg += " (it starts with a UTF-8 byte-order mark; save the file as UTF-8 without BOM)"
		}
	}
	return le
}
