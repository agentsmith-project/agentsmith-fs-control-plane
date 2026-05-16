# GA Readiness Map

Status: planning view. The authoritative scope is `docs/GA_PRE_DEV_READINESS.md`.

AFSCP is planned directly toward GA. The work is not split into product stages.

## Build Toward GA

- Confirm runtime and framework in ADR.
- Implement AFSCP skeleton with internal auth interface and health endpoints.
- Generate schemas and internal OpenAPI before endpoint handlers.
- Implement durable operation store, audit outbox, and recovery.
- Implement volume registry and health.
- Implement namespace volume binding and caller-service authorization.
- Implement repo create and canonical path resolver.
- Implement repo archive, restore-archived, delete, restore-tombstoned, and purge lifecycle operations.
- Integrate pinned JVS init/save point list/direct restore/clone/doctor behavior.
- Implement WebDAV export flow and policy gateway.
- Implement workload mount binding and orchestrator-only mount plan after the orchestrator contract is accepted.
- Implement mount binding status, heartbeat, release, revoke, and stale-lease reconciliation.
- Implement namespace-scoped repo template create and clone.
- Add security and conformance tests for credentials, path traversal, namespace mismatch, `.jvs`, idempotency, stable errors, and audit.
- Add direct restore writer-session fence and active/uncertain read-write session rejection.
- Complete GA runbooks, observability, backup/restore, and risk closure.

## Future Candidates Outside GA

- Operator break-glass restore with richer drain UX.
- Directory quota automation when volume capability supports it.
- Shared PVC and `subPath` mount optimization if appropriate.
- Separate export gateway pool.
- Worker queue for heavy JVS operations.
- Explicit legacy migration tooling.
- Optional SMB/NFS export.
- Multi-region volume policies.
- Compliance-aware volume classes.
- Advanced retention automation beyond the GA repo purge policy.
- Billing/reporting hooks.
- Cross-namespace import/share product, only if explicitly approved in a future design.
