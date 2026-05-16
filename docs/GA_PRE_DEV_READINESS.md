# GA Pre-Dev Readiness

Status: GA implementation baseline source of truth.

This document supersedes stage-oriented planning language in active handoff
documents. Historical review and research documents may still say P0, P1, or
MVP; read those terms as historical scope notes unless this document says
otherwise.

AFSCP is being prepared for one independently releasable GA target, not a
staged product rollout for any first consumer. It must run, evolve, release,
and gate GA as a product-agnostic shared filesystem control plane. The
pre-development handoff is complete and the repository is now in the GA
implementation baseline: service skeleton, handlers, workers, generated
contracts, and focused package tests may exist and continue to evolve. Final GA
is released only when the repo-local gate in `docs/GA_RELEASE_GATES.md` passes.
New capabilities or breaking behavior changes must add or update the
corresponding repo-local evidence before they are merged as GA behavior.

## GA Product Scope

GA proves AFSCP as a product-agnostic shared filesystem control plane with no
release dependency on first or reference consumers. Reference consumer adoption
or handoff recommendations are external compatibility material, not GA gates.

GA includes:

- managed JuiceFS-backed volumes and volume health
- namespaces as storage isolation and caller-service authorization boundaries
- namespace volume binding and policy
- repo create/get with AFSCP-owned canonical path allocation
- repo archive, restore-from-archive, delete request, restore-from-tombstone, and purge lifecycle operations
- JVS-backed save point create/list and direct restore
- namespace-scoped immutable repo templates and same-namespace clone
- WebDAV export sessions with short-lived credentials
- workload mount bindings and orchestrator-only mount plans
- mount binding heartbeat, release, revoke, expiry, and stale-lease reconciliation
- durable operations, idempotency, recovery, low-level audit, and operator
  inspection, with `GET /internal/v1/operations/{operationId}` as the only
  stable GA internal API inspection surface

GA excludes:

- product authorization or product workflow decisions
- caller-specific workspace, catalog object, task, project, or workflow concepts
- global template marketplace or ordinary cross-namespace template sharing
- version merge, conflict resolution, or ordinary single-writer enforcement
- raw JuiceFS direct mount for ordinary users or workloads
- automated legacy migration
- namespace delete APIs
- product display-name rename, catalog detach, or other caller-specific lifecycle vocabulary
- billing UI, per-file ACL UI, SMB/NFS, and multi-region replication policy
- internal API operations list/search endpoints and audit/fence aggregation
  endpoints; correlated-resource lookup, intervention queues, fence views, and
  audit outbox lag views are runbook, read-only DB, observability, or
  deployment-side operator-tooling concerns

If a calling product needs an excluded behavior, the caller must keep that
behavior outside AFSCP or sponsor a new reviewed contract before it enters core.

## Caller Model

Only trusted internal principals call AFSCP directly:

| Actor | Direct AFSCP Access | Responsibility |
| --- | --- | --- |
| Calling product control plane | yes | product authz, business mapping, namespace/repo/template requests |
| Admin job or operator tool | yes | volume, namespace, health, repair, audit, and approved operational action |
| Migration job | yes, only through migration role | explicit audited migration and cutover tooling |
| Dedicated workload orchestrator | yes, orchestrator role only | consume mount plans, execute runtime mounts, report status |
| Client connector or desktop app | no | consume calling-product issued WebDAV credentials |
| Workload container | no | read/write mounted payload only |
| End user | no | authenticate to the calling product |

AFSCP audit records must always distinguish the authenticated
`caller_service` from the `authorized_actor` supplied by that caller.

## Frozen Product Decisions

- AFSCP is a storage execution authority, not a product authorization service.
- New repos use a shared managed JuiceFS-backed volume unless namespace policy chooses another managed volume.
- All new AFSCP-managed repos use separated `control/.jvs` and `payload/` roots.
- WebDAV and workload mounts expose only `payload/`.
- Ordinary client and workload flows never receive JuiceFS root credentials, metadata URLs, object store credentials, raw root paths, or Secret references.
- Only the dedicated orchestrator role may see orchestrator mount plans with Secret references.
- Repo templates are immutable and namespace-scoped in GA.
- Cross-namespace template clone is rejected by default.
- Cross-volume template clone is rejected with `VOLUME_MISMATCH_REQUIRES_IMPORT`.
- Direct restore to a save point is a version mutation. It must acquire the writer-session fence, block new read-write sessions, and reject active or uncertain read-write export/workload sessions.
- Dirty direct restore behavior is fail-closed unless a reviewed API option explicitly models and audits a supported JVS dirty-state choice.
- Namespace disable rejects new mutating operations, new exports, and new mount bindings. Existing read-write sessions must be revoked or allowed to expire according to a documented operator action before destructive or restore activity proceeds.
- `quota_bytes_default` is a policy record and enforcement hook for GA. It is not enforced unless the selected volume capability `directory_quota` supports directory quota enforcement and the corresponding volume integration explicitly enables directory quota enforcement.
- Product deletion or archive workflows call AFSCP repo lifecycle APIs for storage state changes. Product display names and catalog detach remain caller-owned metadata.
- Repo IDs are stable and immutable. AFSCP does not provide a display-name rename API because display names belong to the calling product.
- Repo delete is not a raw filesystem unlink. It is an auditable lifecycle operation that blocks new sessions, drains or revokes existing exports and mounts, tombstones retained data, and purges only after the accepted retention or purge policy permits it.
- Break-glass direct mount is disabled by default and is not part of ordinary GA access.

## GA Implementation Baseline Gates

These items are the readiness contract for building directly toward GA. They
must not be treated as delivery phases or as automatically closed by the
presence of baseline code. Existing handlers and workers must stay aligned with
contracts and automated evidence; new or breaking endpoint and storage mutation
behavior must pass `scripts/verify-ga-release.sh` before it is GA-releasable.

| Area | Automated Evidence |
| --- | --- |
| Runtime decision | ADR records runtime language, framework, packaging, and test command |
| Service auth | caller principal mapping, namespace role policy, and admin/orchestrator allowlists are contract and test guarded |
| API contract | JSON schemas, internal OpenAPI, operation envelope, standard error envelope, and stable error families exist |
| JVS runner | JVS release binary/version/checksum is pinned; argv, JSON success/error shapes, exit-code mapping, dirty-state behavior, and clean-CWD smoke tests are recorded |
| Path resolver | ID grammar, decode rules, symlink policy, `.jvs` denial, and shared resolver corpus are test guarded |
| WebDAV export | credential storage, TTL/reissue, revoke behavior, active session accounting, method policy, and audit redaction are contract and test guarded |
| Workload mount | workload mount platform/runtime contract, payload-only subdir mapping, Secret RBAC, heartbeat/release/revoke, and confirmed-unmounted semantics are contract and test guarded |
| Writer-session fence | acquisition, release, recovery, stale lease behavior, read-only treatment, and operator-intervention behavior are shared contracts with tests |
| Repo lifecycle | archive, restore-archived, delete, restore-tombstoned, purge, transition rules, lifecycle fence, session drain, retention, purge approval-reference, and recovery semantics are contract and test guarded |
| Operation/audit | state machine, idempotency, recovery matrix, audit outbox, retention, redaction, replay, operator intervention, and the single operation-by-ID internal API inspection boundary are contract and test guarded |
| Namespace policy | disable and policy-change behavior for new and existing sessions is contract and test guarded |
| Governance | evidence requirements, risk register, contract versioning, and release-gate docs are active |
| GA operations | required runbooks, observability, alerting, backup/restore, recovery behavior, and on-call criteria are repo-local artifacts |

## GA Admission Criteria

GA readiness requires evidence, not just implementation completion.

- Core product docs, contracts, tests, and fixtures are internally consistent,
  use the AFSCP generic model, and avoid first/reference consumer names,
  caller application methods, or caller business logic.
- The generated OpenAPI matches JSON schemas and the narrative contract.
- Contract tests cover caller authz, namespace mismatch, path traversal, WebDAV method policy, mount-plan secrecy, idempotency, stable error families, and denied audit events.
- JVS smoke tests prove external control root init/save/history/restore/clone/doctor behavior with no `.jvs` under payload roots.
- WebDAV tests prove payload-root chroot, root-level `.jvs` denial, encoded traversal denial, symlink escape denial, TTL, revoke, and credential redaction.
- Workload mount tests prove product callers do not receive Secret refs, orchestrator plans contain only payload subdirs, leases are reconciled, and direct restore treats uncertain writers as active.
- Repo lifecycle tests prove archive/delete block new sessions, drain or revoke existing read-only and read-write exports and mounts, tombstone retained data, honor purge policy and generic caller approval-reference requirements, and recover correctly after process restart.
- Operation recovery tests cover process restart during repo create, save, direct restore, template create/clone, export create/revoke, and mount binding create/revoke.
- Audit events are emitted for success, failure, denied authz, denied path, credential issue/revoke, mount plan issue, restore rejection, and operator intervention.
- Operators have documented runbooks for the GA incident and recovery cases listed in `docs/runbooks/README.md`.
- All GA-blocking risks in `docs/RISK_REGISTER.md` are covered by repo-local automated evidence.
- No ordinary API response, workload environment, client connector flow, or log output exposes forbidden JuiceFS credential material.

## Implementation Guardrail

The implementation baseline may include package layout, health endpoint, config
loading, logging, route registration, generated contract plumbing, endpoint
handlers, workers, and focused tests. This does not close every GA gate. Final
GA remains evidence-driven: existing and future storage handlers must be driven
by accepted schemas, OpenAPI, auth, JVS, operation, audit, export, mount, and
writer-session contracts plus repo-local tests, not by ad hoc payloads,
narrative-only assumptions, or manual approval.
