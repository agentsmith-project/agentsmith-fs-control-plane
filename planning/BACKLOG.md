# Backlog Seeds

These are planning seeds, not committed implementation promises.

## Contract Work

- Decide service auth: mTLS, service token, or both.
- Finalize internal OpenAPI shape.
- Finalize namespace volume binding schema.
- Finalize caller-service authorization schema.
- Finalize workload mount binding/orchestrator plan schema.
- Finalize mount binding lease/status lifecycle.
- Finalize export session/access credential schema.

## Storage Work

- Design volume registry.
- Design namespace binding store.
- Design repo path resolver.
- Design JVS CLI wrapper and JSON parser.
- Design per-repo operation lock.
- Design per-repo writer-session fence.
- Design `.jvs` protected view for WebDAV and workload mounts.

## Functional Work

- Repo create.
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
- Runbooks for failed repo create, save, restore, clone, and export.

## P1 Seeds

- Repo archive/delete/rename/detach lifecycle APIs.
- Operator break-glass restore flow.
- Explicit legacy migration tooling.
