# AFSCP Storage Control Plane

Status: GA implementation baseline; the pre-development handoff is complete,
and implementation is proceeding from accepted contracts.

This repository hosts AFSCP: an independent shared filesystem control plane for
managed volumes, JVS repos, repo lifecycle, repo templates, exports, workload
mounts, and durable storage operations.

AFSCP itself should not know about any caller's business objects. Product
concepts belong in caller-owned integration code or external adoption notes.
Those notes may be useful compatibility references, but they are not the AFSCP
core model or release gate. AFSCP exposes robust filesystem primitives and
enforces storage boundaries.

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
- [docs/JVS_SMOKE_EVIDENCE_2026-05-05-v0.4.8.md](docs/JVS_SMOKE_EVIDENCE_2026-05-05-v0.4.8.md): accepted JVS v0.4.8 smoke evidence for G-005.
- [docs/JVS_SMOKE_EVIDENCE_2026-05-05.md](docs/JVS_SMOKE_EVIDENCE_2026-05-05.md): historical JVS v0.4.7 blocker evidence.
- [docs/TECHNICAL_FEASIBILITY_REVIEW_2026-05-03.md](docs/TECHNICAL_FEASIBILITY_REVIEW_2026-05-03.md): historical feasibility review with 2026-05-04 JVS external-control resolution update.
- [docs/TEAM_REVIEW_2026-05-03.md](docs/TEAM_REVIEW_2026-05-03.md): historical product/architecture/security review closure.
- [docs/DECOUPLING_REVIEW.md](docs/DECOUPLING_REVIEW.md): decoupling analysis and revised boundary.
- [docs/PRODUCT_REQUIREMENTS.md](docs/PRODUCT_REQUIREMENTS.md): product-agnostic requirements.
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md): component boundaries and target architecture.
- [docs/API_CONTRACT_DRAFT.md](docs/API_CONTRACT_DRAFT.md): draft internal API and data contracts.
- [docs/INTEGRATION_GUIDE.md](docs/INTEGRATION_GUIDE.md): external adoption notes, kept outside the core model and release gates.
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
- [docs/REFERENCES.md](docs/REFERENCES.md): local source paths and external references.
- [docs/adr/](docs/adr): architecture decision records.
- [docs/research/](docs/research): historical planning material.

## Implementation Rule

Pre-dev handoff has entered the GA implementation baseline. Core handlers and
operations are being implemented incrementally against accepted schemas,
OpenAPI, auth, JVS, operation/audit, export, mount, and writer-session
contracts. New or modified handlers and storage mutations must still pass
through the corresponding gates, evidence, fences, operation leases, audit
behavior, and focused tests. Do not claim final production GA from this
baseline.

## License

Apache License 2.0.
