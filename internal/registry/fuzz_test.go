package registry

import (
	"errors"
	"os"
	"testing"

	"github.com/MuhammadBilal561/sentinelapi/internal/canonical"
)

// FuzzParseRegistry feeds arbitrary bytes to the registry loader. It must never
// panic. When it accepts a document, every invariant the loader promises must
// hold for the registry it returns.
func FuzzParseRegistry(f *testing.F) {
	if data, err := os.ReadFile(repoFile(f, "examples", "config", "tools.json")); err == nil {
		f.Add(data)
	}
	f.Add([]byte(`{"registry_version":1,"tools":[]}`))
	f.Add([]byte(`{"registry_version":1,"tools":[{"name":"a.b","version":1,"risk":"read","executor":"x","review_fields":[],"audit_fields":[],` +
		`"args_schema":{"type":"object","additionalProperties":false}}]}`))
	f.Add([]byte(`{"registry_version":1,"tools":[{"name":"a.b","version":1,"risk":"read","executor":"x","review_fields":[],"audit_fields":[],` +
		`"args_schema":{"type":"object","additionalProperties":false,"properties":{"p":{"$ref":"https://example.com/x"}}}}]}`))
	f.Add([]byte(`{"registry_version":1,"tools":[{"name":"a.b","version":1,"risk":"read","executor":"x","review_fields":[],"audit_fields":[],` +
		`"args_schema":{"type":"object","additionalProperties":false,"maximum":1e999999999}}]}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := Parse(data)
		if err != nil {
			if r != nil {
				t.Fatal("an error came with a non-nil registry")
			}
			var le *LoadError
			if !errors.As(err, &le) {
				t.Fatalf("error is %T, want *LoadError", err)
			}
			return
		}
		if r.Len() < 1 || r.Len() > MaxTools {
			t.Fatalf("accepted a registry with %d tools", r.Len())
		}
		for _, name := range r.Names() {
			info, ok := r.Tool(name)
			if !ok || info.Name != name || !ValidToolName(name) || info.Version < 1 {
				t.Fatalf("inconsistent tool %q: %+v", name, info)
			}
			if info.Risk != RiskRead && info.Risk != RiskWrite {
				t.Fatalf("tool %q has risk %q", name, info.Risk)
			}
			// An empty object must be answered, not panic or fail internally.
			if err := r.ValidateArguments(name, map[string]any{}); errors.Is(err, ErrInternal) || errors.Is(err, ErrUnknownTool) {
				t.Fatalf("tool %q: %v", name, err)
			}
		}
	})
}

// FuzzValidateArguments feeds arbitrary bytes, parsed with the strict parser,
// to the example registry as tool arguments. Validation must never panic and
// must never fail internally for anything the strict parser accepts.
func FuzzValidateArguments(f *testing.F) {
	r, err := Load(repoFile(f, "examples", "config", "tools.json"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add([]byte(`{"tenant_id":"t_acme","order_id":"ord_1042"}`))
	f.Add([]byte(`{"tenant_id":"t_acme","order_id":"ord_1","amount_cents":1000000,"reason":"x"}`))
	f.Add([]byte(`{"amount_cents":1e999999999}`))
	f.Add([]byte(`{"amount_cents":1e1000001}`))
	f.Add([]byte(`{"amount_cents":99999999999999999999999999999999999999}`))
	f.Add([]byte(`{"reason":"` + string(rune(0x1F600)) + `"}`))
	f.Add([]byte(`{"a":[[[[[[[1]]]]]]]}`))

	names := r.Names()
	f.Fuzz(func(t *testing.T, data []byte) {
		v, err := canonical.Parse(data)
		if err != nil {
			return
		}
		a, ok := v.(map[string]any)
		if !ok {
			return
		}
		for _, name := range names {
			err := r.ValidateArguments(name, a)
			if err != nil && !errors.Is(err, ErrInvalidArguments) {
				t.Fatalf("%s: unexpected error %v", name, err)
			}
		}
	})
}
