package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/MuhammadBilal561/sentinelapi/internal/registry"
)

// exampleRegistryPath returns the absolute path of examples/config/tools.json,
// found from this source file so tests do not depend on the working directory.
func exampleRegistryPath() string {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		panic("cannot locate the api test sources")
	}
	return filepath.Join(filepath.Dir(here), "..", "..", "examples", "config", "tools.json")
}

// kitchenSinkSchema is the argument schema of the test-only tools "a" and
// "a.b". It accepts every JSON value type, so the M1 request-boundary tests,
// which exercise the parser with all kinds of argument values, can still reach
// the end of the pipeline for a registered tool.
const kitchenSinkSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "s":    {"type": "string", "maxLength": 4096},
    "note": {"type": "string"},
    "n":    {"type": "number"},
    "t":    {"type": "boolean"},
    "f":    {"type": "boolean"},
    "z":    {"type": "null"},
    "arr":  {"type": "array", "maxItems": 100, "items": {"type": ["integer", "string"]}},
    "l":    {"type": "array", "maxItems": 100, "items": {"type": "integer"}},
    "obj":  {"type": "object", "additionalProperties": false, "properties": {"k": {"type": "string"}}},
    "k":    {"anyOf": [{"type": "integer"}, {"$ref": "#"}]}
  }
}`

var (
	fixtureOnce sync.Once
	fixtureReg  *registry.Registry
	fixtureErr  error
)

// testRegistry returns the registry shared by the API tests: the three example
// tools plus the test-only kitchen-sink tools "a" and "a.b". A Registry is
// immutable and safe for concurrent use, so sharing it is fine.
func testRegistry() *registry.Registry {
	fixtureOnce.Do(func() {
		data, err := os.ReadFile(exampleRegistryPath())
		if err != nil {
			fixtureErr = err
			return
		}
		var doc struct {
			RegistryVersion json.Number       `json:"registry_version"`
			Tools           []json.RawMessage `json:"tools"`
		}
		if fixtureErr = json.Unmarshal(data, &doc); fixtureErr != nil {
			return
		}
		for _, name := range []string{"a", "a.b"} {
			doc.Tools = append(doc.Tools, json.RawMessage(fmt.Sprintf(
				`{"name":%q,"version":1,"risk":"read","executor":"test.sink","review_fields":[],"audit_fields":[],"args_schema":%s}`,
				name, kitchenSinkSchema)))
		}
		merged, err := json.Marshal(doc)
		if err != nil {
			fixtureErr = err
			return
		}
		fixtureReg, fixtureErr = registry.Parse(merged)
	})
	if fixtureErr != nil {
		panic("test registry: " + fixtureErr.Error())
	}
	return fixtureReg
}
