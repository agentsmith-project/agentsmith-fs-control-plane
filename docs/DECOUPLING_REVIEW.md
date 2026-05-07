# Decoupling Review

Question: should AFSCP understand caller application business concepts?

Decision: no. AFSCP is a shared filesystem control plane with independent
runtime, evolution, release, and gate review. Caller applications translate
their own business objects into AFSCP storage primitives outside this repo.

## Rationale

Storage primitives change more slowly than product workflows. `volume`,
`namespace`, `repo`, `repo_template`, `export`, `workload_mount_binding`,
`orchestrator_mount_plan`, and `operation` are stable platform concepts.
Caller-side objects, screens, methods, and workflow names are intentionally not
part of the AFSCP model.

Keeping the boundary neutral lets AFSCP:

- enforce namespace, path, credential, export, mount, and lifecycle safety
  without depending on a caller's product vocabulary
- release and gate on AFSCP evidence rather than consumer adoption progress
- accept requirements and compatibility feedback from reference consumers
  without turning their implementation into an AFSCP release blocker
- serve multiple trusted caller applications, migration jobs, operator tools,
  and workload orchestrators through the same contracts

## Accepted Mapping Pattern

| Generic AFSCP concept | Caller-owned mapping |
| --- | --- |
| `namespace` | caller isolation boundary |
| namespace volume binding | caller storage profile selection |
| `repo` | caller durable filesystem resource |
| `repo_template` | caller-managed reusable source content |
| `export` | caller-issued client access |
| `workload_mount_binding` | caller-authorized runtime payload access |
| `orchestrator_mount_plan` | privileged mount instructions for the workload orchestrator |
| `operation` | storage operation projected into caller audit or UI state |

The mapping belongs in consumer-owned integration code and generic adoption
guidance, not in AFSCP contracts, tests, schemas, or implementation packages.

## Consequences

- AFSCP does not store caller business object IDs as core fields.
- AFSCP does not expose caller display-name rename or catalog-only detach APIs.
- AFSCP does not accept raw filesystem paths, metadata URLs, bucket
  credentials, or Secret values from ordinary callers.
- AFSCP gate and release closure depends on AFSCP product, security, runtime,
  operations, contract, and generated-client compatibility evidence.
- Reference consumer adoption may inform requirements and compatibility fixes,
  but it is not a gate for AFSCP release.
