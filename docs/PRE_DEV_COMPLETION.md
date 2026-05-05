# Pre-Dev Completion Package

Status: prepared for development handoff.

This package completes the documentation, contract, and decision work that can
be finished inside this repository before service implementation begins. It also
records the one upstream blocker discovered during evidence gathering.

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
- `docs/JVS_SMOKE_EVIDENCE_2026-05-05.md`
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
- first PostgreSQL adapter slice for operation reader/writer, idempotency
  create-or-reuse, and audit outbox append, with focused tests
- operation lease pure model/tests
- repo fence pure model/tests
- audit outbox pure model/tests
- pure recovery planner/classification for operation, fence, and audit outbox
  durable records
- path resolver shared corpus
- test harness

The recovery planner is a read-only classifier for later recovery worker/runbook
decisions; it is not a recovery loop, does not execute a worker, and does not
touch JVS/WebDAV/mount/storage mutation. The first PostgreSQL adapter slice now
implements operation reader/writer, idempotency create-or-reuse, and audit
outbox append. Repo/resource metadata adapters, fence adapters, the recovery
loop, real endpoint handlers, JVS execution, WebDAV serving, workload mount
issuance, and storage mutation are not implemented. Continue directly toward GA
by finishing guardrail review, implementing the remaining durable adapters and
recovery next, then adding handlers only after their dependency gates are
accepted.

Storage mutation remains blocked by G-005, recorded in
`docs/JVS_SMOKE_EVIDENCE_2026-05-05.md`. The JVS team has indicated the next
release will add the required capability, but AFSCP cannot close G-005 or
implement real storage mutation until a new GitHub release binary is pinned,
re-smoked, and accepted as evidence.

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
- Do not close G-005 until the JVS restore-plan blocker is resolved with a new
  GitHub release binary and passing smoke evidence.
