# AFSCP Storage Control Plane

Status: GA implementation baseline; the pre-development handoff is complete,
and implementation is proceeding from accepted contracts.

This repository hosts AFSCP: an independent shared filesystem control plane for
managed volumes, JVS repos, repo lifecycle, repo templates, exports, workload
mounts, and durable storage operations. AFSCP runs, evolves, releases, and
passes gates independently of any reference consumer.

AFSCP itself should not know about any caller's business objects. This repo may
keep generic adoption guidance, but caller-specific adoption or handoff material
belongs outside this repo in consumer-owned repositories. Those materials are
not the AFSCP core model, release gate, or source of business logic. AFSCP
exposes robust filesystem primitives and enforces storage boundaries.

## GA Release Gate

AFSCP current release readiness is checked by the selector-driven GA release
gate:

```bash
bash scripts/verify-ga-release.sh
```

`bash scripts/verify-ga-release.sh` is the single authoritative GA release
entrypoint. The script reads
`docs/release-evidence/ga-release-selector.json`; in this repo that selector
currently has `release_intent=final_candidate`, so the script runs final mode
with `-selector docs/release-evidence/ga-release-selector.json`. Exit code `0`
means the selected repo-local GA release evidence passed; any nonzero exit code
means the selector-driven GA release gate failed.

The current selector has `claimed_optional_capabilities=[]`. That means
unselected optional/future gaps do not block default GA; final mode still rejects
unresolved required/default gaps and selected optional positive gaps.

## Current Decision

- Build AFSCP as an independent storage control plane and deployment.
- This repository is the implementation home for the AFSCP runtime.
- Deploy AFSCP as an independent container with its own Kubernetes Deployment, Service, ServiceAccount, Secrets, and operation store.
- Calling applications remain responsible for product authorization, catalog UX, and business workflows.
- AFSCP owns volume credentials, namespace boundaries, repo path allocation and lifecycle, JVS execution, repo template clone, WebDAV/export runtime, workload mount bindings, orchestrator mount plans, operations, logs, and audit events.
- New repos should use a managed shared JuiceFS-backed volume by default, with room for future volume sharding by tenant, region, compliance profile, or isolation class.
- Do not expose JuiceFS metadata URLs, bucket credentials, access keys, or secret keys to ordinary clients or workloads.
- Do not implement product-specific workflow concepts in AFSCP.
- Do not implement version merge or ordinary single-writer enforcement.

## Core Model

- `Volume`: managed backing filesystem/storage pool, initially JuiceFS.
- `Namespace`: storage isolation and policy boundary inside a managed volume.
- `Repo`: JVS-managed filesystem root inside a namespace.
- `RepoLifecycle`: archive, restore, delete, tombstone, and purge state for a repo.
- `SavePoint`: JVS-managed version marker for a repo payload.
- `RepoTemplate`: namespace-scoped clone source managed by AFSCP.
- `Export`: short-lived user/client access, initially WebDAV.
- `WorkloadMountBinding`: caller-visible mount authorization and lifecycle record.
- `OrchestratorMountPlan`: privileged mount assembly plan for the external orchestrator.
- `Operation`: durable record for mutating storage actions.

## Repository Contents

- [docs/HANDOFF.md](docs/HANDOFF.md): start here.
- [docs/GA_PRE_DEV_READINESS.md](docs/GA_PRE_DEV_READINESS.md): GA implementation baseline readiness source of truth.
- [docs/DEVELOPMENT_GOVERNANCE.md](docs/DEVELOPMENT_GOVERNANCE.md): review, contract, ADR, and risk governance.
- [docs/RISK_REGISTER.md](docs/RISK_REGISTER.md): live GA readiness risk register.
- [docs/READINESS_EVIDENCE.md](docs/READINESS_EVIDENCE.md): GA gate evidence ledger.
- [docs/PRE_DEV_COMPLETION.md](docs/PRE_DEV_COMPLETION.md): pre-dev artifact completion package and current gate status.
- [docs/DEVELOPER_HANDOFF.md](docs/DEVELOPER_HANDOFF.md): coding team entrypoint.
- [docs/USER_GUIDE.md](docs/USER_GUIDE.md): guide for users, integrators, and operators.
- [docs/DEVELOPER_GUIDE.md](docs/DEVELOPER_GUIDE.md): guide for coding, testing, contracts, and gates.
- [docs/JVS_AFSCP_DIRECT_RELEASE_EVIDENCE_2026-05-18.md](docs/JVS_AFSCP_DIRECT_RELEASE_EVIDENCE_2026-05-18.md): current published JVS v0.4.10 release pin and direct runner contract evidence for G-005.
- [docs/JVS_PIN_EVIDENCE_2026-05-12-v0.4.9.md](docs/JVS_PIN_EVIDENCE_2026-05-12-v0.4.9.md): historical JVS v0.4.9 release evidence.
- [docs/JVS_SMOKE_EVIDENCE_2026-05-05-v0.4.8.md](docs/JVS_SMOKE_EVIDENCE_2026-05-05-v0.4.8.md): historical JVS v0.4.8 smoke evidence.
- [docs/JVS_SMOKE_EVIDENCE_2026-05-05.md](docs/JVS_SMOKE_EVIDENCE_2026-05-05.md): historical JVS v0.4.7 blocker evidence.
- [docs/TECHNICAL_FEASIBILITY_REVIEW_2026-05-03.md](docs/TECHNICAL_FEASIBILITY_REVIEW_2026-05-03.md): historical feasibility review with 2026-05-04 JVS external-control resolution update.
- [docs/TEAM_REVIEW_2026-05-03.md](docs/TEAM_REVIEW_2026-05-03.md): historical product/architecture/security review closure.
- [docs/DECOUPLING_REVIEW.md](docs/DECOUPLING_REVIEW.md): decoupling analysis and revised boundary.
- [docs/PRODUCT_REQUIREMENTS.md](docs/PRODUCT_REQUIREMENTS.md): product-agnostic requirements.
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md): component boundaries and target architecture.
- [docs/API_CONTRACT_DRAFT.md](docs/API_CONTRACT_DRAFT.md): draft internal API and data contracts.
- [docs/INTEGRATION_GUIDE.md](docs/INTEGRATION_GUIDE.md): generic adoption guidance; caller-specific adoption and handoff material belongs outside this repo in consumer-owned repositories.
- [docs/SECURITY_AND_TENANCY.md](docs/SECURITY_AND_TENANCY.md): credential, namespace, path, export, and JVS control metadata protection model.
- [docs/MVP_PLAN.md](docs/MVP_PLAN.md): historical delivery plan; use the GA readiness document for active scope.
- [docs/OPERATIONS_AND_MIGRATION.md](docs/OPERATIONS_AND_MIGRATION.md): operations, migration, and rollout strategy.
- [docs/PRODUCT_BOUNDARY.md](docs/PRODUCT_BOUNDARY.md): hard product boundaries and non-goals.
- [docs/STORAGE_LAYOUT.md](docs/STORAGE_LAYOUT.md): shared JuiceFS path model and resolver rules.
- [docs/JVS_INTEGRATION.md](docs/JVS_INTEGRATION.md): JVS executor contract and command expectations.
- [docs/WORKLOAD_MOUNTS.md](docs/WORKLOAD_MOUNTS.md): generic workload mount model.
- [docs/EXPORT_WEBDAV.md](docs/EXPORT_WEBDAV.md): WebDAV export model.
- [docs/contracts/](docs/contracts): focused contract documents for partner teams.
- [docs/runbooks/](docs/runbooks): initial operator and local handoff notes.
- [docs/REFERENCES.md](docs/REFERENCES.md): product-neutral external references.
- [docs/adr/](docs/adr): architecture decision records.
- [docs/research/](docs/research): placeholder for product-neutral research notes.

## Implementation Rule

Pre-dev handoff has entered the current GA implementation baseline. Core
handlers and operations are being implemented incrementally against accepted
schemas, OpenAPI, auth, JVS, operation/audit, export, mount, and writer-session
contracts. New or modified handlers and storage mutations must keep repo-local
release evidence, fences, operation leases, audit behavior, and focused tests
aligned with the selector-driven GA release gate; the authoritative decision is
the exit code from `bash scripts/verify-ga-release.sh`.

## License

Apache License 2.0.
