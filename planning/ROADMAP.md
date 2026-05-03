# Roadmap

## P0

- Confirm runtime and framework.
- Implement AFSCP skeleton with internal auth and health endpoints.
- Implement durable operation store.
- Implement storage pool registry and health.
- Implement repo create and path resolver.
- Integrate JVS init/save/history/restore.
- Integrate sandbox binding v2.
- Implement WebDAV export flow.
- Implement workspace-scoped template clone.
- Add security tests for credentials, path traversal, workspace mismatch, and `.jvs`.

## P1

- Strict restore fencing.
- Directory quota automation.
- Shared PVC and `subPath` mount optimization if appropriate.
- Separate export gateway pool.
- Worker queue for heavy JVS operations.
- Lifecycle UI for archive/rename/detach.
- Explicit legacy migration tooling.
- Optional SMB/NFS export.

## Later

- Multi-region storage policies.
- Compliance-aware storage pools.
- Retention policies.
- Billing/reporting.
- Cross-workspace template product, only if explicitly approved in a future design.
