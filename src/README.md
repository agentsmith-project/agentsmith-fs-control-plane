# Source

AFSCP source currently lives in the Go module under `cmd/` and `internal/`.
This `src/` directory is retained as a placeholder and should not be treated as
the source-of-truth for implementation status.

Current implementation status:

- Go module and neutral service skeleton exist.
- Command entrypoints exist for API, worker, export gateway, and contract
  verification.
- Config loading, structured logging, auth policy guardrails, standard errors,
  operation DTOs, route metadata, and contract verifier checks exist.
- Real endpoint handlers, durable DB mutations, JVS/WebDAV/mount/storage
  mutations, and operation worker execution are not implemented yet.

Continue from [docs/DEVELOPER_HANDOFF.md](../docs/DEVELOPER_HANDOFF.md).
