# Readiness Evidence

Status: active evidence ledger.

FINAL GA ACCEPTANCE REMAINS BLOCKED.
Open blockers include AFSCP owner acceptance, generated-client compatibility
review, security review, runbook drills, deployment drills, and human sign-off.
Implementation-baseline evidence does not close those gates.

AFSCP GA gates are internal to the shared filesystem control plane. Reference
consumer adoption notes can inform compatibility work, but no first consumer or
sibling repository acceptance is an AFSCP gate or release blocker.

This ledger records closure evidence for GA pre-dev admission gates, review
checklist items, and risk decisions. A gate is open until this document links to
reviewed evidence.

Status values:

- `open`: no accepted evidence yet
- `in_review`: evidence exists and is under review
- `closed`: evidence accepted
- `accepted_risk`: residual risk accepted under `docs/DEVELOPMENT_GOVERNANCE.md`
- `blocked`: cannot proceed without upstream decision

Current implementation evidence includes pushed control-plane primitives for the
PostgreSQL migration contract, operation lease pure model/tests, repo fence pure
model/tests, audit outbox pure model/tests, pure recovery planner/classification
for operation, fence, audit outbox, and repo recovery inspection durable
records, the first PostgreSQL adapter slice for operation reader/writer,
DB-only operation lease claim/reclaim/recover/finalize/renew plus lease-fenced
worker progress/terminal update primitive, idempotency create-or-reuse, and
audit outbox append plus DB-only at-least-once delivery primitive, the minimal
PostgreSQL repo fence adapter for held fence read/create/active release, path
resolver shared corpus, pure resource metadata models/store contracts plus the
PostgreSQL adapter and migration contract for volumes, namespaces, namespace
volume bindings, and repo/repo lifecycle metadata, SELECT-only repo recovery
inspection readers for candidate repos and held repo fences, and denied audit
behavior in the neutral shell/AuthGate paths. The planner and repo recovery
inspection are only read-only classifiers for later worker/runbook decisions;
they do not execute a general recovery loop or touch JVS/mount/storage mutation.
Audit outbox stale recovery plus HTTP JSON delivery is wired as an explicitly
enabled at-least-once worker path; that is the AFSCP GA audit delivery scope,
and non-HTTP sink integrations are future extensions rather than GA blockers.
External sinks must dedupe by `audit_event_id`. WebDAV export create/get/revoke,
the WebDAV policy gateway, DB-backed runtime request ledger accounting, stale
open runtime request recovery, and explicit-gated terminal export session
reconcile now exist. The WebDAV/export review gate remains in review: runtime
request rows are a dedicated gateway ledger rather than per-request operation
rows, and external review/runbook evidence is still pending. Current
implementation evidence also includes
repo lifecycle workers, save/restore flows, namespace-scoped template
create/clone, workload mount issuance and orchestrator plans, writer-session
fences with shared repo-row serialization against read-write session admission,
and an explicit workload mount stale-lease scan. Generated-client compatibility
review, AFSCP owner/security acceptance, runbook drills, and human GA acceptance
remain incomplete.

## Gate Ledger

| Gate ID | Area | Status | Owner Role | Reviewer Roles | Evidence Link | Decision | Review Date | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| G-001 | Runtime ADR | in_review | AFSCP maintainer | AFSCP maintainer, platform owner | `docs/adr/0005-runtime-and-service-shape.md` | Go runtime selected for handoff | 2026-05-05 | Human acceptance still required before merge |
| G-002 | Service auth and caller roles | in_review | AFSCP maintainer | Security owner, AFSCP product owner | `docs/adr/0006-service-auth-and-roles.md`, `docs/contracts/afscp-internal-api-v1.md` | mTLS/service principal plus namespace roles | 2026-05-05 | AFSCP security/product acceptance still required before final GA; new or breaking handler auth behavior must not be accepted without this gate |
| G-003 | Schemas and OpenAPI | in_review | AFSCP maintainer | AFSCP product owner, generated-client compatibility owner, operator/tooling owner | `api/schemas/afscp-internal-v1.schema.json`, `api/openapi/internal-v1.openapi.yaml` | machine contract parity pass exists | 2026-05-05 | Generated-client compatibility review still required |
| G-004 | Standard envelopes and stable errors | in_review | AFSCP maintainer | AFSCP product owner, generated-client compatibility owner | `api/schemas/afscp-internal-v1.schema.json`, `docs/API_CONTRACT_DRAFT.md` | operation/error envelope and stable error enum drafted | 2026-05-05 | Error code naming requires AFSCP product and generated-client compatibility acceptance |
| G-005 | JVS runner pin and smoke evidence | closed | AFSCP maintainer | JVS owner | `docs/adr/0009-jvs-runner-pin.md`, `docs/JVS_SMOKE_EVIDENCE_2026-05-05-v0.4.8.md` | JVS v0.4.8 release binary pinned and smoke passed, including clean controlled CWD, restore discard, and pending/idle recovery status; v0.4.7 restore-run recovery plan residual blocker resolved | 2026-05-05 | Only the JVS gate is closed; real storage mutation still requires accepted contracts, fences, session drain, operation leases, audit behavior, and focused tests |
| G-006 | Path resolver contract and corpus | in_review | AFSCP maintainer | Security owner | `docs/adr/0012-path-resolver-and-fences.md`, `docs/contracts/repo-path-contract-v1.md`, `internal/pathresolver/pathresolver.go`, `internal/pathresolver/pathresolver_test.go`, `internal/pathresolver/testcorpus/corpus.go` | resolver contract plumbing implemented; shared reusable corpus exists, awaiting review/acceptance | 2026-05-05 | Gate remains open until security review accepts corpus coverage |
| G-007 | WebDAV export contract | in_review | AFSCP maintainer | Security owner, generated-client compatibility owner | `docs/adr/0010-webdav-export-gateway.md`, `docs/contracts/export-access-webdav-v1.md` | AFSCP-controlled gateway required | 2026-05-05 | Implementation evidence now includes export create/get/revoke, `afscp-export-gateway --serve`, durable runtime request ledger accounting, stale open request recovery, and terminal reconcile; security/generated-client compatibility review and runbook evidence remain pending |
| G-008 | Workload orchestrator contract | in_review | Platform/runtime contract owner | AFSCP maintainer, security owner | `docs/adr/0011-workload-orchestrator-contract.md`, `docs/contracts/workload-mount-binding-v1.md` | two-layer mount contract drafted | 2026-05-05 | Requires platform/runtime contract acceptance |
| G-009 | Writer-session fence contract | in_review | AFSCP maintainer | Operations owner, platform/runtime contract owner, generated-client compatibility owner | `docs/adr/0012-path-resolver-and-fences.md`, `docs/contracts/operation-state-machine-v1.md`, `internal/fences`, `internal/store/postgres` | writer fence and lifecycle fence drafted; handler and recovery integration exists for restore/template writer fences and RW export/workload admission, with shared repo-row serialization before held-fence checks | 2026-05-05 | AFSCP owner/security review, race/drill evidence, and human acceptance remain pending |
| G-010A | Repo lifecycle state and caller mapping | in_review | AFSCP maintainer | AFSCP product owner, generated-client compatibility owner | `docs/adr/0008-repo-lifecycle-policy.md`, `docs/contracts/repo-lifecycle-v1.md` | transition rules and generic caller mapping drafted | 2026-05-05 | AFSCP product acceptance required |
| G-010B | Repo lifecycle fence and session drain | in_review | AFSCP maintainer | Operations owner, platform/runtime contract owner, generated-client compatibility owner | `docs/contracts/repo-lifecycle-v1.md`, `docs/contracts/operation-state-machine-v1.md` | read-only/read-write drain semantics drafted; export terminal reconcile and workload mount stale-lease scan with kept-blocked operator signal exist | 2026-05-05 | Platform/runtime contract acceptance, nonzero/uncertain recovery runbook drills, and human acceptance remain pending |
| G-010C | Repo retention and purge authorization | in_review | AFSCP maintainer | AFSCP product owner, security owner, operations owner | `docs/adr/0008-repo-lifecycle-policy.md`, `api/schemas/afscp-internal-v1.schema.json` | lifecycle policy and caller approval-reference requirements drafted | 2026-05-05 | AFSCP product/security acceptance required |
| G-010D | Repo lifecycle recovery and runbooks | in_review | AFSCP maintainer | Operations owner, security owner | `docs/contracts/repo-lifecycle-v1.md`, `docs/OPERATIONAL_READINESS.md`, `docs/runbooks/ga-runbooks.md` | recovery phases, runbooks, and drill expectations drafted | 2026-05-05 | Drill evidence still required before GA |
| G-010E | Repo lifecycle audit and redaction | in_review | AFSCP maintainer | Security owner, AFSCP product owner | `docs/OPERATIONS_AND_AUDIT.md`, `docs/contracts/repo-lifecycle-v1.md`, `internal/audit/event_test.go` | lifecycle audit events and redaction rules drafted; stable audit taxonomy/redaction guardrail tests added | 2026-05-05 | HTTP JSON audit delivery sink exists; deployment retention evidence and review acceptance remain pending |
| G-011 | Operation recovery and audit | in_review | AFSCP maintainer | Operations owner, security owner | `docs/adr/0007-operation-store-and-audit-outbox.md`, `docs/contracts/operation-state-machine-v1.md`, `docs/OPERATIONAL_READINESS.md`, `migrations/0001_control_plane_persistence.sql`, `internal/store/migration_contract_test.go`, `internal/operations`, `internal/audit`, `internal/inspection`, `internal/store/postgres` | PostgreSQL operation store and outbox selected; migration contract, operation lease pure model/tests plus DB-only lease and lease-fenced worker update primitives, audit outbox pure model/tests, read-only recovery classification including repo recovery inspection, and first PostgreSQL adapter slice for operations/idempotency/audit outbox append plus DB-only at-least-once delivery primitive, minimal repo fence read/create/active-release, SELECT-only repo recovery inspection readers, and explicit-gated HTTP JSON audit delivery worker integration exist | 2026-05-05 | External sink idempotency by `audit_event_id`, delivery drills, and review acceptance remain pending |
| G-012 | Namespace disable and policy-change behavior | in_review | AFSCP maintainer | AFSCP product owner, security owner | `docs/contracts/namespace-volume-binding-v1.md`, `docs/SECURITY_AND_TENANCY.md`, `internal/resources`, `internal/store/postgres` | namespace disable semantics drafted; operation-backed namespace disable handler/recovery, namespace/binding metadata persistence, and mutation/session policy gates are implemented | 2026-05-05 | AFSCP product/security acceptance plus AFSCP deployment drills remain pending |
| G-013 | Required runbooks and drills | in_review | Operations owner | AFSCP maintainer, security owner | `docs/runbooks/README.md`, `docs/runbooks/ga-runbooks.md`, `docs/OPERATIONAL_READINESS.md` | runbook catalog, scenario runbooks, and drill evidence format drafted | 2026-05-05 | Drills still required before GA |
| G-014 | Observability and alerting | in_review | Operations owner | Platform owner, security owner | `docs/OPERATIONAL_READINESS.md` | alert classes and threshold requirements drafted | 2026-05-05 | Numeric SLO thresholds still deployment-dependent |
| G-015 | Backup and restore plan | in_review | Operations owner | Platform owner | `docs/OPERATIONAL_READINESS.md` | backup/restore scope and drill requirements drafted | 2026-05-05 | Drill evidence still required before GA |
| G-016 | Secret redaction review | in_review | Security owner | AFSCP maintainer, operations owner | `docs/SECURITY_AND_TENANCY.md`, `docs/contracts/operation-state-machine-v1.md`, `docs/OPERATIONAL_READINESS.md`, `internal/audit/event_test.go` | redaction surfaces documented; stable audit event redaction guardrail tests added | 2026-05-05 | Security review acceptance still pending |

## Risk Decision Ledger

Risk decisions are summarized in `docs/RISK_REGISTER.md`. If a risk is accepted
instead of closed, this ledger must link the approval artifact, expiration or
review date, compensation controls, and residual risk statement.
