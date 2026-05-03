# API Contract Draft

This document is a draft. Treat it as a starting point for implementation review, not a frozen contract.

## Service Access

AFSCP APIs are internal control-plane APIs.

- Caller: trusted product control planes, admin jobs, migration jobs, or operator tools.
- Auth: service token or mTLS, to be decided before implementation.
- External users, desktop clients, and workloads must not call AFSCP directly.
- Mutating calls must include the authorized end actor, not just the calling service identity.
- AFSCP audit records must distinguish `caller service invoked AFSCP` from `authorized actor requested the operation`.

### InternalRequestContext

```json
{
  "namespace_id": "ns_123",
  "authorized_actor": {
    "type": "user",
    "id": "user_123"
  },
  "caller": {
    "service": "example-caller-api"
  },
  "correlation_id": "corr_123",
  "idempotency_key": "idem_123"
}
```

P0 canonical transport should be the required headers in `docs/contracts/afscp-internal-api-v1.md`. The JSON example above is the logical context that must be recoverable from each mutating request.

## Core Types

### Volume

```json
{
  "volume_id": "vol_default",
  "backend": "juicefs",
  "isolation_class": "shared",
  "status": "active",
  "credential_ref": "secret://afscp/juicefs-default"
}
```

`credential_ref` is internal. It must not be returned to ordinary clients.

### NamespaceVolumeBinding

```json
{
  "namespace_id": "ns_123",
  "default_volume_id": "vol_default",
  "quota_bytes_default": 107374182400,
  "export_policy": {
    "webdav_enabled": true,
    "max_session_seconds": 86400
  },
  "template_policy": {
    "namespace_templates_enabled": true,
    "cross_namespace_clone_enabled": false
  },
  "status": "active"
}
```

Calling products must not provide authoritative raw filesystem paths. AFSCP computes canonical namespace roots from `namespace_id`, `volume_id`, and its own volume configuration.

### Repo

```json
{
  "repo_id": "repo_123",
  "namespace_id": "ns_123",
  "volume_id": "vol_default",
  "repo_kind": "repo",
  "repo_path": "/afscp/namespaces/ns_123/repos/repo_123",
  "payload_subdir": "/afscp/namespaces/ns_123/repos/repo_123",
  "jvs_repo_id": "jvs_repo_abc",
  "status": "active"
}
```

`repo_path` is the JVS `main` workspace real folder. `payload_subdir` is the AFSCP-generated JuiceFS subdirectory mounted/exported for clients; in P0 it is the same directory as `repo_path` and must be protected with `.jvs` filtering/permissions.

### RepoTemplate

```json
{
  "template_id": "tmpl_123",
  "namespace_id": "ns_123",
  "volume_id": "vol_default",
  "template_path": "/afscp/namespaces/ns_123/templates/tmpl_123",
  "jvs_repo_id": "jvs_repo_template_abc",
  "status": "active"
}
```

AFSCP stores template repos and can clone them. Calling products own template catalog metadata such as display names, descriptions, owners, tags, or visibility.

### ExportAccess

Returned by the calling product to a client after product authorization. AFSCP creates the runtime export.

```json
{
  "export_id": "export_123",
  "protocol": "webdav",
  "url": "https://files.example.com/e/export_123/",
  "username": "export_123",
  "password": "short-lived-secret",
  "mode": "read_write",
  "expires_at": "2026-05-03T12:00:00Z"
}
```

Do not include `metadata_url`, bucket URL, access key, or secret key.

### WorkloadMountSpec

```json
{
  "namespace_id": "ns_123",
  "repo_id": "repo_123",
  "volume_id": "vol_default",
  "payload_subdir": "/afscp/namespaces/ns_123/repos/repo_123",
  "mount_path": "/workspace",
  "read_only": false,
  "secret_ref": {
    "namespace": "storage-system",
    "name": "juicefs-vol-default"
  }
}
```

The exact shape must be aligned with the external orchestrator that consumes mount specs.

## Internal Endpoints

### Volumes

```http
POST /internal/v1/volumes/{volumeId}:ensure
GET  /internal/v1/volumes/{volumeId}/health
```

### Namespaces

```http
PUT /internal/v1/namespaces/{namespaceId}/volume-binding
GET /internal/v1/namespaces/{namespaceId}/volume-binding
```

### Repos

```http
POST /internal/v1/repos
GET  /internal/v1/repos/{repoId}
POST /internal/v1/repos/{repoId}:archive
POST /internal/v1/repos/{repoId}:restore-archived
```

### Save Points

```http
POST /internal/v1/repos/{repoId}/save-points
GET  /internal/v1/repos/{repoId}/save-points
POST /internal/v1/repos/{repoId}/restore-preview
POST /internal/v1/repos/{repoId}/restore
```

### Templates

```http
POST /internal/v1/repos/{sourceRepoId}:clone-to-template
POST /internal/v1/templates/{templateId}:clone
```

Required invariant:

```text
source_template.namespace_id == target_namespace_id
```

AFSCP must validate this even if the caller already validated it. Cross-namespace clone is rejected in P0 unless a future explicit admin/import flow is designed.

### Exports

```http
POST /internal/v1/repos/{repoId}/exports
DELETE /internal/v1/exports/{exportId}
```

### Workload Mounts

```http
POST /internal/v1/repos/{repoId}/workload-mount-spec
```

### Operations

```http
GET /internal/v1/operations/{operationId}
```

## Operation Requirements

Mutating endpoints must support:

- idempotency key
- correlation ID
- authorized actor
- caller service identity
- operation ID
- per-repo operation lock where applicable
- durable operation record
- structured JVS JSON output capture
- retry-safe status transitions
- audit event emission
