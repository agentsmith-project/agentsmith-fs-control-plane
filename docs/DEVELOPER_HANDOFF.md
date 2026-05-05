# Developer Handoff

Status: neutral Go skeleton, contract guardrails, resource metadata
persistence, and the first PostgreSQL adapter slices are in place; storage
mutation implementation is not started.

This is the current handoff document for the coding team. It assumes the team is
building AFSCP directly toward GA, not through P0/P1 product stages, and should
continue from the existing skeleton instead of restarting it.

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

Current implementation shape:

```text
cmd/afscp-api
cmd/afscp-worker
cmd/afscp-export-gateway
cmd/afscp-contract-verify
internal/api
internal/auth
internal/config
internal/store
internal/resources
internal/operations
internal/audit
internal/pathresolver
internal/contractcheck
internal/inspection
internal/observability
```

Verification commands:

```bash
go test -count=1 ./...
go run ./cmd/afscp-contract-verify \
  -openapi api/openapi/internal-v1.openapi.yaml \
  -schema api/schemas/afscp-internal-v1.schema.json \
  -api-contract docs/contracts/afscp-internal-api-v1.md \
  -api-draft docs/API_CONTRACT_DRAFT.md
```

## Current Status

Completed:

- Go module bootstrap
- binary entrypoints for API, worker, export gateway, and contract verifier
- config loading
- structured logging
- health/readiness endpoints
- route metadata for internal v1 paths, including method, operation ID, route
  class, mutating flag, and required role
- error envelope helpers
- operation envelope types
- auth role and namespace guardrails
- store interfaces for operation, idempotency, and audit boundaries
- pure resource metadata models and validation for volumes, namespaces,
  namespace volume bindings, and repo/repo lifecycle metadata
- store interfaces for volume, namespace, namespace volume binding, and repo
  metadata persistence
- PostgreSQL migration contract for operations, idempotency, audit outbox, repo
  fences, volumes, namespaces, namespace volume bindings, and repo/repo
  lifecycle metadata
- first PostgreSQL adapter slice for operation reader/writer, idempotency
  create-or-reuse, and audit outbox append plus DB-only at-least-once delivery
  primitive, with focused tests
- PostgreSQL resource metadata adapter for volumes, namespaces, namespace volume
  bindings, and repo/repo lifecycle metadata, with focused tests
- repo and template storage identities are recorded as control-plane metadata;
  RepoTemplate publication lifecycle and handlers remain unimplemented
- minimal PostgreSQL repo fence adapter for held fence read, create, and active
  release, with focused tests
- operation lease pure model and tests
- repo writer/lifecycle fence pure model and tests
- audit outbox pure model and tests
- pure recovery planner/classification for operation, fence, and audit outbox
  durable records
- path resolver guardrails and shared corpus
- denied audit coverage in the neutral shell and AuthGate paths
- contract verifier covering selected OpenAPI, schema, docs, and Go DTO drift
- focused tests for the above

Partially completed:

- API shell routes known contract paths to capability-denied responses, but real
  endpoint handlers are not implemented.
- Operation, idempotency, audit, inspection, and store boundaries exist, with
  pure operation lease, repo fence, audit outbox, and recovery classification
  models. The first PostgreSQL adapter slice implements operation read/write,
  idempotency create-or-reuse, audit outbox append plus DB-only at-least-once
  delivery primitive, and minimal repo fence held read/create/active release.
  The recovery planner only classifies existing durable record values into
  high-level actions; it is not a recovery loop and does not execute workers or
  touch JVS/WebDAV/mount/storage mutation. Real external audit delivery
  worker/sink integration and the recovery loop are not implemented. Resource
  metadata persistence exists only as control-plane metadata storage; it is not
  real repo lifecycle execution, recovery, or storage mutation.
- Path resolver guardrails exist, but there is no storage mutation integration.

Not implemented:

- real volume, namespace, repo, template, export, mount, save, restore, or
  lifecycle handlers
- real repo lifecycle workers, recovery loop, archive/delete/purge execution, or
  storage state transitions
- fence enforcement beyond the minimal repo fence adapter slice
- real external audit delivery worker/sink integration
- JVS execution or repo initialization
- WebDAV export gateway file serving
- workload mount issuance or orchestrator mount plans
- storage mutation, drain, fence enforcement, or lifecycle mutation

## Contract Implementation Order

Continue in dependency order:

1. Finish review and acceptance for the existing contract verifier, denied audit,
   migration contract, lease, fence, outbox, and path resolver guardrails.
2. Build recovery inspection over durable repo and fence records, using the
   existing resource metadata persistence as read/write control-plane state.
3. Add recovery loop behavior only after the remaining durable primitives have
   tests.
4. Implement volume and namespace binding APIs.
5. Implement repo/JVS, export/WebDAV, workload mount, save/restore, template,
   and repo lifecycle handlers only after their dependency gates are accepted.
   Real storage mutation remains blocked by G-005 until the new JVS GitHub
   release binary is smoke-tested and accepted.

## Current Blocker

G-005 is not closed. Smoke with the pinned JVS `v0.4.7` release binary found
that completed restore-run leaves a restore plan that blocks `doctor --strict`
and can block `repo clone`. See
`docs/JVS_SMOKE_EVIDENCE_2026-05-05.md`.

The JVS team has indicated the next release will add the capability AFSCP needs.
AFSCP cannot close G-005, implement real storage mutation, or rely on
clone-after-restore behavior until a new JVS GitHub release binary is pinned,
re-smoked, and accepted as evidence. AFSCP must not delete private JVS files
directly.

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
