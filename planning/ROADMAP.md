# Roadmap

## P0

- Confirm runtime and framework.
- Implement AFSCP skeleton with internal auth and health endpoints.
- Implement durable operation store.
- Implement volume registry and health.
- Implement namespace volume binding and caller-service authorization.
- Implement repo create and path resolver.
- Integrate JVS init/save/history/restore.
- Generate generic workload mount bindings and orchestrator-only mount plans.
- Implement mount binding status, heartbeat, release, revoke, and stale-lease reconciliation.
- Implement WebDAV export flow.
- Implement namespace-scoped repo template clone.
- Add security tests for credentials, path traversal, namespace mismatch, and `.jvs`.
- Add restore-run writer-session fence and active read-write session rejection.

## P1

- Operator break-glass restore and richer drain UX.
- Directory quota automation.
- Shared PVC and `subPath` mount optimization if appropriate.
- Separate export gateway pool.
- Worker queue for heavy JVS operations.
- Lifecycle APIs for archive/rename/detach.
- Explicit legacy migration tooling.
- Optional SMB/NFS export.

## Later

- Multi-region volume policies.
- Compliance-aware volume classes.
- Retention policies.
- Billing/reporting hooks.
- Cross-namespace import/share product, only if explicitly approved in a future design.
