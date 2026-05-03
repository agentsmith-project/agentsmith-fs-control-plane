# MVP Plan

## P0 Scope

P0 must prove the control plane boundary and the end-to-end user value.

Deliver:

- AFSCP independent service/container skeleton.
- Durable operation store.
- Workspace storage profile support in AgentSmith.
- Shared JuiceFS filesystem/storage pool support for new file libraries.
- Repo path allocation under AFSCP-controlled workspace roots.
- JVS init/save/history/restore execution.
- Sandbox binding v2 for repo subdirectory mount.
- WebDAV export for Desktop/Web without JuiceFS credentials.
- Notebook task save-as-template.
- Same-workspace template clone into an independent repo.
- Cross-workspace template clone rejection.
- `.jvs` protection for WebDAV and sandbox paths.
- Legacy file library compatibility.

## P0 Non-Scope

- Migrating all legacy file libraries.
- Multi-region or multi-cloud storage policy.
- Global template marketplace.
- Cross-workspace sharing.
- SMB/NFS export.
- Per-file ACL UI.
- Billing and quota enforcement UI.
- Version merge/conflict resolution.
- Runtime language optimization work.

## Suggested Milestones

### Milestone 1: Contracts And Skeleton

- Pick runtime language/framework in ADR.
- Add service skeleton.
- Add internal auth placeholder.
- Add operation store schema.
- Finalize API contract with AgentSmith and sandbox-manager owners.

### Milestone 2: Provisioning Path

- Ensure storage pool.
- Create repo path.
- Initialize JVS repo.
- Return repo metadata to AgentSmith.
- Add workspace storage profile flow.

### Milestone 3: Sandbox And Export

- Generate sandbox mount spec.
- Support sandbox-manager binding v2.
- Create WebDAV export sessions.
- Ensure credentials are short-lived and scoped.
- Block `.jvs`.

### Milestone 4: JVS Operations

- Save point.
- History.
- Restore preview.
- Restore.
- Operation journal and retry behavior.

### Milestone 5: Templates

- Save notebook task as workspace template.
- Clone source repo into a workspace template repo, then clone template repo into a same-workspace target repo.
- Reject cross-workspace clone at both AgentSmith API and AFSCP.
- Verify cloned repo gets a new JVS identity.

## Definition Of Done

- All P0 acceptance criteria in `docs/PRODUCT_REQUIREMENTS.md` pass.
- No ordinary API response contains JuiceFS root credential material.
- Sandbox workload environment and mounted files contain no JuiceFS root credentials.
- WebDAV cannot read or write `.jvs`.
- JVS `doctor --strict` passes after repo create, save, restore, and clone.
- Legacy file libraries still work through compatibility paths.
