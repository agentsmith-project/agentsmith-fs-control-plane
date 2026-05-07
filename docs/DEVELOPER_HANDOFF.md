# Developer Handoff

Status: neutral Go service, contract guardrails, resource metadata persistence,
durable operation intake/recovery, repo/JVS execution, repo lifecycle recovery,
save/restore flows, namespace-scoped template create/clone, WebDAV export
create/get/revoke plus gateway serving, workload mount issuance/orchestrator
plans, writer fences with shared repo-row serialization, workload mount
stale-lease scanning, and explicit-gated audit outbox HTTP JSON delivery are in
place. The AFSCP GA audit delivery scope is HTTP JSON; non-HTTP audit sink
integrations are future extensions, not GA blockers. AFSCP owner/security
acceptance, generated-client compatibility review, runbook drills, and human GA
acceptance remain open where tracked by readiness gates.

This is the current handoff document for the coding team. It assumes the team is
building AFSCP directly toward GA, not through P0/P1 product stages, and should
continue from the existing skeleton instead of restarting it.

## What AFSCP Is

AFSCP is an internal, product-agnostic shared filesystem control plane. It runs,
evolves, releases, and gate-reviews independently from first or reference
consumers. It manages:

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

Reference consumers may map their own product objects to AFSCP repo IDs, but
their business concepts stay outside AFSCP. Consumer-specific adoption notes are
external references and must not become core AFSCP GA/release gates.

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
internal/exportaccess
internal/exportgateway
internal/exportreconcile
internal/contractcheck
internal/inspection
internal/observability
```

Verification commands:

```bash
bash scripts/verify-ga-baseline.sh
```

The script runs `git diff --check`, `go test -count=1 ./...`, and the internal
API contract verifier. Passing it is local implementation baseline evidence, not
final production GA acceptance.

Internal API deployments must set
`AFSCP_API_WEBDAV_EXPORT_PUBLIC_BASE_URL`. This is an AFSCP control-plane
runtime config value, not caller/product-specific config. It must be an
`http`/`https` absolute URL with no userinfo, query, or fragment, and is used as
the WebDAV export `access.url` base. Missing or invalid configuration fails
closed for export access URL issuance.

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
- WebDAV export create/get/revoke handler and PostgreSQL store boundary that
  atomically commit the create operation, export session, and audit event while
  returning credential secrets only on the first successful create response
- repo and template storage identities are recorded as control-plane metadata;
  namespace-scoped immutable template create/clone handlers and recovery paths
  are implemented with same-namespace and same-volume GA gates
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
- separately gated `repo_purge` recovery through `afscp-worker --run-once`,
  `repoexec`, a dedicated PostgreSQL purge list/acquire/commit boundary, session
  drain checks, destructive removal of AFSCP-managed retained storage, terminal
  `purged` metadata, audit, and lifecycle fence release when the explicit repo
  purge recovery gate is enabled
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
  restore-run/template writer gating, and repo lifecycle drain gating; export
  sessions are wired into API create/get/revoke, WebDAV gateway admission and
  runtime observation, terminal reconcile, and repo archive/delete recovery
  drain checks. Workload mount issuance, orchestrator plans, stale mount
  scanning, restore-run execution, and template create source dirty-race gates
  are wired through durable operation and session/fence boundaries.
- WebDAV export gateway serving through `afscp-export-gateway --serve`, with
  Basic auth, active/unexpired WebDAV session admission, mode/method policy,
  source and `Destination` path policy, payload no-follow filesystem access,
  and DB-backed runtime observation
- durable export runtime observation uses DB atomic deltas: request start
  records `+1` active request and mutating start also records `+1` active
  write; request end records matching `-1` deltas; heartbeat records `0/0`
  without changing active counts. Positive start deltas are admitted only while
  the session is still `active` and unexpired at the DB boundary, closing the
  revoke/expiry TOCTOU window.
- explicit-gated terminal export session reconcile through `afscp-worker
  --run-once` before operation recovery. It is enabled by
  `AFSCP_EXPORT_SESSION_RECONCILE_ENABLED=true`, uses
  `AFSCP_EXPORT_SESSION_RECONCILE_POSTGRES_DSN` with fallback to
  `AFSCP_POSTGRES_DSN` and `AFSCP_DATABASE_URL`, and requires
  `AFSCP_EXPORT_SESSION_RECONCILE_OWNER`; batch size is controlled by
  `AFSCP_EXPORT_SESSION_RECONCILE_LIMIT`.
- terminal export reconcile covers zero-count `revoking -> revoked` and
  zero-count expired `active -> expired` without requiring a fresh gateway
  heartbeat. Nonzero counts and stale/uncertain states still fail closed and
  require operator/runbook follow-up.
- workload mount issuance and orchestrator-only mount plans are implemented.
  Stale non-terminal workload mounts are scanned by an explicit worker runner;
  the scan reports kept-blocked bindings as operator-visible evidence rather
  than silently terminalizing uncertain sessions.
- save point create, restore preview/discard/run, and namespace-scoped template
  create/clone are implemented as operation-backed JVS worker paths using the
  pinned v0.4.8 binary. Template create creates a fresh save point inside the
  operation and uses writer-session fencing plus active/stale export and mount
  checks before cloning.
- writer-session fence acquisition for restore/template and read-write
  export/workload mount admission share the repo row as the durable
  serialization primitive before held-fence checks, closing the source dirty
  race between RW session admission and writer-fence commit.
- audit outbox pure model and tests
- pure recovery planner/classification for operation, fence, audit outbox, and
  repo recovery inspection durable records
- read-only repo recovery inspection composition over repo lifecycle metadata,
  held repo fences, and supplied holder/last lifecycle operation records, plus
  PostgreSQL SELECT-only readers for lifecycle candidate repos and all held repo
  fences
- repo recovery inspection now has durable session surfaces for exports and
  workload mounts where implemented; AFSCP owner/platform-runtime review and
  runbook drills still decide whether evidence is sufficient for GA closure
- path resolver guardrails and shared corpus
- denied audit coverage in the neutral shell and AuthGate paths
- contract verifier covering selected OpenAPI, schema, docs, and Go DTO drift
- focused tests for the above

Partially completed:

- API shell routes known contract paths to concrete handlers where implemented,
  including namespace upsert/disable, namespace volume binding, repo create/read
  and lifecycle intake, save/restore, template create/clone, WebDAV export,
  workload mount issuance/plan/status/heartbeat/release/revoke, and operation
  inspection. The explicit-gated workers cover metadata recovery, repo create,
  repo lifecycle and purge, save/restore, template create/clone, export terminal
  reconcile, workload mount stale-lease scanning, and audit outbox HTTP JSON
  delivery. External acceptance, generated-client review, deployment drills,
  and human GA sign-off remain open where tracked.
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
  `repo_create`, explicit-gated `repo_archive`/`repo_restore_archived`/
  `repo_delete`/`repo_restore_tombstoned`, and separately gated `repo_purge`
  recovery executors. With the repo create gate enabled, `repo_create` runs JVS
  `init` plus `doctor --strict` and commits through the dedicated PostgreSQL
  repo-create boundary with fence release. With the repo lifecycle recovery gate
  enabled, archive, restore-archived, delete, and restore-tombstoned commit
  through dedicated PostgreSQL repo lifecycle boundaries with fence release.
  Delete is a metadata tombstone, not physical deletion. Restore-tombstoned uses
  the accepted operation time with exclusive retention expiry and restores to
  recorded `pre_delete_status`. With the repo purge gate enabled, purge removes
  AFSCP-managed retained storage and commits `purged` metadata through a
  dedicated purge boundary. Export session reconcile and workload mount
  stale-lease scanning run before operation recovery when enabled. Save/restore
  and template create/clone run through dedicated JVS worker paths. The
  explicitly configured HTTP JSON outbox delivery worker is the GA audit
  delivery implementation; other sink kinds remain future extensions.
- Path resolver guardrails exist and are used by repo create/recovery,
  save/restore, template, WebDAV export gateway, and workload mount paths. File
  API integration remains outside this handoff scope.

Not implemented:

- complex gateway crash recovery or per-request WebDAV operation records. If a
  gateway process crashes after a positive start delta commits and before the
  matching end delta, active request/write counts may conservatively remain
  positive until an operator/runbook repair or future recovery path resolves
  the session.
- deployment evidence that the configured external HTTP JSON audit sink dedupes
  by `audit_event_id`
- generated clients, AFSCP owner/security acceptance, deployment-specific
  observability thresholds, runbook drills, and human GA acceptance evidence

## Contract Implementation Order

Continue in dependency order:

1. Finish review and acceptance for the existing contract verifier, denied audit,
   migration contract, lease, fence, outbox, and path resolver guardrails.
2. Review and accept the read-only repo recovery inspection over durable repo,
   fence, and operation records.
3. Add recovery loop behavior only after the remaining durable primitives have
   tests.
4. Implement volume and namespace binding APIs.
5. Continue from the implemented repo create, repo lifecycle, export/WebDAV,
   workload mount, save/restore, and template paths by closing remaining review
   evidence only through accepted contracts, fences, session drain, operation
   leases, audit behavior, focused tests, runbook drills, and owner acceptance.
   G-005 is closed by JVS v0.4.8 evidence; it is not by itself GA acceptance for
   storage mutation.

## JVS Gate Status

G-005 is closed. JVS v0.4.8 is pinned and smoke-tested in
`docs/JVS_SMOKE_EVIDENCE_2026-05-05-v0.4.8.md`; the v0.4.7 blocker evidence
remains historical in `docs/JVS_SMOKE_EVIDENCE_2026-05-05.md`.

This closes the JVS gate. Repo create, save/restore, template create/clone, and
repo lifecycle JVS verification now have explicit-gated worker paths for their
pinned JVS commands. GA acceptance for storage mutation still requires accepted
contracts, fences, session drain, operation leases, audit behavior, focused
tests, runbook drills, and owner review. AFSCP must not delete private JVS files
directly.

## Repo Lifecycle Rules

Repo lifecycle GA target contract rules cover:

- `archive`
- `restore-archived`
- `delete`
- `restore-tombstoned`
- `purge`

Current implementation includes opt-in worker execution for `archive`,
`restore-archived`, `delete`, `restore-tombstoned`, and separately gated
`purge`.

Important behavior:

- Archive/delete/purge acquire lifecycle fence.
- Lifecycle fence blocks new exports, mounts, save, restore-run, template
  operations, and lifecycle mutations.
- Archive/delete/purge drain existing read-only and read-write exports/mounts
  before storage state changes that require no further access.
- Purge is permanent and requires retention policy, caller approval reference,
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
- purge approval-reference and retention denial
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
