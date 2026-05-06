# Developer Handoff

Status: neutral Go skeleton, contract guardrails, resource metadata
persistence, metadata recovery workers, opt-in `repo_create` JVS execution, and
explicit-gated repo lifecycle recovery for archive, restore-archived, delete,
and restore-tombstoned are in place; purge, WebDAV, mount, save/restore,
template, and audit delivery workers remain unimplemented.

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
10. `docs/JVS_SMOKE_EVIDENCE_2026-05-05-v0.4.8.md`

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
- first PostgreSQL adapter slice for operation reader/writer, DB-only operation
  lease claim/reclaim/recover/finalize/renew plus lease-fenced worker
  progress/terminal update primitive, idempotency create-or-reuse, and audit
  outbox append plus DB-only at-least-once delivery primitive, with focused
  tests
- PostgreSQL resource metadata adapter for volumes, namespaces, namespace volume
  bindings, and repo/repo lifecycle metadata, with focused tests
- read-only PostgreSQL session-state readers for export sessions and workload
  mount bindings, limited to safe admission/drain fields
- repo and template storage identities are recorded as control-plane metadata;
  RepoTemplate publication lifecycle and handlers remain unimplemented
- minimal PostgreSQL repo fence adapter for held fence read, create, and active
  release, with focused tests
- repo create intake for durable `repo_create` operations
- JVS v0.4.8 runner foundations for fixed `init` and `doctor --strict`
  commands
- opt-in `repo_create` recovery through `afscp-worker --run-once`, `repoexec`,
  JVS `init`/`doctor --strict`, dedicated PostgreSQL atomic commit, and fence
  release when the explicit repo create recovery gate is enabled
- opt-in repo lifecycle recovery for `repo_archive`, `repo_restore_archived`,
  `repo_delete`, and `repo_restore_tombstoned` through `afscp-worker
  --run-once`, `repoexec`, dedicated scoped PostgreSQL list/acquire/commit
  boundaries, lifecycle fence release on success, session drain checks for
  archive/delete, JVS `doctor --strict` verification for restore operations,
  metadata tombstone for delete, and restore-tombstoned recovery to recorded
  `pre_delete_status` when the explicit repo lifecycle recovery gate is enabled
- namespace-bound repo read handlers for `GET /internal/v1/repos/{repoId}` and
  `GET /internal/v1/repos?namespace_id=&lifecycle_status=`, exposed through
  `InternalAPIShell` as repo storage projections without product catalog fields
- operation inspection handler for `GET /internal/v1/operations/{operationId}`,
  exposed through `InternalAPIShell` as a redacted `OperationRecord`
- operation lease pure model and tests
- repo writer/lifecycle fence pure model and tests
- repo access admission pure model for active/archived/tombstoned/purged repo
  status, namespace/binding status, writer-session fences, lifecycle fences,
  and lifecycle source-status rules; it is wired to lifecycle intake/admission
- session substrate pure model for export and workload-mount session state,
  restore-run writer gating, and repo lifecycle drain gating; it is wired into
  repo archive/delete recovery drain checks, but not yet to WebDAV gateway,
  mount plan, restore-run execution, or storage-backed session handlers
- audit outbox pure model and tests
- pure recovery planner/classification for operation, fence, audit outbox, and
  repo recovery inspection durable records
- read-only repo recovery inspection composition over repo lifecycle metadata,
  held repo fences, and supplied holder/last lifecycle operation records, plus
  PostgreSQL SELECT-only readers for lifecycle candidate repos and all held repo
  fences
- repo recovery inspection marks sessions, exports, and mounts as explicit
  `not_implemented` / not-inspectable surfaces; that marker is not evidence that
  no sessions, exports, or mounts exist
- path resolver guardrails and shared corpus
- denied audit coverage in the neutral shell and AuthGate paths
- contract verifier covering selected OpenAPI, schema, docs, and Go DTO drift
- focused tests for the above

Partially completed:

- API shell routes known contract paths to capability-denied responses, with
  metadata-only namespace upsert and namespace volume binding intake/read
  handlers implemented, plus repo create intake, repo lifecycle operation
  intake/admission, namespace-bound repo read storage projections, and operation
  inspection. The explicit-gated repo lifecycle worker covers
  archive/restore-archived/delete/restore-tombstoned; purge workers, WebDAV,
  mount, save/restore, template, broader session drain execution, and
  storage-backed handlers beyond the listed intake/read surfaces remain
  unimplemented.
- Operation, idempotency, audit, inspection, and store boundaries exist, with
  pure operation lease, repo fence, audit outbox, and recovery classification
  models. The first PostgreSQL adapter slice implements operation read/write,
  DB-only operation lease claim/reclaim/recover/finalize/renew plus
  lease-fenced worker progress/terminal update primitive, idempotency
  create-or-reuse, audit outbox append plus DB-only at-least-once delivery
  primitive, minimal repo fence held read/create/active release, and read-only
  repo recovery inspection readers. Worker-owned progress/terminal writes must
  use the lease-fenced update primitive, not unguarded `UpdateOperation`. The
  recovery planner and repo recovery inspection classify existing durable
  record values into high-level actions. `afscp-worker --run-once` now has an
  opt-in production bootstrap for the minimal `volume_ensure`,
  `namespace_upsert`, `namespace_volume_binding_put`, explicit-gated
  `repo_create`, and explicit-gated `repo_archive`/`repo_restore_archived`/
  `repo_delete`/`repo_restore_tombstoned` recovery executors. With the repo
  create gate enabled, `repo_create` runs JVS
  `init` plus `doctor --strict` and commits through the dedicated PostgreSQL
  repo-create boundary with fence release. With the repo lifecycle recovery gate
  enabled, archive, restore-archived, delete, and restore-tombstoned commit
  through dedicated PostgreSQL repo lifecycle boundaries with fence release.
  Delete is a metadata tombstone, not physical deletion. Restore-tombstoned uses
  the accepted operation time with exclusive retention expiry and restores to
  recorded `pre_delete_status`. It does not implement repo purge, save/restore,
  template, WebDAV, mount, or external audit delivery.
- Path resolver guardrails exist and are used by repo create recovery; broader
  WebDAV, mount, file API, and remaining lifecycle integration remains absent.

Not implemented:

- real repo lifecycle purge worker or physical delete storage transition
- real template, export, mount, save, or restore handlers
- repo lifecycle purge worker, session drain execution beyond lifecycle worker
  checks, and JVS save/restore/template/export/mount handlers; repo lifecycle
  intake/admission handlers and
  archive/restore-archived/delete/restore-tombstoned recovery already exist
- concrete handler wiring for the session admission model beyond lifecycle
  intake checks; repo access admission is already wired for lifecycle
  intake/admission
- real external audit delivery worker/sink integration
- JVS execution beyond repo create `init`/`doctor --strict` and repo lifecycle
  restore `doctor --strict`
- WebDAV export gateway file serving
- workload mount issuance or orchestrator mount plans
- session drain, broader fence enforcement, or lifecycle mutation

## Contract Implementation Order

Continue in dependency order:

1. Finish review and acceptance for the existing contract verifier, denied audit,
   migration contract, lease, fence, outbox, and path resolver guardrails.
2. Review and accept the read-only repo recovery inspection over durable repo,
   fence, and operation records.
3. Add recovery loop behavior only after the remaining durable primitives have
   tests.
4. Implement volume and namespace binding APIs.
5. Continue from implemented repo create intake, explicit-gated `repo_create`
   JVS recovery, and explicit-gated
   archive/restore-archived/delete/restore-tombstoned lifecycle recovery toward
   the remaining repo lifecycle, export/WebDAV, workload mount,
   save/restore, and template handlers only after their dependency gates are
   accepted. G-005 is closed by JVS v0.4.8 evidence; remaining repo/JVS/storage
   work may proceed only through accepted contracts, fences, session drain,
   operation leases, audit behavior, and focused tests.

## JVS Gate Status

G-005 is closed. JVS v0.4.8 is pinned and smoke-tested in
`docs/JVS_SMOKE_EVIDENCE_2026-05-05-v0.4.8.md`; the v0.4.7 blocker evidence
remains historical in `docs/JVS_SMOKE_EVIDENCE_2026-05-05.md`.

This closes the JVS gate. Repo create, restore-archived, and
restore-tombstoned now have explicit-gated worker paths for their pinned JVS
commands. Remaining storage mutation still requires accepted contracts, fences,
session drain, operation leases, audit behavior, and focused tests. AFSCP must
not delete private JVS files directly.

## Repo Lifecycle Rules

Repo lifecycle GA target contract rules cover:

- `archive`
- `restore-archived`
- `delete`
- `restore-tombstoned`
- `purge`

Current implementation includes opt-in worker execution for `archive`,
`restore-archived`, `delete`, and `restore-tombstoned`. `purge` remains an
unimplemented worker path.

Important behavior:

- Archive/delete/purge acquire lifecycle fence.
- Lifecycle fence blocks new exports, mounts, save, restore-run, template
  operations, and lifecycle mutations.
- Archive/delete/purge drain existing read-only and read-write exports/mounts
  before storage state changes that require no further access.
- Purge is permanent and requires retention policy, product confirmation,
  lifecycle role authorization, operation record, and audit.
- Delete is a metadata tombstone and not physical deletion.
- Restore tombstoned uses the accepted operation time and exclusive
  `retention_expires_at` boundary, then returns to the recorded pre-delete
  accessibility state.

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
