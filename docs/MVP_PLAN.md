# MVP Plan

## P0 Scope

P0 must prove the storage control plane boundary and the end-to-end functional primitives.

Deliver:

- AFSCP independent service/container skeleton.
- Durable operation store.
- Volume registry and health checks.
- Namespace-to-volume binding.
- Namespace caller-service authorization.
- Shared JuiceFS-backed volume support for new repos.
- Repo path allocation under AFSCP-controlled namespace roots.
- JVS init/save/history/restore execution.
- Workload mount binding generation and orchestrator-only mount plans only after the JVS external-control/payload-only mount strategy is implemented.
- WebDAV export without JuiceFS credentials.
- Repo clone into namespace-scoped immutable template repo.
- Same-namespace template clone into an independent repo.
- Cross-namespace template clone rejection by default.
- JVS control metadata protection gate for WebDAV and workload mounts.
- Workload mount binding lease/status lifecycle.
- Restore-run writer-session fencing that blocks new read-write sessions and rejects active read-write export/workload sessions by default.
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
- Repo archive/delete/rename/detach lifecycle APIs.
- Creating templates from older historical save points.
- SMB/NFS export.
- Per-file ACL UI.
- Billing and quota enforcement UI.
- Version merge/conflict resolution.
- Runtime language optimization work.

## Suggested Milestones

### Milestone 0: Feasibility Gates

- Pin and package a JVS binary that includes external control root support and the required CLI commands.
- Confirm AFSCP repo layout uses JVS external control root mode and payload-only mounts.
- Confirm the orchestrator can stop/unmount active read-write workload bindings for revoke and restore fencing.
- Confirm WebDAV export will be served by an AFSCP policy gateway, not stock `juicefs webdav` alone.
- Confirm the sandbox v2 contract consumes AFSCP orchestrator plans instead of caller-provided `metadata_url`.

### Milestone 1: Contracts And Skeleton

- Pick runtime language/framework in ADR.
- Add service skeleton.
- Add internal service-auth interface and caller-service authorization gate.
- Add operation store schema.
- Finalize volume, namespace, repo, template, export, mount binding, and orchestrator plan contracts.
- Generate initial internal OpenAPI before endpoint implementation.

### Milestone 2: Provisioning Path

- Ensure volume.
- Bind namespace to volume.
- Create repo path.
- Initialize JVS repo.
- Return repo metadata to caller.

### Milestone 3: Mounts And Export

- Generate workload mount bindings and orchestrator-only mount plans.
- Implement mount binding status, heartbeat, release, and revoke.
- Create WebDAV export sessions.
- Ensure credentials are short-lived and scoped.
- Serve only payload roots and reject root-level `.jvs` access/creation attempts as defense-in-depth.

### Milestone 4: JVS Operations

- Save point.
- History.
- Restore preview.
- Restore.
- Writer-session fence and active read-write export/workload session rejection for restore-run.
- Operation journal and retry behavior.

### Milestone 5: Templates

- Clone source repo into namespace-scoped template repo.
- Clone template repo into same-namespace target repo.
- Reject cross-namespace clone.
- Verify cloned repo gets a new JVS identity.

## Definition Of Done

- All P0 acceptance criteria in `docs/PRODUCT_REQUIREMENTS.md` pass.
- No ordinary API response contains JuiceFS root credential material.
- Workload mount bindings, orchestrator plans, and workload environments contain no JuiceFS root credentials.
- Ordinary product callers cannot see JuiceFS Secret references.
- WebDAV exposes only payload roots and cannot access JVS control metadata.
- Workload mounts expose only payload roots and never include `.jvs` for AFSCP-managed repos.
- JVS `doctor --strict` passes after repo create, save, restore, and clone.
- Calling products can map their own business objects to AFSCP primitives without AFSCP knowing those business object types.
