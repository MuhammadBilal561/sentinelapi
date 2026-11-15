package registry

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"

	"github.com/MuhammadBilal561/sentinelapi/internal/canonical"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// errExternalLoad is what the URL loader answers to every request.
var errExternalLoad = errors.New("loading external schemas is disabled")

// refusingLoader is the URLLoader given to every schema compiler. It refuses
// every URL, so a schema can never cause a file read, an HTTP request or any
// other external access, whatever it references.
//
// The library answers requests for the standard JSON Schema meta-schemas from
// copies embedded in the library, before it consults a loader, so Draft
// 2020-12 compilation does not need this loader to succeed.
type refusingLoader struct{}

func (refusingLoader) Load(string) (any, error) { return nil, errExternalLoad }

// resourceURL is the base URI under which one tool's schema is registered.
// The ".invalid" top-level domain is reserved (RFC 2606) and can never
// resolve, so even a misconfigured loader could not reach a real host.
func resourceURL(name string, version int) string {
	return "https://sentinelapi.invalid/tools/" + name + "/v" + strconv.Itoa(version) + "/args.json"
}

// compileSchema compiles one args_schema as JSON Schema Draft 2020-12 with
// format assertions on and content assertions off. loader is consulted only
// for URLs the library cannot answer itself; production code passes
// refusingLoader and tests pass a recording one.
//
// The schema is canonically encoded and then read back with the library's own
// UnmarshalJSON, which keeps numbers as json.Number, so the compiler works on
// a private copy with exact numbers.
func compileSchema(name string, version int, doc any, loader jsonschema.URLLoader) (sch *jsonschema.Schema, err error) {
	defer func() {
		// The registry loads once at startup, so a failure is reported, not
		// fatal to a running service. Turn any library panic into an error
		// that fails the load like any other schema problem.
		if r := recover(); r != nil {
			sch, err = nil, errors.New("the schema compiler failed on this schema")
		}
	}()

	encoded, err := canonical.Encode(doc)
	if err != nil {
		return nil, fmt.Errorf("cannot encode the schema: %w", err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("cannot read the encoded schema: %w", err)
	}

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020) // schemas without "$schema" are Draft 2020-12
	c.AssertFormat()                     // "format" is enforced, not just annotated
	// AssertContent is deliberately NOT called: contentEncoding,
	// contentMediaType and contentSchema remain annotations.
	c.UseLoader(loader)

	url := resourceURL(name, version)
	if err := c.AddResource(url, instance); err != nil {
		return nil, err
	}
	return c.Compile(url)
}
