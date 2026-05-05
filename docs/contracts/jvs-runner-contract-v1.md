# Contract: JVS Runner V1

Status: GA pre-dev review draft

AFSCP integrates with JVS through CLI JSON output for GA.

## Version Pin

Before endpoint implementation, the team must pin the supported JVS release
version, binary asset name, checksum, and tested CLI spec. The runner contract
should be updated whenever JVS JSON schemas or exit codes change.

The accepted handoff pin is `v0.4.8` from
`https://github.com/agentsmith-project/jvs/releases/tag/v0.4.8`. For
`linux/amd64`, use asset `jvs-linux-amd64` with SHA-256
`f011699fa92abae59e70153d32f3b9a10de1159fc23a390b22208db23f965521`.
Package or download the released binary; do not compile JVS from source as the
GA handoff path. CI must verify the checksum, verify Sigstore/cosign bundles
where supported, and smoke-test the required command surface and a minimal
`init -> save -> history -> restore preview -> restore-run -> repo clone ->
doctor --strict` flow.

The release has matching `jvs-linux-amd64.bundle` and `SHA256SUMS.bundle`
assets. Local smoke recorded bundle presence; local cosign verification was not
performed because `cosign` was not installed. The CLI has no `--version`, so
version checks use the pinned release asset and checksum.

## Rules

- AFSCP invokes JVS with canonical internal repo paths.
- JVS output is captured and stored with operation records.
- Each mutating JVS command runs under the resource locks defined below.
- AFSCP maps JVS exit codes and JSON errors into stable internal errors.
- `jvs doctor --strict` is part of repo create, restore-run, and clone validation.
- Dirty source state must be surfaced as a stable caller-visible error unless the operation first creates an explicit save point.
- Template clone history mode must be pinned to the supported JVS version. `--save-points all` is allowed only after durable imported-save-point protection is supported; otherwise GA uses `--save-points main`.
- With external control roots, JVS target selection must come from explicit
  `--control-root <control> --workspace main`; the runner must not rely on CWD
  discovery and must not inherit an unrelated JVS repo from its process CWD.
- `--control-root` cannot be combined with `--repo`. After `init`, run commands
  from a clean, controlled CWD with `--control-root <control> --workspace main
  --json`; the payload root is an explicit command argument only where JVS
  requires one.
- Pending restore previews are public recovery state. AFSCP must inspect
  `restore_state`, fail closed on `E_RECOVERY_BLOCKING`, and use
  `restore discard <plan_id>` for cleanup; it must not delete private JVS files.

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

## JVS v0.4.8 Command Matrix

The v0.4.8 runner contract is self-contained for worker implementation. All
commands must use the pinned release binary, request `--json`, capture stdout
and stderr, and map non-zero exit or malformed JSON to stable AFSCP operation
errors. For external-control-root repos, commands must run from a clean,
controlled CWD that is not inside another JVS repo; target selection comes only
from explicit `--control-root <control_root_path> --workspace main`, never from
CWD discovery.

| Capability | Argv Template | JSON Fields AFSCP Must Parse | Fail-Closed Behavior |
| --- | --- | --- | --- |
| init | `jvs init <payload_root_path> --control-root <control_root_path> --workspace main --json` | repo/workspace identity such as `repo_id` or equivalent stable repo identifier, workspace name, status/ok field | Reject if payload/control roots already contain incompatible data, if workspace is not `main`, if control metadata is created under payload, or if JSON identity/status is missing. |
| save | `jvs --control-root <control_root_path> --workspace main save --message <message> --json` | `save_point_id`, current/dirty status, created timestamp when present | Require non-empty audited message; reject missing save point id, dirty-state ambiguity, malformed JSON, or output that does not identify the saved point. |
| history | `jvs --control-root <control_root_path> --workspace main history --json` | `workspace`, `save_points[].save_point_id`, `newest_save_point`, timestamp/message fields when present | Treat malformed output as failure; reject entries missing stable IDs; if a handler requires complete history and output/pagination fields indicate incompleteness, fail closed. |
| restore preview | `jvs --control-root <control_root_path> --workspace main restore <save_point_id> --json` | `mode:"preview"`, `plan_id`, `source_save_point`, `run_command`, `files_changed:false`, `history_changed:false`, `workspace` | This is the real preview command shape in external-control-root mode. Do not require `restore_state` on success; that belongs to recovery status. Fail closed on missing plan/source/run command, changed files/history, malformed JSON, or dirty-state ambiguity. |
| restore-run | `jvs --control-root <control_root_path> --workspace main restore --run <plan_id> --json` | `mode:"run"`, `plan_id`, `source_save_point` or `restored_save_point`, `files_changed`, `history_changed:false`, `unsaved_changes:false`, `workspace` | Run only for a recorded preview plan under the repo lock. Do not require `restore_state` on success; fail closed on missing plan/source, unexpected history changes, unsaved changes, malformed JSON, or dirty-state ambiguity. |
| restore discard | `jvs --control-root <control_root_path> --workspace main restore discard <plan_id> --json` | `mode:"discard"`, `plan_id`, `plan_discarded:true`, `files_changed:false`, `history_changed:false` | Cleanup pending preview state with the JVS command; never delete private control-root files directly. Missing discard confirmation or changed files/history is operator-visible recovery state. |
| recovery status | `jvs --control-root <control_root_path> --workspace main recovery status --json` | `restore_state`, active `plan_id` when present, blocking/recovery reason when present | If a restore is pending/blocking, do not start unrelated mutations. Missing status or unknown state maps to operator intervention, not silent recovery. |
| repo clone | `jvs --control-root <source_control_root_path> --workspace main repo clone <target_payload_root_path> --target-control-root <target_control_root_path> --save-points main --json` | `source_repo_id`, `target_repo_id`, `target_folder`, `target_control_root`, `save_points_mode`, `save_points_copied_count`, `runtime_state_copied` | GA uses `--save-points main`. Reject non-empty target payload/control roots, missing target identity, source dirty ambiguity, or any attempt to clone by CWD discovery. |
| doctor --strict | `jvs --control-root <control_root_path> --workspace main doctor --strict --json` | `ok`, plus health/findings/healthy fields where present | External-control-root doctor uses only `doctor --strict --json`; do not pass `--repair-runtime`. Any missing/false ok, error-severity finding, malformed JSON, or non-zero exit fails the operation or marks recovery/operator intervention. |

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
