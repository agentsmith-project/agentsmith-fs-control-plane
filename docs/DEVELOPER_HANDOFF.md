# Developer Handoff

Status: ready for implementation team pickup.

This is the starting document for the coding team. It assumes the team is
building AFSCP directly toward GA, not through P0/P1 product stages.

## What AFSCP Is

AFSCP is an internal, product-agnostic storage control plane. It manages:

- JuiceFS-backed volumes
- namespaces and namespace policies
- JVS-backed repos
- repo lifecycle
- save points and version restore
- namespace-scoped repo templates
- WebDAV exports
- workload mount bindings
- orchestrator-only mount plans
- durable operations
- audit events

AgentSmith is the first expected caller, but AgentSmith product concepts stay
outside AFSCP. AgentSmith maps file libraries to AFSCP repo IDs and keeps its
own display names, catalog state, permissions, and user-facing workflows.

## Start Here

Read in this order:

1. `docs/PRE_DEV_COMPLETION.md`
2. `docs/GA_PRE_DEV_READINESS.md`
3. `docs/PRODUCT_REQUIREMENTS.md`
4. `docs/ARCHITECTURE.md`
5. `docs/API_CONTRACT_DRAFT.md`
6. `docs/contracts/`
7. `api/openapi/internal-v1.openapi.yaml`
8. `api/schemas/afscp-internal-v1.schema.json`
9. `docs/RISK_REGISTER.md`
10. `docs/JVS_SMOKE_EVIDENCE_2026-05-05.md`

## Runtime And Build

ADR 0005 selects Go.

Expected first implementation shape:

```text
cmd/afscp-api
cmd/afscp-worker
cmd/afscp-export-gateway
internal/api
internal/auth
internal/config
internal/store
internal/operations
internal/audit
internal/pathresolver
internal/jvsrunner
internal/repos
internal/exports
internal/mounts
internal/templates
internal/observability
```

Initial command:

```bash
go test ./...
```

## First Coding PR

The first coding PR should include only neutral skeleton work:

- Go module bootstrap
- binary entrypoints
- config loading
- structured logging
- health/readiness endpoints
- empty route registration from OpenAPI paths
- error envelope helpers
- operation envelope types
- test harness

It must not implement storage mutations, WebDAV file access, JVS execution, or
mount issuance.

## Contract Implementation Order

After skeleton, implement in dependency order:

1. config, logging, request context, standard envelopes
2. service authentication and caller role checks
3. PostgreSQL migrations for metadata, operations, fences, and audit outbox
4. path resolver and resolver corpus tests
5. operation store, idempotency, leases, and recovery inspection
6. volume and namespace binding APIs
7. repo create with external control root JVS init, only after G-005 is fixed
8. export session records and WebDAV gateway policy tests
9. workload mount binding records and orchestrator fake tests
10. writer-session fence
11. save/history/restore-preview
12. restore-run, only after JVS restore-plan blocker is fixed
13. template create/clone
14. repo lifecycle archive/delete/restore/purge

## Current Blocker

G-005 is not closed. Smoke with the pinned JVS `v0.4.7` release binary found
that completed restore-run leaves a restore plan that blocks `doctor --strict`
and can block `repo clone`. See
`docs/JVS_SMOKE_EVIDENCE_2026-05-05.md`.

Do not implement restore-run, clone-after-restore assumptions, or repo
reactivation flows until the JVS owner resolves this or accepts a command-level
cleanup contract. AFSCP must not delete private JVS files directly.

## Repo Lifecycle Rules

Repo lifecycle is in GA:

- `archive`
- `restore-archived`
- `delete`
- `restore-tombstoned`
- `purge`

Important behavior:

- Archive/delete/purge acquire lifecycle fence.
- Lifecycle fence blocks new exports, mounts, save, restore-run, template
  operations, and lifecycle mutations.
- Archive/delete/purge drain existing read-only and read-write exports/mounts
  before storage state changes that require no further access.
- Delete tombstones retained storage; it is not physical deletion.
- Purge is permanent and requires retention policy, product confirmation,
  lifecycle role authorization, operation record, and audit.
- Restore tombstoned returns to the recorded pre-delete accessibility state.

## Security Rules

Never return these to ordinary product callers, clients, workloads, logs, or
standard errors:

- JuiceFS metadata URL
- object store bucket credentials
- access keys or secret keys
- Kubernetes Secret references
- full host paths
- repo control root
- `.jvs` paths
- WebDAV passwords after secret-bearing response

Only `orchestrator_mount` can fetch mount plans with Secret refs.

## Test Expectations

Every boundary package needs tests before handler work depends on it:

- stable error envelope
- idempotency conflict
- caller role denial
- namespace mismatch
- path traversal, encoded traversal, double-decoded traversal
- symlink escape
- `.jvs` access/create denial
- WebDAV method matrix
- export revoke and active request reconciliation
- mount heartbeat/release/revoke terminal semantics
- writer-session fence with uncertain sessions
- lifecycle fence and session drain
- purge confirmation and retention denial
- operation restart recovery
- audit redaction

## Definition Of Ready For Storage Handlers

Before implementing any storage mutation handler:

- related ADR is accepted
- related schema/OpenAPI path is updated
- related gate in `docs/READINESS_EVIDENCE.md` links to evidence
- risk register entry is closed or explicitly accepted when waivable
- runbook exists for failure and intervention
- tests exist for denial, idempotency, audit, and recovery

Credential exposure, tenant isolation failure, user data loss, irrecoverable
operation ambiguity, and caller-visible contract break risks are non-waivable
for GA.
