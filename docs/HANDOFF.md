# Handoff

This repository is the handoff package for building the AgentSmith FS Control Plane, abbreviated AFSCP.

AFSCP is a new internal application module for AgentSmith. It should manage shared JuiceFS storage pools, execute JVS operations, expose controlled user exports, and generate sandbox mount specifications. It should not become a user-facing product surface.

## Source Of Truth

Current handoff source of truth is the root-level documentation in this repository, especially `docs/HANDOFF.md`, `docs/PRODUCT_REQUIREMENTS.md`, `docs/ARCHITECTURE.md`, `docs/STORAGE_LAYOUT.md`, `docs/API_CONTRACT_DRAFT.md`, and `docs/contracts/`.

The planning work that produced this handoff was committed in:

- Local planning repo: `/home/percy/works/mbos-v1/improve-agentsmith-fs`
- Planning commit: `9a3e127 docs: plan AgentSmith workspace storage control plane`
- Research snapshot: `docs/research/agentsmith-workspace-storage-technical-design.md`
- Discussion scratchpad: `docs/research/scratch.md`

Important: do not use `agentsmith-oss` for current-state analysis. It is an old version and was explicitly excluded.

## Product Goal

AgentSmith needs persistent, versioned file libraries for agent sandboxes and user desktops without creating one JuiceFS metadata DB and bucket per notebook task.

The target design is:

- A default shared JuiceFS filesystem/storage pool for new file libraries.
- AgentSmith workspace-level storage profiles so different tenant workspaces can bind to different AFSCP instances or storage pools.
- AgentSmith-controlled access to each file library/repo.
- JVS-managed save points, restores, repo lifecycle, and repo clone.
- User PC access through controlled exports such as WebDAV, without exposing JuiceFS credentials.
- Sandbox access through controlled subdirectory mounts.
- Notebook tasks can be saved as templates and cloned by users in the same AgentSmith workspace.
- No cross-workspace template sharing or clone.

## The New Module

Build `agentsmith-fs-control-plane` as an independent application module in this repository.

MVP deployment shape:

- One container image.
- One Kubernetes Deployment and Service.
- Dedicated ServiceAccount.
- Dedicated Secret access for JuiceFS root credentials.
- Persistent operation store.
- Internal HTTP API reachable by AgentSmith API and privileged admin jobs only.

Integration adapters and compatibility changes may land in sibling AgentSmith repositories. The AFSCP runtime, operation store, path resolver, JVS runner, and export gateway should live in this repository and should not be implemented as helper modules inside AgentSmith API.

## Authority Boundaries

AgentSmith API owns:

- User, workspace, project, file library, and template authorization.
- Workspace storage profile product configuration.
- File library and template catalog records.
- User-visible audit projection.
- API entrypoints for Web, Desktop, and notebook task flows.
- Rejecting cross-workspace template clone before calling AFSCP.

AFSCP owns:

- JuiceFS root credentials and root mount access.
- Storage pool bootstrap and health checks.
- Repo path allocation and path resolution.
- Directory creation, permission setup, and quota hooks.
- JVS `init`, `save`, `history`, `restore`, `repo clone`, and lifecycle execution.
- WebDAV/export runtime and short-lived export credentials.
- Sandbox mount spec generation.
- Operation journal, idempotency, retries, and low-level audit events.

Sandbox-manager owns:

- Kubernetes Secret, PV, PVC, and Pod mount execution.
- CSI and workload binding status.
- No product permission decisions.

Desktop owns:

- Consuming AgentSmith `ExportAccess`.
- Local mount UX and diagnostics.
- No raw JuiceFS credential handling for ordinary users.

## MVP Must Not Expand Into

- Cross-workspace templates.
- Git remote semantics.
- Merge/conflict resolution.
- Real-time collaborative editing.
- Per-file ACL UI.
- Per-task JuiceFS DB/bucket provisioning.
- Ordinary user JuiceFS direct mount.
- A full NAS account system.

## First Engineering Checkpoints

1. Confirm runtime language and framework in an ADR.
2. Finalize workspace storage profile schema with AgentSmith API owners.
3. Finalize AFSCP internal API contract.
4. Finalize sandbox binding v2 with sandbox-manager owners.
5. Finalize Desktop `ExportAccess` contract.
6. Build AFSCP skeleton with operation store before mutating storage.
7. Add `.jvs` protection tests before enabling sandbox or WebDAV write access.
