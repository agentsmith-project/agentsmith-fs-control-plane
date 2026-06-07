# Readiness Evidence

Status: active selector-driven implementation evidence ledger.

AFSCP current release-readiness convergence is governed by one repo-local gate:

```bash
bash scripts/verify-ga-release.sh
```

The script selects the release evidence manifest verifier mode from
`docs/release-evidence/ga-release-selector.json`. When that selector is absent,
or is present with `release_intent=convergence_seed`, the script runs
seed/convergence verification. When the selector exists with
`release_intent=final_candidate`, the same script must invoke final mode with
`-selector docs/release-evidence/ga-release-selector.json`.

Final mode rejects required/default `seed_gap_*_open` markers unless an exact
implemented or closed replacement exists. It also emits a machine finding with a
nonzero final-mode exit for an optional gap once the authoritative selector
declares the corresponding optional capability. The default GA selector uses
`claimed_optional_capabilities=[]`, so the remaining workload, purge, template,
and optional fixture gaps are unselected optional/future gaps and do not block
final. Once the selector claims an optional capability, the matching selected
optional gap hard-fails final mode with a machine finding and nonzero exit. A
direct `-mode final -check-only` run is structural only and cannot declare final
acceptance. Manual acceptance, role-based approval, generated-client approval,
owner approval, runbook meetings, sibling project status, and first-consumer
adoption are not GA gate conditions.

`docs/release-evidence/ga-manifest.json` is the repo-local machine-readable
index for release evidence. It refines this ledger into enumerated evidence
items with types, commands, capability IDs, and source anchors, and is executed
by the same `scripts/verify-ga-release.sh` selector-driven gate. In
seed/convergence mode it remains a ledger over already-existing checks; in final
mode it is evaluated with the authoritative selector, digest checks, required
evidence execution, and selected optional capability rules.

Optional capability positive evidence becomes a final blocker only when its
manifest entry explicitly declares fixture conformance with
`evidence_profile=repo-local-fixture-enabled`, `fixture_enabled_mode=true`,
`default_mode=false`, `optional_gated=true`, and `required=true`. A plain
`seed_gap_*_open` marker records missing optional fixture coverage; it does not
by itself make the optional capability part of default GA.

AFSCP GA gates are internal to the shared filesystem control plane. Reference
consumer adoption notes can inform compatibility work, but no first consumer or
sibling repository acceptance is an AFSCP gate or release blocker.

This ledger records the repo-local evidence that `scripts/verify-ga-release.sh`
is expected to cover. Owner roles identify maintenance responsibility only.

Status values:

- `auto_verified`: repo-local scripts, tests, contracts, schemas, OpenAPI, or
  docs provide objective evidence covered by `scripts/verify-ga-release.sh`

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
non-terminal runtime request recovery, and explicit-gated terminal export session
reconcile now exist. Runtime request rows are a dedicated gateway ledger rather
than per-request operation rows. Current
implementation evidence also includes
repo lifecycle workers, save/restore flows, namespace-scoped template
create/clone, workload mount issuance and orchestrator plans, writer-session
fences with shared repo-row serialization against read-write session admission,
an explicit workload mount stale-lease scan, and run-once worker execution
bounded by `AFSCP_WORKER_RUN_ONCE_TIMEOUT` so stuck JVS-backed mutations converge
through the existing durable failure and writer-fence release paths instead of
leaving same-repo mutation gates non-terminal indefinitely. These artifacts are current
release evidence only when covered by repo-local verification from
`bash scripts/verify-ga-release.sh`; manual acceptance or approval does not
constitute a gate.

## Gate Ledger

| Gate ID | Area | Status | Owner Role | Automated Evidence/Check | Decision | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| G-001 | Runtime ADR | auto_verified | AFSCP maintainer | `scripts/verify-ga-release.sh`, `docs/adr/0005-runtime-and-service-shape.md`, `go test -count=1 ./...` | Go runtime baseline is repo-local and testable | Owner maintains ADR and test command alignment |
| G-002 | Service auth and caller roles | auto_verified | AFSCP maintainer | `scripts/verify-ga-release.sh`, `docs/adr/0006-service-auth-and-roles.md`, `docs/contracts/afscp-internal-api-v1.md`, auth and route tests | Canonical service principal, namespace roles, and admin/orchestrator boundaries are contract-checked | New or breaking auth behavior must add repo-local tests |
| G-003 | Schemas and OpenAPI | auto_verified | AFSCP maintainer | `scripts/verify-ga-release.sh`, `api/schemas/afscp-internal-v1.schema.json`, `api/openapi/internal-v1.openapi.yaml`, `cmd/afscp-contract-verify` | Schema/OpenAPI parity is machine checked | Generated-client compatibility is represented by schema/OpenAPI stability checks |
| G-004 | Standard envelopes and stable errors | auto_verified | AFSCP maintainer | `scripts/verify-ga-release.sh`, `api/schemas/afscp-internal-v1.schema.json`, `docs/API_CONTRACT_DRAFT.md`, contract verifier and API tests | Operation/error envelopes and stable error families are repo-local contracts | Error changes require updated tests and artifacts |
| G-005 | JVS runner pin and direct contract evidence | auto_verified | AFSCP maintainer | `scripts/verify-ga-release.sh`, `docs/adr/0009-jvs-runner-pin.md`, `docs/JVS_AFSCP_DIRECT_RELEASE_EVIDENCE_2026-05-18.md`, `docs/contracts/jvs-runner-contract-v1.md`, JVS runner tests | Published direct-capable JVS release pin, JVS binary artifact SHA-256, source ref, and `jvs afscp` command expectations are recorded | Historical v0.4.8/v0.4.9/local evidence is retained as context only |
| G-006 | Path resolver contract and corpus | auto_verified | AFSCP maintainer | `scripts/verify-ga-release.sh`, `docs/adr/0012-path-resolver-and-fences.md`, `docs/contracts/repo-path-contract-v1.md`, `internal/pathresolver/pathresolver_test.go`, `internal/pathresolver/testcorpus/corpus.go` | Resolver grammar, traversal denial, `.jvs` denial, and corpus behavior are test-covered | Security semantics are encoded as tests and contracts |
| G-007 | WebDAV export contract | auto_verified | AFSCP maintainer | `scripts/verify-ga-release.sh`, `docs/adr/0010-webdav-export-gateway.md`, `docs/contracts/export-access-webdav-v1.md`, export gateway/session/reconcile tests | AFSCP-controlled WebDAV gateway and runtime request ledger behavior are testable repo-local scope | Stock `juicefs webdav` is not the GA policy boundary |
| G-008 | Workload orchestrator contract | auto_verified | AFSCP maintainer | `scripts/verify-ga-release.sh`, `docs/adr/0011-workload-orchestrator-contract.md`, `docs/contracts/workload-mount-binding-v1.md`, workload mount tests | Payload-only mount plans, Secret boundaries, heartbeat/release/revoke semantics are contract-covered | Runtime operator integration is not a sibling-project gate |
| G-009 | Writer-session fence contract | auto_verified | AFSCP maintainer | `scripts/verify-ga-release.sh`, `docs/adr/0012-path-resolver-and-fences.md`, `docs/contracts/operation-state-machine-v1.md`, `internal/fences`, session/fence/store tests | Restore/template writer fences and RW export/workload admission share repo-row serialization | Race coverage must remain in repo-local tests |
| G-010A | Repo lifecycle state and caller mapping | auto_verified | AFSCP maintainer | `scripts/verify-ga-release.sh`, `docs/adr/0008-repo-lifecycle-policy.md`, `docs/contracts/repo-lifecycle-v1.md`, repo lifecycle tests | Generic archive/delete/restore/purge lifecycle mapping is contract-covered | Caller product vocabulary stays outside AFSCP |
| G-010B | Repo lifecycle fence and session drain | auto_verified | AFSCP maintainer | `scripts/verify-ga-release.sh`, `docs/contracts/repo-lifecycle-v1.md`, `docs/contracts/operation-state-machine-v1.md`, export reconcile and workload stale-lease tests | Lifecycle drain and uncertain-session fail-closed behavior are testable | Operator intervention remains runtime safety behavior |
| G-010C | Repo retention and purge authorization | auto_verified | AFSCP maintainer | `scripts/verify-ga-release.sh`, `docs/adr/0008-repo-lifecycle-policy.md`, `api/schemas/afscp-internal-v1.schema.json`, purge/retention tests | Retention and caller approval-reference requirements are schema and test guarded | Caller approval reference is product safety data, not GA approval workflow |
| G-010D | Repo lifecycle recovery and runbooks | auto_verified | AFSCP maintainer | `scripts/verify-ga-release.sh`, `docs/contracts/repo-lifecycle-v1.md`, `docs/OPERATIONAL_READINESS.md`, `docs/runbooks/ga-runbooks.md`, recovery tests | Recovery phases and operator actions are documented and covered by repo-local checks | Runbooks are artifacts; meetings are not gates |
| G-010E | Repo lifecycle audit and redaction | auto_verified | AFSCP maintainer | `scripts/verify-ga-release.sh`, `docs/OPERATIONS_AND_AUDIT.md`, `docs/contracts/repo-lifecycle-v1.md`, `internal/audit/event_test.go` | Lifecycle audit taxonomy and redaction guardrails are test-covered | HTTP JSON audit delivery is the GA sink scope |
| G-011 | Operation recovery and audit | auto_verified | AFSCP maintainer | `scripts/verify-ga-release.sh`, `docs/adr/0007-operation-store-and-audit-outbox.md`, `docs/contracts/operation-state-machine-v1.md`, `internal/operations`, `internal/audit`, `internal/inspection`, `internal/store/postgres` | Operation store, leases, recovery classification, and audit outbox behavior are covered by tests | External sinks dedupe by `audit_event_id` as a deployment contract |
| G-012 | Namespace disable and policy-change behavior | auto_verified | AFSCP maintainer | `scripts/verify-ga-release.sh`, `docs/contracts/namespace-volume-binding-v1.md`, `internal/resources`, `internal/store/postgres`, namespace policy tests | Namespace disable and mutation/session policy gates are test-covered | Existing session handling remains explicit operator/runtime policy |
| G-013 | Required runbooks | auto_verified | Operations owner | `scripts/verify-ga-release.sh`, `docs/runbooks/README.md`, `docs/runbooks/ga-runbooks.md`, `docs/OPERATIONAL_READINESS.md`, doc guard checks | Required runbook artifacts are repo-local evidence | Drill meetings are not GA gates |
| G-014 | Observability and alerting | auto_verified | Operations owner | `scripts/verify-ga-release.sh`, `docs/OPERATIONAL_READINESS.md`, observability tests/docs | Alert classes and readiness profile semantics are documented and testable | Deployment-specific numeric thresholds are config, not subjective gate approval |
| G-015 | Backup and restore plan | auto_verified | Operations owner | `scripts/verify-ga-release.sh`, `docs/OPERATIONAL_READINESS.md`, recovery/runbook docs and tests | Backup/restore scope and idempotent replay expectations are repo-local artifacts | Deployment backup execution remains operational responsibility |
| G-016 | Secret redaction review | auto_verified | Security owner | `scripts/verify-ga-release.sh`, `docs/contracts/operation-state-machine-v1.md`, `docs/OPERATIONAL_READINESS.md`, `internal/audit/event_test.go`, secret redaction tests | Forbidden secret-bearing surfaces are guarded by tests and docs | Redaction is enforced by automated checks |

## Risk Decision Ledger

Risk decisions are summarized in `docs/RISK_REGISTER.md`. A GA-blocking risk is
closed for release only when repo-local automated evidence covers the mitigation
and `scripts/verify-ga-release.sh` passes.
