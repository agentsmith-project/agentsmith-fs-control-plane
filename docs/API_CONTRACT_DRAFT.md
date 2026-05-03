# API Contract Draft

This document is a draft. Treat it as a starting point for implementation review, not a frozen contract.

## Service Access

AFSCP APIs are internal only.

- Caller: AgentSmith API or privileged admin/migration jobs.
- Auth: service token or mTLS, to be decided before implementation.
- External users, Desktop, and sandbox workloads must not call AFSCP directly.

## Core Types

### WorkspaceStorageProfile

Owned by AgentSmith API, executed by AFSCP.

```json
{
  "tenant_workspace_id": "ws_123",
  "afscp_endpoint_id": "afscp_default",
  "default_filesystem_id": "jfs_default",
  "default_storage_pool_id": "pool_default",
  "path_prefix": "/agentsmith/workspaces/ws_123",
  "quota_bytes_default": 107374182400,
  "export_policy": {
    "webdav_enabled": true,
    "max_session_seconds": 86400
  },
  "template_policy": {
    "workspace_templates_enabled": true
  },
  "status": "active"
}
```

Do not add `allow_cross_workspace_clone`.

### StorageRepo

```json
{
  "storage_repo_id": "repo_123",
  "tenant_workspace_id": "ws_123",
  "filesystem_id": "jfs_default",
  "storage_pool_id": "pool_default",
  "repo_kind": "file_library",
  "repo_path": "/agentsmith/workspaces/ws_123/repos/repo_123",
  "payload_subdir": "workspace",
  "jvs_repo_id": "jvs_repo_abc",
  "status": "active"
}
```

### ExportAccess

Returned by AgentSmith API to Desktop/Web after AgentSmith authorization. AFSCP creates the runtime export.

```json
{
  "protocol": "webdav",
  "url": "https://files.example.com/e/export_123/",
  "username": "export_123",
  "password": "short-lived-secret",
  "mode": "read_write",
  "expires_at": "2026-05-03T12:00:00Z"
}
```

Do not include `metadata_url`, bucket URL, access key, or secret key.

### SandboxMountSpec

```json
{
  "storage_repo_id": "repo_123",
  "tenant_workspace_id": "ws_123",
  "filesystem_id": "jfs_default",
  "storage_pool_id": "pool_default",
  "payload_subdir": "/agentsmith/workspaces/ws_123/repos/repo_123/workspace",
  "mount_path": "/workspace",
  "read_only": false,
  "secret_ref": {
    "namespace": "sandbox-system",
    "name": "juicefs-pool-default"
  }
}
```

The exact shape must be aligned with sandbox-manager binding v2.

## Internal Endpoints

### Storage Pools

```http
POST /internal/v1/storage-pools/{poolId}:ensure
GET  /internal/v1/storage-pools/{poolId}/health
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
POST /internal/v1/templates/{templateId}:clone
```

Required invariant:

```text
source_template.tenant_workspace_id == target_workspace_id
```

AFSCP must validate this even if AgentSmith already validated it.

### Exports

```http
POST /internal/v1/repos/{repoId}/exports
DELETE /internal/v1/exports/{exportId}
```

### Sandbox

```http
POST /internal/v1/repos/{repoId}/sandbox-mount-spec
```

### Operations

```http
GET /internal/v1/operations/{operationId}
```

## Operation Requirements

Mutating endpoints must support:

- Idempotency key.
- Operation ID.
- Per-repo operation lock where applicable.
- Durable operation record.
- Structured JVS JSON output capture.
- Retry-safe status transitions.
- Audit event emission.
