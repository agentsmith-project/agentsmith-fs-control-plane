# AgentSmith FS Control Plane

Status: handoff scaffold, no implementation yet.

`agentsmith-fs-control-plane` (AFSCP) is the proposed internal storage control plane for AgentSmith workspace file libraries. It is intended to manage a shared JuiceFS storage pool, run JVS operations, provide controlled exports to users, and generate sandbox mount specifications without exposing JuiceFS root credentials to users, desktops, or agent workloads.

This repository is initialized for the development team that will implement the module. It contains the product and architecture handoff, decision records, draft API contracts, integration notes, security boundaries, and MVP plan.

## Current Decision

- Build AFSCP as a new independent application module and deployment.
- MVP may live in the existing AgentSmith/mbos workspace as an independent package or service directory, but this repository is the handoff target for the standalone project.
- Deploy AFSCP as an independent container with its own Kubernetes Deployment, Service, ServiceAccount, Secrets, and operation store.
- AgentSmith API remains the product and permission authority.
- AFSCP owns JuiceFS root credentials, repo path allocation, JVS execution, WebDAV export runtime, and sandbox mount specs.
- New file libraries should use a default shared JuiceFS filesystem/storage pool, with room for future workspace, tenant, region, or compliance sharding.
- Do not expose JuiceFS metadata URLs, bucket credentials, access keys, or secret keys to ordinary users, Desktop, or agent workloads.
- Do not implement cross-workspace template sharing or template clone.
- Do not implement version merge or ordinary single-writer enforcement.

## Repository Contents

- [docs/HANDOFF.md](docs/HANDOFF.md): start here.
- [docs/PRODUCT_REQUIREMENTS.md](docs/PRODUCT_REQUIREMENTS.md): product scope, non-goals, MVP, acceptance criteria.
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md): component boundaries and target architecture.
- [docs/API_CONTRACT_DRAFT.md](docs/API_CONTRACT_DRAFT.md): draft internal API and data contracts.
- [docs/INTEGRATION_GUIDE.md](docs/INTEGRATION_GUIDE.md): AgentSmith, Desktop, sandbox-manager, and JVS integration notes.
- [docs/SECURITY_AND_TENANCY.md](docs/SECURITY_AND_TENANCY.md): credential, tenancy, path, export, and `.jvs` protection model.
- [docs/MVP_PLAN.md](docs/MVP_PLAN.md): delivery plan and backlog guardrails.
- [docs/OPERATIONS_AND_MIGRATION.md](docs/OPERATIONS_AND_MIGRATION.md): operations, migration, and rollout strategy.
- [docs/PRODUCT_BOUNDARY.md](docs/PRODUCT_BOUNDARY.md): hard product boundaries and non-goals.
- [docs/STORAGE_LAYOUT.md](docs/STORAGE_LAYOUT.md): shared JuiceFS path model and resolver rules.
- [docs/JVS_INTEGRATION.md](docs/JVS_INTEGRATION.md): JVS executor contract and command expectations.
- [docs/SANDBOX_BINDING_V2.md](docs/SANDBOX_BINDING_V2.md): sandbox-manager v2 binding proposal.
- [docs/EXPORT_WEBDAV.md](docs/EXPORT_WEBDAV.md): Desktop/Web export model.
- [docs/contracts/](docs/contracts): focused contract documents for partner teams.
- [docs/runbooks/](docs/runbooks): initial operator and local handoff notes.
- [docs/REFERENCES.md](docs/REFERENCES.md): local source paths and external references.
- [docs/adr/](docs/adr): architecture decision records.
- [docs/research/](docs/research): copied planning material from the initial research repo.

## Implementation Rule

This repository intentionally has no application code yet. The first development milestone should create the runtime skeleton only after the team confirms the API contracts, storage profile schema, and deployment shape described in the handoff docs.

## License

Apache License 2.0.
