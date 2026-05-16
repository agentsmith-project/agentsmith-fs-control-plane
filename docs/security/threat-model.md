# Threat Model

## Assets

- JuiceFS metadata and object store credentials.
- Managed volume root.
- Repo payload data.
- JVS control metadata.
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

### JVS Control Metadata Tampering

Risk: client or workload modifies JVS metadata.

Controls:

- AFSCP-managed repos use JVS external control roots.
- WebDAV and workload mounts expose only payload roots.
- WebDAV rejects root-level `.jvs` access and creation attempts as defense-in-depth.
- Embedded-control repos are rejected for workload mounts until migrated or protected by a verified filtered view.
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

Risk: direct restore to a save point changes version state while a read-write export or workload mount keeps writing.

Controls:

- active session registry
- per-repo writer-session fence blocks new read-write sessions during direct restore
- direct restore rejects active or uncertain read-write sessions for GA
- repo JVS exclusive lock
- doctor verification
