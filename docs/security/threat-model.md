# Threat Model

## Assets

- JuiceFS metadata and object store credentials.
- Managed volume root.
- Repo payload data.
- JVS `.jvs` metadata.
- Namespace volume bindings.
- Export credentials.
- Operation records and audit logs.
- Orchestrator mount plans and Secret references.

## Primary Threats

### Credential Exposure

Risk: client or workload receives JuiceFS root credential material.

Controls:

- AFSCP owns credentials.
- Calling products return WebDAV export credentials only.
- K8s Secrets or equivalent secret refs are scoped to CSI/Mount Pods.
- Secret references are returned only to the dedicated orchestrator role.
- Logs redact secrets.

### Caller Confused Deputy

Risk: a trusted service token is used to operate the wrong namespace.

Controls:

- caller-service authorization on namespace binding
- operation role checks
- namespace/resource consistency checks
- denied authorization audit events

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
- workload filtered mount/view or equivalent gate that blocks all `.jvs` lookup/read/write/create/rename/unlink/chmod/chown/link attempts
- reject workload mounts if `.jvs` cannot be hidden or blocked
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

### Restore Racing Active Writers

Risk: restore-run changes version state while a read-write export or workload mount keeps writing.

Controls:

- active session registry
- per-repo writer-session fence blocks new read-write sessions during restore-run
- restore-run rejects active read-write sessions in P0
- repo JVS exclusive lock
- doctor verification
