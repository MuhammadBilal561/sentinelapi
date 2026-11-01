package canonical

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// canonicalOf canonicalizes in, which may use the "~u" escape placeholder (see
// esc in parse_test.go).
func canonicalOf(t *testing.T, in string) string {
	t.Helper()
	out, err := Canonicalize([]byte(esc(in)))
	if err != nil {
		t.Fatalf("Canonicalize(%q): %v", abbreviate(in), err)
	}
	return string(out)
}

func TestCanonicalFormExamples(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"scalars", ` [ null , true , false , "x" , 1 ] `, `[null,true,false,"x",1]`},
		{"whitespace removed", "{ \"a\" :\n [ 1 ,\t2 ] }", `{"a":[1,2]}`},
		{"keys sorted", `{"b":2,"a":1}`, `{"a":1,"b":2}`},
		{"nested keys sorted", `{"z":{"d":1,"c":2},"a":[{"y":1,"x":2}]}`, `{"a":[{"x":2,"y":1}],"z":{"c":2,"d":1}}`},
		{"array order is preserved", `[2,1,3]`, `[2,1,3]`},
		{"escape spellings unified", `"~u0041\/~u00e9~u0022"`, `"A/é\""`},
		{"escaped key", `{"~u0061":1}`, `{"a":1}`},
		{"surrogate pair becomes literal", `"~ud83d~ude00"`, `"😀"`},
		{"short control escapes", `"\b\f\n\r\t"`, `"\b\f\n\r\t"`},
		{"other controls use lowercase u00xx", `"~u0001~u001F~u0000"`, `"~u0001~u001f~u0000"`},
		{"quote and backslash", `"\"\\"`, `"\"\\"`},
		{"DEL and html characters are literal", "\"\x7f<>&\"", "\"\x7f<>&\""},
		{"line separators are literal", `"~u2028~u2029"`, "\"  \""},
		{"numbers are kept verbatim", `[1.0,1E2,-0,1e-7,0.10]`, `[1.0,1E2,-0,1e-7,0.10]`},
		{"empty containers", `{"a":[],"b":{}}`, `{"a":[],"b":{}}`},
		{"top-level string", ` "x" `, `"x"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := canonicalOf(t, tt.in), esc(tt.want); got != want {
				t.Fatalf("canonical(%q)\n got %s\nwant %s", tt.in, got, want)
			}
		})
	}
}

func TestCanonicalKeyOrderIsUTF8ByteOrder(t *testing.T) {
	// Bytewise: "B" < "a" < "aa" < "b" < "é".
	got := canonicalOf(t, `{"é":5,"b":4,"aa":3,"a":2,"B":1}`)
	if want := `{"B":1,"a":2,"aa":3,"b":4,"é":5}`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}

	// This is deliberately not RFC 8785. U+FFFD (EF BF BD in UTF-8) sorts before
	// U+1F600 (F0 9F 98 80) here, but JCS orders by UTF-16 code units, where the
	// surrogate pair D83D DE00 sorts before U+FFFD.
	got = canonicalOf(t, "{\"😀\":1,\"�\":2}")
	if want := "{\"�\":2,\"😀\":1}"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestCanonicalEqualityUnderReorderingAndWhitespace(t *testing.T) {
	groups := [][]string{
		{
			`{"a":1,"b":[true,null,{"c":"x","d":2}],"e":{"f":{"g":1,"h":2}}}`,
			`{"e":{"f":{"h":2,"g":1}},"b":[true,null,{"d":2,"c":"x"}],"a":1}`,
			"{\n  \"b\" : [ true , null , { \"c\" : \"x\" , \"d\" : 2 } ] ,\n  \"a\" : 1 ,\n  \"e\" : { \"f\" : { \"g\" : 1 , \"h\" : 2 } }\n}\n",
			"\t{\"a\":1,\r\n\"e\":{\"f\":{\"g\":1,\"h\":2}},\"b\":[true,null,{\"c\":\"x\",\"d\":2}]}   ",
		},
		{
			`{"s":"A/é😀"}`,
			`{"s":"~u0041\/~u00e9~ud83d~ude00"}`,
			`{ "~u0073" : "A\/~u00E9~uD83D~uDE00" }`,
		},
		{`{}`, ` { } `, "{\n}"},
		{`[]`, ` [ ] `},
		{`{"amount_cents":12000,"order_id":"ord_1042"}`, `{"order_id":"ord_1042","amount_cents":12000}`},
	}
	for gi, group := range groups {
		want := canonicalOf(t, group[0])
		wantFP := Fingerprint([]byte(want))
		for _, in := range group[1:] {
			got := canonicalOf(t, in)
			if got != want {
				t.Errorf("group %d: canonical differs\n got %s\nwant %s\n  in %q", gi, got, want, abbreviate(in))
			}
			if fp := Fingerprint([]byte(got)); fp != wantFP {
				t.Errorf("group %d: fingerprint differs for %q", gi, abbreviate(in))
			}
		}
	}
}

func TestEncodeIsDeterministic(t *testing.T) {
	v := mustParse(t, `{"k1":1,"k2":2,"k3":{"a":1,"b":2,"c":3},"k4":[{"z":1,"y":2}],"k5":"x","k6":null,"k7":true}`)
	first, err := Encode(v)
	if err != nil {
		t.Fatal(err)
	}
	// Go randomizes map iteration order, so repeated runs exercise ordering.
	for i := 0; i < 200; i++ {
		again, err := Encode(v)
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatalf("run %d produced different output:\n%s\n%s", i, first, again)
		}
	}
}

func TestCanonicalOutputIsParseableAndIdempotent(t *testing.T) {
	inputs := []string{
		`{"b":[1,2,{"c":"~u00e9"}],"a":null}`, `[ ]`, `"~u0000~u001f"`, `-0.0e+0`,
		`{"😀":"~ud83d~ude00","é":1}`, nestedMixed(8), `[[[],[]],{"a":{}}]`,
	}
	for _, in := range inputs {
		c1 := canonicalOf(t, in)
		if !json.Valid([]byte(c1)) {
			t.Fatalf("canonical output is not valid JSON: %s", c1)
		}
		if c2 := canonicalOf(t, c1); c2 != c1 {
			t.Fatalf("canonicalization is not idempotent:\n%s\n%s", c1, c2)
		}
	}
}

func TestEncodeNilContainers(t *testing.T) {
	if out, err := Encode(map[string]any(nil)); err != nil || string(out) != "{}" {
		t.Fatalf("nil map: %q, %v", out, err)
	}
	if out, err := Encode([]any(nil)); err != nil || string(out) != "[]" {
		t.Fatalf("nil slice: %q, %v", out, err)
	}
	if out, err := Encode(nil); err != nil || string(out) != "null" {
		t.Fatalf("nil: %q, %v", out, err)
	}
}

func TestEncodeRejectsUnsupportedValues(t *testing.T) {
	type custom struct{ A int }
	deep := any("x")
	for i := 0; i < maxEncodeDepth+1; i++ {
		deep = []any{deep}
	}
	tests := []struct {
		name string
		v    any
	}{
		{"int", 1},
		{"float64", 1.5},
		{"string slice", []string{"a"}},
		{"string map", map[string]string{"a": "b"}},
		{"struct", custom{1}},
		{"pointer", &custom{1}},
		{"nested unsupported", []any{map[string]any{"a": 1}}},
		{"invalid UTF-8 string", "\xff"},
		{"invalid UTF-8 key", map[string]any{"\xff": 1}},
		{"empty number", json.Number("")},
		{"comma number", json.Number("1,2")},
		{"leading zero number", json.Number("01")},
		{"trailing dot number", json.Number("1.")},
		{"leading dot number", json.Number(".5")},
		{"plus number", json.Number("+1")},
		{"NaN number", json.Number("NaN")},
		{"hex number", json.Number("0x1")},
		{"empty exponent number", json.Number("1e")},
		{"injection via number", json.Number(`1,"x":2`)},
		{"nesting beyond encoder limit", deep},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := Encode(tt.v)
			if err == nil {
				t.Fatalf("Encode(%#v) = %s, want an error", tt.v, out)
			}
			if out != nil {
				t.Fatalf("Encode returned output %s together with an error", out)
			}
		})
	}
	if _, err := Encode(1); !errors.Is(err, ErrUnsupportedValue) {
		t.Fatalf("unsupported type error = %v, want ErrUnsupportedValue", err)
	}
	if _, err := Encode(deep); !errors.Is(err, ErrTooDeep) {
		t.Fatalf("over-deep value error = %v, want ErrTooDeep", err)
	}
}

func TestEncodeAcceptsMaximumEncoderDepth(t *testing.T) {
	v := any("x")
	for i := 0; i < maxEncodeDepth; i++ {
		v = []any{v}
	}
	out, err := Encode(v)
	if err != nil {
		t.Fatalf("depth %d: %v", maxEncodeDepth, err)
	}
	if want := strings.Repeat("[", maxEncodeDepth) + `"x"` + strings.Repeat("]", maxEncodeDepth); string(out) != want {
		t.Fatalf("unexpected output %s", out)
	}
}

func TestValidNumber(t *testing.T) {
	valid := []string{"0", "-0", "1", "-1", "10", "0.5", "-0.5", "1e5", "1E5", "1e+5", "1e-5", "1.5e10", "0e0", "123456789012345678901234567890"}
	invalid := []string{"", "-", "+1", "01", "00", "-01", ".5", "1.", "1.e5", "1e", "1e+", "e5", "0x1", "1_0", "NaN", "Infinity", " 1", "1 ", "1,2", "--1", "1.2.3", "١"}
	for _, s := range valid {
		if !validNumber(s) {
			t.Errorf("validNumber(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if validNumber(s) {
			t.Errorf("validNumber(%q) = true, want false", s)
		}
	}
}
