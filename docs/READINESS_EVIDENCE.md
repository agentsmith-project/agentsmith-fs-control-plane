# Readiness Evidence

Status: active evidence ledger.

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
model/tests, audit outbox pure model/tests, path resolver shared corpus, and
denied audit behavior in the neutral shell/AuthGate paths. Durable PostgreSQL
adapters, recovery loop, real endpoint handlers, JVS/WebDAV/mount/storage
mutation, and other external integrations remain unimplemented.

## Gate Ledger

| Gate ID | Area | Status | Owner Role | Reviewer Roles | Evidence Link | Decision | Review Date | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| G-001 | Runtime ADR | in_review | AFSCP maintainer | AFSCP maintainer, platform owner | `docs/adr/0005-runtime-and-service-shape.md` | Go runtime selected for handoff | 2026-05-05 | Human acceptance still required before merge |
| G-002 | Service auth and caller roles | in_review | AFSCP maintainer | Security owner, calling product owner | `docs/adr/0006-service-auth-and-roles.md`, `docs/contracts/afscp-internal-api-v1.md` | mTLS/service principal plus namespace roles | 2026-05-05 | Blocks endpoint handlers until accepted |
| G-003 | Schemas and OpenAPI | in_review | AFSCP maintainer | Calling product owner, operator/tooling owner | `api/schemas/afscp-internal-v1.schema.json`, `api/openapi/internal-v1.openapi.yaml` | machine contract parity pass exists | 2026-05-05 | Generated client review still required |
| G-004 | Standard envelopes and stable errors | in_review | AFSCP maintainer | Calling product owner | `api/schemas/afscp-internal-v1.schema.json`, `docs/API_CONTRACT_DRAFT.md` | operation/error envelope and stable error enum drafted | 2026-05-05 | Error code naming requires product acceptance |
| G-005 | JVS runner pin and smoke evidence | blocked | AFSCP maintainer | JVS owner | `docs/adr/0009-jvs-runner-pin.md`, `docs/JVS_SMOKE_EVIDENCE_2026-05-05.md` | JVS release binary pinned, smoke blocker found | 2026-05-05 | JVS team has indicated the next release will add the required capability, but AFSCP cannot close this gate or implement real storage mutation until a new GitHub release binary is pinned, re-smoked, and accepted as evidence |
| G-006 | Path resolver contract and corpus | in_review | AFSCP maintainer | Security owner | `docs/adr/0012-path-resolver-and-fences.md`, `docs/contracts/repo-path-contract-v1.md`, `internal/pathresolver/pathresolver.go`, `internal/pathresolver/pathresolver_test.go`, `internal/pathresolver/testcorpus/corpus.go` | resolver contract plumbing implemented; shared reusable corpus exists, awaiting review/acceptance | 2026-05-05 | Gate remains open until security review accepts corpus coverage |
| G-007 | WebDAV export contract | in_review | AFSCP maintainer | Security owner, client connector owner | `docs/adr/0010-webdav-export-gateway.md`, `docs/contracts/export-access-webdav-v1.md` | AFSCP-controlled gateway required | 2026-05-05 | Blocks export handlers until accepted |
| G-008 | Workload orchestrator contract | in_review | Orchestrator owner | AFSCP maintainer, security owner | `docs/adr/0011-workload-orchestrator-contract.md`, `docs/contracts/workload-mount-binding-v1.md` | two-layer mount contract drafted | 2026-05-05 | Requires orchestrator owner acceptance |
| G-009 | Writer-session fence contract | in_review | AFSCP maintainer | Operations owner, orchestrator owner, client connector owner | `docs/adr/0012-path-resolver-and-fences.md`, `docs/contracts/operation-state-machine-v1.md`, `internal/fences` | writer fence and lifecycle fence drafted; pure fence model/tests implemented | 2026-05-05 | DB adapter, handler integration, and recovery integration still pending review/implementation |
| G-010A | Repo lifecycle state and caller mapping | in_review | AFSCP maintainer | Product owner, calling product owner | `docs/adr/0008-repo-lifecycle-policy.md`, `docs/contracts/repo-lifecycle-v1.md`, `docs/INTEGRATION_GUIDE.md` | transition and catalog mapping drafted | 2026-05-05 | Product acceptance required |
| G-010B | Repo lifecycle fence and session drain | in_review | AFSCP maintainer | Operations owner, orchestrator owner, client connector owner | `docs/contracts/repo-lifecycle-v1.md`, `docs/contracts/operation-state-machine-v1.md` | read-only/read-write drain semantics drafted | 2026-05-05 | Requires export/orchestrator acceptance |
| G-010C | Repo retention and purge authorization | in_review | AFSCP maintainer | Product owner, security owner, operations owner | `docs/adr/0008-repo-lifecycle-policy.md`, `api/schemas/afscp-internal-v1.schema.json` | lifecycle policy and purge confirmation drafted | 2026-05-05 | Product/security acceptance required |
| G-010D | Repo lifecycle recovery and runbooks | in_review | AFSCP maintainer | Operations owner, security owner | `docs/contracts/repo-lifecycle-v1.md`, `docs/OPERATIONAL_READINESS.md`, `docs/runbooks/ga-runbooks.md` | recovery phases, runbooks, and drill expectations drafted | 2026-05-05 | Drill evidence still required before GA |
| G-010E | Repo lifecycle audit and redaction | in_review | AFSCP maintainer | Security owner, calling product owner | `docs/OPERATIONS_AND_AUDIT.md`, `docs/contracts/repo-lifecycle-v1.md` | lifecycle audit events and redaction rules drafted | 2026-05-05 | Redaction tests still required |
| G-011 | Operation recovery and audit | in_review | AFSCP maintainer | Operations owner, security owner | `docs/adr/0007-operation-store-and-audit-outbox.md`, `docs/contracts/operation-state-machine-v1.md`, `docs/OPERATIONAL_READINESS.md`, `migrations/0001_control_plane_persistence.sql`, `internal/store/migration_contract_test.go`, `internal/operations`, `internal/audit` | PostgreSQL operation store and outbox selected; migration contract, operation lease pure model/tests, and audit outbox pure model/tests exist | 2026-05-05 | Durable DB adapter and recovery loop still pending |
| G-012 | Namespace disable and policy-change behavior | in_review | AFSCP maintainer | Product owner, security owner | `docs/contracts/namespace-volume-binding-v1.md`, `docs/SECURITY_AND_TENANCY.md` | namespace disable semantics drafted | 2026-05-05 | Product/security acceptance required |
| G-013 | Required runbooks and drills | in_review | Operations owner | AFSCP maintainer, security owner | `docs/runbooks/README.md`, `docs/runbooks/ga-runbooks.md`, `docs/OPERATIONAL_READINESS.md` | runbook catalog, scenario runbooks, and drill evidence format drafted | 2026-05-05 | Drills still required before GA |
| G-014 | Observability and alerting | in_review | Operations owner | Platform owner, security owner | `docs/OPERATIONAL_READINESS.md` | alert classes and threshold requirements drafted | 2026-05-05 | Numeric SLO thresholds still deployment-dependent |
| G-015 | Backup and restore plan | in_review | Operations owner | Platform owner | `docs/OPERATIONAL_READINESS.md` | backup/restore scope and drill requirements drafted | 2026-05-05 | Drill evidence still required before GA |
| G-016 | Secret redaction review | in_review | Security owner | AFSCP maintainer, operations owner | `docs/SECURITY_AND_TENANCY.md`, `docs/contracts/operation-state-machine-v1.md`, `docs/OPERATIONAL_READINESS.md` | redaction surfaces documented | 2026-05-05 | Redaction tests still required |

## Risk Decision Ledger

Risk decisions are summarized in `docs/RISK_REGISTER.md`. If a risk is accepted
instead of closed, this ledger must link the approval artifact, expiration or
review date, compensation controls, and residual risk statement.
