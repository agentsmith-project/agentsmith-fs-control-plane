# Contract: AFSCP Internal API V1

Status: GA implementation-baseline contract. FINAL GA is governed by
`docs/GA_RELEASE_GATES.md`, `docs/READINESS_EVIDENCE.md`, and
`scripts/verify-ga-release.sh`.

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
- direct restore
- repo template create/clone
- export create/get/revoke
- workload mount binding create/get
- workload mount binding status/heartbeat/release/revoke
- orchestrator mount plan get
- operation get by ID

Product display-name rename and catalog detach are outside AFSCP. Repo storage
lifecycle is in GA through [repo-lifecycle-v1.md](repo-lifecycle-v1.md).

`GET /internal/v1/operations/{operationId}` is the stable GA internal API
inspection surface. `POST /internal/v1/operations/{operationId}:repair` is the
only stable GA operator repair write surface and is constrained by
[operator-repair-v1.md](operator-repair-v1.md). GA does not define operations
list/search APIs, correlated resource lookup APIs, intervention queue APIs,
held-fence aggregation APIs, or audit outbox lag aggregation APIs. Those
operator inspection workflows are implemented through runbooks, read-only
database queries, observability dashboards, or deployment-side operator tooling.

Direct restore is the primary product restore API. The machine-readable API
contract exposes `POST /internal/v1/repos/{repoId}/restore`, operation type
`restore`, request/response schemas, route, and OpenAPI contract fixtures for
handlers and generated clients. GA does not expose a restore admission/preflight
endpoint; unsupported runtime/config is reported through durable restore
operation failure or readiness/operator evidence.

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
- Direct restore request bodies contain `save_point_id` only. User confirmation
  is expressed through caller UI and idempotent operation submission, not a
  legacy confirmation field.
- Direct restore must not create a preview, planning artifact, secondary run
  request, cleanup request, or safety save point. It creates only a durable
  `restore` operation and the worker calls JVS direct restore.
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

`StandardError.details`, `OperationRecord.jvs_json_output`, and
`OperationRecord.verification_result` are active API response surfaces after
redaction. They must not expose raw diagnostic dumps or JVS/storage-internal
fields such as checksum, digest, capacity, tree scan, file count, payload tree,
sync, hash, proof, internal path, control-root, home, or raw command material.
Handlers recursively remove those fields before returning caller-facing
operation projections.

## Secret Path Redaction Boundary

Default control-plane output surfaces must redact AFSCP managed raw roots and
storage material. This includes `/srv/afscp...`, `.jvs`, managed
`afscp/namespaces/.../control` and `afscp/namespaces/.../payload` subdirs,
`control_volume_subdir`, `payload_volume_subdir`, AFSCP raw JVS command shapes
with examples such as `jvs init`, old repo clone helpers, old public
`jvs doctor`/`jvs save`/`jvs history` commands when found in historical data,
and the active direct shape
`jvs afscp --control-root ... --home ... <save|list|restore|clone|status|doctor> --json`
forms, `juicefs mount` commands, SecretRef values, metadata URLs, tokens, passwords,
credentials, audit/outbox
payloads, readiness errors, operation persistence, operation inspection,
WebDAV gateway denies, and release evidence strings. The boundary is product neutral
and must not be satisfied by optional fixture, discovery-only, helper-only,
contract-only, manifest-only, or deployment-runtime-support evidence.

## Discovery Surface Boundary

readyz is service readiness, not caller authorization. It reports capability
readiness and default/optional gating state, but it must not grant access,
advertise caller roles, or make optional runtime capabilities default ready.

caller discovery is limited to caller-scoped repo projection and namespace-
scoped operation inspection. Repo projection and operation inspection must not
expose operator-only state, runtime mount material, Secret refs, raw paths,
credentials, or globally scoped audit/intervention/fence/session state.

orchestrator discovery is limited to the dedicated workload mount
`orchestrator_mount` surface and orchestrator plan contract. In default-denied
or not-enabled mode, orchestrator discovery must fail closed without returning
an orchestrator plan, SecretRef, payload subdir, raw path, or mount material.

operator inspection is a read-only redacted operation inspection boundary unless
the caller invokes the separate allowlisted repair route. Operator inspection
must stay distinct from repair side effects.

evidence classification reads the release evidence manifest and capability
matrix, but must not replace runtime admission, caller authorization, readiness,
or actor-specific discovery output tests.

## GA Role Matrix

| Role | Endpoint Groups |
| --- | --- |
| `volume_admin` | volume ensure/health |
| `namespace_admin` | namespace create/disable and volume binding update |
| `repo_admin` | repo create/get/list, save point create/list, history |
| `repo_lifecycle_admin` | repo archive, restore-archived, delete, restore-tombstoned, purge when policy permits |
| `restore_admin` | direct restore |
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
expiry/revoke/not-ready admission, mount terminal state, repo lifecycle invalid
state, lifecycle session drain failure, missing purge
confirmation, purge retention denial, operation recovery required, durable
metadata/store unavailability, and unclassified internal service bugs.

Direct restore is a mutating restore operation and the fastest product path:
`POST /internal/v1/repos/{repoId}/restore` with body
`{"save_point_id":"sp_..."}` returns an `OperationEnvelope` for a durable
`restore` operation. The operation enters
the normal queued/running/succeeded/failed chain and can be inspected through
`GET /internal/v1/operations/{operationId}`. Repeating the same
`Idempotency-Key` with the same body reuses the same operation; changing the
body returns `IDEMPOTENCY_CONFLICT`.

The direct restore worker invokes JVS as
`jvs afscp --control-root <controlRoot> --home <home> restore --save-point <save_point_id> --json`.
It validates the `jvs.afscp.direct.v1` JSON result (`command=restore`,
`status=succeeded`, `data.restored_save_point_id`, `data.previous_head`, and
`data.new_head`) and must not expect or persist `plan_id`, `run_command`, raw
paths, or secondary command metadata.

Restore error mapping should use stable codes. Active or stale writer
denials use the writer-session codes. Dirty restore state uses
`RESTORE_DIRTY_STATE`. Ambiguous direct restore output, explicit
diagnostic/recovery evidence requiring repair, or uncertain writer-fence
recovery uses `OPERATION_RECOVERY_REQUIRED` or operator intervention instead of
returning generic `JVS_COMMAND_FAILED` when caller/operator action is required.

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

Operator repair returns an operator repair response, not a normal
`OperationEnvelope`. It requires `operator_admin`, no namespace header, an
allowlisted action, reason, evidence reference, affected IDs, before/after
state, and an audit event. It must not expose arbitrary SQL, generic state
rewrite, fence release, session mutation, restore mutation, or repo/storage/JVS
mutation.

`STORAGE_UNAVAILABLE` is for durable control-plane metadata/store outages,
timeouts, or connection/query failures and should map to HTTP 503 with
`retryable=true`. `INTERNAL_ERROR` is for handler, invariant, serialization, or
other unclassified server bugs and should map to HTTP 500 with
`retryable=false`. Store outages must not be disguised as `CAPABILITY_DENIED` or
`NAMESPACE_NOT_FOUND`.
