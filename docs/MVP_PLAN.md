# MVP Plan

## P0 Scope

P0 must prove the storage control plane boundary and the end-to-end functional primitives.

Deliver:

- AFSCP independent service/container skeleton.
- Durable operation store.
- Volume registry and health checks.
- Namespace-to-volume binding.
- Shared JuiceFS-backed volume support for new repos.
- Repo path allocation under AFSCP-controlled namespace roots.
- JVS init/save/history/restore execution.
- Workload mount spec generation for repo root mounts.
- WebDAV export without JuiceFS credentials.
- Repo clone into namespace-scoped template repo.
- Same-namespace template clone into an independent repo.
- Cross-namespace template clone rejection by default.
- `.jvs` protection for WebDAV and workload mounts.
- Low-level audit event emission.

## P0 Non-Scope

- Product-specific UI or workflows.
- Product authorization.
- Caller-specific job/task semantics.
- Caller-specific catalog semantics.
- Migrating all legacy repos.
- Multi-region or multi-cloud storage policy.
- Global template marketplace.
- Cross-namespace sharing.
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
- Finalize volume, namespace, repo, template, export, and mount contracts.

### Milestone 2: Provisioning Path

- Ensure volume.
- Bind namespace to volume.
- Create repo path.
- Initialize JVS repo.
- Return repo metadata to caller.

### Milestone 3: Mounts And Export

- Generate workload mount spec.
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

- Clone source repo into namespace-scoped template repo.
- Clone template repo into same-namespace target repo.
- Reject cross-namespace clone.
- Verify cloned repo gets a new JVS identity.

## Definition Of Done

- All P0 acceptance criteria in `docs/PRODUCT_REQUIREMENTS.md` pass.
- No ordinary API response contains JuiceFS root credential material.
- Workload mount specs and workload environments contain no JuiceFS root credentials.
- WebDAV cannot read or write `.jvs`.
- Writable workload mounts cannot read or write `.jvs`.
- JVS `doctor --strict` passes after repo create, save, restore, and clone.
- Calling products can map their own business objects to AFSCP primitives without AFSCP knowing those business object types.
