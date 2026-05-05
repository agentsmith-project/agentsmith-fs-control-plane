# API Contract Draft

Status: implementation review draft. This document is the product-agnostic narrative source for the first internal OpenAPI spec.

The implementation team may start service skeleton work before this draft is frozen. Endpoint handlers and generated clients should wait until request, response, and error schemas are written under `api/schemas/` and the P0 OpenAPI file exists under `api/openapi/`.

## Service Access

AFSCP APIs are internal control-plane APIs.

- Caller: trusted product control planes, admin jobs, migration jobs, operator tools, and a dedicated workload orchestrator service.
- Authentication: deployment-specific service authentication, such as mTLS SPIFFE identity, signed service token, or both. The authenticated principal must map to canonical `caller_service`.
- Authorization: AFSCP authorizes `caller_service` against namespace and admin policies before checking resource consistency.
- External users, desktop clients, and workloads must not call AFSCP directly.
- Mutating calls must include the authorized end actor, not just the calling service identity.
- AFSCP audit records must distinguish `caller_service invoked AFSCP` from `authorized_actor requested the operation`.

### Canonical Request Context

```json
{
  "namespace_id": "ns_123",
  "caller_service": "example-product-api",
  "authorized_actor": {
    "type": "user",
    "id": "user_123"
  },
  "correlation_id": "corr_123",
  "idempotency_key": "idem_123"
}
```

The P0 transport uses the required headers in [contracts/afscp-internal-api-v1.md](contracts/afscp-internal-api-v1.md). Header values must map into this canonical context.

## Core Types

### Volume

```json
{
  "volume_id": "vol_default",
  "backend": "juicefs",
  "isolation_class": "shared",
  "status": "active",
  "credential_ref": "secret://afscp/juicefs-default",
  "capabilities": {
    "webdav_export": true,
    "workload_mount": true,
    "jvs_external_control_root": true,
    "filtered_mount": false,
    "directory_quota": false,
    "csi_driver": "juicefs.csi.io",
    "storage_class": "juicefs-default",
    "permission_model": "posix_uid_gid"
  }
}
```

`credential_ref` is internal. It must not be returned to ordinary clients, workloads, or non-admin caller responses.

`workload_mount=true` requires JVS control metadata to be outside the workload-visible payload root, or an equivalent verified filtered view. The default AFSCP path uses JVS external control root mode, so `filtered_mount=false` is acceptable: the orchestrator mounts only `payload_volume_subdir`.

### NamespaceVolumeBinding

```json
{
  "namespace_id": "ns_123",
  "default_volume_id": "vol_default",
  "allowed_callers": [
    {
      "caller_service": "example-product-api",
      "roles": ["repo_admin", "restore_admin", "export_admin", "template_admin", "mount_admin"]
    },
    {
      "caller_service": "example-orchestrator",
      "roles": ["orchestrator_mount"]
    }
  ],
  "quota_bytes_default": 107374182400,
  "export_policy": {
    "webdav_enabled": true,
    "max_session_seconds": 86400
  },
  "mount_policy": {
    "workload_mount_enabled": true,
    "workload_mount_requires_jvs_external_control_root": true,
    "allow_privileged_workload": false
  },
  "template_policy": {
    "namespace_templates_enabled": true,
    "cross_namespace_clone_enabled": false
  },
  "status": "active"
}
```

Calling products must not provide authoritative raw filesystem paths. AFSCP computes canonical namespace roots from `namespace_id`, `volume_id`, and its own volume configuration.

`mount_policy.workload_mount_enabled=true` is a namespace permission, not proof that the selected volume/runtime can mount JVS repos safely. AFSCP must also check `Volume.capabilities.workload_mount`, require JVS external control roots for new repos, and issue only payload-root mount plans.

### Repo

Ordinary repo responses expose IDs and status only.

```json
{
  "repo_id": "repo_123",
  "namespace_id": "ns_123",
  "volume_id": "vol_default",
  "repo_kind": "repo",
  "jvs_repo_id": "jvs_repo_abc",
  "status": "active",
  "created_at": "2026-05-03T12:00:00Z"
}
```

`control_root_path`, `payload_root_path`, `control_volume_subdir`, `payload_volume_subdir`, `.jvs` paths, and JuiceFS root details are internal implementation state. Admin/debug APIs may expose them behind break-glass controls, but ordinary product callers should not depend on them.

### RepoTemplate

P0 `RepoTemplate` is an immutable published snapshot repo. To change template contents, callers create a new template or a new template revision outside the P0 core contract.

```json
{
  "template_id": "tmpl_123",
  "namespace_id": "ns_123",
  "volume_id": "vol_default",
  "source_repo_id": "repo_123",
  "source_save_point_id": "sp_456",
  "jvs_repo_id": "jvs_repo_template_abc",
  "status": "published",
  "created_at": "2026-05-03T12:00:00Z"
}
```

AFSCP owns template repo storage and clone execution. Calling products own template catalog metadata such as display names, descriptions, owners, tags, and product visibility.

### ExportSession And Access Credential

AFSCP stores an `ExportSession` and returns a short-lived secret-bearing credential view when created.

```json
{
  "export_id": "export_123",
  "namespace_id": "ns_123",
  "repo_id": "repo_123",
  "protocol": "webdav",
  "mode": "read_write",
  "status": "active",
  "expires_at": "2026-05-03T12:00:00Z",
  "access": {
    "url": "https://files.example.com/e/export_123/",
    "auth": {
      "type": "basic",
      "username": "export_123",
      "password": "short-lived-secret"
    }
  }
}
```

Do not include `metadata_url`, bucket URL, access key, secret key, raw mount command, or JuiceFS root credential references.

### Workload Mount Binding

Product callers create a mount binding and receive an opaque identifier suitable for their orchestration flow.

```json
{
  "mount_binding_id": "wmb_123",
  "namespace_id": "ns_123",
  "repo_id": "repo_123",
  "volume_id": "vol_default",
  "mount_path": "/workspace",
  "read_only": false,
  "status": "issued",
  "lease_expires_at": "2026-05-03T13:00:00Z"
}
```

The privileged orchestrator service obtains an `OrchestratorMountPlan` for a binding. It is not returned to ordinary product callers or workloads.

```json
{
  "mount_binding_id": "wmb_123",
  "volume_id": "vol_default",
  "payload_volume_subdir": "afscp/namespaces/ns_123/repos/repo_123/payload",
  "mount_path": "/workspace",
  "read_only": false,
  "secret_ref": {
    "namespace": "storage-system",
    "name": "juicefs-vol-default"
  },
  "security_policy": {
    "run_as_non_root": true,
    "allow_privileged": false,
    "drop_capabilities": ["CAP_DAC_OVERRIDE"],
    "jvs_control_outside_payload": true
  }
}
```

This example assumes an AFSCP-managed repo created with JVS external control root mode. AFSCP must not return `control_volume_subdir` or `control_root_path` in the orchestrator plan.

`payload_volume_subdir` is relative to the JuiceFS filesystem root and must have no leading slash. The AFSCP-managed subroot is `afscp/`, so repo payload subdirs include that prefix. `secret_ref` is visible only to the dedicated orchestrator identity.

## Internal Endpoints

### Volumes

```http
POST /internal/v1/volumes/{volumeId}:ensure
GET  /internal/v1/volumes/{volumeId}/health
```

### Namespaces

```http
PUT  /internal/v1/namespaces/{namespaceId}
POST /internal/v1/namespaces/{namespaceId}:disable
PUT  /internal/v1/namespaces/{namespaceId}/volume-binding
GET  /internal/v1/namespaces/{namespaceId}/volume-binding
```

### Repos

```http
POST /internal/v1/repos
GET  /internal/v1/repos/{repoId}
```

Repo archive/delete/rename/detach are P1 lifecycle APIs. They should not be implemented in the P0 API unless a separate lifecycle state machine and active session drain contract is accepted.

### Save Points

```http
POST /internal/v1/repos/{repoId}/save-points
GET  /internal/v1/repos/{repoId}/save-points
POST /internal/v1/repos/{repoId}/restore-preview
POST /internal/v1/repos/{repoId}/restore-run
```

P0 `restore-run` must reject active read-write export or workload mount sessions by default. An operator break-glass restore can be designed separately with explicit approval, session revoke/drain, and audit.

Restore-run must acquire the per-repo writer-session fence before checking active sessions. While this fence is held, AFSCP rejects new read-write export and workload mount binding issuance for the repo. The fence is released only after restore, `jvs doctor --strict`, and audit completion.

### Templates

```http
POST /internal/v1/repo-templates
POST /internal/v1/repo-templates/{templateId}:clone
```

`CreateRepoTemplateFromRepoRequest`:

```json
{
  "namespace_id": "ns_123",
  "source_repo_id": "repo_123",
  "target_template_id": "tmpl_123",
  "clone_history_mode": "main"
}
```

P0 template creation always creates a fresh source save point inside the operation and records it as `source_save_point_id` on the resulting `RepoTemplate`. Caller-provided historical `source_save_point_id` is not accepted in P0 because JVS `repo clone` clones the current source repo/workspace rather than directly cloning an arbitrary historical save point.

If the source repo changes after the source save point is created and JVS reports dirty/current-state mismatch before clone, AFSCP fails with `SOURCE_DIRTY_AFTER_TEMPLATE_SAVE` and the caller may retry. Creating a template from an older save point requires a future staging restore/import flow.

`clone_history_mode` must be pinned to the verified JVS capability for the deployment. P0 may use `main` first. `all` is allowed only after the pinned JVS version supports durable imported-save-point protection.

`CloneRepoTemplateRequest`:

```json
{
  "namespace_id": "ns_123",
  "template_id": "tmpl_123",
  "target_repo_id": "repo_new_123"
}
```

Required invariants:

```text
template.namespace_id == request.namespace_id
template.volume_id == namespace.default_volume_id in P0
cross_namespace_clone_enabled == false in P0
```

If a namespace binding changes after a template is created and the template volume differs from the namespace default volume, P0 clone must reject with `VOLUME_MISMATCH_REQUIRES_IMPORT`. It must not silently create a new repo outside the current namespace volume policy.

### Exports

```http
POST   /internal/v1/repos/{repoId}/exports
GET    /internal/v1/exports/{exportId}
DELETE /internal/v1/exports/{exportId}
```

### Workload Mounts

```http
POST /internal/v1/repos/{repoId}/workload-mount-bindings
GET  /internal/v1/workload-mount-bindings/{mountBindingId}
PATCH /internal/v1/workload-mount-bindings/{mountBindingId}/status
POST /internal/v1/workload-mount-bindings/{mountBindingId}:heartbeat
POST /internal/v1/workload-mount-bindings/{mountBindingId}:release
POST /internal/v1/workload-mount-bindings/{mountBindingId}:revoke
GET  /internal/v1/workload-mount-bindings/{mountBindingId}/orchestrator-plan
```

Only caller services with the `orchestrator_mount` role may call `orchestrator-plan`.

P0 mount bindings must be lease-based. Read-write bindings in `issued`, `pending`, `active`, or `releasing` state with a live lease count as active writer sessions for restore-run. Expired leases are treated as active until reconciliation marks them terminal, because stale writable mounts are a safety risk. `revoked` is terminal only after the orchestrator confirms the runtime mount is stopped or unable to write; a requested revoke remains `releasing`.

### Operations

```http
GET /internal/v1/operations/{operationId}
```

## Operation Requirements

Mutating endpoints must support:

- idempotency key scoped by `caller_service + namespace_id + operation_type + idempotency_key`
- request body hash; the same idempotency key with a different body returns conflict
- correlation ID
- authorized actor
- caller_service
- operation ID
- resource locks where applicable
- per-repo writer-session fence for restore-run versus read-write export/mount issuance
- durable operation record with phase and external resource IDs
- structured JVS JSON output capture
- retry-safe status transitions
- stable caller-facing error codes
- audit event emission for success, failure, and denied requests

Minimum P0 operation matrix:

| Operation | Lock | Active Session Rule | Retry Rule |
| --- | --- | --- | --- |
| repo_create | target repo exclusive create | none | inspect path and JVS identity |
| save_point_create | repo JVS exclusive | allow ordinary IO | retry only from recorded phase |
| restore_preview | repo JVS shared/read | allow ordinary IO | retry safe |
| restore_run | repo JVS exclusive plus writer-session fence | block new read-write sessions, then reject existing active read-write sessions | inspect and doctor |
| template_create | source repo JVS exclusive during save phase, then source read gate plus target template exclusive create | fail if source becomes dirty after template save point | inspect source save point, clone history mode, and target template path |
| template_clone | template read gate plus target repo exclusive create | none | inspect target repo path |
| export_create | repo export lock plus writer-session fence for read_write mode | reject if repo not active or restore fence is held | revoke leaked partial credential |
| export_revoke | export session lock | revoke idempotently | repeat returns terminal state |
| mount_binding_create | repo mount lock plus writer-session fence for read_write mode | reject mount unless repo uses external control root or equivalent verified protection; reject read_write when restore fence is held | repeat returns existing binding |
