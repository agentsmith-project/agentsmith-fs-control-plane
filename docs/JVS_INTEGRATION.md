# JVS Integration

AFSCP is the only ordinary JVS executor in the storage-control path.

## Integration Mode

P0 should integrate through the JVS CLI with JSON output.

Do not reimplement JVS save, restore, clone, or lifecycle semantics inside AFSCP.

## Required Commands

AFSCP should support:

- `jvs init`
- save point creation
- save point history/list
- restore preview
- restore-run
- repo clone
- `jvs doctor --strict`

Repo lifecycle operations are P1 unless a separate lifecycle drain/recovery contract is accepted.

See:

- `/home/percy/works/mbos-v1/jvs/docs/02_CLI_SPEC.md`
- `/home/percy/works/mbos-v1/jvs/docs/06_RESTORE_SPEC.md`
- `/home/percy/works/mbos-v1/jvs/docs/24_REPO_CLONE_PRODUCT_PLAN.md`
- `/home/percy/works/mbos-v1/jvs/docs/25_REPO_WORKSPACE_LIFECYCLE_PRODUCT_PLAN.md`

## Operation Rules

- Every mutating JVS action must have an AFSCP operation record.
- Mutating JVS actions must use resource locks.
- JVS JSON output should be stored with the operation record.
- AFSCP should map JVS errors into stable caller-visible error codes.
- `doctor --strict` should be run after repo create, restore, and clone in P0 smoke paths.
- The supported JVS version or commit must be pinned before endpoint implementation.
- Cross-resource operations must use deterministic lock ordering.

## Resource Locks

- Save and restore-run use an exclusive repo JVS lock.
- History and restore-preview use a shared/read repo gate.
- Template create uses an exclusive source repo JVS lock while materializing the source save point, then a shared/read gate on the source repo plus an exclusive create lock on the target template while cloning.
- Template clone uses a shared/read gate on the template repo and an exclusive create lock on the target repo.
- Multi-resource locks are acquired in lexical order by `(volume_id, namespace_id, repo_kind, resource_id)`.

## Repo Create

Creating a repo should:

1. Resolve namespace and volume policy.
2. Allocate a canonical repo path.
3. Create the repo folder.
4. Run `jvs init <repo_path> --json`.
5. Store `repo_id`, `namespace_id`, `volume_id`, `repo_path`, and `jvs_repo_id`.
6. Return only IDs and status to ordinary callers. Raw paths remain internal.

## Template Flow

Creating a repo template should:

1. Caller authorizes the request in its own product domain.
2. Caller invokes AFSCP with source repo, target template identity, namespace context, actor, correlation ID, and idempotency key.
3. AFSCP resolves the source repo and validates the namespace boundary.
4. AFSCP creates a fresh source save point under an exclusive source repo JVS lock and records it as the template's `source_save_point_id`.
5. AFSCP allocates a new template repo path under the same namespace root.
6. AFSCP clones the source repo into the template repo with the pinned `clone_history_mode`.
7. AFSCP returns the template repo identity and JVS repo identity.

The P0 template is immutable after publication. Replacing a template means creating a new template or a caller-managed revision that points to a new AFSCP `template_id`.

P0 does not accept caller-provided historical `source_save_point_id` for template creation. JVS `repo clone` clones the current source repo/workspace; creating a template from an older save point requires a future staging restore/import flow.

P0 must not hard-code `--save-points all` until the pinned JVS version supports durable imported-save-point protection. If that support is not accepted, use `--save-points main` and record `clone_history_mode=main` on the template. If the source becomes dirty after the template save point is created and before clone, fail with `SOURCE_DIRTY_AFTER_TEMPLATE_SAVE`.

Using a template should:

1. Caller authorizes the request in its own product domain.
2. AFSCP validates that source template repo and target namespace are the same namespace.
3. AFSCP validates volume policy. If the template volume differs from the target namespace default volume, P0 rejects with `VOLUME_MISMATCH_REQUIRES_IMPORT`.
4. AFSCP creates a new target repo path.
5. AFSCP runs `jvs --repo <template_repo_path> repo clone <target_repo_path> --save-points <clone_history_mode> --json`.
6. AFSCP returns the new target repo metadata to the caller.

Both clone steps create independent JVS repo identities. Modifying a cloned repo must not affect the source repo or template repo.

Template clone is not Git clone. Do not add remote/push/pull/origin concepts.

## Dirty State

JVS repo clone can reject dirty source state depending on command semantics. AFSCP must use an explicit source save point before template creation so the product behavior is explicit and repeatable.
