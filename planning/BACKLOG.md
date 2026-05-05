# Open Readiness Items

Status: planning seeds. The authoritative scope is `docs/GA_PRE_DEV_READINESS.md`.

## Contract Work

- Decide service auth: mTLS, service token, or both.
- Finalize internal OpenAPI shape.
- Finalize standard operation and error envelopes.
- Finalize stable error families.
- Finalize namespace volume binding schema.
- Finalize caller-service authorization schema.
- Finalize workload mount binding/orchestrator plan schema.
- Finalize mount binding lease/status lifecycle.
- Finalize export session/access credential schema.
- Finalize writer-session fence contract.
- Finalize repo lifecycle fence, drain, tombstone, restore, purge, and recovery contract.

## Storage And Safety Work

- Design volume registry.
- Design namespace binding store.
- Design repo path resolver.
- Design JVS CLI wrapper and JSON parser.
- Design per-repo operation lock.
- Design per-repo writer-session fence.
- Design `.jvs` protected view for WebDAV and workload mounts.
- Define namespace disable behavior for active and uncertain sessions.
- Define repo lifecycle behavior for session drain, retention, restore, and purge.

## Functional Work

- Repo create.
- Repo archive/restore/delete/purge lifecycle.
- Save/history/restore.
- Template create from repo.
- Same-namespace template clone.
- Export create/revoke.
- Workload mount binding and orchestrator plan generation.

## Operations Work

- AFSCP deployment manifest.
- Secret management plan.
- Operation recovery reconciliation.
- Audit event outbox.
- Required GA runbooks.
- Observability and alerting.
- Backup and restore plan.
- Risk register closure.

## Outside GA Unless Re-Approved

- Operator break-glass restore flow.
- Explicit legacy migration tooling.
- Cross-namespace template sharing/import.
- Product display-name rename or catalog detach APIs inside AFSCP.
- Namespace delete APIs.
