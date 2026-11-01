package canonical

import (
	"encoding/json"
	"errors"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// esc turns the placeholder "~u" into a JSON "\u" escape introducer. Tests
// write "~u0061" where they mean the six characters \u0061, so the escape
// stays explicit in the source and cannot be decoded by an editor or tool
// before the test runs.
func esc(s string) string { return strings.ReplaceAll(s, "~u", `\u`) }

func mustParse(t *testing.T, in string) any {
	t.Helper()
	v, err := Parse([]byte(esc(in)))
	if err != nil {
		t.Fatalf("Parse(%q) error = %v, want success", abbreviate(in), err)
	}
	return v
}

func wantKind(t *testing.T, data []byte, lim Limits, want Kind) {
	t.Helper()
	v, err := ParseWithLimits(data, lim)
	if err == nil {
		t.Fatalf("ParseWithLimits(%q) = %v, want %s error", abbreviate(string(data)), v, want)
	}
	if v != nil {
		t.Fatalf("ParseWithLimits returned a value %v together with an error", v)
	}
	got, ok := KindOf(err)
	if !ok || got != want {
		t.Fatalf("ParseWithLimits(%q) error kind = %v (ok=%v), want %s; err = %v",
			abbreviate(string(data)), got, ok, want, err)
	}
}

func abbreviate(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}

func TestParseAcceptsValidJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want any
	}{
		{"null", "null", nil},
		{"true", "true", true},
		{"false with whitespace", " false ", false},
		{"zero", "0", json.Number("0")},
		{"negative exponent number", "-1.5e3", json.Number("-1.5e3")},
		{"string", `"hello"`, "hello"},
		{"escapes", `"a\n~u00e9~ud83d~ude00\/"`, "a\né😀/"},
		{"empty array", "[]", []any{}},
		{"empty object", "{}", map[string]any{}},
		{"nested", `{"a":[1,{"b":null}],"c":"d"}`,
			map[string]any{"a": []any{json.Number("1"), map[string]any{"b": nil}}, "c": "d"}},
		{"surrounding whitespace", " \t\r\n{\"a\" : 1 }\n\n", map[string]any{"a": json.Number("1")}},
		{"escaped backslash before u is text", `"\\ud800"`, `\ud800`},
		{"same key in sibling objects", `{"x":{"a":1},"y":{"a":2}}`,
			map[string]any{"x": map[string]any{"a": json.Number("1")}, "y": map[string]any{"a": json.Number("2")}}},
		{"keys differing in case are distinct", `{"a":1,"A":2}`,
			map[string]any{"a": json.Number("1"), "A": json.Number("2")}},
		{"NUL escape is accepted", `"~u0000"`, "\x00"},
		{"raw DEL is allowed", "\"\x7f\"", "\x7f"},
		{"raw non-ASCII", `"héllo ✓"`, "héllo ✓"},
		{"escaped quote and solidus", `"~u0022~u002f"`, `"/`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustParse(t, tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Parse(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Kind
	}{
		// Empty input.
		{"empty", "", KindSyntax},
		{"only spaces", "   ", KindSyntax},
		{"only newlines", "\n\r\n", KindSyntax},

		// Malformed JSON.
		{"unterminated object", `{`, KindSyntax},
		{"unterminated array", `[1,2`, KindSyntax},
		{"unterminated string", `"abc`, KindSyntax},
		{"missing value", `{"a":}`, KindSyntax},
		{"trailing comma in array", `[1,]`, KindSyntax},
		{"trailing comma in object", `{"a":1,}`, KindSyntax},
		{"leading comma", `[,1]`, KindSyntax},
		{"double comma in array", `[1,,2]`, KindSyntax},
		{"double comma in object", `{"a":1,,"b":2}`, KindSyntax},
		{"single quotes", `{'a':1}`, KindSyntax},
		{"unquoted key", `{a:1}`, KindSyntax},
		{"numeric key", `{1:2}`, KindSyntax},
		{"missing colon", `{"a" 1}`, KindSyntax},
		{"key without value", `{"a"}`, KindSyntax},
		{"missing comma in array", `[1 2]`, KindSyntax},
		{"missing comma in object", `{"a":1 "b":2}`, KindSyntax},
		{"mismatched brackets", `[}`, KindSyntax},
		{"mismatched braces", `{]`, KindSyntax},
		{"stray closing bracket", `]`, KindSyntax},
		{"stray closing brace", `}`, KindSyntax},
		{"truncated true", `tru`, KindSyntax},
		{"capitalized true", `True`, KindSyntax},
		{"truncated null", `nul`, KindSyntax},
		{"undefined", `undefined`, KindSyntax},
		{"bad escape", `"\x41"`, KindSyntax},
		{"short unicode escape", `"~u12"`, KindSyntax},
		{"non-hex unicode escape", `"~u12G4"`, KindSyntax},
		{"raw newline in string", "\"a\nb\"", KindSyntax},
		{"raw tab in string", "\"a\tb\"", KindSyntax},
		{"raw control character in string", "\"a\x01b\"", KindSyntax},
		{"line comment", "{} // c", KindSyntax},
		{"block comment", "/* c */ {}", KindSyntax},
		{"byte order mark", "\xef\xbb\xbf{}", KindSyntax},

		// Invalid numbers.
		{"leading zero", `01`, KindSyntax},
		{"negative leading zero", `-01`, KindSyntax},
		{"lone minus", `-`, KindSyntax},
		{"trailing dot", `1.`, KindSyntax},
		{"leading dot", `.5`, KindSyntax},
		{"plus sign", `+1`, KindSyntax},
		{"empty exponent", `1e`, KindSyntax},
		{"signed empty exponent", `1e+`, KindSyntax},
		{"hex number", `0x10`, KindSyntax},
		{"NaN", `NaN`, KindSyntax},
		{"Infinity", `Infinity`, KindSyntax},
		{"negative Infinity", `-Infinity`, KindSyntax},
		{"double minus", `--1`, KindSyntax},
		{"two dots", `1.2.3`, KindSyntax},
		{"digit separator", `1_000`, KindSyntax},

		// Trailing data.
		{"two objects", `{} {}`, KindSyntax},
		{"two numbers", `1 2`, KindSyntax},
		{"two nulls", `null null`, KindSyntax},
		{"two strings", `"a""b"`, KindSyntax},
		{"garbage after object", `{}x`, KindSyntax},
		{"extra closing brace", `{}}`, KindSyntax},
		{"extra closing bracket", `[]]`, KindSyntax},
		{"comma after value", `{},`, KindSyntax},
		{"NUL after value", "{}\x00", KindSyntax},
		{"second object after newline", "{\"a\":1}\n{\"b\":2}", KindSyntax},

		// Invalid raw UTF-8, anywhere in the input.
		{"invalid byte in string", "\"\xff\"", KindInvalidUTF8},
		{"invalid continuation", "{\"a\":\"\xc3\x28\"}", KindInvalidUTF8},
		{"overlong encoding", "\"\xc0\xaf\"", KindInvalidUTF8},
		{"UTF-8 encoded surrogate", "\"\xed\xa0\x80\"", KindInvalidUTF8},
		{"code point above U+10FFFF", "\"\xf4\x90\x80\x80\"", KindInvalidUTF8},
		{"invalid byte in key", "{\"\xff\":1}", KindInvalidUTF8},
		{"invalid byte outside strings", "\xff{}", KindInvalidUTF8},
		{"invalid byte after value", "{}\xff", KindInvalidUTF8},
		{"truncated multibyte sequence", "\"\xe2\x82\"", KindInvalidUTF8},

		// Invalid \u escapes (lone or mispaired surrogates).
		{"lone high surrogate", `"~ud800"`, KindInvalidUTF8},
		{"lone low surrogate", `"~udc00"`, KindInvalidUTF8},
		{"high surrogate then ordinary escape", `"~ud800~u0041"`, KindInvalidUTF8},
		{"high surrogate then text", `"~ud800x"`, KindInvalidUTF8},
		{"high surrogate then high surrogate", `"~ud800~ud800"`, KindInvalidUTF8},
		{"low then high surrogate", `"~udc00~ud800"`, KindInvalidUTF8},
		{"lone surrogate in key", `{"~ud800":1}`, KindInvalidUTF8},
		{"uppercase hex lone surrogate", `"~uD800"`, KindInvalidUTF8},
		{"high surrogate at end of input", `"~ud800`, KindInvalidUTF8},

		// Duplicate keys at every depth.
		{"duplicate top-level key", `{"a":1,"a":2}`, KindDuplicateKey},
		{"duplicate nested key", `{"x":{"a":1,"a":2}}`, KindDuplicateKey},
		{"duplicate in array element", `[{"a":1,"a":2}]`, KindDuplicateKey},
		{"duplicate with identical values", `{"a":1,"a":1}`, KindDuplicateKey},
		{"duplicate separated by other keys", `{"a":1,"b":2,"c":3,"a":4}`, KindDuplicateKey},
		{"duplicate via simple escape", `{"a":1,"~u0061":2}`, KindDuplicateKey},
		{"duplicate via escape first", `{"~u0061":1,"a":2}`, KindDuplicateKey},
		{"duplicate via two escape spellings", `{"~u0061":1,"~u0061":2}`, KindDuplicateKey},
		{"duplicate via hex case", `{"~u00e9":1,"~u00E9":2}`, KindDuplicateKey},
		{"duplicate via literal and escape", `{"é":1,"~u00e9":2}`, KindDuplicateKey},
		{"duplicate via surrogate pair", `{"😀":1,"~ud83d~ude00":2}`, KindDuplicateKey},
		{"duplicate via solidus escape", `{"a/b":1,"a\/b":2}`, KindDuplicateKey},
		{"duplicate via control escapes", `{"\n":1,"~u000a":2}`, KindDuplicateKey},
		{"duplicate via quote escapes", `{"\"":1,"~u0022":2}`, KindDuplicateKey},
		{"duplicate empty key", `{"":1,"":2}`, KindDuplicateKey},
		{"duplicate deep inside arrays", `{"a":[[{"b":{"c":1,"c":2}}]]}`, KindDuplicateKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantKind(t, []byte(esc(tt.in)), DefaultLimits(), tt.want)
		})
	}
}

func TestParseDecodedKeysAreComparedWithoutNormalization(t *testing.T) {
	// Composed and decomposed é are different byte sequences, so different keys.
	in := "{\"\u00e9\":1,\"e\u0301\":2}"
	if got := mustParse(t, in).(map[string]any); len(got) != 2 {
		t.Fatalf("got %d keys, want 2 distinct keys: %#v", len(got), got)
	}
}

func TestParseErrorIsClassifiableAndLeaksNothing(t *testing.T) {
	const canary = "SECRET-CANARY-7731"
	inputs := map[Kind]string{
		KindSyntax:        `{"` + canary + `": nope}`,
		KindInvalidUTF8:   "{\"" + canary + "\":\"\xff\"}",
		KindDuplicateKey:  `{"` + canary + `":1,"` + canary + `":2}`,
		KindTooDeep:       strings.Repeat(`{"`+canary+`":`, 9) + "1" + strings.Repeat("}", 9),
		KindLimitExceeded: `{"k":"` + strings.Repeat("a", 4097) + canary + `"}`,
	}
	sentinels := map[Kind]error{
		KindSyntax: ErrSyntax, KindInvalidUTF8: ErrInvalidUTF8, KindDuplicateKey: ErrDuplicateKey,
		KindTooDeep: ErrTooDeep, KindLimitExceeded: ErrLimitExceeded, KindTooLarge: ErrTooLarge,
	}
	for kind, in := range inputs {
		_, err := Parse([]byte(in))
		if err == nil {
			t.Fatalf("%s: expected an error", kind)
		}
		if got, ok := KindOf(err); !ok || got != kind {
			t.Errorf("%s: KindOf = %v, %v", kind, got, ok)
		}
		if !errors.Is(err, sentinels[kind]) {
			t.Errorf("%s: errors.Is(err, sentinel) = false; err = %v", kind, err)
		}
		for other, s := range sentinels {
			if other != kind && errors.Is(err, s) {
				t.Errorf("%s error also matches the %s sentinel", kind, other)
			}
		}
		var pe *Error
		if !errors.As(err, &pe) || pe.Kind != kind {
			t.Errorf("%s: errors.As failed", kind)
		}
		if strings.Contains(err.Error(), canary) {
			t.Errorf("%s: error text leaks input: %q", kind, err.Error())
		}
	}
	if _, ok := KindOf(errors.New("other")); ok {
		t.Error("KindOf must be false for a foreign error")
	}
}

func TestKindStringIsStable(t *testing.T) {
	want := map[Kind]string{
		KindSyntax: "syntax", KindInvalidUTF8: "invalid_utf8", KindDuplicateKey: "duplicate_key",
		KindTooDeep: "too_deep", KindLimitExceeded: "limit_exceeded", KindTooLarge: "too_large", Kind(0): "unknown",
	}
	for k, w := range want {
		if k.String() != w {
			t.Errorf("Kind(%d).String() = %q, want %q", int(k), k.String(), w)
		}
	}
}

func nestedArrays(n int) string { return strings.Repeat("[", n) + strings.Repeat("]", n) }

func nestedObjects(n int) string {
	return strings.Repeat(`{"a":`, n) + "1" + strings.Repeat("}", n)
}

// nestedMixed alternates arrays and objects, starting with an array.
func nestedMixed(n int) string {
	var open, closeS []string
	for i := 0; i < n; i++ {
		if i%2 == 0 {
			open = append(open, "[")
			closeS = append([]string{"]"}, closeS...)
		} else {
			open = append(open, `{"a":`)
			closeS = append([]string{"}"}, closeS...)
		}
	}
	return strings.Join(open, "") + "1" + strings.Join(closeS, "")
}

func TestParseDepthLimit(t *testing.T) {
	builders := map[string]func(int) string{
		"arrays": nestedArrays, "objects": nestedObjects, "mixed": nestedMixed,
	}
	for name, build := range builders {
		t.Run(name, func(t *testing.T) {
			mustParse(t, build(1)) // the root container is level 1
			mustParse(t, build(8)) // exactly the limit
			wantKind(t, []byte(build(9)), DefaultLimits(), KindTooDeep)
			wantKind(t, []byte(build(50)), DefaultLimits(), KindTooDeep)
		})
	}
}

func TestParseDepthLimitIsReportedBeforeLaterProblems(t *testing.T) {
	// Unterminated and very deep: the depth violation is found first.
	wantKind(t, []byte(strings.Repeat("[", 100)), DefaultLimits(), KindTooDeep)
	wantKind(t, []byte(strings.Repeat(`{"a":`, 100)), DefaultLimits(), KindTooDeep)
}

func TestParseCustomDepthLimit(t *testing.T) {
	lim := Limits{MaxDepth: 2}
	if _, err := ParseWithLimits([]byte(nestedArrays(2)), lim); err != nil {
		t.Fatalf("depth 2 with limit 2: %v", err)
	}
	wantKind(t, []byte(nestedArrays(3)), lim, KindTooDeep)
}

func TestParseRejectsExtremeNestingQuickly(t *testing.T) {
	// Millions of opening brackets must be rejected after a handful of tokens,
	// without recursion or buffering the whole input. The raised input limit
	// lets the input reach the depth check.
	lim := Limits{MaxInputBytes: 64 << 20}
	wantKind(t, []byte(strings.Repeat("[", 5_000_000)), lim, KindTooDeep)
	wantKind(t, []byte(strings.Repeat(`{"a":`, 1_000_000)), lim, KindTooDeep)
	wantKind(t, []byte(strings.Repeat(`[{"a":`, 1_000_000)), lim, KindTooDeep)
}

func TestParseStringLengthLimit(t *testing.T) {
	str := func(n int) string { return `"` + strings.Repeat("a", n) + `"` }

	mustParse(t, str(4096))
	wantKind(t, []byte(str(4097)), DefaultLimits(), KindLimitExceeded)

	// Object keys count too.
	mustParse(t, `{"`+strings.Repeat("k", 4096)+`":1}`)
	wantKind(t, []byte(`{"`+strings.Repeat("k", 4097)+`":1}`), DefaultLimits(), KindLimitExceeded)

	// The limit counts decoded UTF-8 bytes: "€" is 3 bytes, so 1365 fit and 1366 do not.
	mustParse(t, `"`+strings.Repeat("€", 1365)+`"`)
	wantKind(t, []byte(`"`+strings.Repeat("€", 1366)+`"`), DefaultLimits(), KindLimitExceeded)

	// It applies to the decoded text, not the escaped source: 4096 six-byte
	// escapes decode to 4096 bytes and are accepted; one more is rejected.
	mustParse(t, `"`+strings.Repeat("~u0061", 4096)+`"`)
	wantKind(t, []byte(esc(`"`+strings.Repeat("~u0061", 4097)+`"`)), DefaultLimits(), KindLimitExceeded)

	// A surrogate pair is one 4-byte character.
	mustParse(t, `"`+strings.Repeat("~ud83d~ude00", 1024)+`"`)
	wantKind(t, []byte(esc(`"`+strings.Repeat("~ud83d~ude00", 1025)+`"`)), DefaultLimits(), KindLimitExceeded)

	// Nested positions and a custom limit.
	wantKind(t, []byte(`{"a":[{"b":`+str(4097)+`}]}`), DefaultLimits(), KindLimitExceeded)
	wantKind(t, []byte(`"abcd"`), Limits{MaxStringBytes: 3}, KindLimitExceeded)
	if _, err := ParseWithLimits([]byte(`"abc"`), Limits{MaxStringBytes: 3}); err != nil {
		t.Fatalf("string at custom limit: %v", err)
	}
}

func TestParseArrayLengthLimit(t *testing.T) {
	arr := func(n int) string {
		if n == 0 {
			return "[]"
		}
		return "[" + strings.TrimSuffix(strings.Repeat("0,", n), ",") + "]"
	}

	mustParse(t, arr(0))
	mustParse(t, arr(99))
	mustParse(t, arr(100))
	wantKind(t, []byte(arr(101)), DefaultLimits(), KindLimitExceeded)
	wantKind(t, []byte(arr(1000)), DefaultLimits(), KindLimitExceeded)

	// The limit applies recursively and per array, not in total.
	inner := arr(100)
	mustParse(t, "["+strings.TrimSuffix(strings.Repeat(inner+",", 100), ",")+"]")
	wantKind(t, []byte(`{"a":[`+arr(101)+`]}`), DefaultLimits(), KindLimitExceeded)
	wantKind(t, []byte(`{"a":{"b":[[`+arr(101)+`]]}}`), DefaultLimits(), KindLimitExceeded)
	wantKind(t, []byte("["+strings.TrimSuffix(strings.Repeat(inner+",", 100), ",")+","+arr(1)+"]"), DefaultLimits(), KindLimitExceeded)

	wantKind(t, []byte(arr(3)), Limits{MaxArrayLen: 2}, KindLimitExceeded)
	if _, err := ParseWithLimits([]byte(arr(2)), Limits{MaxArrayLen: 2}); err != nil {
		t.Fatalf("array at custom limit: %v", err)
	}
}

func TestParseInputSizeLimit(t *testing.T) {
	pad := func(n int) string { return "[]" + strings.Repeat(" ", n-2) }

	mustParse(t, pad(DefaultMaxInputBytes-1))
	// Whitespace counts, and exactly the limit is allowed.
	mustParse(t, pad(DefaultMaxInputBytes))
	wantKind(t, []byte(pad(DefaultMaxInputBytes+1)), DefaultLimits(), KindTooLarge)

	wantKind(t, []byte("[]   "), Limits{MaxInputBytes: 4}, KindTooLarge)
}

func TestParseZeroLimitsMeanDefaults(t *testing.T) {
	wantKind(t, []byte(nestedArrays(9)), Limits{}, KindTooDeep)
	if _, err := ParseWithLimits([]byte(nestedArrays(8)), Limits{}); err != nil {
		t.Fatalf("zero Limits must behave as defaults: %v", err)
	}
	if DefaultLimits() != (Limits{MaxDepth: 8, MaxStringBytes: 4096, MaxArrayLen: 100, MaxInputBytes: 65536}) {
		t.Fatalf("unexpected defaults: %+v", DefaultLimits())
	}
}

func TestParsePreservesNumbersExactly(t *testing.T) {
	numbers := []string{
		"0", "-0", "-0.0", "0.0", "1", "1.0", "1e0", "1E0", "1e+2", "1E-2",
		// 2^53 + 1 is not representable as float64.
		"9007199254740993",
		"-9007199254740993",
		// Far beyond int64 and float64 precision.
		"12345678901234567890123456789",
		"0.1000000000000000055511151231257827",
		"3.141592653589793238462643383279",
		"1E400", "-1E400", "1e-400", "123456789e-1000000",
		"100000000000000000000", "0.000000000000000000001",
	}
	for _, n := range numbers {
		t.Run(n, func(t *testing.T) {
			got := mustParse(t, n)
			num, ok := got.(json.Number)
			if !ok {
				t.Fatalf("Parse(%q) = %T, want json.Number", n, got)
			}
			if string(num) != n {
				t.Fatalf("number text changed: got %q, want %q", num, n)
			}

			// The same holds nested, and survives canonical encoding unchanged.
			obj := mustParse(t, `{"v":[`+n+`]}`)
			enc, err := Encode(obj)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if want := `{"v":[` + n + `]}`; string(enc) != want {
				t.Fatalf("canonical = %s, want %s", enc, want)
			}
		})
	}
}

func TestParseNeverPanicsOnArbitraryInput(t *testing.T) {
	seeds := []string{
		`{"tool_name":"orders.lookup","arguments":{"id":1,"tags":["a","b"],"n":null}}`,
		esc(`[1,2,3,{"a":[true,false,null,"x~u00e9~ud83d~ude00"]}]`),
		esc(`"\\~u00e9\n\/"`), `-12.5e+10`, `{}`, `[]`,
	}
	rng := rand.New(rand.NewSource(20261016))
	alphabet := []byte("{}[]\":,\\/ \t\n\r0123456789.-+eEtrufalsn\x00\x7f\x80\xc3\xff\xed\xa0uUabcdefABCDEF")

	check := func(data []byte) {
		v, err := Parse(data)
		if err != nil {
			if _, ok := KindOf(err); !ok {
				t.Fatalf("Parse(%q) returned a foreign error: %v", data, err)
			}
			return
		}
		if !json.Valid(data) {
			t.Fatalf("Parse accepted %q, which encoding/json rejects", data)
		}
		if _, err := Encode(v); err != nil {
			t.Fatalf("Encode of accepted value from %q: %v", data, err)
		}
	}

	for i := 0; i < 20000; i++ {
		switch i % 3 {
		case 0: // random bytes from a JSON-flavoured alphabet
			b := make([]byte, rng.Intn(48))
			for j := range b {
				b[j] = alphabet[rng.Intn(len(alphabet))]
			}
			check(b)
		case 1: // fully random bytes
			b := make([]byte, rng.Intn(48))
			rng.Read(b)
			check(b)
		default: // mutations of valid documents
			b := []byte(seeds[rng.Intn(len(seeds))])
			for k := 0; k < 1+rng.Intn(4); k++ {
				switch rng.Intn(3) {
				case 0:
					if len(b) > 0 {
						b[rng.Intn(len(b))] = alphabet[rng.Intn(len(alphabet))]
					}
				case 1:
					if len(b) > 0 {
						p := rng.Intn(len(b))
						b = append(b[:p], b[p+1:]...)
					}
				default:
					p := rng.Intn(len(b) + 1)
					b = append(b[:p], append([]byte{alphabet[rng.Intn(len(alphabet))]}, b[p:]...)...)
				}
			}
			check(b)
		}
	}
}

func TestValidUnicodeEscapes(t *testing.T) {
	good := []string{
		``, `"abc"`, `"~u0041"`, `"~ud83d~ude00"`, `"~uD83D~uDE00"`, `"\\u"`, `"\\ud800"`, `"\\\\ud800"`,
		// Outside a string it is not an escape at all.
		`~ud800`,
		// Malformed escapes are left for the decoder to reject.
		`"~u12"`, `"~u12G4"`,
		// Truncated input.
		`"abc\`, `"~u`,
		// BMP characters just outside the surrogate range.
		`"~ue000~uffff"`,
	}
	for _, in := range good {
		if !validUnicodeEscapes([]byte(esc(in))) {
			t.Errorf("validUnicodeEscapes(%q) = false, want true", in)
		}
	}
	bad := []string{
		`"~ud800"`, `"~udbff"`, `"~udc00"`, `"~udfff"`, `"~ud800~ud800"`, `"~ud800x"`, `"~ud800~u0041"`,
		`"\\~ud800"`, `"~ud83d~ud83d~ude00"`, `"a~udc00b"`,
	}
	for _, in := range bad {
		if validUnicodeEscapes([]byte(esc(in))) {
			t.Errorf("validUnicodeEscapes(%q) = true, want false", in)
		}
	}
}
