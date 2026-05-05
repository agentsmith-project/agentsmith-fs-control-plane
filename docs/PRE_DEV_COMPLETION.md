# Pre-Dev Completion Package

Status: prepared for development handoff.

This package completes the documentation, contract, and decision work that can
be finished inside this repository before service implementation begins. The
JVS upstream blocker found during evidence gathering is now closed by v0.4.8
smoke evidence.

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
later recovery worker/runbook decisions. `afscp-worker --run-once` now has an
opt-in production bootstrap for the minimal `namespace_upsert` and
`namespace_volume_binding_put` recovery executors plus metadata-only
`volume_ensure`; it does not touch JVS/WebDAV/mount/storage mutation. The first
PostgreSQL adapter slice now implements operation reader/writer, DB-only
operation lease claim/reclaim/recover/finalize/renew plus lease-fenced worker
progress/terminal update primitive, idempotency create-or-reuse, audit outbox
append plus DB-only at-least-once delivery primitive, minimal repo fence held
read/create/active release, and SELECT-only repo recovery inspection readers.
Worker-owned progress/terminal writes must use the lease-fenced update
primitive, not unguarded `UpdateOperation`. Resource metadata persistence for
volumes, namespaces, namespace volume bindings, and repo/repo lifecycle metadata
also exists as control-plane state only. Real external audit delivery
worker/sink integration, repo lifecycle workers, repo/JVS/WebDAV/mount endpoint
handlers, JVS execution, WebDAV serving, workload mount issuance, and storage
mutation are not implemented. Continue directly toward GA by finishing guardrail review,
reviewing the read-only repo recovery inspection, then adding remaining recovery
loop behavior and handlers only after their dependency gates are accepted.

G-005 is closed by JVS v0.4.8 evidence in
`docs/JVS_SMOKE_EVIDENCE_2026-05-05-v0.4.8.md`. This only closes the JVS gate.
Repo/JVS/storage handlers may now proceed only through accepted contracts,
fences, session drain, operation leases, audit behavior, and focused tests.

## Non-Negotiable Guardrails

- Do not expose JuiceFS root credentials, metadata URLs, object store
  credentials, Secret references, raw host paths, or control roots to ordinary
  product callers, clients, or workloads.
- Do not put AgentSmith workspace, file library, project, notebook task, or
  catalog concepts into AFSCP core packages.
- Do not implement storage mutation handlers from narrative docs alone; use the
  schemas/OpenAPI and accepted ADRs/contracts.
- Do not implement repo delete as raw filesystem delete.
- Do not enable WebDAV, workload mount, restore-run, or repo lifecycle behavior
  without the relevant session drain/fence semantics.
- Do not treat G-005 closure as implementation approval for storage mutation;
  use accepted contracts, fences, session drain, operation leases, audit
  behavior, and focused tests.
