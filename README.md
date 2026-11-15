# SentinelAPI

SentinelAPI is intended to become an open-source security gateway for LLM
agent tool calls, written in Go. An agent proposes a tool name and JSON
arguments; the gateway decides, with deterministic server-side controls,
whether that call is valid, authorized, permitted by policy and eligible to
run. The model is never the authority that grants permission.

> **Status: Milestone M2 (tool registry and JSON Schema validation). This is
> not yet an authenticated or fully operational security gateway.** It does not
> authenticate callers, authorize agents, evaluate policy, handle approvals,
> write an audit log, or execute any tool. Calls that pass validation get
> `501 NOT_IMPLEMENTED`. **It has no authentication: run it on loopback only
> and never expose it to a network.** It is not production-ready.

## What is implemented

- **M0** HTTP skeleton: config from environment, JSON logs, `/healthz`,
  `/readyz`, request IDs, panic recovery, timeouts, graceful shutdown.
- **M1** Strict JSON parsing (`internal/canonical`): duplicate keys, depth,
  size and string limits, exact numbers, canonical encoding, and the request
  envelope for `POST /v1/tool-calls/evaluate` and `/execute`.
- **M2** A startup-loaded **tool registry** (`internal/registry`) and
  **JSON Schema validation** of tool arguments (Draft 2020-12).

## Request flow in M2

`POST /v1/tool-calls/evaluate` and `POST /v1/tool-calls/execute` run these
steps in order and stop at the first failure:

1. Content type, body size and strict JSON parse (M1). Errors: 415, 413, 400.
2. Envelope shape (M1): `tool_name`, `arguments`, optional `client_request_id`
   and `approval_id`, and `idempotency_key` (forbidden on evaluate, required on
   execute). Unknown fields are rejected. Error: 400 `INVALID_REQUEST`.
3. **Is the tool registered?** If not: `404 TOOL_UNKNOWN`.
4. **Do the arguments satisfy the tool's schema?** If not:
   `400 ARGS_SCHEMA_INVALID`.
5. Otherwise `501 NOT_IMPLEMENTED`. Nothing is authorized, approved or run.

Responses never echo argument values, schema text or schema paths. The error
for invalid arguments is deliberately fixed text; it says *that* the arguments
are invalid, not *why*. (Operator-useful detail appears only in startup errors
about the registry file, which contain no request data.)

```json
{"request_id":"req_...","error":{"code":"TOOL_UNKNOWN","message":"The requested tool is not registered.","field":"tool_name"}}
{"request_id":"req_...","error":{"code":"ARGS_SCHEMA_INVALID","message":"The tool arguments do not satisfy the tool's schema.","field":"arguments"}}
```

**Schema validation is not authorization.** A call that passes step 4 is
merely well-formed for that tool. It says nothing about who is asking or
whether they may do it. `accounts.close` is registered in the example although
no authorization exists yet. M3 and later add that.

## The tool registry

Location: `examples/config/tools.json`, or the file named by
`TOOL_REGISTRY_PATH`. The default is a path relative to the working
directory, so run the service from the repository root or set an absolute path.
Format rationale: [ADR](docs/decisions/ADR-tool-registry-format.md).

```json
{
  "registry_version": 1,
  "tools": [
    {
      "name": "orders.lookup",
      "version": 1,
      "risk": "read",
      "executor": "mock.orders",
      "review_fields": ["order_id"],
      "audit_fields": ["tenant_id", "order_id"],
      "args_schema": { "$schema": "https://json-schema.org/draft/2020-12/schema", "...": "..." }
    }
  ]
}
```

| Field | Rule |
| --- | --- |
| `name` | Unique. Lowercase dot-separated, same rule as the request envelope (`orders.lookup`), at most 64 characters |
| `version` | Positive integer |
| `risk` | `read` or `write` |
| `executor` | Identifier such as `mock.orders`. **Metadata only**: nothing is instantiated or called |
| `review_fields` | Top-level argument properties a human reviewer should see |
| `audit_fields` | Top-level argument properties meant for the audit log |
| `args_schema` | JSON Schema, rules below |

Unknown fields, duplicate tool names, duplicate JSON keys and malformed
metadata are startup errors. Limits: file 1 MiB (read with a bound), 32 tools,
schema nesting depth 32, strings 8192 bytes, arrays 256 elements.

The example registers three tools: `orders.lookup` (read), `payments.refund`
(write; `amount_cents` is an integer from 1 to 1000000, `reason` at most 200
characters) and `accounts.close` (write).

### Startup behaviour

The registry is loaded and every schema compiled **before** the listener
opens. If anything fails, the service logs `cannot load tool registry` with the
path and a message, exits with code 2, and never serves. Partial registries are
not served. `/readyz` can only report ready after a successful load.

### Schema rules

Draft 2020-12 is the only dialect (`$schema`, if present, must be that URI and
only at the root). Schemas are compiled by the library; we do not reimplement
validation. Our additional rules, checked by a walker before compiling:

- The root is `"type": "object"` with `"additionalProperties": false`.
- **Every nested object schema** (anything with `type: object`, `properties`,
  `patternProperties`, `additionalProperties` or `unevaluatedProperties`) must
  also set `"additionalProperties": false`. The walker follows real subschema
  positions (`properties`, `patternProperties`, `$defs`, `dependentSchemas`,
  `items`, `prefixItems`, `allOf`, `anyOf`, `oneOf`, `not`, `if`, `then`,
  `else`, `contains`, `propertyNames`) and does not treat `const`, `enum`,
  `default` or `examples` as schemas.
- **`$ref` must be local**: `#` or `#/pointer`, and must point at a schema
  location in the same document (for example `#/$defs/orderIdentifier`).
  `http`, `https`, `file` and every other external reference is rejected.
- `$id`, `$dynamicRef`, `$dynamicAnchor`,
  `$recursiveRef`, `$recursiveAnchor` and `$vocabulary` are rejected, and a
  `$ref` to an anchor name is rejected.
- The compiler gets a loader that **refuses every load**. The Draft 2020-12
  meta-schema is built into the library, so compilation works offline. There is
  no network or filesystem access when compiling or validating.
- `format` is **asserted** (for example `date-time`, `email`). Unknown formats
  are ignored. `contentEncoding`, `contentMediaType` and `contentSchema` stay
  **annotations only**.
- Patterns use Go RE2 syntax (linear time) and are unanchored unless you write
  `^...$`.
- Numbers in schemas and arguments may have at most 256 characters and an
  exponent no larger than 1000 in magnitude; larger ones are refused (the
  schema at startup, arguments as `ARGS_SCHEMA_INVALID`). This guards against a
  library panic and resource abuse.

Known limitations of these rules: a schema inside `if`/`then`/`else` or
`allOf` that uses `properties` must itself be closed (which changes its
meaning); untyped schemas such as `{}` or `true` accept any value, so give
every property a `type`; references by `$id` or anchor name are not supported.

### Validation preserves numbers

Arguments are strictly parsed, canonically encoded and handed to the library
with `jsonschema.UnmarshalJSON`, so integers keep exact precision and are never
converted to `float64`. Tests cover boundaries and 20+ digit integers.

### Adding a tool safely

1. Copy an existing entry and give it a new unique `name`.
2. Set `risk` honestly: anything that changes state is `write`.
3. Write the schema with `"additionalProperties": false` at the root and in
   every nested object. Give every property a `type` and bounds (`pattern`,
   `maxLength`, `minimum`, `maximum`). Mark required properties.
4. List only declared top-level properties in `review_fields` / `audit_fields`.
5. Start the service. A schema mistake stops startup with a message naming the
   tool and the location.
6. Add tests for valid and invalid arguments.

## Configuration

All variables are optional. An unset or empty variable uses its default.

| Variable | Default | Meaning |
| --- | --- | --- |
| `HTTP_ADDR` | `127.0.0.1:8080` | Listen address, `host:port` (port `0` picks a free port) |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `TOOL_REGISTRY_PATH` | `examples/config/tools.json` | Tool registry file (relative paths resolve against the working directory) |
| `HTTP_READ_HEADER_TIMEOUT` | `5s` | Time allowed to read request headers |
| `HTTP_READ_TIMEOUT` | `10s` | Time allowed to read the whole request |
| `HTTP_WRITE_TIMEOUT` | `15s` | Time allowed to write the response |
| `HTTP_IDLE_TIMEOUT` | `60s` | Keep-alive idle timeout |
| `SHUTDOWN_TIMEOUT` | `10s` | Maximum time to drain on shutdown |

Durations use Go syntax such as `500ms`, `5s` or `2m` and must be greater than
zero. The default address is loopback only; do not bind `0.0.0.0` while there
is no authentication.

Logs contain request ID, method, path, status and duration. They never contain
request bodies, headers, query strings, credentials or argument values.

## Run on Windows (PowerShell)

From the repository root:

```powershell
go run ./cmd/sentinelapi
```

Or build and run:

```powershell
go build -o bin\sentinelapi.exe .\cmd\sentinelapi
.\bin\sentinelapi.exe
```

Use another registry for one run:

```powershell
$env:TOOL_REGISTRY_PATH = "C:\path\to\tools.json"
go run ./cmd/sentinelapi
```

## Try it

With the server running (use `curl.exe`, not the `curl` alias):

```powershell
# Valid call: 501 NOT_IMPLEMENTED
curl.exe -i -X POST http://127.0.0.1:8080/v1/tool-calls/evaluate -H "Content-Type: application/json" --data-raw '{\"tool_name\":\"orders.lookup\",\"arguments\":{\"tenant_id\":\"t_acme\",\"order_id\":\"ord_1042\"}}'

# Unknown tool: 404 TOOL_UNKNOWN
curl.exe -i -X POST http://127.0.0.1:8080/v1/tool-calls/evaluate -H "Content-Type: application/json" --data-raw '{\"tool_name\":\"nope.tool\",\"arguments\":{}}'

# Invalid arguments (amount over the maximum): 400 ARGS_SCHEMA_INVALID
curl.exe -i -X POST http://127.0.0.1:8080/v1/tool-calls/evaluate -H "Content-Type: application/json" --data-raw '{\"tool_name\":\"payments.refund\",\"arguments\":{\"tenant_id\":\"t_acme\",\"order_id\":\"ord_1\",\"amount_cents\":1000001,\"reason\":\"x\"}}'
```

If quoting gives trouble, put the JSON in a file and use `--data-binary "@call.json"`.

## Test and check

```powershell
gofmt -l .            # no output means all formatted
go mod tidy
go mod verify
go vet ./...
go test -count=1 ./...
go test -race ./...   # see the note below
go build ./...
```

Optional fuzzing (each runs until stopped or the time elapses):

```powershell
go test ./internal/canonical -run '^$' -fuzz FuzzParseStrict -fuzztime 30s
go test ./internal/registry  -run '^$' -fuzz FuzzParseRegistry -fuzztime 30s
go test ./internal/registry  -run '^$' -fuzz FuzzValidateArguments -fuzztime 30s
```

The race detector needs cgo and a C compiler. On Windows that means a MinGW-w64
`gcc` and `$env:CGO_ENABLED = "1"`. A very old MinGW gcc can produce
executables that fail with `0xc0000139` (see golang/go#66934); if so, run
`-race` in CI or WSL instead. Tests locate repository files from their own
source path, so they do not depend on the working directory.

## Continuous integration

`.github/workflows/ci.yml` runs gofmt, `go vet`, `go test -race` and
`go build` on push and pull request.

## Roadmap

**M3 (not started)** adds the decision layer: agent identity and
authorization, policy evaluation and the first APPROVED/BLOCKED decisions.
Later: single-use approvals bound to exact arguments, a tamper-evident audit
log, and a mock executor. M2 deliberately includes none of these.

## License

MIT. See [LICENSE](LICENSE).
