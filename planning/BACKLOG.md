# Backlog Seeds

These are planning seeds, not committed implementation promises.

## Contract Work

- Decide service auth: mTLS, service token, or both.
- Finalize internal OpenAPI shape.
- Finalize namespace volume binding schema.
- Finalize workload mount spec schema.
- Finalize `ExportAccess` schema.

## Storage Work

- Design volume registry.
- Design namespace binding store.
- Design repo path resolver.
- Design JVS CLI wrapper and JSON parser.
- Design per-repo operation lock.
- Design `.jvs` protected view for WebDAV and workload mounts.

## Functional Work

- Repo create/archive/delete.
- Save/history/restore.
- Template create from repo.
- Same-namespace template clone.
- Export create/revoke.
- Workload mount spec generation.

## Operations Work

- AFSCP deployment manifest.
- Secret management plan.
- Operation recovery reconciliation.
- Audit event outbox.
- Runbooks for failed repo create, save, restore, clone, and export.
