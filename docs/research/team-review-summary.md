# Team Review Summary

This summary captures the product and architecture review conclusions used to create the handoff scaffold.

## Product Review

Must emphasize:

- AFSCP is a storage control plane, not a user-facing app.
- Default shared JuiceFS filesystem/storage pool replaces per-task DB/bucket provisioning for new file libraries.
- AgentSmith workspace storage profile is the tenant policy boundary.
- Template clone is same-workspace only.
- Template clone creates an independent repo, not a shared directory.
- Desktop ordinary access moves to WebDAV/export.
- Ordinary concurrent file writes are allowed; AgentSmith does not merge conflicts.
- JVS operations are serialized per repo and recorded as durable operations.
- `.jvs` protection is a P0 blocker.

## Architecture Review

Recommended initial repo shape:

- `cmd/`: future API, worker, export gateway entrypoints.
- `internal/`: future application packages.
- `pkg/`: future shared contracts only if needed.
- `api/`: OpenAPI and schemas.
- `docs/contracts/`: cross-repo contracts.
- `docs/security/`: threat model and security review material.
- `docs/runbooks/`: local and operator runbooks.
- `deploy/`: future Kubernetes/Docker packaging.
- `migrations/`: future operation store schema.
- `test/`: future conformance and integration tests.

Implementation priorities:

1. Contracts.
2. Path resolver.
3. Durable operation store.
4. JVS runner.
5. Sandbox mount spec adapter.
6. WebDAV export with `.jvs` filtering and credential TTL.

## Critical Risks

- Existing direct credential routes can bypass AFSCP if not closed for ordinary users.
- Path resolver mistakes can become tenant isolation bugs.
- `.jvs` exposure breaks JVS trust.
- Cross-workspace template clone must be rejected in both AgentSmith API and AFSCP.
- AFSCP crash during storage mutation must be recoverable through operation records.
