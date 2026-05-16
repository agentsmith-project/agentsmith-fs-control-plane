# Contract: Namespace Volume Binding V1

Status: GA implementation-baseline contract. FINAL GA is governed by
`docs/GA_RELEASE_GATES.md`, `docs/READINESS_EVIDENCE.md`, and
`scripts/verify-ga-release.sh`.

Namespace volume binding is owned and enforced by AFSCP. Trusted callers may request or configure bindings according to deployment policy, but they do not provide authoritative raw filesystem paths.

## Required Fields

- `namespace_id`
- `default_volume_id`
- `allowed_callers`
- `quota_bytes_default`
- `export_policy`
- `lifecycle_policy`
- `mount_policy`
- `template_policy`
- `status`

## Namespace Lifecycle

AFSCP treats namespace as a first-class storage boundary.

GA statuses:

- `active`: repo create, export, mount, save, restore, and template clone may proceed if policy permits.
- `disabled`: new mutating operations and new session issuance are rejected; existing read-only inspection may continue for operators.
- `deleting`: outside GA, requires a lifecycle drain design.

`namespace_id` may be supplied by a trusted caller, but AFSCP owns the binding, status, allowed caller policy, and canonical storage root.

## Quota Semantics

AFSCP manages and validates namespace volume bindings, but `quota_bytes_default`
is a policy record and enforcement hook, not enforced as a capacity limit by
itself. Callers may treat quota as enforced only when the selected volume
capability `directory_quota=true` and the corresponding volume integration
explicitly enables directory quota enforcement.

## Caller Policy

Each binding lists service principals that may act in the namespace.
Within one binding, each canonical `caller_service` may appear at most once.
Ordinary multi-role access must be expressed in that single entry; a dedicated
orchestrator or migration identity must not be combined with ordinary product
identity by repeating the same service in another `allowed_callers` entry.

Ordinary namespace binding roles may include product-scoped roles such as:

- `namespace_admin`
- `repo_admin`
- `repo_lifecycle_admin`
- `restore_admin`
- `export_admin`
- `template_admin`
- `mount_admin`
- `operation_inspector`

Dedicated namespace binding roles may include `orchestrator_mount` for a
dedicated orchestrator caller and `migration_admin` for a dedicated migration
caller where deployment policy permits. Each dedicated role must be the only
role in its `allowed_callers` entry and must not be mixed with ordinary product
caller roles or with another dedicated role.

`volume_admin`, `operator_admin`, and `break_glass_admin` are
deployment/global policy roles, not ordinary namespace binding roles. They must
not be used to replace
namespace-scoped product roles or `operation_inspector` in ordinary caller
policy.

Binding-scoped `namespace_admin` authorizes ordinary read-only namespace
administration and namespace-scoped inspection where policy permits. It does not
authorize namespace create, namespace disable, or volume-binding update.
Namespace governance and first-binding bootstrap operations are authorized by
deployment namespace policy so a binding cannot self-authorize its own creation
or lock out the initial namespace setup path.

AFSCP must check `caller_service` and role before each namespace-bound operation. Denied checks are audited.
`operation_inspector` grants namespace-scoped operation inspection of redacted records. `operator_admin` grants global/operator inspection and repair through deployment policy only.

## Rules

- A binding is selected when creating new repos.
- Changing a binding affects new repos only.
- Existing repos require explicit migration.
- AFSCP computes and owns the canonical namespace root for each volume.
- `cross_namespace_clone_enabled` defaults to false.
- Template policy may enable namespace templates but not ordinary cross-namespace templates.
- Template clone must not cross volumes in GA. If source/template volume differs from the namespace default volume, AFSCP rejects with `VOLUME_MISMATCH_REQUIRES_IMPORT`.
- Mount/export requests must be checked against volume capabilities and namespace policy before issuing credentials or mount plans.
- Lifecycle policy defines tombstone retention, purge eligibility, and whether break-glass purge is enabled for the namespace. AFSCP records the applicable policy with each delete/purge operation so recovery and audit do not depend on later policy drift.
- `mount_policy.workload_mount_enabled=true` is only a namespace permission. AFSCP must still reject workload mounts when the selected repo/runtime cannot provide a payload-only mount with control metadata outside the mounted root.
- Disabling a namespace rejects repo create, save point create, direct restore, template create, template clone, export create, workload mount binding create, and other new mutating/session issuance operations.
- Health inspection, operation inspection, and audit inspection may continue for authorized operator roles. Any operator or break-glass exception for direct restore is a future independent capability, not current GA behavior for the namespace-bound restore APIs.
- Existing ordinary exports and workload mounts remain governed by the namespace disable policy until revoked, expired and reconciled, or handled by explicit operator action. Direct restore must reject active or uncertain read-write sessions; destructive lifecycle activity must drain any non-terminal export or mount session required by the repo lifecycle contract.
- Changing a binding affects new repos only. Existing repos retain their recorded volume and path state until explicit migration.
