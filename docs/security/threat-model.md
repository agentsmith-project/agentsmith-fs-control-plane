# Threat Model

## Assets

- JuiceFS metadata and object store credentials.
- Managed volume root.
- Repo payload data.
- JVS `.jvs` metadata.
- Namespace volume bindings.
- Export credentials.
- Operation records and audit logs.

## Primary Threats

### Credential Exposure

Risk: client or workload receives JuiceFS root credential material.

Controls:

- AFSCP owns credentials.
- Calling products return `ExportAccess` only.
- K8s Secrets or equivalent secret refs are scoped to CSI/Mount Pods.
- Logs redact secrets.

### Path Escape

Risk: caller accesses another namespace/repo or filesystem root through path traversal or symlink escape.

Controls:

- canonical path resolver
- no caller-provided raw paths
- namespace/repo consistency checks
- traversal and symlink tests

### `.jvs` Tampering

Risk: client or workload modifies JVS metadata.

Controls:

- WebDAV filter
- non-root workload
- restrictive `.jvs` ownership
- `jvs doctor --strict`

### Cross-Namespace Template Leak

Risk: template clone exposes data across namespaces.

Controls:

- AFSCP source/target namespace check
- default cross-namespace clone rejection
- test actors with access to multiple namespaces

### Operation Loss

Risk: AFSCP crashes during save/restore/clone and loses state.

Controls:

- durable operation store
- idempotency keys
- startup reconciliation
- per-operation recovery policy
