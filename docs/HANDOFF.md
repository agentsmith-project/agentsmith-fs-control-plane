# Handoff

This repository is the handoff package for building AFSCP, a product-agnostic file storage control plane.

AFSCP runs, evolves, releases, and passes gates independently from consumer
applications. Reference consumers may provide functional requirements and
compatibility feedback, but they cannot become an AFSCP gate or release
dependency.

AFSCP should manage volumes, namespaces, repos, repo lifecycle, repo templates, exports, workload mount bindings, orchestrator mount plans, durable operations, logs, and audit events. It should not understand product workflows from any one caller.

## Source Of Truth

Current active source of truth is the root-level documentation in this repository, especially `docs/DEVELOPER_HANDOFF.md`, `docs/USER_GUIDE.md`, `docs/DEVELOPER_GUIDE.md`, `docs/PRE_DEV_COMPLETION.md`, `docs/GA_PRE_DEV_READINESS.md`, `docs/DEVELOPMENT_GOVERNANCE.md`, `docs/RISK_REGISTER.md`, `docs/READINESS_EVIDENCE.md`, `docs/HANDOFF.md`, `docs/PRODUCT_REQUIREMENTS.md`, `docs/ARCHITECTURE.md`, `docs/STORAGE_LAYOUT.md`, `docs/API_CONTRACT_DRAFT.md`, `api/openapi/internal-v1.openapi.yaml`, `api/schemas/afscp-internal-v1.schema.json`, and `docs/contracts/`.

Historical review and research documents may still use P0, P1, or MVP
language. For active planning, read those terms through the GA implementation
baseline and readiness evidence documents.

Historical consumer-named planning snapshots and local sibling checkout paths
are intentionally not part of the current repository. Active AFSCP planning is
the product-neutral documentation set listed above.

## GA Implementation Baseline Rules

1. ADR 0005 records the runtime language/framework; any new runtime decision
   needs ADR review before it changes the baseline.
2. The handoff has entered the GA implementation baseline. Implementation may
   continue against accepted package layout, health endpoint, config loading,
   logging, test harness, generated contract plumbing, route registration, and
   handler/operation contracts.
3. Keep internal auth, caller-service authorization, schemas, OpenAPI, JVS runner, operation/audit, WebDAV, workload mount, writer-session fence, and namespace policy changes aligned with versioned contracts. Contract-breaking changes need repo-local evidence before implementation proceeds.
4. FINAL GA remains governed by `docs/GA_RELEASE_GATES.md`,
   `docs/READINESS_EVIDENCE.md`, and `scripts/verify-ga-release.sh`.

## Revised Boundary

AFSCP is a functional storage substrate.

AFSCP should know:

- volumes
- namespaces
- repos
- repo lifecycle
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

- caller job or task object
- caller catalog object
- project
- caller workspace or tenant vocabulary
- template catalog UX
- user-facing product permissions
- business workflow state

Calling products map their own concepts to AFSCP primitives outside this repo.
Generic consumer adoption guidance lives in `docs/INTEGRATION_GUIDE.md`; AFSCP
core contracts stay neutral.

## Module Shape

Build AFSCP as an independent application module in this repository.

GA deployment shape:

- One container image.
- One Kubernetes Deployment and Service.
- Dedicated ServiceAccount.
- Dedicated Secret access for JuiceFS root credentials.
- Persistent operation store.
- Internal API reachable by trusted application control planes, privileged admin jobs, migration jobs, operator tools, and the dedicated orchestrator service only.

Integration adapters and compatibility changes may land in consumer-owned
repositories. The AFSCP runtime, operation store, path resolver, JVS runner,
and export gateway should live here.

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
- repo archive, restore, delete, tombstone, and purge lifecycle
- JVS `init` plus direct `save/list/restore/clone/status/doctor` execution
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

## GA Must Not Expand Into

- product workflow engine
- product authorization service
- caller job or task lifecycle
- caller catalog
- global template marketplace
- Git remote workflows
- merge/conflict resolution
- real-time collaborative editing
- per-file ACL UI
- raw JuiceFS direct mount for ordinary users
- product display-name rename or catalog detach APIs inside AFSCP
- namespace delete APIs

## First Engineering Checkpoints

1. Use the existing Go service skeleton and `docs/DEVELOPER_HANDOFF.md` as the
   implementation baseline. `docs/USER_GUIDE.md` is the current entrypoint for
   users, integrators, and operators; `docs/DEVELOPER_GUIDE.md` is the current
   entrypoint for coding, testing, contract, and gate work.
2. Continue or modify storage mutation handlers only through versioned contracts,
   gate evidence, and focused tests; baseline
   implementation does not by itself close the relevant gate in
   `docs/READINESS_EVIDENCE.md`.
3. Treat G-005 as covered for pre-GA by the current local direct JVS pin and
   runner contract evidence in
   `docs/JVS_AFSCP_DIRECT_LOCAL_EVIDENCE_2026-05-18.md`;
   repo/JVS/storage handlers still require the other contracts, fences,
   session drain, operation leases, audit behavior, and focused tests.
4. Keep generated clients and handlers aligned with `api/openapi/internal-v1.openapi.yaml` and `api/schemas/afscp-internal-v1.schema.json`.
5. Keep GA-blocking risks in `docs/RISK_REGISTER.md` covered by repo-local automated evidence.
