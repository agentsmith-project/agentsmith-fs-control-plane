# Contract: JVS Runner V1

Status: GA pre-dev review draft

AFSCP integrates with JVS through CLI JSON output for GA.

## Version Pin

Before endpoint implementation, the team must pin the supported JVS release
version, binary asset name, checksum, and tested CLI spec. The runner contract
should be updated whenever JVS JSON schemas or exit codes change.

The first handoff pin is `v0.4.7` from
`https://github.com/agentsmith-project/jvs/releases/tag/v0.4.7`. For
`linux/amd64`, use asset `jvs-linux-amd64` with SHA-256
`4ec030d62b24d980192af550d04cb4b7455299b285b68c91843386cfa5157e6d`.
Package or download the released binary; do not compile JVS from source as the
GA handoff path. CI must verify the checksum, verify Sigstore/cosign bundles
where supported, and smoke-test the required command surface and a minimal
`init -> save -> history -> restore preview -> restore-run -> repo clone ->
doctor --strict` flow.

## Rules

- AFSCP invokes JVS with canonical internal repo paths.
- JVS output is captured and stored with operation records.
- Each mutating JVS command runs under the resource locks defined below.
- AFSCP maps JVS exit codes and JSON errors into stable internal errors.
- `jvs doctor --strict` is part of repo create, restore-run, and clone validation.
- Dirty source state must be surfaced as a stable caller-visible error unless the operation first creates an explicit save point.
- Template clone history mode must be pinned to the supported JVS version. `--save-points all` is allowed only after durable imported-save-point protection is supported; otherwise GA uses `--save-points main`.
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

Repo lifecycle support for GA is provided by the accepted AFSCP tombstone/purge
lifecycle contract. JVS lifecycle commands are optional helpers only after
their external-control-root behavior is pinned to this contract.

## GA Freeze Matrix

Before storage handlers depend on JVS, this contract must record for each
required command:

| Command | Required Freeze |
| --- | --- |
| `init` | argv template, success JSON fields, existing path behavior, doctor verification |
| `save` | argv template, non-empty message behavior, save point ID field, dirty/current-state behavior |
| `history/list` | argv template, pagination or bounded output behavior, JSON schema |
| `restore preview` | argv template, dirty-state reporting, retry safety |
| `restore-run` | argv template, recovery marker behavior, dirty-state default, resume/rollback or operator-intervention mapping |
| `repo clone` | argv template, target empty/missing behavior, `--target-control-root`, `--save-points` mode, new repo identity field |
| `doctor --strict` | argv template, `ok` field, failure mapping, post-operation verification expectations |

The JVS release version, asset names, checksums, and packaged binary paths must
be recorded in an ADR or in this contract before endpoint implementation.

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
