# Product Boundary

AFSCP is an internal shared filesystem control plane. It is independently run,
evolved, released, and gate-reviewed from any consumer product. It is not a
user-facing product application and should not become a caller-specific
authorization layer.

## AFSCP Concepts

AFSCP should expose:

- volume
- namespace
- repo
- repo lifecycle
- repo template
- save point
- restore
- export
- workload mount binding
- orchestrator mount plan
- operation
- audit event

AFSCP should not expose or depend on:

- caller job or task object
- caller catalog object
- project
- caller workspace
- caller desktop connector UX
- product template catalog UX
- product display names
- user-facing product permission model

## Authority Split

Calling applications decide who may do something.

AFSCP executes storage operations after a trusted caller supplies a namespace, resource IDs, an authorized actor for audit, and an idempotency/correlation context.

AFSCP validates storage boundaries. It should reject namespace/resource mismatches, path traversal, cross-namespace template clone, invalid volume access, and unsafe paths. It should not ask or answer product-specific authorization questions.

AFSCP also validates whether the calling service principal is allowed to operate in a namespace. That is storage-control authorization, not end-user product authorization.

## Hard Product Decisions

- The default new storage model is managed JuiceFS-backed volumes plus controlled repo paths.
- Different namespaces may use different volume bindings and isolation classes.
- Repo templates are namespace-scoped for GA.
- Cross-namespace template clone is rejected by default.
- Template clone creates an independent repo, not a shared collaborative directory.
- Ordinary file reads and writes can happen concurrently.
- AFSCP does not provide version merge or conflict resolution.
- JVS save, restore-run, and clone operations must be serialized per repo.
- Restore-run rejects active or uncertain read-write export/workload sessions.
- Repo lifecycle operations are storage-control operations in GA. Archive, restore-from-archive, delete request, restore-from-tombstone, and purge are owned by AFSCP because they affect storage availability and retention.
- Repo lifecycle operations must drain or revoke all existing export and workload mount sessions, read-only or read-write, before tombstone or purge.
- Purge additionally requires lifecycle policy approval, a caller approval reference, and audited authorization because it permanently removes storage.
- Ordinary client access uses controlled exports, initially WebDAV.
- Product display-name rename and catalog detach remain caller-owned metadata changes; AFSCP repo IDs are stable and immutable.
- `quota_bytes_default` is a policy record and enforcement hook. It is not enforced unless the selected volume capability `directory_quota` supports directory quota enforcement and the corresponding volume integration explicitly enables directory quota enforcement.

## GA Guardrails

Do not expand GA into:

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
- ordinary raw JuiceFS direct mount
- caller-visible Kubernetes Secret references
- product display-name rename or catalog detach APIs
- namespace delete APIs
