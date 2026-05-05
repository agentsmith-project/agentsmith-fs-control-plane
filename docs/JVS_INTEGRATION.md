# JVS Integration

AFSCP is the only ordinary JVS executor in the storage-control path.

## Integration Mode

GA should integrate through the JVS CLI with JSON output.

Do not reimplement JVS save, version restore, or clone semantics inside AFSCP.
Repo availability, archive, tombstone, and purge semantics are owned by the
AFSCP repo lifecycle contract.

## Required Commands

AFSCP should support:

- `jvs init`
- save point creation
- save point history/list
- restore preview
- restore-run
- recovery status/resume/rollback or an explicit operator-intervention state for failed restore runs
- repo clone
- `jvs doctor --strict`

Repo lifecycle operations are GA storage-control operations implemented through
AFSCP durable lifecycle state, session drain, and the accepted tombstone/purge
storage contract. JVS lifecycle commands are optional implementation helpers
only after their external-control-root behavior is pinned; they are not the GA
source of lifecycle authority. AFSCP must not perform lifecycle filesystem
changes that leave JVS control metadata inconsistent.

See:

- JVS `v0.4.8` release/tag documentation and assets:
  `https://github.com/agentsmith-project/jvs/releases/tag/v0.4.8`
- [contracts/jvs-runner-contract-v1.md](contracts/jvs-runner-contract-v1.md)
  for the self-contained AFSCP command matrix and fail-closed behavior.

Local checkouts such as `/home/percy/works/mbos-v1/jvs` are development-machine
references only. They are not a handoff dependency for AFSCP worker
implementation; use the pinned release/tag documentation and asset checksums as
the authority.

## External Control Root Mode

AFSCP-managed repos must use JVS external control roots for GA.

Repo create command shape:

```bash
jvs init <payload_root_path> --control-root <control_root_path> --workspace main --json
```

Routine command shape:

```bash
jvs --control-root <control_root_path> --workspace main <command> --json
```

External control root rules accepted for AFSCP:

- `payload_root_path` is the JVS `main` workspace folder and contains user files only.
- `control_root_path` contains JVS control metadata and is not mounted/exported.
- A bare payload folder cannot auto-discover the control root; AFSCP runner must pass `--control-root` and `--workspace main`.
- `--repo` is not the selector for external control root repos.
- Current JVS external control root contract is main-only.
- AFSCP runner target selection is authoritative only through explicit
  `--control-root <control_root_path> --workspace main`. The process CWD must
  be clean and controlled, must not be inside another JVS repo, and must never
  be used to discover the target repo.
- JVS has repo/workspace lifecycle commands for ordinary repos, but AFSCP GA
  lifecycle does not depend on them. If AFSCP later uses those commands, their
  external-control-root behavior must first be pinned and tested against the
  same AFSCP lifecycle contract.

## Operation Rules

- Every mutating JVS action must have an AFSCP operation record.
- Mutating JVS actions must use resource locks.
- JVS JSON output should be stored with the operation record.
- AFSCP should map JVS errors into stable caller-visible error codes.
- `doctor --strict` should be run after repo create, restore, and clone in GA smoke paths.
- `doctor --strict` should be run before reactivating archived or tombstoned repos when retained JVS metadata is expected to remain usable.
- The supported JVS release version, binary asset name, checksum, and signature bundle must be pinned before endpoint implementation.
- The packaged JVS binary must come from the pinned GitHub release asset; CI should verify the checksum, verify Sigstore/cosign bundles where supported, and smoke-test the required commands instead of trusting a stale local artifact.
- AFSCP should run JVS commands from a clean working directory outside another JVS repo; explicit `--control-root --workspace main` is the target selector and CWD must not affect target resolution.
- Cross-resource operations must use deterministic lock ordering.

## Required Separation Smoke Test

Before enabling workload mounts, CI should prove the pinned JVS binary can:

1. `jvs init <payload> --control-root <control> --workspace main --json`.
2. Create a save point from the separated repo.
3. Confirm `<payload>/.jvs` is absent and `<control>/.jvs` is present.
4. Clone with `jvs --control-root <control> --workspace main repo clone <target_payload> --target-control-root <target_control> --save-points main --json`.
5. Confirm `<target_payload>/.jvs` is absent and `jvs --control-root <target_control> --workspace main doctor --strict --json` reports success.

## Resource Locks

- Save and restore-run use an exclusive repo JVS lock.
- History and restore-preview use a shared/read repo gate.
- Template create uses an exclusive source repo JVS lock while materializing the source save point, then a shared/read gate on the source repo plus an exclusive create lock on the target template while cloning.
- Template clone uses a shared/read gate on the template repo and an exclusive create lock on the target repo.
- Multi-resource locks are acquired in lexical order by `(volume_id, namespace_id, repo_kind, resource_id)`.

## Repo Create

Creating a repo should:

1. Resolve namespace and volume policy.
2. Allocate canonical `control_root_path` and `payload_root_path`.
3. Ensure parent directories exist and the payload root is ready for adoption.
4. Run `jvs init <payload_root_path> --control-root <control_root_path> --workspace main --json`.
5. Store `repo_id`, `namespace_id`, `volume_id`, `control_root_path`, `payload_root_path`, `control_volume_subdir`, `payload_volume_subdir`, and `jvs_repo_id`.
6. Return only IDs and status to ordinary callers. Raw paths remain internal.

## Repo Lifecycle

AFSCP repo lifecycle should:

1. Acquire the repo lifecycle fence.
2. Block new export, mount, save, restore-run, template, and lifecycle mutations.
3. Drain or revoke existing exports and workload mounts, read-only or read-write, according to the lifecycle operation.
4. Preserve JVS control metadata for archive and tombstone restore.
5. Verify retained repos with `doctor --strict` before returning to `active` or the recorded lifecycle accessibility state.
6. Permanently remove control and payload metadata only during approved purge.
7. Store JVS output or lifecycle verification output with the operation record.

## Template Flow

Creating a repo template should:

1. Caller authorizes the request in its own product domain.
2. Caller invokes AFSCP with source repo, target template identity, namespace context, actor, correlation ID, and idempotency key.
3. AFSCP resolves the source repo and validates the namespace boundary.
4. AFSCP creates a fresh source save point under an exclusive source repo JVS lock and records it as the template's `source_save_point_id`.
5. AFSCP allocates new template control and payload roots under the same namespace root.
6. AFSCP clones the source repo into the template repo with the pinned `clone_history_mode`.
7. AFSCP returns the template repo identity and JVS repo identity.

The GA template is immutable after publication. Replacing a template means creating a new template or a caller-managed revision that points to a new AFSCP `template_id`.

GA does not accept caller-provided historical `source_save_point_id` for template creation. JVS `repo clone` clones the current source repo/workspace; creating a template from an older save point requires a future staging restore/import flow.

For external control root repos, GA must use `--save-points main` and record `clone_history_mode=main` unless a pinned JVS version supports durable imported-save-point protection and the contract is updated. If the source becomes dirty after the template save point is created and before clone, fail with `SOURCE_DIRTY_AFTER_TEMPLATE_SAVE`.

Using a template should:

1. Caller authorizes the request in its own product domain.
2. AFSCP validates that source template repo and target namespace are the same namespace.
3. AFSCP validates volume policy. If the template volume differs from the target namespace default volume, GA rejects with `VOLUME_MISMATCH_REQUIRES_IMPORT`.
4. AFSCP creates new target control and payload roots.
5. AFSCP runs `jvs --control-root <template_control_root_path> --workspace main repo clone <target_payload_root_path> --target-control-root <target_control_root_path> --save-points <clone_history_mode> --json`.
6. AFSCP returns the new target repo metadata to the caller.

Both clone steps create independent JVS repo identities. Modifying a cloned repo must not affect the source repo or template repo.

Template clone is not Git clone. Do not add remote/push/pull/origin concepts.

## Dirty State

JVS repo clone can reject dirty source state depending on command semantics. AFSCP must use an explicit source save point before template creation so the product behavior is explicit and repeatable.

Template creation must also prevent source writes between the fresh save point and clone publication, or detect the race and fail closed with cleanup. `doctor --strict` is not a substitute for proving the source stayed clean relative to the template save point.

Restore preview/run must model JVS dirty-state decisions explicitly. GA should fail closed on dirty repos unless the API exposes a supported `discard_unsaved` or `save_first` mode and audits that choice.

External control root `jvs repo clone` permits target payload and target control roots to be missing or empty, but fails closed if they are non-empty. AFSCP should allocate the target paths but must not pre-populate the clone target roots before invoking JVS.
