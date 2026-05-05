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
or body, it must equal `X-AFSCP-Namespace-Id`. Volume-global admin operations do
not carry this header.

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
- restore preview/run
- repo template create/clone
- export create/get/revoke
- workload mount binding create/get
- workload mount binding status/heartbeat/release/revoke
- orchestrator mount plan get
- operation get

Product display-name rename and catalog detach are outside AFSCP. Repo storage
lifecycle is in GA through [repo-lifecycle-v1.md](repo-lifecycle-v1.md).

See [../API_CONTRACT_DRAFT.md](../API_CONTRACT_DRAFT.md) for the current draft payloads.

## Required Invariants

- Every request includes namespace context where resource access is namespace-bound.
- Operation inspection derives namespace context from the stored operation
  record; no synthetic namespace is required when the record namespace is null.
- Every mutating request includes the authorized end actor for audit.
- AFSCP validates caller service authorization before namespace/resource consistency.
- AFSCP validates namespace/repo/template/export consistency.
- AFSCP rejects mismatches between `X-AFSCP-Namespace-Id` and any path, query, body, or stored resource namespace.
- Cross-namespace template clone is rejected by default.
- Cross-volume template clone is rejected with `VOLUME_MISMATCH_REQUIRES_IMPORT`.
- Mutations create operation records before executing external effects.
- Ordinary product caller responses never include JuiceFS root credentials, raw root paths, or Secret references.
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
| `restore_admin` | restore preview/run |
| `template_admin` | repo template create/clone |
| `export_admin` | export create/get/revoke |
| `mount_admin` | workload mount binding create/get/revoke |
| `operation_inspector` | namespace-scoped operation inspection of redacted operation records |
| `orchestrator_mount` | orchestrator plan, mount status, heartbeat, release |
| `migration_admin` | future migration tooling |
| `operator_admin` | global/operator inspection and operational repair |
| `break_glass_admin` | explicitly approved break-glass flows only |

Deployments may split these roles further, but they must not merge ordinary product caller roles with `orchestrator_mount`, `operator_admin`, or `break_glass_admin`. `operation_inspector` is the minimum namespace-scoped role for redacted operation inspection; `operator_admin` is reserved for global/operator inspection and repair.

## Stable Error Families

The internal API must expose a standard error envelope and stable error codes
for authentication, caller authorization, namespace/resource mismatch,
capability denial, idempotency conflict, active writer restore rejection, dirty
restore rejection, JVS failure, export expiry/revoke, mount terminal state, repo
lifecycle invalid state, lifecycle session drain failure, missing purge
confirmation, purge retention denial, and operation recovery required.
