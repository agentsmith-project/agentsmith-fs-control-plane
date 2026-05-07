# Pre-Dev Completion Package

Status: GA implementation-baseline package.

This package records the documentation, contract, and decision work that
admitted the current implementation baseline. The JVS upstream blocker found
during evidence gathering is now closed by v0.4.8 smoke evidence. Final GA is
governed by `docs/GA_RELEASE_GATES.md`, `docs/READINESS_EVIDENCE.md`, and the
repo-local command `scripts/verify-ga-release.sh`.

## Completed Artifacts

Product and scope:

- `docs/GA_PRE_DEV_READINESS.md`
- `docs/PRODUCT_REQUIREMENTS.md`
- `docs/PRODUCT_BOUNDARY.md`
- `docs/INTEGRATION_GUIDE.md`

Architecture and contracts:

- `docs/ARCHITECTURE.md`
- `docs/STORAGE_LAYOUT.md`
- `docs/API_CONTRACT_DRAFT.md`
- `docs/contracts/`
- `api/schemas/afscp-internal-v1.schema.json`
- `api/openapi/internal-v1.openapi.yaml`

ADR decisions:

- `docs/adr/0001-create-afscp.md`
- `docs/adr/0002-default-shared-juicefs-pool.md`
- `docs/adr/0003-namespace-scoped-templates.md`
- `docs/adr/0004-no-ordinary-single-writer-lock.md`
- `docs/adr/0005-runtime-and-service-shape.md`
- `docs/adr/0006-service-auth-and-roles.md`
- `docs/adr/0007-operation-store-and-audit-outbox.md`
- `docs/adr/0008-repo-lifecycle-policy.md`
- `docs/adr/0009-jvs-runner-pin.md`
- `docs/adr/0010-webdav-export-gateway.md`
- `docs/adr/0011-workload-orchestrator-contract.md`
- `docs/adr/0012-path-resolver-and-fences.md`

Operational readiness:

- `docs/OPERATIONS_AND_AUDIT.md`
- `docs/OPERATIONAL_READINESS.md`
- `docs/runbooks/README.md`
- `docs/JVS_SMOKE_EVIDENCE_2026-05-05-v0.4.8.md`
- `docs/JVS_SMOKE_EVIDENCE_2026-05-05.md` historical v0.4.7 blocker evidence
- `docs/RISK_REGISTER.md`
- `docs/READINESS_EVIDENCE.md`

Development handoff:

- `docs/DEVELOPER_HANDOFF.md`

## Implementation Admission

Neutral service skeleton and control-plane primitives now exist:

- Go module bootstrap
- package layout
- config loading
- health/readiness endpoint
- logging
- generated schema/OpenAPI plumbing
- neutral route registration and denied audit shell/AuthGate paths
- PostgreSQL migration contract
- first PostgreSQL adapter slice for operation reader/writer, DB-only operation
  lease claim/reclaim/recover/finalize/renew plus lease-fenced worker
  progress/terminal update primitive, idempotency create-or-reuse, and audit
  outbox append plus DB-only at-least-once delivery primitive, with focused
  tests
- pure resource metadata models, store contracts, PostgreSQL adapter, and
  migration contract for volumes, namespaces, namespace volume bindings, and
  repo/repo lifecycle metadata
- minimal PostgreSQL repo fence adapter for held fence read, create, and active
  release, with focused tests
- operation lease pure model/tests
- repo fence pure model/tests
- audit outbox pure model/tests
- pure recovery planner/classification for operation, fence, audit outbox, and
  repo recovery inspection durable records
- path resolver shared corpus
- test harness

The recovery planner and repo recovery inspection are read-only classifiers for
worker/runbook decisions. `afscp-worker --run-once` now has explicit-gated
production bootstraps for metadata recovery, repo create, repo lifecycle and
purge recovery, save/restore flows, template create/clone, export terminal
reconcile, workload mount stale-lease scanning, and audit outbox HTTP JSON
delivery. The PostgreSQL adapter slice implements operation reader/writer,
DB-only operation lease claim/reclaim/recover/finalize/renew plus lease-fenced
worker progress/terminal update primitives, idempotency create-or-reuse, audit
outbox append plus at-least-once delivery primitives, repo fence held/read/create
and active release, and SELECT-only repo recovery inspection readers.
Worker-owned progress/terminal writes must use the lease-fenced update
primitive, not unguarded `UpdateOperation`. Resource metadata persistence for
volumes, namespaces, namespace volume bindings, repo/repo lifecycle metadata,
exports, workload mounts, restore plans, and repo templates exists as
control-plane state. API/runtime implementation now includes repo/JVS lifecycle,
save/restore, namespace-scoped template create/clone, WebDAV export
create/get/revoke plus gateway serving, workload mount issuance and
orchestrator plans, writer fences, and durable operation-backed storage
mutation. Continue directly toward GA by keeping guardrails, generated
artifacts, security boundaries, runbooks, and operations behavior covered by
repo-local verification.

G-005 is auto-verified by JVS v0.4.8 evidence in
`docs/JVS_SMOKE_EVIDENCE_2026-05-05-v0.4.8.md`. This only covers the JVS gate.
Repo/JVS/storage handlers may now proceed only through versioned contracts,
fences, session drain, operation leases, audit behavior, and focused tests.

## Non-Negotiable Guardrails

- Do not expose JuiceFS root credentials, metadata URLs, object store
  credentials, Secret references, raw host paths, or control roots to ordinary
  product callers, clients, or workloads.
- Do not put caller workspace, catalog object, project, job/task object, or
  caller-specific catalog concepts into AFSCP core packages.
- Do not implement storage mutation handlers from narrative docs alone; use the
  schemas/OpenAPI and recorded ADRs/contracts.
- Do not implement repo delete as raw filesystem delete.
- Do not enable WebDAV, workload mount, restore-run, or repo lifecycle behavior
  without the relevant session drain/fence semantics.
- Do not treat G-005 closure as implementation approval for storage mutation;
  use contracts, fences, session drain, operation leases, audit behavior, and
  focused tests covered by `scripts/verify-ga-release.sh`.
