# JVS Integration

AFSCP is the only ordinary JVS executor in the AgentSmith product path.

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
- AFSCP should map JVS errors into stable AgentSmith-visible error codes.
- `doctor --strict` should be run after repo create, restore, and clone in P0 smoke paths.

## Template Flow

Saving a notebook task as a template should:

1. Resolve the source file library repo.
2. Create a save point.
3. Create or update a workspace-scoped template catalog record in AgentSmith.
4. Clone the source repo into a template repo or clone from a template repo into a target repo, depending on final product flow.
5. Ensure the cloned target has a new JVS repo identity.

Template clone is not Git clone. Do not add remote/push/pull/origin concepts.

## Dirty State

JVS repo clone can reject dirty source state depending on command semantics. AFSCP should create a save point before template clone so the product behavior is explicit and repeatable.
