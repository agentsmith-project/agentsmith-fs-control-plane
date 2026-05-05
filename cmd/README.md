# Commands

No executable commands are implemented yet.

ADR 0005 selects Go. Expected commands:

- `afscp-api`: internal HTTP API.
- `afscp-worker`: async operation runner. Local development may run it in the same process as API.
- `afscp-export-gateway`: WebDAV gateway. It may run as a sidecar-compatible binary or package-backed command.
