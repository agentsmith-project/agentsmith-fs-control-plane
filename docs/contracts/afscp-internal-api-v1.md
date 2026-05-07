# Contract: AFSCP Internal API V1

Status: GA pre-dev review draft

AFSCP internal APIs are called by trusted product control planes, privileged admin jobs, migration jobs, operator tools, and a dedicated workload orchestrator service.

## Required Headers

- `Authorization`: service credential. Deployment may use mTLS identity, signed service token, or both, but the credential must authenticate a stable service principal.
- `Idempotency-Key`: required for mutating requests.
- `X-Correlation-Id`: required.
- `X-AFSCP-Namespace-Id`: required for namespace-bound requests.
- `X-AFSCP-Actor-Type`: required for mutating requests; examples: `user`, `system`, `admin_job`.
- `X-AFSCP-Actor-Id`: required for mutating requests; the authorized end actor, not the caller service identity.
- `X-AFSCP-Caller-Service`: required; must match the authenticated service principal or a configured alias.

Namespace-bound requests include namespace create/disable and volume-binding
operations, repo create/list/get/lifecycle/save/restore, template create/clone,
export create/get/revoke, workload mount operations, and namespace-bound
operation inspection. If the request also carries a namespace in the path, query,
or body, it must equal `X-AFSCP-Namespace-Id`. Volume-global admin operations
must not carry this header; AFSCP rejects non-empty namespace headers on those
routes.

## Caller Authorization

AFSCP must authorize `caller_service` for every namespace-bound request.

GA authorization sources:

- `NamespaceVolumeBinding.allowed_callers`
- deployment-level admin/operator allowlist
- deployment-level migration allowlist
- dedicated orchestrator allowlist for `orchestrator-plan`

AFSCP must reject and audit:

- caller not allowed for namespace
- caller role missing for requested operation
- caller attempts to access a repo/template/export outside the namespace context
- caller attempts to fetch orchestrator-only secret references without the orchestrator role
- caller attempts global/operator inspection or repair with only a namespace-scoped role

## Endpoint Groups

- volume ensure/health
- namespace create/disable and volume binding get/update
- repo create/get/list
- repo archive, restore-archived, delete, restore-tombstoned, and purge
- save point create/list
- restore preview/run/discard
- repo template create/clone
- export create/get/revoke
- workload mount binding create/get
- workload mount binding status/heartbeat/release/revoke
- orchestrator mount plan get
- operation get by ID

Product display-name rename and catalog detach are outside AFSCP. Repo storage
lifecycle is in GA through [repo-lifecycle-v1.md](repo-lifecycle-v1.md).

`GET /internal/v1/operations/{operationId}` is the only stable GA internal API
inspection surface. GA does not define operations list/search APIs, correlated
resource lookup APIs, intervention queue APIs, held-fence aggregation APIs, or
audit outbox lag aggregation APIs. Those operator inspection workflows are
implemented through runbooks, read-only database queries, observability
dashboards, or deployment-side operator tooling.

Restore preview discard is part of the current GA restore slice. The
machine-readable API contract exposes the endpoint, operation type
`restore_preview_discard`, request/response schemas, route, and OpenAPI
contract fixtures for handlers and generated clients.

See [../API_CONTRACT_DRAFT.md](../API_CONTRACT_DRAFT.md) for the current draft payloads.

## Required Invariants

- Every request includes namespace context where resource access is namespace-bound.
- Operation inspection derives namespace context from the stored operation
  record; no synthetic namespace is required when the record namespace is null.
- Every mutating request includes the authorized end actor for audit.
- AFSCP validates caller service authorization before namespace/resource consistency.
- AFSCP validates namespace/repo/template/export consistency.
- AFSCP rejects mismatches between `X-AFSCP-Namespace-Id` and any path, query, body, or stored resource namespace.
- AFSCP rejects non-empty `X-AFSCP-Namespace-Id` on volume-global admin operations.
- Volume health is not metadata-only; healthy requires valid metadata,
  required volume capabilities, and an explicitly passing backend probe. A
  missing backend probe degrades health with a stable finding code. Missing
  volume metadata uses `VOLUME_NOT_FOUND`.
- Cross-namespace template clone is rejected by default.
- Cross-volume template clone is rejected with `VOLUME_MISMATCH_REQUIRES_IMPORT`.
- Restore-run and restore-preview discard references to preview operations must
  stay inside the same namespace, repo, and resource boundary; cross-namespace
  references return `OPERATION_NOT_FOUND` or the existing non-leaking
  equivalent.
- Mutations create operation records before executing external effects.
- Ordinary product caller responses never include JuiceFS root credentials, raw root paths, or Secret references.
- `quota_bytes_default` is a policy record and enforcement hook, not enforced as
  a capacity limit unless the selected volume capability `directory_quota`
  supports directory quota enforcement and the volume integration explicitly
  enables it.
- Errors are stable enough for callers to render product-facing messages.

## Response Shape Boundary

Mutating resource endpoints return the flat `OperationEnvelope` API response.
The durable `OperationRecord` is the operation-store record and is returned only
by `GET /internal/v1/operations/{operationId}` after redaction. Operation
inspection does not require `X-AFSCP-Namespace-Id`; lookup resolves the record
by `operationId`, then authorizes against the stored namespace when present or
operator/global policy when the stored namespace is null. Handlers must not
return an `OperationRecord` where an `OperationEnvelope` is specified, and
operation inspection must not wrap the record in an `OperationEnvelope`.

## GA Role Matrix

| Role | Endpoint Groups |
| --- | --- |
| `volume_admin` | volume ensure/health |
| `namespace_admin` | namespace create/disable and volume binding update |
| `repo_admin` | repo create/get/list, save point create/list, history |
| `repo_lifecycle_admin` | repo archive, restore-archived, delete, restore-tombstoned, purge when policy permits |
| `restore_admin` | restore preview/run/discard |
| `template_admin` | repo template create/clone |
| `export_admin` | export create/get/revoke |
| `mount_admin` | workload mount binding create/get/revoke |
| `operation_inspector` | namespace-scoped operation inspection of redacted operation records |
| `orchestrator_mount` | orchestrator plan, mount status, heartbeat, release |
| `migration_admin` | future migration tooling |
| `operator_admin` | global/operator inspection and operational repair |
| `break_glass_admin` | explicitly approved break-glass flows only |

Deployments may split these roles further, but they must not merge ordinary product caller roles with `volume_admin`, `orchestrator_mount`, `migration_admin`, `operator_admin`, or `break_glass_admin`. `operation_inspector` is the minimum namespace-scoped role for redacted operation inspection; `volume_admin`, `operator_admin`, and `break_glass_admin` are deployment/global roles, while `orchestrator_mount` and `migration_admin` are dedicated non-ordinary caller roles.
`namespace_admin` authorizes namespace create/disable and volume-binding update only through deployment namespace policy; binding-scoped `namespace_admin` does not authorize binding self-creation or modification.

## Stable Error Families

The internal API must expose a standard error envelope and stable error codes
for authentication, caller authorization, namespace/resource mismatch,
capability denial, idempotency conflict, missing repo or volume metadata, active
writer restore rejection, dirty restore rejection, JVS failure, export
expiry/revoke, mount terminal state, repo lifecycle invalid state, lifecycle
session drain failure, missing purge
confirmation, purge retention denial, operation recovery required, durable
metadata/store unavailability, and unclassified internal service bugs.

Restore preview creates a durable pending JVS restore plan and is authorized as
a mutating restore operation. Restore preview discard is the caller-triggerable
cleanup path for a cancelled preview and is part of the `restore_admin` endpoint
group. It must validate the matching preview operation and pending plan, invoke
JVS restore discard through the runner, and never require ordinary callers to
ask operators to delete private JVS files.

The durable `RestorePlan` entity is the source of truth for pending, consuming,
consumed, discarding, discarded, and operator-intervention restore states.
Operation records should reference restore plans only through safe existing
metadata containers such as `external_resource_ids`, redacted
`jvs_json_output`, `input_summary`, and `verification_result` until the
OpenAPI/schema/Go/DB contracts are intentionally upgraded.

Restore error mapping should reuse existing codes. Active or stale writer
denials use the writer-session codes. Dirty restore state uses
`RESTORE_DIRTY_STATE`. JVS blocking state, mismatched or multiple pending
plans, stale preview plans, or ambiguous recovery use
`OPERATION_RECOVERY_REQUIRED` or operator intervention instead of introducing a
new public enum or returning generic `JVS_COMMAND_FAILED` when caller/operator
action is required.

Repo-create intake resolves idempotency before checking target repo metadata:
the same idempotency key and same request body reuses the original operation
even if the repo metadata now exists. `REPO_ALREADY_EXISTS` is the stable 409
response only for a new repo create request targeting an existing repo. It is
distinct from `IDEMPOTENCY_CONFLICT`, which is reserved for reusing the same
idempotency key with a different request body.

Repo read APIs are namespace-bound. Missing repos, including repos that exist
under a different namespace than the request namespace, use the stable
`REPO_NOT_FOUND` response and must not reveal cross-namespace existence.

Volume health is volume-global rather than namespace-bound. Missing volume
metadata for `GET /internal/v1/volumes/{volumeId}/health` uses HTTP 404 with
`VOLUME_NOT_FOUND` and must not reveal raw store or SQL details.

Operation inspection returns a redacted `OperationRecord` directly. Missing
operations use the stable `OPERATION_NOT_FOUND` response. Product caller
operation-inspection denials, including cross-namespace or global operation
records, also return `OPERATION_NOT_FOUND` to avoid revealing operation
existence; operator/admin policy denials remain authorization failures. This
operation-by-ID endpoint is not a contract for operation enumeration, search,
correlated-resource discovery, intervention aggregation, fence aggregation, or
audit outbox aggregation.

`STORAGE_UNAVAILABLE` is for durable control-plane metadata/store outages,
timeouts, or connection/query failures and should map to HTTP 503 with
`retryable=true`. `INTERNAL_ERROR` is for handler, invariant, serialization, or
other unclassified server bugs and should map to HTTP 500 with
`retryable=false`. Store outages must not be disguised as `CAPABILITY_DENIED` or
`NAMESPACE_NOT_FOUND`.
