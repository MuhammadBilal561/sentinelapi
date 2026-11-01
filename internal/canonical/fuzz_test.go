package canonical

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// FuzzParseStrict feeds arbitrary bytes to the strict parser. It must never
// panic. A rejected input must carry a classifiable error. For an accepted
// input the standard library must agree it is valid JSON, and canonicalization
// must be deterministic, produce parseable JSON, and be a fixed point.
//
// Run it with:
//
//	go test -run '^$' -fuzz FuzzParseStrict -fuzztime 30s ./internal/canonical
func FuzzParseStrict(f *testing.F) {
	seeds := []string{
		``, ` `, `null`, `true`, `0`, `-0.0e+10`, `1E400`, `"x"`, `[]`, `{}`,
		`{"tool_name":"orders.lookup","arguments":{"id":1}}`,
		`{"a":1,"a":2}`, `{"a":1,"~u0061":2}`, `{"é":1,"~u00e9":2}`,
		`[1,2,3,{"a":[true,false,null,"x~u00e9~ud83d~ude00"]}]`,
		`"~ud800"`, `"~udc00"`, `"~ud83d~ude00"`, `"\\ud800"`, "\"\xff\"", "\xef\xbb\xbf{}",
		`{} {}`, `[1,]`, `{"a":}`, `01`, `1.`, `.5`, `+1`, `NaN`, `"a\nb"`, "\"a\nb\"",
		nestedArrays(8), nestedArrays(9), nestedObjects(8), nestedMixed(9),
		`[` + strings.TrimSuffix(strings.Repeat("0,", 101), ",") + `]`,
		`"` + strings.Repeat("a", 4097) + `"`,
		`{"b":2,"a":{"d":4,"c":[3,2,1]}}`, "{ \"a\" :\t[ 1 ,\n 2 ] }",
	}
	for _, s := range seeds {
		f.Add([]byte(esc(s)))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		v, err := Parse(data)
		if err != nil {
			if v != nil {
				t.Fatalf("value returned together with error %v", err)
			}
			kind, ok := KindOf(err)
			if !ok || kind < KindSyntax || kind > KindTooLarge {
				t.Fatalf("error %v is not a classifiable *Error (kind=%v ok=%v)", err, kind, ok)
			}
			return
		}

		if !json.Valid(data) {
			t.Fatalf("parser accepted input that encoding/json rejects: %q", data)
		}

		c1, err := Encode(v)
		if err != nil {
			t.Fatalf("Encode of an accepted value failed: %v (input %q)", err, data)
		}
		c2, err := Encode(v)
		if err != nil || string(c1) != string(c2) {
			t.Fatalf("Encode is not deterministic: %q vs %q (err %v)", c1, c2, err)
		}
		if !json.Valid(c1) {
			t.Fatalf("canonical output is not valid JSON: %q (input %q)", c1, data)
		}

		// Canonical output must be accepted under the same limits and be a
		// fixed point: parsing it and encoding again changes nothing.
		v2, err := Parse(c1)
		if err != nil {
			t.Fatalf("canonical output was rejected: %v (canonical %q, input %q)", err, c1, data)
		}
		c3, err := Encode(v2)
		if err != nil || string(c3) != string(c1) {
			t.Fatalf("canonicalization is not idempotent: %q vs %q (err %v)", c1, c3, err)
		}
		if !reflect.DeepEqual(v, v2) {
			t.Fatalf("value changed across canonical round trip: %#v vs %#v", v, v2)
		}
		if Fingerprint(c1) != Fingerprint(c3) {
			t.Fatalf("fingerprints differ for identical canonical bytes")
		}
	})
}
