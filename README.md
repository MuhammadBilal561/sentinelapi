# SentinelAPI

SentinelAPI is intended to become an open-source security gateway for LLM
agent tool calls, written in Go. An agent proposes a tool name and JSON
arguments; the gateway decides, with deterministic server-side controls,
whether that call is valid, authorized, permitted by policy and eligible to
run. The model is never the authority that grants permission.

> **Status: Milestone M0 (service skeleton). This is not yet a security
> gateway.** It does not authenticate callers, validate tool calls, apply
> policy, handle approvals, write an audit log, or execute or authorize any
> tool call. There is no `/v1/tool-calls` endpoint yet. Do not rely on it for
> security, and it is not production-ready.

## What is implemented in M0

- A Go standard-library HTTP service (no third-party dependencies).
- Configuration from environment variables, validated at startup. Invalid
  configuration makes the process exit with code 2.
- Structured JSON logs (`log/slog`) on stdout.
- `GET /healthz` returns `200 {"status":"ok"}` (liveness).
- `GET /readyz` returns `200 {"status":"ready"}` once startup has finished,
  and `503 {"status":"not_ready"}` before that and while shutting down.
  At this stage readiness does **not** assert that a tool registry, policy
  engine or database is healthy, because none exist yet. Real dependency
  checks plug in through `api.ReadinessCheck` in later milestones.
- A server-generated `X-Request-ID` header (`req_` plus 16 hex characters) on
  every response. Client-supplied IDs are ignored.
- Panic recovery that returns a generic `500`. The log records only a generic 
  event with the request ID and the panic's Go type, never the panic value or
  a stack trace, because either can contain sensitive data.
- HTTP server timeouts, and graceful shutdown on Ctrl+C or SIGTERM.
- Access logs contain only request ID, method, path, status and duration: no
  bodies, headers, query strings or credentials.

## Configuration

All variables are optional. An unset or empty variable uses its default.

| Variable | Default | Meaning |
| --- | --- | --- |
| `HTTP_ADDR` | `127.0.0.1:8080` | Listen address, `host:port` (port `0` picks a free port) |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `HTTP_READ_HEADER_TIMEOUT` | `5s` | Time allowed to read request headers |
| `HTTP_READ_TIMEOUT` | `10s` | Time allowed to read the whole request |
| `HTTP_WRITE_TIMEOUT` | `15s` | Time allowed to write the response |
| `HTTP_IDLE_TIMEOUT` | `60s` | Keep-alive idle timeout |
| `SHUTDOWN_TIMEOUT` | `10s` | Maximum time to drain on shutdown |

Durations use Go syntax such as `500ms`, `5s` or `2m` and must be greater than
zero. The default address listens on loopback only; binding to `0.0.0.0` exposes
an unauthenticated service, so avoid it for now.

## Run on Windows (PowerShell)

Requires Go. The `go` line in `go.mod` sets the minimum Go version.

```powershell
go run ./cmd/sentinelapi
```

Or build and run a binary:

```powershell
go build -o bin\sentinelapi.exe .\cmd\sentinelapi
.\bin\sentinelapi.exe
```

Override settings for one run:

```powershell
$env:HTTP_ADDR = "127.0.0.1:9090"; $env:LOG_LEVEL = "debug"
go run ./cmd/sentinelapi
```

Press Ctrl+C to stop; the server drains in-flight requests first.

## Try the endpoints

In a second PowerShell window, with the server running on the default address:

```powershell
Invoke-RestMethod http://127.0.0.1:8080/healthz
Invoke-RestMethod http://127.0.0.1:8080/readyz

# Show status code and the request ID header:
$r = Invoke-WebRequest http://127.0.0.1:8080/healthz
$r.StatusCode
$r.Headers["X-Request-ID"]
```

`curl.exe` also works (use `curl.exe`, not the `curl` alias):

```powershell
curl.exe -i http://127.0.0.1:8080/readyz
```

## Test and check

No GNU Make is needed. Run these from the repository root:

```powershell
gofmt -l .            # lists unformatted files; no output means all formatted
gofmt -w .            # formats files in place
go vet ./...
go test ./...
go test -race ./...   # see the note below
go build ./...
```

The race detector needs cgo and a C compiler. On Windows that means installing
a MinGW-w64 `gcc` and setting `$env:CGO_ENABLED = "1"`. If you do not have one,
`go test ./...` still works locally and CI runs `go test -race ./...` on Linux.

If you do have GNU Make, `make check`, `make race` and `make run` wrap the same
commands.

## Continuous integration

`.github/workflows/ci.yml` runs on every push and pull request: gofmt check,
`go vet`, `go test -race` and `go build`, using the Go version from `go.mod`.

## Roadmap

Later milestones (not yet started) add strict request parsing, a tool registry
with JSON Schema validation, agent identity and authorization, a policy engine,
single-use approvals bound to exact arguments, a tamper-evident audit log, and a
mock executor. See the design specification for the threat model and invariants.

## License

MIT. See [LICENSE](LICENSE).