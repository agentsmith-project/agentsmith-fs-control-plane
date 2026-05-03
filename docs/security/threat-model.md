# Threat Model

## Assets

- JuiceFS metadata and object store credentials.
- Shared JuiceFS filesystem root.
- Repo payload data.
- JVS `.jvs` metadata.
- Workspace storage profile.
- Export credentials.
- Operation records and audit logs.

## Primary Threats

### Credential Exposure

Risk: Desktop, Web, or sandbox workload receives JuiceFS root credential material.

Controls:

- AFSCP owns credentials.
- AgentSmith API returns `ExportAccess` only.
- K8s Secrets are scoped to CSI/Mount Pods.
- Logs redact secrets.

### Path Escape

Risk: caller accesses another workspace/repo or filesystem root through path traversal or symlink escape.

Controls:

- canonical path resolver
- no caller-provided raw paths
- workspace/repo consistency checks
- traversal and symlink tests

### `.jvs` Tampering

Risk: user, Desktop, or agent modifies JVS metadata.

Controls:

- WebDAV filter
- non-root sandbox workload
- restrictive `.jvs` ownership
- `jvs doctor --strict`

### Cross-Workspace Template Leak

Risk: template clone exposes data across AgentSmith workspaces.

Controls:

- AgentSmith API authorization check
- AFSCP source/target workspace path check
- test users with membership in multiple workspaces

### Operation Loss

Risk: AFSCP crashes during save/restore/clone and loses state.

Controls:

- durable operation store
- idempotency keys
- startup reconciliation
- per-operation recovery policy
