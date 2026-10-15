# SentinelAPI

SentinelAPI is an experimental Go-based security gateway for LLM agent tool execution.

Its goal is to enforce deterministic validation and authorization policies before proposed tool calls reach downstream systems.

## Project status

Early development. The security architecture and threat model are being established before implementation.

This project is not production-ready and must not be used to authorize real financial transactions, destructive operations, or other sensitive actions.

## Planned capabilities

- HTTP gateway for structured tool calls
- JSON Schema validation
- Agent and tool authorization
- Policy-based allow and deny decisions
- Human approval for selected high-risk actions
- Structured audit events
- Adversarial security tests
- Reproducible performance benchmarks

## Development

The implementation uses Go. Setup and test instructions will be documented as the project develops.

## Security

Please do not submit real secrets, credentials, personal data, or production payloads in issues or test fixtures.

Security reporting instructions will be added before the first public release.

## License

License selection is pending.
