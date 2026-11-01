package canonical

import (
	"regexp"
	"testing"
)

var fingerprintPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func TestFingerprintFormatAndKnownVector(t *testing.T) {
	got := Fingerprint([]byte(`{"a":1}`))
	if !fingerprintPattern.MatchString(got) {
		t.Fatalf("fingerprint %q does not match %s", got, fingerprintPattern)
	}
	// sha256 of the seven bytes {"a":1}, computed independently with sha256sum.
	const want = "sha256:015abd7f5cc57a2dd94b7590f04ad8084273905ee33ec5cebeae62276a97f862"
	if got != want {
		t.Fatalf("Fingerprint = %s, want %s", got, want)
	}
	if FingerprintPrefix != "sha256:" {
		t.Fatalf("FingerprintPrefix = %q", FingerprintPrefix)
	}
	empty := Fingerprint(nil)
	if empty != "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("empty-input fingerprint = %s", empty)
	}
}

func TestFingerprintValueMatchesFingerprintOfCanonicalBytes(t *testing.T) {
	v := mustParse(t, `{"b":[1,2],"a":"x"}`)
	got, err := FingerprintValue(v)
	if err != nil {
		t.Fatal(err)
	}
	if want := Fingerprint([]byte(`{"a":"x","b":[1,2]}`)); got != want {
		t.Fatalf("FingerprintValue = %s, want %s", got, want)
	}
	if _, err := FingerprintValue(struct{}{}); err == nil {
		t.Fatal("FingerprintValue of an unsupported value must fail")
	}
}

func fingerprintOf(t *testing.T, in string) string {
	t.Helper()
	return Fingerprint([]byte(canonicalOf(t, in)))
}

func TestFingerprintChangesWhenValuesChange(t *testing.T) {
	pairs := []struct{ name, a, b string }{
		{"number value", `{"amount_cents":12000}`, `{"amount_cents":12001}`},
		{"number vs string", `{"v":1}`, `{"v":"1"}`},
		{"bool vs string", `{"v":true}`, `{"v":"true"}`},
		{"null vs string", `{"v":null}`, `{"v":"null"}`},
		{"null vs missing", `{"v":null}`, `{}`},
		{"true vs false", `[true]`, `[false]`},
		{"array order", `[1,2]`, `[2,1]`},
		{"array length", `[1,2]`, `[1,2,3]`},
		{"key renamed", `{"a":1}`, `{"b":1}`},
		{"key case", `{"a":1}`, `{"A":1}`},
		{"extra key", `{"a":1}`, `{"a":1,"b":2}`},
		{"nested value", `{"a":{"b":[1,{"c":"x"}]}}`, `{"a":{"b":[1,{"c":"y"}]}}`},
		{"moved between levels", `{"a":{"b":1}}`, `{"a":{},"b":1}`},
		{"empty object vs empty array", `{}`, `[]`},
		{"empty string vs null", `""`, `null`},
		{"large number last digit", `{"n":12345678901234567890123}`, `{"n":12345678901234567890124}`},
		{"float64-indistinguishable numbers", `{"n":9007199254740992}`, `{"n":9007199254740993}`},
		{"many-digit decimals", `{"n":0.1000000000000000055511151231257827}`, `{"n":0.1000000000000000055511151231257828}`},
		{"string content", `{"s":"ord_1042"}`, `{"s":"ord_1043"}`},
		{"unicode string", `{"s":"é"}`, "{\"s\":\"é\"}"},
		{"whitespace inside a string matters", `{"s":"a b"}`, `{"s":"a  b"}`},
		{"escaped backslash is not an escape", `{"s":"\\u0041"}`, `{"s":"~u0041"}`},
		// Numbers are canonicalized verbatim, so these are different by design.
		{"1 vs 1.0 (documented)", `{"n":1}`, `{"n":1.0}`},
		{"-0 vs 0 (documented)", `{"n":-0}`, `{"n":0}`},
		{"1e2 vs 100 (documented)", `{"n":1e2}`, `{"n":100}`},
	}
	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			if fingerprintOf(t, p.a) == fingerprintOf(t, p.b) {
				t.Fatalf("fingerprints are equal for %s and %s", p.a, p.b)
			}
		})
	}
}

func TestFingerprintStableWhenOnlyFormattingChanges(t *testing.T) {
	a := fingerprintOf(t, `{"tool":"payments.refund","args":{"order_id":"ord_1","amount_cents":500}}`)
	b := fingerprintOf(t, "{ \"args\" : { \"amount_cents\" : 500 , \"order_id\" : \"ord_1\" } ,\n \"tool\" : \"payments.refund\" }")
	if a != b {
		t.Fatalf("fingerprints differ for reformatted input: %s vs %s", a, b)
	}
}
