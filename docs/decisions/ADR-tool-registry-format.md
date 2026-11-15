# ADR: Tool registry format (JSON, not YAML)

- Status: accepted (M2)
- Scope: `examples/config/tools.json`, `internal/registry`

## Context

SentinelAPI must know, before it serves anything, which tools exist, how risky
each is, and exactly which arguments each accepts. The registry is operator-
written configuration, but it is still *untrusted input to the loader*: a typo,
a hostile schema or a huge file must fail startup safely, never crash the
process, reach the network, or be served half-loaded.

## Decision

The registry is a single **JSON** file (default `examples/config/tools.json`,
override with `TOOL_REGISTRY_PATH`), parsed with the same strict parser that
reads HTTP requests (`internal/canonical`), under explicit limits.

Tool argument schemas are **JSON Schema Draft 2020-12**, compiled and evaluated
by `github.com/santhosh-tekuri/jsonschema/v6` (pinned v6.0.3). We do not write
our own validator.

## Reasons

1. **One strict parser.** Duplicate keys are rejected, depth, string length,
   array length and input size are bounded, numbers keep their exact text. A
   YAML parser would be a second, larger attack surface with anchors, aliases,
   implicit typing ("no" becoming false), multiple documents and duplicate-key
   behaviour that differs between libraries.
2. **No extra dependency.** Go has no YAML parser in the standard library. JSON
   needs none, and JSON Schema is itself JSON, so the schemas need no
   conversion step that could change their meaning.
3. **Exactness.** Large integers and boundaries must mean the same thing in the
   registry and in requests. Both go through the same number handling.
4. **Reviewability.** A diff of a JSON file is unambiguous.

## Consequences

- No comments in the registry file. Use the `description` keyword in schemas.
  A UTF-8 byte-order mark is rejected (the error says so); save without BOM.
- Limits: file 1 MiB, 32 tools, schema nesting depth 32, strings 8192 bytes,
  arrays 256 elements, numbers at most 256 characters with |exponent| <= 1000.
- Loading is all-or-nothing. Any problem in any tool stops startup with a
  non-zero exit; the listener is never opened.
- The registry is immutable after load. Changing it means restarting.
- Schemas are further restricted beyond the standard (see README): every object
  schema must set `additionalProperties: false`; `$ref` may only point inside
  the same schema; `$id`, dynamic references and `$vocabulary` are refused; the
  loader refuses every external load, so there is no network or filesystem
  access during compilation.
- If a friendlier format is wanted later, add a converter that emits this JSON
  and keep the loader unchanged.

## Alternatives considered

- **YAML**: rejected for the reasons above.
- **TOML**: no standard-library parser and no natural fit for nested schemas.
- **Go code**: would make the registry a compile-time artifact and mix operator
  configuration with source.
