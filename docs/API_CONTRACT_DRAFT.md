# API Contract Draft

This document is a draft. Treat it as a starting point for implementation review, not a frozen contract.

## Service Access

AFSCP APIs are internal only.

- Caller: AgentSmith API or privileged admin/migration jobs.
- Auth: service token or mTLS, to be decided before implementation.
- External users, Desktop, and sandbox workloads must not call AFSCP directly.
- Mutating calls must include the authorized end actor, not just the calling service identity. AFSCP audit records must distinguish `AgentSmith API called AFSCP` from `user/system actor requested the product operation`.

### InternalRequestContext

```json
{
  "tenant_workspace_id": "ws_123",
  "authorized_actor": {
    "type": "user",
    "id": "user_123"
  },
  "correlation_id": "corr_123",
  "idempotency_key": "idem_123"
}
```

P0 canonical transport should be the required headers in `docs/contracts/agentsmith-afscp-internal-api-v1.md`. The JSON example above is the logical context that must be recoverable from each mutating request.

## Core Types

### WorkspaceStorageProfile

Owned by AgentSmith API, executed by AFSCP.

```json
{
  "tenant_workspace_id": "ws_123",
  "afscp_endpoint_id": "afscp_default",
  "default_filesystem_id": "jfs_default",
  "default_storage_pool_id": "pool_default",
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

AgentSmith API must not provide an authoritative raw filesystem path in the workspace storage profile. AFSCP computes the canonical workspace root from `tenant_workspace_id`, `filesystem_id`, `storage_pool_id`, and its own storage pool configuration.

### StorageRepo

```json
{
  "storage_repo_id": "repo_123",
  "tenant_workspace_id": "ws_123",
  "filesystem_id": "jfs_default",
  "storage_pool_id": "pool_default",
  "repo_kind": "file_library",
  "repo_path": "/agentsmith/workspaces/ws_123/repos/repo_123",
  "payload_subdir": "/agentsmith/workspaces/ws_123/repos/repo_123",
  "jvs_repo_id": "jvs_repo_abc",
  "status": "active"
}
```

`repo_path` is the JVS `main` workspace real folder. `payload_subdir` is the AFSCP-generated JuiceFS subdirectory mounted/exported for users; in P0 it is the same directory as `repo_path` and must be protected with `.jvs` filtering/permissions.

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
  "payload_subdir": "/agentsmith/workspaces/ws_123/repos/repo_123",
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

Saving a notebook task as a template uses a separate product flow:

```http
POST /internal/v1/repos/{sourceRepoId}:clone-to-template
```

The clone-to-template operation creates a workspace-scoped template repo from the source task/file-library repo after AFSCP creates a save point.

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
