# Decoupling Review

## Question

Should AFSCP understand AgentSmith concepts such as notebook tasks, file libraries, projects, and workspaces?

## Conclusion

No. The concern is reasonable and should shape the project boundary.

AFSCP should be a robust functional storage control plane. It should expose storage-native primitives and enforce storage safety. AgentSmith should translate its business objects into those primitives.

## Why Decoupling Is Better

### Stability

Storage primitives change more slowly than product workflows. `volume`, `namespace`, `repo`, `template`, `export`, and `operation` are stable platform concepts. `notebook task` and `file library` are AgentSmith product concepts and may change as AgentSmith evolves.

### Testability

A generic control plane can be tested with conformance cases:

- namespace isolation
- path traversal rejection
- repo create/save/restore/clone
- template clone boundaries
- export TTL and revoke
- `.jvs` protection
- operation recovery

Those tests do not need AgentSmith product fixtures.

### Security

AFSCP should make storage boundary decisions from canonical IDs and paths, not from product objects. A caller may be AgentSmith today and another application tomorrow. AFSCP should authenticate the caller service, accept an authorized actor for audit, and enforce namespace/resource boundaries.

### Reuse

If AFSCP does not depend on AgentSmith semantics, it can support other internal products, admin migration jobs, CI environments, or data-processing runtimes.

## Revised Object Model

| Generic AFSCP concept | AgentSmith mapping example |
| --- | --- |
| `namespace` | AgentSmith workspace or tenant boundary |
| `volume` | configured storage pool / JuiceFS filesystem |
| `repo` | file library backend repo |
| `repo_template` | template repo referenced by AgentSmith template catalog |
| `export` | Desktop/WebDAV access session |
| `workload_mount_binding` / `orchestrator_mount_plan` | sandbox-manager binding input |
| `operation` | save/restore/clone/export job visible in AgentSmith audit |

The mapping belongs in AgentSmith integration code, not inside AFSCP core.

## Boundary Rules

- AFSCP does not store notebook task IDs as core fields.
- AFSCP does not store file library IDs as core fields.
- AFSCP does not store project IDs as core fields.
- AFSCP does not decide whether a user can access a product object.
- AFSCP accepts a `namespace_id`, `repo_id`, `template_id`, caller identity, authorized actor, and idempotency/correlation data.
- AFSCP validates namespace/resource consistency.
- AFSCP emits low-level events that callers can project into their own product audit model.

## Template Policy

AFSCP can support namespace-scoped repo templates without knowing what product flow created them.

P0 rule:

- Clone from template is allowed only within the same namespace.
- Cross-namespace clone is rejected by default.
- A future explicit admin/import flow can be designed separately if cross-namespace movement is needed.

For AgentSmith, mapping one AgentSmith workspace to one AFSCP namespace preserves the product requirement that templates do not cross workspace boundaries.

## Recommendation

Update all core docs and contracts to use generic terms:

- `namespace_id` instead of `tenant_workspace_id`
- `namespace volume binding` instead of `workspace storage profile`
- `repo` instead of `file library`
- `repo template` instead of AgentSmith template catalog
- `workload mount` instead of sandbox binding
- `caller application` instead of AgentSmith API

Keep AgentSmith specifics only in `docs/INTEGRATION_GUIDE.md`, `docs/REFERENCES.md`, and historical research snapshots.
