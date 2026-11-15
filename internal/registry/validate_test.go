package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MuhammadBilal561/sentinelapi/internal/canonical"
)

func exampleRegistry(t testing.TB) *Registry {
	t.Helper()
	r, err := Load(repoFile(t, "examples", "config", "tools.json"))
	if err != nil {
		t.Fatalf("cannot load the example registry: %v", err)
	}
	return r
}

type argCase struct {
	name string
	json string
	ok   bool
}

func runArgCases(t *testing.T, r *Registry, tool string, cases []argCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var want error
			if !c.ok {
				want = ErrInvalidArguments
			}
			expectArgs(t, r, tool, c.json, want)
		})
	}
}

// ---------------------------------------------------------------------------
// orders.lookup

func TestOrdersLookupArguments(t *testing.T) {
	r := exampleRegistry(t)
	tenant32 := "t_" + strings.Repeat("a", 32)
	tenant33 := "t_" + strings.Repeat("a", 33)
	runArgCases(t, r, "orders.lookup", []argCase{
		{"valid", `{"tenant_id":"t_acme","order_id":"ord_1042"}`, true},
		{"key order does not matter", `{"order_id":"ord_1042","tenant_id":"t_acme"}`, true},
		{"whitespace does not matter", ` { "tenant_id" : "t_acme" , "order_id" : "ord_1" } `, true},
		{"shortest tenant", `{"tenant_id":"t_a","order_id":"ord_1"}`, true},
		{"tenant with digits", `{"tenant_id":"t_a1b2c3","order_id":"ord_1"}`, true},
		{"longest tenant (32)", `{"tenant_id":"` + tenant32 + `","order_id":"ord_1"}`, true},
		{"tenant one too long (33)", `{"tenant_id":"` + tenant33 + `","order_id":"ord_1"}`, false},
		{"tenant with no characters after t_", `{"tenant_id":"t_","order_id":"ord_1"}`, false},
		{"tenant without prefix", `{"tenant_id":"acme","order_id":"ord_1"}`, false},
		{"tenant uppercase", `{"tenant_id":"t_ACME","order_id":"ord_1"}`, false},
		{"tenant with dash", `{"tenant_id":"t_ac-me","order_id":"ord_1"}`, false},
		{"tenant with space", `{"tenant_id":"t_ac me","order_id":"ord_1"}`, false},
		{"tenant with trailing newline", `{"tenant_id":"t_acme\n","order_id":"ord_1"}`, false},
		{"tenant with leading space", `{"tenant_id":" t_acme","order_id":"ord_1"}`, false},
		{"tenant with fullwidth letters", "{\"tenant_id\":\"t_ａｃｍｅ\",\"order_id\":\"ord_1\"}", false},
		{"tenant with accent", "{\"tenant_id\":\"t_café\",\"order_id\":\"ord_1\"}", false},
		{"tenant is empty", `{"tenant_id":"","order_id":"ord_1"}`, false},
		{"order with 12 digits", `{"tenant_id":"t_a","order_id":"ord_123456789012"}`, true},
		{"order with 13 digits", `{"tenant_id":"t_a","order_id":"ord_1234567890123"}`, false},
		{"order with no digits", `{"tenant_id":"t_a","order_id":"ord_"}`, false},
		{"order with letters", `{"tenant_id":"t_a","order_id":"ord_12a"}`, false},
		{"order uppercase prefix", `{"tenant_id":"t_a","order_id":"ORD_12"}`, false},
		{"order with trailing newline", `{"tenant_id":"t_a","order_id":"ord_12\n"}`, false},
		{"order with arabic-indic digits", "{\"tenant_id\":\"t_a\",\"order_id\":\"ord_١٢\"}", false},
		{"order as a number", `{"tenant_id":"t_a","order_id":1042}`, false},
		{"missing tenant", `{"order_id":"ord_1"}`, false},
		{"missing order", `{"tenant_id":"t_a"}`, false},
		{"empty object", `{}`, false},
		{"extra property", `{"tenant_id":"t_a","order_id":"ord_1","extra":1}`, false},
		{"extra property that looks like identity", `{"tenant_id":"t_a","order_id":"ord_1","agent_id":"admin"}`, false},
		{"tenant is null", `{"tenant_id":null,"order_id":"ord_1"}`, false},
		{"tenant is an array", `{"tenant_id":["t_a"],"order_id":"ord_1"}`, false},
		{"tenant is an object", `{"tenant_id":{"x":"t_a"},"order_id":"ord_1"}`, false},
		{"tenant is a boolean", `{"tenant_id":true,"order_id":"ord_1"}`, false},
	})
}

// ---------------------------------------------------------------------------
// payments.refund

func TestPaymentsRefundArguments(t *testing.T) {
	r := exampleRegistry(t)
	with := func(amount, reason string) string {
		return `{"tenant_id":"t_acme","order_id":"ord_1","amount_cents":` + amount + `,"reason":` + reason + `}`
	}
	emoji := strings.Repeat(string(rune(0x1F600)), 200)
	runArgCases(t, r, "payments.refund", []argCase{
		{"valid", with("12000", `"duplicate charge"`), true},
		{"minimum amount", with("1", `"x"`), true},
		{"maximum amount", with("1000000", `"x"`), true},
		{"zero", with("0", `"x"`), false},
		{"negative one", with("-1", `"x"`), false},
		{"negative zero", with("-0", `"x"`), false},
		{"one over the maximum", with("1000001", `"x"`), false},
		{"far over the maximum", with("99999999999999999999", `"x"`), false},
		{"huge negative", with("-99999999999999999999", `"x"`), false},
		{"fraction", with("1.5", `"x"`), false},
		{"just under the maximum as a fraction", with("999999.5", `"x"`), false},
		{"just over the maximum as a fraction", with("1000000.5", `"x"`), false},
		{"integer written with a zero fraction", with("12000.0", `"x"`), true},
		{"integer written with an exponent", with("1e6", `"x"`), true},
		{"exponent over the maximum", with("1e7", `"x"`), false},
		{"tiny fraction that is not an integer", with("1.0000000000000000000001", `"x"`), false},
		{"amount as a string", with(`"100"`, `"x"`), false},
		{"amount as null", with("null", `"x"`), false},
		{"amount as a boolean", with("true", `"x"`), false},
		{"amount as an array", with("[100]", `"x"`), false},
		{"amount as an object", with(`{"v":100}`, `"x"`), false},
		{"reason of 200 characters", with("5", `"`+strings.Repeat("a", 200)+`"`), true},
		{"reason of 201 characters", with("5", `"`+strings.Repeat("a", 201)+`"`), false},
		{"reason of 200 two-byte characters", with("5", `"`+strings.Repeat("é", 200)+`"`), true},
		{"reason of 201 two-byte characters", with("5", `"`+strings.Repeat("é", 201)+`"`), false},
		{"reason of 200 four-byte characters", with("5", `"`+emoji+`"`), true},
		{"reason of 201 four-byte characters", with("5", `"`+emoji+string(rune(0x1F600))+`"`), false},
		{"empty reason is allowed", with("5", `""`), true},
		{"reason as a number", with("5", `7`), false},
		{"reason as null", with("5", `null`), false},
		{"missing reason", `{"tenant_id":"t_a","order_id":"ord_1","amount_cents":5}`, false},
		{"missing amount", `{"tenant_id":"t_a","order_id":"ord_1","reason":"x"}`, false},
		{"missing tenant", `{"order_id":"ord_1","amount_cents":5,"reason":"x"}`, false},
		{"missing order", `{"tenant_id":"t_a","amount_cents":5,"reason":"x"}`, false},
		{"additional property", `{"tenant_id":"t_a","order_id":"ord_1","amount_cents":5,"reason":"x","currency":"usd"}`, false},
		{"approval fields do not belong in arguments", `{"tenant_id":"t_a","order_id":"ord_1","amount_cents":5,"reason":"x","approval_id":"apr_0123456789abcdef"}`, false},
	})
}

// ---------------------------------------------------------------------------
// accounts.close

func TestAccountsCloseArguments(t *testing.T) {
	r := exampleRegistry(t)
	runArgCases(t, r, "accounts.close", []argCase{
		{"valid", `{"tenant_id":"t_acme","account_id":"acct_77","reason":"customer request"}`, true},
		{"account with 12 digits", `{"tenant_id":"t_a","account_id":"acct_123456789012","reason":"x"}`, true},
		{"account with 13 digits", `{"tenant_id":"t_a","account_id":"acct_1234567890123","reason":"x"}`, false},
		{"account with the wrong prefix", `{"tenant_id":"t_a","account_id":"ord_1","reason":"x"}`, false},
		{"account with no digits", `{"tenant_id":"t_a","account_id":"acct_","reason":"x"}`, false},
		{"empty reason is not allowed", `{"tenant_id":"t_a","account_id":"acct_1","reason":""}`, false},
		{"reason of 200", `{"tenant_id":"t_a","account_id":"acct_1","reason":"` + strings.Repeat("r", 200) + `"}`, true},
		{"reason of 201", `{"tenant_id":"t_a","account_id":"acct_1","reason":"` + strings.Repeat("r", 201) + `"}`, false},
		{"missing reason", `{"tenant_id":"t_a","account_id":"acct_1"}`, false},
		{"missing account", `{"tenant_id":"t_a","reason":"x"}`, false},
		{"extra property", `{"tenant_id":"t_a","account_id":"acct_1","reason":"x","force":true}`, false},
	})
}

// ---------------------------------------------------------------------------
// Errors and the unknown/invalid distinction

func TestUnknownToolIsDistinctFromInvalidArguments(t *testing.T) {
	r := exampleRegistry(t)

	err := r.ValidateArguments("orders.refund", args(t, `{}`))
	if !errors.Is(err, ErrUnknownTool) || errors.Is(err, ErrInvalidArguments) || errors.Is(err, ErrInternal) {
		t.Errorf("unknown tool: error = %v", err)
	}
	err = r.ValidateArguments("orders.lookup", args(t, `{}`))
	if !errors.Is(err, ErrInvalidArguments) || errors.Is(err, ErrUnknownTool) || errors.Is(err, ErrInternal) {
		t.Errorf("invalid arguments: error = %v", err)
	}

	// Unknown wins even when the arguments are also wrong, and even for nil.
	if err := r.ValidateArguments("nope.nope", nil); !errors.Is(err, ErrUnknownTool) {
		t.Errorf("unknown tool with nil args: %v", err)
	}
	// Lookup is exact: no case folding, no trimming, no prefix matching.
	for _, name := range []string{"Orders.Lookup", "orders.lookup ", " orders.lookup", "orders", "orders.lookup.v2", "orders.look", ""} {
		if r.Has(name) {
			t.Errorf("Has(%q) = true", name)
		}
		if err := r.ValidateArguments(name, args(t, `{}`)); !errors.Is(err, ErrUnknownTool) {
			t.Errorf("ValidateArguments(%q) = %v, want ErrUnknownTool", name, err)
		}
	}
}

func TestNilArgumentsAreInvalid(t *testing.T) {
	r := exampleRegistry(t)
	if err := r.ValidateArguments("orders.lookup", nil); !errors.Is(err, ErrInvalidArguments) {
		t.Fatalf("nil arguments: %v", err)
	}
}

func TestErrorsCarryNoArgumentOrSchemaDetail(t *testing.T) {
	r := exampleRegistry(t)
	canary := "CANARYVALUE9931"
	bad := []string{
		`{"tenant_id":"` + canary + `","order_id":"ord_1"}`,
		`{"tenant_id":"t_a","order_id":"ord_1","` + canary + `":1}`,
		`{"tenant_id":"t_a","order_id":{"` + canary + `":1}}`,
	}
	for _, text := range bad {
		err := r.ValidateArguments("orders.lookup", args(t, text))
		if !errors.Is(err, ErrInvalidArguments) {
			t.Fatalf("%s: %v", text, err)
		}
		if err.Error() != ErrInvalidArguments.Error() {
			t.Errorf("error text %q differs from the fixed sentinel text", err.Error())
		}
		for _, leak := range []string{canary, "pattern", "tenant_id", "t_", "additionalProperties", "required", "orders.lookup"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("error text %q leaks %q", err.Error(), leak)
			}
		}
	}
}

func TestUnsupportedValuesAreInternalErrors(t *testing.T) {
	r := exampleRegistry(t)
	// A float64 can only come from a caller that bypassed the strict parser.
	for _, v := range []any{1.5, int(3), func() {}, make(chan int), struct{}{}} {
		err := r.ValidateArguments("orders.lookup", map[string]any{"tenant_id": v})
		if !errors.Is(err, ErrInternal) {
			t.Errorf("value %T: error = %v, want ErrInternal", v, err)
			continue
		}
		if strings.Contains(err.Error(), "1.5") {
			t.Errorf("internal error leaks a value: %v", err)
		}
	}
}

func TestValidationDoesNotModifyTheArguments(t *testing.T) {
	r := exampleRegistry(t)
	a := args(t, `{"tenant_id":"t_a","order_id":"ord_1","amount_cents":5,"reason":"x"}`)
	before, err := canonical.Encode(a)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.ValidateArguments("payments.refund", a)
	_ = r.ValidateArguments("orders.lookup", a)
	after, _ := canonical.Encode(a)
	if string(before) != string(after) {
		t.Fatalf("arguments changed during validation:\n%s\n%s", before, after)
	}
}

// ---------------------------------------------------------------------------
// Numbers are exact

func bigSchema(t testing.TB) *Registry {
	t.Helper()
	big := json.Number("123456789012345678901234567890")
	return mustSingleTool(t, "num.exact", closedObject(obj{
		"max":   obj{"type": "integer", "maximum": json.Number("9007199254740992")},
		"min":   obj{"type": "integer", "minimum": json.Number("9007199254740993")},
		"eq":    obj{"const": json.Number("9007199254740993")},
		"huge":  obj{"type": "integer", "maximum": big},
		"dec":   obj{"type": "number", "maximum": json.Number("0.3")},
		"step":  obj{"type": "number", "multipleOf": json.Number("0.01")},
		"whole": obj{"type": "integer"},
	}))
}

func TestNumbersAreNotConvertedToFloat64(t *testing.T) {
	r := bigSchema(t)

	// 2^53 + 1 cannot be represented as a float64: it rounds to 2^53. Each pair
	// below would give the wrong answer if the value passed through float64.
	cases := []argCase{
		{"2^53 is at the maximum", `{"max":9007199254740992}`, true},
		{"2^53+1 is over the maximum", `{"max":9007199254740993}`, false},
		{"2^53+2 is over the maximum", `{"max":9007199254740994}`, false},
		{"2^53 is under the minimum", `{"min":9007199254740992}`, false},
		{"2^53+1 is at the minimum", `{"min":9007199254740993}`, true},
		{"2^53+1 equals the constant", `{"eq":9007199254740993}`, true},
		{"2^53 does not equal the constant", `{"eq":9007199254740992}`, false},
		{"2^53+2 does not equal the constant", `{"eq":9007199254740994}`, false},
		{"a 30-digit maximum is honored", `{"huge":123456789012345678901234567890}`, true},
		{"one over a 30-digit maximum", `{"huge":123456789012345678901234567891}`, false},
		{"a 30-digit value that rounds to the same float64", `{"huge":123456789012345678901234567900}`, false},
		{"0.3 is at the decimal maximum", `{"dec":0.3}`, true},
		{"0.30000000000000004 is over it", `{"dec":0.30000000000000004}`, false},
		{"0.3000000000000000000000001 is over it", `{"dec":0.3000000000000000000000001}`, false},
		{"0.07 is a multiple of 0.01", `{"step":0.07}`, true},
		{"0.29 is a multiple of 0.01", `{"step":0.29}`, true},
		{"1.15 is a multiple of 0.01", `{"step":1.15}`, true},
		{"0.075 is not", `{"step":0.075}`, false},
		{"100 is an integer", `{"whole":100}`, true},
		{"100.0 is an integer", `{"whole":100.0}`, true},
		{"1e2 is an integer", `{"whole":1e2}`, true},
		{"1E2 is an integer", `{"whole":1E2}`, true},
		{"1.5e1 is an integer", `{"whole":1.5e1}`, true},
		{"100.00000000000000000001 is not an integer", `{"whole":100.00000000000000000001}`, false},
		{"a 100-digit integer is an integer", `{"whole":` + strings.Repeat("9", 100) + `}`, true},
	}
	runArgCases(t, r, "num.exact", cases)
}

func TestLargeIntegersSurviveTheWholeRoundTrip(t *testing.T) {
	// From text, through the strict parser, canonical encoding and the schema
	// library's own reader, the digits stay exactly as sent.
	text := `{"n":9007199254740993,"m":18446744073709551617,"k":-9223372036854775809}`
	a := args(t, text)
	enc, err := canonical.Encode(a)
	if err != nil {
		t.Fatal(err)
	}
	if string(enc) != `{"k":-9223372036854775809,"m":18446744073709551617,"n":9007199254740993}` {
		t.Fatalf("canonical encoding changed the digits: %s", enc)
	}
	r := mustSingleTool(t, "num.exact", closedObject(obj{
		"n": obj{"type": "integer"}, "m": obj{"type": "integer"}, "k": obj{"type": "integer"},
	}))
	expectArgs(t, r, "num.exact", text, nil)
}

func TestIntegerBoundariesOfTheExampleSchema(t *testing.T) {
	r := exampleRegistry(t)
	base := func(n string) string {
		return `{"tenant_id":"t_a","order_id":"ord_1","amount_cents":` + n + `,"reason":"x"}`
	}
	for n, ok := range map[string]bool{
		"0": false, "1": true, "2": true, "999999": true, "1000000": true, "1000001": false,
		"-1": false, "-1000000": false, "10000000": false,
	} {
		var want error
		if !ok {
			want = ErrInvalidArguments
		}
		expectArgs(t, r, "payments.refund", base(n), want)
	}
}

// ---------------------------------------------------------------------------
// Number bounds

func TestNumberBounds(t *testing.T) {
	ok := []string{"0", "-0", "1", "1e1000", "1E+1000", "1e-1000", "1e0", "1e00001", "0.5e10", "123456789e500", strings.Repeat("9", MaxNumberChars)}
	for _, n := range ok {
		if !numberInRange(n) {
			t.Errorf("numberInRange(%q) = false, want true", abbrev(n))
		}
	}
	bad := []string{"1e1001", "1e-1001", "1E+1001", "1e10000", "1e999999", "1e999999999", "-1e999999999", "1e-999999999",
		"123456789e99999999999999999999", strings.Repeat("9", MaxNumberChars+1), "0." + strings.Repeat("0", MaxNumberChars) + "1"}
	for _, n := range bad {
		if numberInRange(n) {
			t.Errorf("numberInRange(%q) = true, want false", abbrev(n))
		}
	}
	if numberInRange("1e") || numberInRange("1ex") {
		t.Error("malformed exponents must not pass")
	}
}

func abbrev(s string) string {
	if len(s) > 24 {
		return s[:24] + "..."
	}
	return s
}

func TestExtremeNumbersAreRefusedCheaplyAndNeverPanic(t *testing.T) {
	// Before the bound existed, "1e1000001" made the schema library
	// dereference a nil pointer and "1e999999" cost ~20 ms and ~5 MB each.
	r := mustSingleTool(t, "num.exact", closedObject(obj{
		"n": obj{"type": "number", "maximum": 10},
		"i": obj{"type": "integer"},
		"m": obj{"type": "number", "multipleOf": json.Number("0.01")},
		"a": obj{"type": "array", "items": obj{"type": "number", "maximum": 10}},
	}))
	start := time.Now()
	for _, num := range []string{"1e1001", "1e999999", "1e1000001", "1e999999999", "-1e999999999", "1e-999999999",
		"123456789e99999999999999999999", strings.Repeat("9", MaxNumberChars+1)} {
		for _, field := range []string{"n", "i", "m"} {
			expectArgs(t, r, "num.exact", fmt.Sprintf(`{"%s":%s}`, field, num), ErrInvalidArguments)
		}
	}
	many := "[" + strings.TrimSuffix(strings.Repeat("1e999999,", 100), ",") + "]"
	expectArgs(t, r, "num.exact", `{"a":`+many+`}`, ErrInvalidArguments)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("refusing extreme numbers took %v", d)
	}

	// Numbers inside the bounds still work, including ones the old limit of
	// float64 could not hold.
	expectArgs(t, r, "num.exact", `{"i":1e400}`, nil)
	expectArgs(t, r, "num.exact", `{"n":1e-400}`, nil)
	expectArgs(t, r, "num.exact", `{"n":1e400}`, ErrInvalidArguments) // over the maximum of 10
}

func TestExtremeNumbersInASchemaFailTheLoad(t *testing.T) {
	for _, bad := range []any{json.Number("1e999999"), json.Number("1e-5000"), json.Number("1" + strings.Repeat("0", 300))} {
		s := closedObject(obj{"a": obj{"type": "number", "maximum": bad}})
		r, err := parseSchema(t, s)
		expectLoadError(t, r, err, KindSchema, "a number in the schema")
	}
}

func TestNumbersInRangeBoundsItsOwnRecursion(t *testing.T) {
	var deep any = json.Number("1")
	for i := 0; i < maxNumberWalkDepth+5; i++ {
		deep = []any{deep}
	}
	if numbersInRange(deep, 0) {
		t.Error("an absurdly deep value must be reported out of range, not walked")
	}
}

// ---------------------------------------------------------------------------
// Format assertions are on; content assertions are off

func TestFormatAssertionsAreEnforced(t *testing.T) {
	props := obj{}
	for _, f := range []string{"uuid", "date-time", "date", "time", "email", "ipv4", "ipv6", "hostname", "uri", "duration", "json-pointer"} {
		props["f_"+strings.ReplaceAll(f, "-", "_")] = obj{"type": "string", "format": f}
	}
	r := mustSingleTool(t, "fmt.check", closedObject(props))

	cases := []argCase{
		{"uuid ok", `{"f_uuid":"3d7b3a52-0c3f-4a74-8bb4-2f6f6c9d9e11"}`, true},
		{"uuid bad", `{"f_uuid":"not-a-uuid"}`, false},
		{"uuid with extra", `{"f_uuid":"3d7b3a52-0c3f-4a74-8bb4-2f6f6c9d9e11x"}`, false},
		{"date-time ok", `{"f_date_time":"2026-11-13T18:18:00+05:00"}`, true},
		{"date-time ok Z", `{"f_date_time":"2026-11-13T18:18:00Z"}`, true},
		{"date-time bad", `{"f_date_time":"yesterday"}`, false},
		{"date-time without zone", `{"f_date_time":"2026-11-13T18:18:00"}`, false},
		{"date ok", `{"f_date":"2026-11-13"}`, true},
		{"date bad month", `{"f_date":"2026-13-01"}`, false},
		{"date bad day", `{"f_date":"2026-02-30"}`, false},
		{"time ok", `{"f_time":"18:18:00Z"}`, true},
		{"time bad", `{"f_time":"25:00:00Z"}`, false},
		{"email ok", `{"f_email":"agent@example.com"}`, true},
		{"email bad", `{"f_email":"agent-at-example.com"}`, false},
		{"ipv4 ok", `{"f_ipv4":"192.168.0.1"}`, true},
		{"ipv4 bad", `{"f_ipv4":"256.1.1.1"}`, false},
		{"ipv6 ok", `{"f_ipv6":"2001:db8::1"}`, true},
		{"ipv6 bad", `{"f_ipv6":"2001:db8::zzzz"}`, false},
		{"hostname ok", `{"f_hostname":"example.com"}`, true},
		{"hostname bad", `{"f_hostname":"-bad-.example"}`, false},
		{"uri ok", `{"f_uri":"https://example.com/a?b=c"}`, true},
		{"uri bad", `{"f_uri":"not a uri"}`, false},
		{"duration ok", `{"f_duration":"P3DT4H"}`, true},
		{"duration bad", `{"f_duration":"3 days"}`, false},
		{"json-pointer ok", `{"f_json_pointer":"/a/b/0"}`, true},
		{"json-pointer bad", `{"f_json_pointer":"a/b"}`, false},
		{"format does not apply to non-strings", `{"f_uuid":"3d7b3a52-0c3f-4a74-8bb4-2f6f6c9d9e11"}`, true},
	}
	runArgCases(t, r, "fmt.check", cases)
}

func TestFormatAppliesOnlyToStrings(t *testing.T) {
	r := mustSingleTool(t, "fmt.check", closedObject(obj{"x": obj{"format": "email"}}))
	// No "type", so a number is allowed and the string-only format ignores it.
	expectArgs(t, r, "fmt.check", `{"x":5}`, nil)
	expectArgs(t, r, "fmt.check", `{"x":"nope"}`, ErrInvalidArguments)
	expectArgs(t, r, "fmt.check", `{"x":"a@b.co"}`, nil)
}

func TestUnknownFormatsAreAnnotationsOnly(t *testing.T) {
	r := mustSingleTool(t, "fmt.check", closedObject(obj{"x": obj{"type": "string", "format": "x-my-custom-format"}}))
	expectArgs(t, r, "fmt.check", `{"x":"anything at all"}`, nil)
}

func TestContentAssertionsStayDisabled(t *testing.T) {
	r := mustSingleTool(t, "content.check", closedObject(obj{
		"b64":  obj{"type": "string", "contentEncoding": "base64"},
		"json": obj{"type": "string", "contentMediaType": "application/json"},
		"both": obj{"type": "string", "contentEncoding": "base64", "contentMediaType": "application/json",
			"contentSchema": obj{"type": "object", "additionalProperties": false}},
	}))
	// With content assertions on, each of these would be rejected.
	expectArgs(t, r, "content.check", `{"b64":"!!! this is not base64 !!!"}`, nil)
	expectArgs(t, r, "content.check", `{"json":"{ definitely not json"}`, nil)
	expectArgs(t, r, "content.check", `{"both":"@@@@"}`, nil)
}

// ---------------------------------------------------------------------------
// Draft 2020-12 behavior

func TestDraft2020Semantics(t *testing.T) {
	t.Run("prefixItems is enforced", func(t *testing.T) {
		r := mustSingleTool(t, "d.t", closedObject(obj{"pair": obj{"type": "array",
			"prefixItems": []any{obj{"type": "integer"}, obj{"type": "string"}}, "items": false}}))
		expectArgs(t, r, "d.t", `{"pair":[1,"a"]}`, nil)
		expectArgs(t, r, "d.t", `{"pair":["a",1]}`, ErrInvalidArguments)
		expectArgs(t, r, "d.t", `{"pair":[1,"a",3]}`, ErrInvalidArguments)
	})
	t.Run("dependentRequired and if/then/else", func(t *testing.T) {
		// Conditions are written with "required": an "if" that used
		// "properties" would itself have to be closed, which changes its meaning.
		s := closedObject(obj{"kind": obj{"enum": []any{"card", "bank"}}, "card": obj{"type": "string"}, "bank": obj{"type": "string"}})
		s["dependentRequired"] = obj{"card": []any{"kind"}}
		s["if"] = obj{"required": []any{"card"}}
		s["then"] = obj{"required": []any{"kind"}}
		s["else"] = obj{"required": []any{"bank"}}
		r := mustSingleTool(t, "d.t", s)
		expectArgs(t, r, "d.t", `{"card":"x","kind":"card"}`, nil)
		expectArgs(t, r, "d.t", `{"card":"x"}`, ErrInvalidArguments)
		expectArgs(t, r, "d.t", `{"bank":"y"}`, nil)
		expectArgs(t, r, "d.t", `{"kind":"bank"}`, ErrInvalidArguments)
	})
	t.Run("const, enum, uniqueItems, minItems, maxItems", func(t *testing.T) {
		r := mustSingleTool(t, "d.t", closedObject(obj{
			"c": obj{"const": "x"},
			"e": obj{"enum": []any{"a", 1, nil}},
			"l": obj{"type": "array", "uniqueItems": true, "minItems": 1, "maxItems": 3, "items": obj{"type": "integer"}},
		}))
		expectArgs(t, r, "d.t", `{"c":"x","e":null,"l":[1,2,3]}`, nil)
		expectArgs(t, r, "d.t", `{"c":"y"}`, ErrInvalidArguments)
		expectArgs(t, r, "d.t", `{"e":"b"}`, ErrInvalidArguments)
		expectArgs(t, r, "d.t", `{"l":[]}`, ErrInvalidArguments)
		expectArgs(t, r, "d.t", `{"l":[1,1]}`, ErrInvalidArguments)
		expectArgs(t, r, "d.t", `{"l":[1,2,3,4]}`, ErrInvalidArguments)
		expectArgs(t, r, "d.t", `{"l":[1.0,1]}`, ErrInvalidArguments) // equal as numbers
	})
	t.Run("oneOf and not", func(t *testing.T) {
		s := closedObject(obj{"v": obj{"oneOf": []any{obj{"type": "integer", "minimum": 10}, obj{"type": "string"}}, "not": obj{"const": 99}}})
		r := mustSingleTool(t, "d.t", s)
		expectArgs(t, r, "d.t", `{"v":10}`, nil)
		expectArgs(t, r, "d.t", `{"v":"s"}`, nil)
		expectArgs(t, r, "d.t", `{"v":5}`, ErrInvalidArguments)
		expectArgs(t, r, "d.t", `{"v":99}`, ErrInvalidArguments)
	})
	t.Run("a local $ref is enforced, including through $defs", func(t *testing.T) {
		s := closedObject(obj{"id": obj{"$ref": "#/$defs/id"}, "ids": obj{"type": "array", "items": obj{"$ref": "#/$defs/id"}}})
		s["$defs"] = obj{"id": obj{"type": "string", "pattern": "^[a-z]{2,4}$"}}
		r := mustSingleTool(t, "d.t", s)
		expectArgs(t, r, "d.t", `{"id":"abc"}`, nil)
		expectArgs(t, r, "d.t", `{"id":"ABC"}`, ErrInvalidArguments)
		expectArgs(t, r, "d.t", `{"ids":["ab","cd"]}`, nil)
		expectArgs(t, r, "d.t", `{"ids":["ab","c"]}`, ErrInvalidArguments)
	})
	t.Run("nested objects are closed", func(t *testing.T) {
		r := mustSingleTool(t, "d.t", closedObject(obj{"o": closedObject(obj{"k": obj{"type": "string"}}, "k")}))
		expectArgs(t, r, "d.t", `{"o":{"k":"v"}}`, nil)
		expectArgs(t, r, "d.t", `{"o":{"k":"v","x":1}}`, ErrInvalidArguments)
		expectArgs(t, r, "d.t", `{"o":{}}`, ErrInvalidArguments)
		expectArgs(t, r, "d.t", `{"o":[]}`, ErrInvalidArguments)
	})
	t.Run("patternProperties still rejects names outside the pattern", func(t *testing.T) {
		s := obj{"type": "object", "additionalProperties": false, "patternProperties": obj{"^x_[a-z]+$": obj{"type": "integer"}}}
		r := mustSingleTool(t, "d.t", s)
		expectArgs(t, r, "d.t", `{"x_a":1,"x_b":2}`, nil)
		expectArgs(t, r, "d.t", `{"x_a":"s"}`, ErrInvalidArguments)
		expectArgs(t, r, "d.t", `{"y_a":1}`, ErrInvalidArguments)
	})
	t.Run("pattern uses Go regular expressions and is not anchored by default", func(t *testing.T) {
		r := mustSingleTool(t, "d.t", closedObject(obj{"p": obj{"type": "string", "pattern": "[0-9]+"}}))
		expectArgs(t, r, "d.t", `{"p":"abc123"}`, nil) // unanchored: a match anywhere
		expectArgs(t, r, "d.t", `{"p":"abc"}`, ErrInvalidArguments)
	})
	t.Run("a pattern that backtracking engines choke on runs in linear time", func(t *testing.T) {
		r := mustSingleTool(t, "d.t", closedObject(obj{"p": obj{"type": "string", "maxLength": 4000, "pattern": "^(a+)+$"}}))
		start := time.Now()
		expectArgs(t, r, "d.t", `{"p":"`+strings.Repeat("a", 3000)+`b"}`, ErrInvalidArguments)
		if d := time.Since(start); d > 2*time.Second {
			t.Fatalf("pattern evaluation took %v", d)
		}
	})
}

func TestStringLengthCountsCodePoints(t *testing.T) {
	r := mustSingleTool(t, "d.t", closedObject(obj{"s": obj{"type": "string", "maxLength": 3}}))
	expectArgs(t, r, "d.t", `{"s":"ééé"}`, nil) // 6 bytes, 3 characters
	expectArgs(t, r, "d.t", `{"s":"éééé"}`, ErrInvalidArguments)
}

// ---------------------------------------------------------------------------
// Concurrency

func TestConcurrentValidationIsSafe(t *testing.T) {
	r := exampleRegistry(t)
	type job struct {
		tool string
		args map[string]any
		want error
	}
	jobs := []job{
		{"orders.lookup", args(t, `{"tenant_id":"t_a","order_id":"ord_1"}`), nil},
		{"orders.lookup", args(t, `{"tenant_id":"T_A","order_id":"ord_1"}`), ErrInvalidArguments},
		{"payments.refund", args(t, `{"tenant_id":"t_a","order_id":"ord_1","amount_cents":1000000,"reason":"x"}`), nil},
		{"payments.refund", args(t, `{"tenant_id":"t_a","order_id":"ord_1","amount_cents":1000001,"reason":"x"}`), ErrInvalidArguments},
		{"accounts.close", args(t, `{"tenant_id":"t_a","account_id":"acct_1","reason":"x"}`), nil},
		{"accounts.close", args(t, `{"tenant_id":"t_a","account_id":"acct_1","reason":""}`), ErrInvalidArguments},
		{"orders.refund", args(t, `{}`), ErrUnknownTool},
	}

	const workers, rounds = 32, 150
	var wg sync.WaitGroup
	errs := make(chan string, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				j := jobs[(w+i)%len(jobs)]
				got := r.ValidateArguments(j.tool, j.args) // the same maps are shared on purpose: validation must not write to them
				if !errors.Is(got, j.want) && !(j.want == nil && got == nil) {
					errs <- fmt.Sprintf("%s: got %v, want %v", j.tool, got, j.want)
					return
				}
				_ = r.Has(j.tool)
				_, _ = r.Tool(j.tool)
				_ = r.Names()
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
