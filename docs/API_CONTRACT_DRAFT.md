# API Contract Draft

Status: GA implementation-baseline API narrative. This document is the
product-agnostic narrative source for the internal OpenAPI spec.

The implementation baseline, request/response/error schemas under
`api/schemas/`, and internal OpenAPI under `api/openapi/` now exist. Changes to
schemas, endpoint handlers, generated clients, or storage behavior must keep
this narrative, generated artifacts, and contract verification aligned. Final
GA is governed by `docs/GA_RELEASE_GATES.md`, `docs/READINESS_EVIDENCE.md`,
and `scripts/verify-ga-release.sh`.

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

The GA transport uses the required headers in [contracts/afscp-internal-api-v1.md](contracts/afscp-internal-api-v1.md). Header values must map into this canonical context.

`X-AFSCP-Namespace-Id` is required for every namespace-bound request. When a route also carries `namespace_id` in the path, query, or body, all namespace values must match before AFSCP reads or mutates the resource. Volume-global admin operations must not carry a namespace header; AFSCP rejects non-empty namespace headers on those routes.

Operation inspection is the exception to request-carried namespace context:
`GET /internal/v1/operations/{operationId}` does not require
`X-AFSCP-Namespace-Id` because stored `operation.namespace_id` may be null. The
handler resolves the record by `operationId`, then enforces namespace
authorization when the stored namespace is present or operator/global
authorization when it is absent.

`operation_inspector` is the minimum namespace-scoped operation inspection role
for redacted operation records. `operator_admin` covers global/operator
inspection and repair; it must not be substituted into ordinary namespace
caller policy when namespace-scoped redacted inspection is sufficient.

### Standard Operation Envelope

Mutating responses must use one envelope shape across resource types:

```json
{
  "operation_id": "op_123",
  "operation_state": "succeeded",
  "resource": {
    "type": "repo",
    "id": "repo_123"
  },
  "result": {},
  "error": null
}
```

Asynchronous mutations may return `queued` or `running` with the same envelope.
Synchronous mutations may return terminal state and result in the same envelope.
This is the flat API response envelope for resource mutation handlers. It is not
the durable `OperationRecord`; handlers must not return persisted operation
records from repo, template, export, mount, namespace, or volume mutation routes.

### Standard Error Envelope

Errors must be stable enough for callers to build product-facing behavior
without parsing messages:

```json
{
  "error": {
    "code": "ACTIVE_WRITER_SESSIONS",
    "message": "direct restore is blocked by active read-write sessions",
    "retryable": false,
    "correlation_id": "corr_123",
    "operation_id": "op_123",
    "details": {
      "repo_id": "repo_123"
    }
  }
}
```

The standard error envelope is an active product-facing API surface. `details`
may carry only stable, safe caller-action details such as IDs, validation error
codes, or disabled capability names. It must not carry raw credentials, backend
URLs, AFSCP/JVS raw roots, command material, checksum, digest, capacity, tree
scan, file count, payload tree, sync, hash, proof, internal path, control-root,
home, or other JVS/internal evidence fields.

GA error families:

- `AUTHENTICATION_FAILED`
- `CALLER_NOT_ALLOWED`
- `ROLE_NOT_ALLOWED`
- `NAMESPACE_NOT_FOUND`
- `NAMESPACE_DISABLED`
- `RESOURCE_NAMESPACE_MISMATCH`
- `INVALID_ID`
- `PATH_DENIED`
- `CAPABILITY_DENIED`
- `IDEMPOTENCY_CONFLICT`
- `REPO_ALREADY_EXISTS`
- `REPO_NOT_FOUND`
- `VOLUME_NOT_FOUND`
- `OPERATION_NOT_FOUND`
- `STORAGE_UNAVAILABLE`
- `INTERNAL_ERROR`
- `REPO_JVS_MUTATION_IN_PROGRESS`
- `ACTIVE_WRITER_SESSIONS`
- `WRITER_SESSION_FENCE_HELD`
- `STALE_WRITER_SESSION_UNCERTAIN`
- `RESTORE_DIRTY_STATE`
- `JVS_COMMAND_FAILED`
- `JVS_DOCTOR_FAILED`
- `SOURCE_DIRTY_AFTER_TEMPLATE_SAVE`
- `VOLUME_MISMATCH_REQUIRES_IMPORT`
- `EXPORT_EXPIRED`
- `EXPORT_REVOKED`
- `MOUNT_BINDING_TERMINAL`
- `REPO_LIFECYCLE_INVALID_STATE`
- `REPO_LIFECYCLE_FENCE_HELD`
- `ACTIVE_SESSIONS_BLOCK_LIFECYCLE`
- `STALE_SESSION_BLOCKS_LIFECYCLE`
- `REPO_ARCHIVED`
- `REPO_TOMBSTONED`
- `REPO_PURGED`
- `PURGE_CONFIRMATION_REQUIRED`
- `PURGE_RETENTION_NOT_MET`
- `PURGE_REQUIRES_OPERATOR_APPROVAL`
- `OPERATION_RECOVERY_REQUIRED`

`STORAGE_UNAVAILABLE` means the control-plane durable metadata/store dependency
is temporarily unavailable, timed out, or failed a connection/query; handlers
should map it to HTTP 503 with `retryable=true`. `INTERNAL_ERROR` is reserved
for otherwise unclassified handler, invariant, serialization, or service bugs;
handlers should map it to HTTP 500 and default `retryable=false`. Store outages
must not be disguised as `CAPABILITY_DENIED` or `NAMESPACE_NOT_FOUND`.
Missing volume metadata on volume-global health uses `VOLUME_NOT_FOUND` with
HTTP 404 and must not reveal raw store or SQL details.
`REPO_JVS_MUTATION_IN_PROGRESS` means a same-repo JVS mutation is non-terminal;
handlers should map it to HTTP 409 with `retryable=true`.
Direct restore validation must use stable codes. Active or stale writers use
`ACTIVE_WRITER_SESSIONS`, `WRITER_SESSION_FENCE_HELD`, or
`STALE_WRITER_SESSION_UNCERTAIN`. Dirty restore state uses
`RESTORE_DIRTY_STATE`. Ambiguous direct restore evidence, JVS output mismatch,
explicit diagnostic/recovery evidence requiring repair, or uncertain fence
recovery uses
`OPERATION_RECOVERY_REQUIRED` and/or moves the operation to
`operator_intervention_required` rather than falling through to generic
`JVS_COMMAND_FAILED` when caller or operator action is required.

## Core Types

### Volume

```json
{
  "volume_id": "vol_default",
  "backend": "juicefs",
  "isolation_class": "shared",
  "status": "active",
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

Credential references, secrets, metadata URLs, and raw/root storage paths are deployment-internal configuration. They are not part of `ensureVolume` requests or ordinary `Volume` responses.

`workload_mount=true` requires JVS control metadata to be outside the workload-visible payload root, or an equivalent verified filtered view. The default AFSCP path uses JVS external control root mode, so `filtered_mount=false` is acceptable: the orchestrator mounts only `payload_volume_subdir`.

### NamespaceVolumeBinding

```json
{
  "namespace_id": "ns_123",
  "default_volume_id": "vol_default",
  "allowed_callers": [
    {
      "caller_service": "example-product-api",
      "roles": ["repo_admin", "repo_lifecycle_admin", "restore_admin", "export_admin", "template_admin", "mount_admin", "operation_inspector"]
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
  "lifecycle_policy": {
    "tombstone_retention_seconds": 2592000,
    "purge_requires_lifecycle_admin": true,
    "break_glass_purge_enabled": false
  },
  "mount_policy": {
    "workload_mount_enabled": true,
    "workload_mount_requires_external_control_root": true,
    "allow_privileged_workload": false
  },
  "template_policy": {
    "namespace_templates_enabled": true,
    "cross_namespace_clone_enabled": false
  },
  "status": "active"
}
```

`quota_bytes_default` is a policy record and enforcement hook, not enforced as a capacity limit unless the selected volume capability `directory_quota` supports directory quota enforcement and the volume integration explicitly enables it.

Calling products must not provide authoritative raw filesystem paths. AFSCP computes canonical namespace roots from `namespace_id`, `volume_id`, and its own volume configuration.

`mount_policy.workload_mount_enabled=true` is a namespace permission, not proof that the selected volume/runtime can mount workload repos safely. AFSCP must also check `Volume.capabilities.workload_mount`, require external control roots for new repos, and issue only payload-root mount plans.

### Repo

Ordinary repo responses expose IDs and status only.
`jvs_repo_id` is an opaque storage/JVS identity for diagnostics and correlation, not a product catalog handle, credential, mount material, or authorization source.

```json
{
  "repo_id": "repo_123",
  "namespace_id": "ns_123",
  "volume_id": "vol_default",
  "repo_kind": "repo",
  "jvs_repo_id": "jvs_repo_abc",
  "status": "active",
  "lifecycle": {
    "status": "active",
    "retention_expires_at": null,
    "last_lifecycle_operation_id": null
  },
  "created_at": "2026-05-03T12:00:00Z"
}
```

`control_root_path`, `payload_root_path`, `control_volume_subdir`, `payload_volume_subdir`, `.jvs` paths, and JuiceFS root details are internal implementation state. Admin/debug APIs may expose them behind break-glass controls, but ordinary product callers should not depend on them.

Repo IDs are stable and immutable. Product display-name rename and catalog
detach are caller-owned metadata operations and are not AFSCP repo rename
operations.

GA repo lifecycle statuses include `active`, `archiving`, `archived`,
`restoring_archived`, `deleting`, `tombstoned`, `restoring_tombstoned`,
`purging`, `purged`, and `operator_intervention_required`.

Lifecycle operations use product-familiar storage semantics without adopting
product vocabulary: archive is retained but unavailable, delete is recoverable
tombstone/trash while retention allows, and purge is permanent deletion. Delete
is allowed from `active` or `archived`; restore-tombstoned returns the repo to
the recorded pre-delete accessibility state.

### Direct Restore

Direct restore is represented by one durable `restore` operation. It does not
create a planning artifact, secondary run request, cleanup request, or safety
save point. The operation stores only safe restore metadata
in existing `OperationRecord` containers such as `input_summary.save_point_id`,
redacted JVS JSON output, and verification results. `plan_id`,
`restore_plan_id`, `run_command`, and recovery-command material are forbidden
on active direct restore records.

### RepoTemplate

GA `RepoTemplate` is an immutable published snapshot repo. To change template contents, callers create a new template or a new template revision outside the GA core contract.

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

AFSCP stores an `ExportSession` and returns a short-lived secret-bearing
credential view only in the first successful `export_create` response for an
idempotency key. `POST /internal/v1/repos/{repoId}/exports` is a synchronous
durable boundary that returns `202` with a flat `OperationEnvelope`; its
`result` nests the redacted session under `export` and, only for a newly created
session, the one-time credential under `access`. Replaying the same idempotency
key returns the redacted session without `access`.

```json
{
  "export": {
    "export_id": "export_123",
    "namespace_id": "ns_123",
    "repo_id": "repo_123",
    "protocol": "webdav",
    "mode": "read_write",
    "status": "active",
    "created_by_caller_service": "example-product-api",
    "created_by_actor": {
      "type": "user",
      "id": "user_123"
    },
    "created_at": "2026-05-03T11:55:00Z",
    "updated_at": "2026-05-03T11:55:00Z",
    "expires_at": "2026-05-03T12:00:00Z",
    "revoked_at": null,
    "last_accessed_at": null,
    "active_request_count": 0,
    "active_write_count": 0,
    "last_observed_at": null,
    "last_gateway_heartbeat_at": null,
    "gateway_heartbeat_expires_at": null,
    "write_drained_at": null,
    "terminal_observed_at": null,
    "status_reason": ""
  },
  "access": {
    "url": "https://files.example.com/e/export_123/",
    "auth": {
      "type": "basic",
      "username": "export_123",
      "password": "short-lived-secret"
    },
    "mode": "read_write",
    "expires_at": "2026-05-03T12:00:00Z"
  }
}
```

Idempotent replay of the same create request returns the same operation/session
shape but omits `access`:

```json
{
  "export": {
    "export_id": "export_123",
    "namespace_id": "ns_123",
    "repo_id": "repo_123",
    "protocol": "webdav",
    "mode": "read_write",
    "status": "active",
    "created_by_caller_service": "example-product-api",
    "created_by_actor": {
      "type": "user",
      "id": "user_123"
    },
    "created_at": "2026-05-03T11:55:00Z",
    "updated_at": "2026-05-03T11:55:00Z",
    "expires_at": "2026-05-03T12:00:00Z",
    "revoked_at": null,
    "last_accessed_at": null,
    "active_request_count": 0,
    "active_write_count": 0,
    "last_observed_at": null,
    "last_gateway_heartbeat_at": null,
    "gateway_heartbeat_expires_at": null,
    "write_drained_at": null,
    "terminal_observed_at": null,
    "status_reason": ""
  }
}
```

`GET /internal/v1/exports/{exportId}` returns only the redacted
`ExportSession` and must not return `access` or the WebDAV password:

```json
{
  "export_id": "export_123",
  "namespace_id": "ns_123",
  "repo_id": "repo_123",
  "protocol": "webdav",
  "mode": "read_write",
  "status": "active",
  "created_by_caller_service": "example-product-api",
  "created_by_actor": {
    "type": "user",
    "id": "user_123"
  },
  "created_at": "2026-05-03T11:55:00Z",
  "updated_at": "2026-05-03T11:55:00Z",
  "expires_at": "2026-05-03T12:00:00Z",
  "revoked_at": null,
  "last_accessed_at": null,
  "active_request_count": 0,
  "active_write_count": 0,
  "last_observed_at": null,
  "last_gateway_heartbeat_at": null,
  "gateway_heartbeat_expires_at": null,
  "write_drained_at": null,
  "terminal_observed_at": null,
  "status_reason": ""
}
```

`DELETE /internal/v1/exports/{exportId}` is also a synchronous durable boundary
that returns `202` with a flat `OperationEnvelope`. It records the request and
moves the session to `revoking` so the gateway can drain active requests.
Terminal `revoked` is set only after gateway or reconcile confirmation.

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

Volume health combines durable volume metadata, required volume capabilities,
and an injected backend volume health probe. `status:"healthy"` is allowed only
when all three pass. If the backend probe is missing, fails, or errors, the
response is not healthy and uses stable finding codes such as
`BACKEND_PROBE_MISSING`, `BACKEND_PROBE_FAILED`, or `BACKEND_PROBE_ERROR`
without returning raw backend paths, secrets, or underlying error text. If
volume metadata for `{volumeId}` is missing, the endpoint returns HTTP 404 with
`VOLUME_NOT_FOUND` and a generic `volume was not found` message.

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
GET  /internal/v1/repos?namespace_id={namespaceId}&lifecycle_status={status}
GET  /internal/v1/repos/{repoId}
POST /internal/v1/repos/{repoId}:archive
POST /internal/v1/repos/{repoId}:restore-archived
POST /internal/v1/repos/{repoId}:delete
POST /internal/v1/repos/{repoId}:restore-tombstoned
POST /internal/v1/repos/{repoId}:purge
```

Repo lifecycle operations are asynchronous durable operations. They use the
standard operation envelope and the repo lifecycle contract in
[contracts/repo-lifecycle-v1.md](contracts/repo-lifecycle-v1.md).

`CreateRepoRequest` is metadata intake only; the worker later performs storage
provisioning/recovery. The request body is strictly:

```json
{
  "namespace_id": "ns_123",
  "target_repo_id": "repo_123"
}
```

Durable intake resolves idempotency before checking target repo metadata. The
same idempotency key and same request body reuses the original operation even if
the repo metadata now exists. `409 REPO_ALREADY_EXISTS` applies only to a new
create request targeting an existing repo. This is distinct from
`IDEMPOTENCY_CONFLICT`, which means the same idempotency key was reused with a
different request body.

`archive` retains repo data and blocks ordinary access. `restore-archived`
reactivates an archived repo. `delete` is a logical delete request that drains
sessions and tombstones retained data. `restore-tombstoned` is allowed only
before purge and within retention policy. `purge` is irreversible and requires
retention-policy approval or an approved operator break-glass purge.

Product display-name rename and catalog detach are not AFSCP repo lifecycle
operations.

Repo get is namespace-bound. Missing repos return `REPO_NOT_FOUND`; repos from
another namespace must not be revealed.

Repo list is a namespace-bound storage inventory projection for trusted callers
and operators. The `X-AFSCP-Namespace-Id` header must match the required
`namespace_id` query parameter. `lifecycle_status`, when supplied, must be one
of the repo lifecycle statuses. Repo list must not become a product catalog API:
no display names, business objects, raw paths, or storage credentials are
returned.

`PurgeRepoRequest` must carry a caller-side confirmation or approval reference
and a reason. If the caller requests a retention override, the request must use
an approved break-glass policy; otherwise AFSCP rejects it with
`PURGE_CONFIRMATION_REQUIRED`, `PURGE_RETENTION_NOT_MET`, or
`PURGE_REQUIRES_OPERATOR_APPROVAL`.

### Save Points

```http
POST /internal/v1/repos/{repoId}/save-points
GET  /internal/v1/repos/{repoId}/save-points
POST /internal/v1/repos/{repoId}/restore
```

`restore` is the direct product restore API. It requires `Idempotency-Key`,
namespace/auth headers, and body:

```json
{
  "save_point_id": "sp_001"
}
```

Successful intake returns the normal operation envelope for operation type
`restore`; callers poll `GET /internal/v1/operations/{operationId}`. Repeated
requests with the same idempotency key and body reuse the same operation; a
different body conflicts through the existing idempotency semantics. User
confirmation is expressed outside this request body by the caller UI and the
idempotent operation submission.

Direct restore does not create a planning artifact, secondary run request,
cleanup request, or safety save point. Worker execution calls:

```bash
jvs afscp --control-root <controlRoot> --home <home> restore --save-point <save_point_id> --json
```

The worker expects the `jvs.afscp.direct.v1` envelope with
`command:"restore"`, `status:"succeeded"`, `data.restored_save_point_id`,
optional `data.previous_head`, and `data.new_head`. It must not parse or expect
`plan_id`, `run_command`, planning-artifact state, raw paths, or command
material.

`jvs_json_output` and `verification_result` are active redacted operation
inspection surfaces, not raw diagnostic dumps. They must recursively strip JVS
and storage-internal field names such as checksum, digest, capacity, tree scan,
file count, payload tree, sync, hash, proof, internal path, control-root, home,
and raw command material before any caller-facing projection.

Direct restore is a version restore and must reject active or uncertain
read-write export or workload mount sessions by default. Lifecycle restore is
handled by `restore-archived` and `restore-tombstoned`; audit event names and
product copy must distinguish these from version restore. An operator
break-glass restore can be designed separately with explicit approval,
session revoke/drain, and audit.

Direct restore phase order is fixed:

1. Validate the queued `restore` operation and `save_point_id`.
2. Acquire the per-repo writer-session fence.
3. Reject active or uncertain read-write sessions.
4. Invoke JVS direct restore for the requested save point.
5. Atomically record operation success, audit success, and writer-fence release.

Writer-session denial before JVS is invoked releases the writer-session fence
and records a stable writer error. JVS direct restore failure before confirmed
mutation records a terminal failed operation. Ambiguous JVS output, direct
restore result mismatch, recovery-required metadata, or uncertain fence recovery
keeps the operation in `operator_intervention_required` until an operator repair
path resolves it.

The writer-session fence is also the API contract for read-write export and
mount issuance: while the fence is held, AFSCP rejects new read-write export and
workload mount binding issuance for the repo. The fence is released only after
restore and audit completion. `jvs afscp status --json` and
`jvs afscp doctor --json` are explicit metadata-only diagnostics and are not
called by default in the restore hot path.

Dirty-state behavior is fail-closed by default with `RESTORE_DIRTY_STATE`.
Any future `discard_unsaved` or `save_first` mode must be represented in the
request schema and audited explicitly.

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

GA template creation always creates a fresh source save point inside the
operation and records it as `source_save_point_id` on the resulting
`RepoTemplate`. Template materialization uses JVS direct clone from that save
point, so it does not perform ordinary workspace dirty checks or re-read the
mutable HOME after the save point is created.

If the source repo has active or stale writer sessions before the source save
point is created, AFSCP fails with `SOURCE_DIRTY_AFTER_TEMPLATE_SAVE` and the
caller may retry after those sessions are closed. Creating a template from an
older save point requires a future explicit product flow.

`clone_history_mode` must be pinned to the verified JVS capability for the deployment. GA may use `main`. `all` is allowed only after the pinned JVS version supports durable imported-save-point protection.

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
template.volume_id == namespace.default_volume_id
cross_namespace_clone_enabled == false
```

If a namespace binding changes after a template is created and the template volume differs from the namespace default volume, GA clone must reject with `VOLUME_MISMATCH_REQUIRES_IMPORT`. It must not silently create a new repo outside the current namespace volume policy.

### Exports

```http
POST   /internal/v1/repos/{repoId}/exports
GET    /internal/v1/exports/{exportId}
DELETE /internal/v1/exports/{exportId}
```

Export create defines:

- default TTL of 3600 seconds, minimum TTL of 60 seconds, and namespace policy maximum TTL
- credentials are not reissued after create; idempotent replay omits `access`
- one-time secret-bearing response behavior for first successful create only
- revoke moves the session to `revoking` for gateway drain; terminal `revoked` requires gateway or reconcile confirmation
- whether read-write exports count as active writer sessions until revoked, expired, or reconciled terminal
- credential hashing or encryption at rest
- access log and audit redaction fields

The machine contract uses `ExportCreateOperationEnvelope` for create responses.
Its `result` contains `ExportCreateResult`, which includes the redacted
`ExportSession` plus the one-time `ExportAccessCredential` only on first create.
`GET /internal/v1/exports/{exportId}` returns only `ExportSession` directly and
must not return the WebDAV password again.

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

GA mount bindings must be lease-based. Read-write bindings in `issued`, `pending`, `active`, or `releasing` state with a live lease count as active writer sessions for direct restore. Expired leases are treated as active until reconciliation marks them terminal, because stale writable mounts are a safety risk. `revoked` is terminal only after the orchestrator confirms the runtime mount is stopped or unable to write; a requested revoke remains `releasing`.

If the orchestrator contract cannot prove payload-only mount, heartbeat,
release, revoke, and confirmed-unmounted behavior, AFSCP must reject mount
binding creation with `CAPABILITY_DENIED` instead of issuing a degraded binding.

### Operations

```http
GET /internal/v1/operations/{operationId}
```

`GET /internal/v1/operations/{operationId}` returns a redacted
`OperationRecord`, not the standard mutation response envelope. Operation
list/search is not part of the first GA internal OpenAPI surface; it can be
added later for operator tooling after access policy and pagination are
reviewed.

Operation inspection does not require `X-AFSCP-Namespace-Id`; an operation may
have `namespace_id: null` for volume-global or operator workflows. The handler
returns the redacted `OperationRecord` directly after authorizing against the
stored namespace when present, or operator/global policy when absent. It must not
wrap that record in `OperationEnvelope`.
Missing operations return `OPERATION_NOT_FOUND`.

Product callers use `operation_inspector` for namespace-scoped operation
inspection of redacted records; product caller denials, including cross-namespace
or global operation records, return `OPERATION_NOT_FOUND` to avoid exposing
operation existence. Operator/admin callers use `operator_admin` for
global/operator inspection and repair, including records without a stored
namespace; operator/admin policy denials remain authorization failures.

## Operation Requirements

Mutating endpoints must support:

- idempotency key scoped by `caller_service + namespace_id + operation_type + idempotency_key`
- request body hash; the same idempotency key with a different body returns conflict
- correlation ID
- authorized actor
- caller_service
- operation ID
- resource locks where applicable
- per-repo writer-session fence for direct restore versus read-write export/mount issuance
- per-repo lifecycle fence for archive, restore-archived, delete, restore-tombstoned, and purge versus all export/mount/session issuance and repo storage mutations
- durable operation record with phase and external resource IDs
- structured JVS JSON output capture reduced to safe metadata; `run_command`,
  `recommended_next_command`, raw paths, stdout/stderr, and secrets must not be
  stored or returned verbatim
- retry-safe status transitions
- stable caller-facing error codes
- audit event emission for success, failure, and denied requests

Minimum GA operation matrix:

| Operation | Lock | Active Session Rule | Retry Rule |
| --- | --- | --- | --- |
| repo_create | target repo exclusive create | none | inspect path and JVS identity |
| repo_archive | repo lifecycle exclusive plus session drain | block new sessions and mutations, then reject or drain existing sessions, including read-only sessions | inspect lifecycle status, session terminal state, and retained storage |
| repo_restore_archived | repo lifecycle exclusive | block new sessions and mutations until active | inspect lifecycle status and repo health |
| repo_delete | repo lifecycle exclusive plus session drain | block new sessions and mutations, then reject or drain existing sessions, including read-only sessions | inspect tombstone status and retained storage |
| repo_restore_tombstoned | repo lifecycle exclusive | reject after purge or retention denial | inspect tombstone status, retention policy, and repo health |
| repo_purge | repo lifecycle exclusive plus session drain | require no active or uncertain sessions | inspect purge marker and absence of retained storage |
| save_point_create | repo JVS exclusive | allow ordinary IO | retry only from recorded phase |
| restore | repo JVS exclusive direct restore mutation | block other same-repo JVS mutations and active writer sessions; no preview/plan/safety save point | recover from operation phase, writer fence, direct JVS output, and explicit diagnostic/recovery evidence |
| template_create | source repo JVS exclusive during save phase, then source read gate plus target template exclusive create | fail if source becomes dirty after template save point | inspect source save point, clone history mode, and target template path |
| template_clone | template read gate plus target repo exclusive create | none | inspect target repo path |
| export_create | repo export lock plus writer-session fence for read_write mode | reject if repo not active, restore fence is held for read_write, or lifecycle fence is held | revoke leaked partial credential |
| export_revoke | export session lock | revoke idempotently | repeat returns terminal state |
| export_session_reconcile | export session lock | terminal only after gateway confirms no future access for lifecycle, and no future writes for direct restore | repeat from observed gateway state |
| mount_binding_create | repo mount lock plus writer-session fence for read_write mode | reject mount unless repo is active, lifecycle fence is clear, and repo uses external control root or equivalent verified protection; reject read_write when restore fence is held | repeat returns existing binding |
| mount_binding_status_update | mount binding lock | terminal only when orchestrator confirms runtime access ended or failed safely | repeat from observed orchestrator state |
| mount_binding_heartbeat | mount binding lock | extend only non-terminal live bindings | repeat with same lease state |
| mount_binding_release | mount binding lock | terminal only after runtime access ended | repeat returns terminal state |
| mount_binding_revoke | mount binding lock | requested revoke remains non-terminal until runtime access ended or is confirmed unable to write | repeat returns current teardown state |
