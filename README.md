# AFSCP Storage Control Plane

Status: handoff scaffold, no implementation yet.

`agentsmith-fs-control-plane` hosts AFSCP: a product-agnostic file storage control plane for managed volumes, JVS repos, repo templates, exports, workload mounts, and durable storage operations.

AgentSmith is expected to be the first consumer, but AFSCP itself should not know about any caller's business objects. Product concepts belong in the calling product and integration guide. AFSCP should expose robust functional primitives and enforce storage boundaries.

## Current Decision

- Build AFSCP as an independent storage control plane and deployment.
- This repository is the implementation home for the AFSCP runtime.
- Deploy AFSCP as an independent container with its own Kubernetes Deployment, Service, ServiceAccount, Secrets, and operation store.
- Calling applications remain responsible for product authorization, catalog UX, and business workflows.
- AFSCP owns volume credentials, namespace boundaries, repo path allocation, JVS execution, repo template clone, WebDAV/export runtime, workload mount bindings, orchestrator mount plans, operations, logs, and audit events.
- New repos should use a managed shared JuiceFS-backed volume by default, with room for future volume sharding by tenant, region, compliance profile, or isolation class.
- Do not expose JuiceFS metadata URLs, bucket credentials, access keys, or secret keys to ordinary clients or workloads.
- Do not implement product-specific workflow concepts in AFSCP.
- Do not implement version merge or ordinary single-writer enforcement.

## Core Model

- `Volume`: managed backing filesystem/storage pool, initially JuiceFS.
- `Namespace`: storage isolation and policy boundary inside a managed volume.
- `Repo`: JVS-managed filesystem root inside a namespace.
- `RepoTemplate`: namespace-scoped clone source managed by AFSCP.
- `Export`: short-lived user/client access, initially WebDAV.
- `WorkloadMountBinding`: caller-visible mount authorization and lifecycle record.
- `OrchestratorMountPlan`: privileged mount assembly plan for the external orchestrator.
- `Operation`: durable record for mutating storage actions.

## Repository Contents

- [docs/HANDOFF.md](docs/HANDOFF.md): start here.
- [docs/TECHNICAL_FEASIBILITY_REVIEW_2026-05-03.md](docs/TECHNICAL_FEASIBILITY_REVIEW_2026-05-03.md): current technical blocker and feasibility review.
- [docs/TEAM_REVIEW_2026-05-03.md](docs/TEAM_REVIEW_2026-05-03.md): product/architecture/security review closure.
- [docs/DECOUPLING_REVIEW.md](docs/DECOUPLING_REVIEW.md): decoupling analysis and revised boundary.
- [docs/PRODUCT_REQUIREMENTS.md](docs/PRODUCT_REQUIREMENTS.md): product-agnostic requirements.
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md): component boundaries and target architecture.
- [docs/API_CONTRACT_DRAFT.md](docs/API_CONTRACT_DRAFT.md): draft internal API and data contracts.
- [docs/INTEGRATION_GUIDE.md](docs/INTEGRATION_GUIDE.md): AgentSmith integration mapping, kept outside the core model.
- [docs/SECURITY_AND_TENANCY.md](docs/SECURITY_AND_TENANCY.md): credential, namespace, path, export, and `.jvs` protection model.
- [docs/MVP_PLAN.md](docs/MVP_PLAN.md): delivery plan and backlog guardrails.
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

This repository intentionally has no application code yet. The first development milestone should create the runtime skeleton only after the team confirms the generic API contracts, namespace/volume model, operation store, and deployment shape described in the handoff docs.

## License

Apache License 2.0.
