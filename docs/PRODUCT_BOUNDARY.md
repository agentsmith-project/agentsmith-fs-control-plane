# Product Boundary

AFSCP is an internal storage control plane. It is not a user-facing product and should not become the main AgentSmith authorization layer.

## User-Facing Concepts

Users and admins should see:

- AgentSmith workspace.
- File library.
- Notebook task workspace.
- Save point.
- Restore.
- Workspace template.
- Template clone into a new file library.
- Desktop/Web export.

Users should not see:

- JuiceFS metadata DB.
- Object store bucket.
- Access key or secret key.
- JVS CLI commands.
- Raw repo filesystem paths.
- K8s Secret names, PV names, or PVC names.

## Authority Split

AgentSmith API decides who may do something.

AFSCP executes storage operations after AgentSmith authorizes them.

This means AFSCP should validate workspace and path boundaries, but it should not invent user permissions or bypass AgentSmith catalog state.

## Hard Product Decisions

- The default new storage model is shared JuiceFS filesystem/storage pool plus controlled repo paths.
- Different AgentSmith workspaces may use different storage profiles and storage pools.
- Templates are workspace-scoped only.
- Cross-workspace template sharing and clone are not allowed.
- Template clone creates an independent repo, not a shared collaborative directory.
- Ordinary file reads and writes can happen concurrently.
- AgentSmith does not provide version merge or conflict resolution.
- JVS save, restore, clone, and lifecycle operations must be serialized per repo.
- Desktop ordinary path uses WebDAV/export, not raw JuiceFS mount.

## MVP Guardrails

Do not expand MVP into:

- a NAS product
- a global template marketplace
- real-time collaboration
- cross-workspace sharing
- per-file ACL UI
- Git remote workflows
- automatic legacy migration
- billing or quota UI
