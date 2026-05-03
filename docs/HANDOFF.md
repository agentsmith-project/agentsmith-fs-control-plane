# Handoff

This repository is the handoff package for building AFSCP, a product-agnostic file storage control plane.

AFSCP should manage volumes, namespaces, repos, repo templates, exports, workload mount bindings, orchestrator mount plans, durable operations, logs, and audit events. It should not understand product workflows from any one caller.

## Source Of Truth

Current handoff source of truth is the root-level documentation in this repository, especially `docs/HANDOFF.md`, `docs/PRODUCT_REQUIREMENTS.md`, `docs/ARCHITECTURE.md`, `docs/STORAGE_LAYOUT.md`, `docs/API_CONTRACT_DRAFT.md`, and `docs/contracts/`.

The planning work that produced this handoff was committed in:

- Local planning repo: `/home/percy/works/mbos-v1/improve-agentsmith-fs`
- Planning commit: `9a3e127 docs: plan AgentSmith workspace storage control plane`
- Research snapshot: `docs/research/agentsmith-workspace-storage-technical-design.md`
- Discussion scratchpad: `docs/research/scratch.md`

Important: do not use `agentsmith-oss` for current-state analysis. It is an old version and was explicitly excluded.

## Day 1 Sequence

1. Read `docs/TEAM_REVIEW_2026-05-03.md` and confirm the P0 gates are still accepted.
2. Record the runtime language/framework in a new ADR.
3. Start only the neutral service skeleton: package layout, health endpoint, config loading, logging, test harness, and empty route registration.
4. Freeze internal auth and caller-service authorization.
5. Convert `docs/API_CONTRACT_DRAFT.md` into JSON schemas and the P0 internal OpenAPI.
6. Freeze the `.jvs` mount protection strategy with orchestrator owners.
7. Freeze operation store schema, writer-session fence, and recovery matrix before implementing storage handlers.

## Revised Boundary

AFSCP is a functional storage substrate.

AFSCP should know:

- volumes
- namespaces
- repos
- repo templates
- exports
- workload mount bindings
- orchestrator mount plans
- JVS operations
- path resolution
- quota hooks
- operation state
- logs and audit events

AFSCP should not know:

- notebook task
- file library
- project
- AgentSmith workspace
- template catalog UX
- user-facing product permissions
- business workflow state

Calling products map their own concepts to AFSCP primitives. Product-specific examples belong in `docs/INTEGRATION_GUIDE.md`, not in AFSCP core contracts.

## Module Shape

Build AFSCP as an independent application module in this repository.

MVP deployment shape:

- One container image.
- One Kubernetes Deployment and Service.
- Dedicated ServiceAccount.
- Dedicated Secret access for JuiceFS root credentials.
- Persistent operation store.
- Internal API reachable by trusted application control planes, privileged admin jobs, migration jobs, operator tools, and the dedicated orchestrator service only.

Integration adapters and compatibility changes may land in sibling repositories. The AFSCP runtime, operation store, path resolver, JVS runner, and export gateway should live here.

## Authority Boundaries

Calling application owns:

- end-user authentication and product authorization
- product catalog records
- product workflow decisions
- product-level audit projection
- user-visible UI and API vocabulary
- mapping business resources to AFSCP namespaces/repos/templates

AFSCP owns:

- volume credentials and health
- namespace boundaries
- caller-service authorization for namespace-bound storage operations
- repo path allocation and path resolution
- JVS `init`, `save`, `history`, `restore`, and `repo clone` execution
- repo template storage and clone execution
- WebDAV/export runtime and short-lived export credentials
- workload mount binding generation
- orchestrator-only mount plan generation
- operation journal, idempotency, retries, logs, and low-level audit events

External orchestrator owns:

- Kubernetes Secret, PV, PVC, and Pod mount execution, or equivalent runtime mounting.
- Workload binding status.
- No product permission decisions.

Client/desktop connector owns:

- Consuming application-issued export access.
- Local mount UX and diagnostics.
- No raw JuiceFS credential handling for ordinary users.

## MVP Must Not Expand Into

- product workflow engine
- product authorization service
- notebook task lifecycle
- file-library catalog
- global template marketplace
- Git remote workflows
- merge/conflict resolution
- real-time collaborative editing
- per-file ACL UI
- raw JuiceFS direct mount for ordinary users
- repo archive/delete/rename/detach lifecycle APIs in P0

## First Engineering Checkpoints

1. Confirm runtime language and framework in an ADR.
2. Finalize generic volume, namespace, repo, template, export, mount binding, and orchestrator plan contracts.
3. Finalize internal service auth, caller identity, and namespace caller authorization.
4. Generate P0 OpenAPI before implementing handlers.
5. Finalize operation store schema, writer-session fence, and recovery matrix.
6. Finalize `.jvs` protection strategy before enabling exports or workload mounts.
7. Confirm first-consumer mapping in `docs/INTEGRATION_GUIDE.md` without moving those concepts into core AFSCP.
