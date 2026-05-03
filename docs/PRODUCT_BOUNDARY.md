# Product Boundary

AFSCP is an internal storage control plane. It is not a user-facing product application and should not become a caller-specific authorization layer.

## AFSCP Concepts

AFSCP should expose:

- volume
- namespace
- repo
- repo template
- save point
- restore
- export
- workload mount spec
- operation
- audit event

AFSCP should not expose or depend on:

- notebook task
- file library
- project
- AgentSmith workspace
- Desktop product UX
- product template catalog UX
- user-facing product permission model

## Authority Split

Calling applications decide who may do something.

AFSCP executes storage operations after a trusted caller supplies a namespace, resource IDs, an authorized actor for audit, and an idempotency/correlation context.

AFSCP validates storage boundaries. It should reject namespace/resource mismatches, path traversal, cross-namespace template clone, invalid volume access, and unsafe paths. It should not ask or answer product-specific authorization questions.

## Hard Product Decisions

- The default new storage model is managed JuiceFS-backed volumes plus controlled repo paths.
- Different namespaces may use different volume bindings and isolation classes.
- Repo templates are namespace-scoped in P0.
- Cross-namespace template clone is rejected by default in P0.
- Template clone creates an independent repo, not a shared collaborative directory.
- Ordinary file reads and writes can happen concurrently.
- AFSCP does not provide version merge or conflict resolution.
- JVS save, restore, clone, and lifecycle operations must be serialized per repo.
- Ordinary client access uses controlled exports, initially WebDAV.

## MVP Guardrails

Do not expand MVP into:

- a product workflow engine
- a product authorization service
- a NAS product
- a global template marketplace
- real-time collaboration
- cross-namespace sharing
- per-file ACL UI
- Git remote workflows
- automatic legacy migration
- billing or quota UI
