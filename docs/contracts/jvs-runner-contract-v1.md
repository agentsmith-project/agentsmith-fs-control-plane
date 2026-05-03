# Contract: JVS Runner V1

Status: P0 review draft

AFSCP integrates with JVS through CLI JSON output in P0.

## Version Pin

Before endpoint implementation, the team must pin the supported JVS version or commit and record the tested CLI spec. The runner contract should be updated whenever JVS JSON schemas or exit codes change.

The packaged JVS binary must be built from the pinned commit. CI must smoke-test the required command surface and a minimal `init -> save -> repo clone -> doctor --strict` flow.

## Rules

- AFSCP invokes JVS with canonical internal repo paths.
- JVS output is captured and stored with operation records.
- Each mutating JVS command runs under the resource locks defined below.
- AFSCP maps JVS exit codes and JSON errors into stable internal errors.
- `jvs doctor --strict` is part of repo create, restore-run, and clone validation.
- Dirty source state must be surfaced as a stable caller-visible error unless the operation first creates an explicit save point.
- Template clone history mode must be pinned to the supported JVS version. `--save-points all` is allowed only after durable imported-save-point protection is supported; otherwise P0 uses `--save-points main`.
- JVS commands should run from a clean working directory outside another JVS repo, or the runner must prove CWD cannot affect target resolution.

## Required Commands

- init
- save
- history/list
- restore preview
- restore-run
- recovery status/resume/rollback or explicit operator-intervention state
- repo clone
- doctor

Repo lifecycle commands are P1 unless lifecycle APIs are explicitly pulled into scope.

## Resource Locks

JVS and filesystem mutations must use ordered locks to avoid deadlocks.

- Save and restore-run: exclusive lock on the repo.
- Restore-preview and history: shared/read gate on the repo.
- Template create: exclusive source repo lock during source save-point materialization, then shared/read gate on the source repo and exclusive create lock on the target template.
- Template clone: shared/read gate on the source template and exclusive create lock on the target repo.
- Repo create: exclusive create lock on the target repo ID/path.

When multiple resources are locked, acquire them in lexical order by `(volume_id, namespace_id, repo_kind, resource_id)` and release all locks on failure.

## CLI Shape To Freeze Before Implementation

For each command, record:

- argv template
- JSON success schema
- JSON error schema
- exit code mapping
- retry-safe phases
- doctor/verification step
- compensation behavior if verification fails
