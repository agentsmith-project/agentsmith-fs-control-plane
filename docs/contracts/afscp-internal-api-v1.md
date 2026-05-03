# Contract: AFSCP Internal API V1

Status: P0 review draft

AFSCP internal APIs are called by trusted product control planes, privileged admin jobs, migration jobs, operator tools, and a dedicated workload orchestrator service.

## Required Headers

- `Authorization`: service credential. Deployment may use mTLS identity, signed service token, or both, but the credential must authenticate a stable service principal.
- `Idempotency-Key`: required for mutating requests.
- `X-Correlation-Id`: required.
- `X-AFSCP-Namespace-Id`: required where applicable.
- `X-AFSCP-Actor-Type`: required for mutating requests; examples: `user`, `system`, `admin_job`.
- `X-AFSCP-Actor-Id`: required for mutating requests; the authorized end actor, not the caller service identity.
- `X-AFSCP-Caller-Service`: required; must match the authenticated service principal or a configured alias.

## Caller Authorization

AFSCP must authorize `caller_service` for every namespace-bound request.

P0 authorization sources:

- `NamespaceVolumeBinding.allowed_callers`
- deployment-level admin/operator allowlist
- deployment-level migration allowlist
- dedicated orchestrator allowlist for `orchestrator-plan`

AFSCP must reject and audit:

- caller not allowed for namespace
- caller role missing for requested operation
- caller attempts to access a repo/template/export outside the namespace context
- caller attempts to fetch orchestrator-only secret references without the orchestrator role

## Endpoint Groups

- volume ensure/health
- namespace create/disable and volume binding get/update
- repo create/get
- save point create/list
- restore preview/run
- repo template create/clone
- export create/get/revoke
- workload mount binding create/get
- workload mount binding status/heartbeat/release/revoke
- orchestrator mount plan get
- operation get/list

Repo archive/delete/rename/detach are P1 unless a lifecycle drain contract is separately accepted.

See [../API_CONTRACT_DRAFT.md](../API_CONTRACT_DRAFT.md) for the current draft payloads.

## Required Invariants

- Every request includes namespace context where resource access is namespace-bound.
- Every mutating request includes the authorized end actor for audit.
- AFSCP validates caller service authorization before namespace/resource consistency.
- AFSCP validates namespace/repo/template/export consistency.
- Cross-namespace template clone is rejected by default in P0.
- Cross-volume template clone is rejected with `VOLUME_MISMATCH_REQUIRES_IMPORT` in P0.
- Mutations create operation records before executing external effects.
- Ordinary product caller responses never include JuiceFS root credentials, raw root paths, or Secret references.
- Errors are stable enough for callers to render product-facing messages.

## P0 Role Matrix

| Role | Endpoint Groups |
| --- | --- |
| `volume_admin` | volume ensure/health |
| `namespace_admin` | namespace create/disable and volume binding update |
| `repo_admin` | repo create/get, save point create/list, history |
| `restore_admin` | restore preview/run |
| `template_admin` | repo template create/clone |
| `export_admin` | export create/get/revoke |
| `mount_admin` | workload mount binding create/get/revoke |
| `orchestrator_mount` | orchestrator plan, mount status, heartbeat, release |
| `migration_admin` | future migration tooling |
| `operator_admin` | operation inspection and operational repair |
| `break_glass_admin` | explicitly approved break-glass flows only |

Deployments may split these roles further, but they must not merge ordinary product caller roles with `orchestrator_mount` or `break_glass_admin`.
