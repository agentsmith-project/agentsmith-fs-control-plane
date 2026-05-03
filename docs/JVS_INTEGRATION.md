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
- restore run
- repo clone
- repo lifecycle operations
- `jvs doctor --strict`

See:

- `/home/percy/works/mbos-v1/jvs/docs/02_CLI_SPEC.md`
- `/home/percy/works/mbos-v1/jvs/docs/06_RESTORE_SPEC.md`
- `/home/percy/works/mbos-v1/jvs/docs/24_REPO_CLONE_PRODUCT_PLAN.md`
- `/home/percy/works/mbos-v1/jvs/docs/25_REPO_WORKSPACE_LIFECYCLE_PRODUCT_PLAN.md`

## Operation Rules

- Every mutating JVS action must have an AFSCP operation record.
- Mutating JVS actions must use per-repo locks.
- JVS JSON output should be stored with the operation record.
- AFSCP should map JVS errors into stable caller-visible error codes.
- `doctor --strict` should be run after repo create, restore, and clone in P0 smoke paths.

## Repo Create

Creating a repo should:

1. Resolve namespace and volume policy.
2. Allocate a canonical repo path.
3. Create the repo folder.
4. Run `jvs init <repo_path> --json`.
5. Store `repo_id`, `namespace_id`, `volume_id`, `repo_path`, and `jvs_repo_id`.

## Template Flow

Creating a repo template should:

1. Caller authorizes the request in its own product domain.
2. Caller invokes AFSCP with source repo, target template identity, namespace context, actor, correlation ID, and idempotency key.
3. AFSCP resolves the source repo and validates the namespace boundary.
4. AFSCP creates a save point in the source repo.
5. AFSCP allocates a new template repo path under the same namespace root.
6. AFSCP clones the source repo into the template repo with `jvs --repo <source_repo_path> repo clone <template_repo_path> --save-points all --json`.
7. AFSCP returns the template repo identity and JVS repo identity.

Using a template should:

1. Caller authorizes the request in its own product domain.
2. AFSCP validates that source template repo and target namespace are the same namespace.
3. AFSCP creates a new target repo path.
4. AFSCP runs `jvs --repo <template_repo_path> repo clone <target_repo_path> --save-points all --json`.
5. AFSCP returns the new target repo metadata to the caller.

Both clone steps create independent JVS repo identities. Modifying a cloned repo must not affect the source repo or template repo.

Template clone is not Git clone. Do not add remote/push/pull/origin concepts.

## Dirty State

JVS repo clone can reject dirty source state depending on command semantics. AFSCP should create a save point before template clone so the product behavior is explicit and repeatable.
