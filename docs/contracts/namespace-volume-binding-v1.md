# Contract: Namespace Volume Binding V1

Status: P0 review draft

Namespace volume binding is owned and enforced by AFSCP. Trusted callers may request or configure bindings according to deployment policy, but they do not provide authoritative raw filesystem paths.

## Required Fields

- `namespace_id`
- `default_volume_id`
- `allowed_callers`
- `quota_bytes_default`
- `export_policy`
- `mount_policy`
- `template_policy`
- `status`

## Namespace Lifecycle

AFSCP treats namespace as a first-class storage boundary.

P0 statuses:

- `active`: repo create, export, mount, save, restore, and template clone may proceed if policy permits.
- `disabled`: new repo/export/mount/template operations are rejected; existing read-only inspection may continue for operators.
- `deleting`: P1 only, requires a lifecycle drain design.

`namespace_id` may be supplied by a trusted caller, but AFSCP owns the binding, status, allowed caller policy, and canonical storage root.

## Caller Policy

Each binding lists service principals that may act in the namespace.

Example roles:

- `repo_admin`
- `restore_admin`
- `export_admin`
- `template_admin`
- `mount_admin`
- `orchestrator_mount`
- `migration_admin`
- `operator_admin`
- `break_glass_admin`

AFSCP must check `caller_service` and role before each namespace-bound operation. Denied checks are audited.

## Rules

- A binding is selected when creating new repos.
- Changing a binding affects new repos only.
- Existing repos require explicit migration.
- AFSCP computes and owns the canonical namespace root for each volume.
- `cross_namespace_clone_enabled` defaults to false in P0.
- Template policy may enable namespace templates but not cross-namespace templates in P0.
- Template clone must not cross volumes in P0. If source/template volume differs from the namespace default volume, AFSCP rejects with `VOLUME_MISMATCH_REQUIRES_IMPORT`.
- Mount/export requests must be checked against volume capabilities and namespace policy before issuing credentials or mount plans.
- `mount_policy.workload_mount_enabled=true` is only a namespace permission. AFSCP must still reject workload mounts when the selected volume/runtime lacks verified `.jvs` protection.
