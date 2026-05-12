# Contract: JVS Runner V1

Status: GA implementation-baseline contract. FINAL GA is governed by
`docs/GA_RELEASE_GATES.md`, `docs/READINESS_EVIDENCE.md`, and
`scripts/verify-ga-release.sh`.

AFSCP integrates with JVS through CLI JSON output for GA.

## Version Pin

The implementation baseline pins the supported JVS release version, binary
asset name, checksum, and tested CLI spec. The runner contract should be
updated whenever JVS JSON schemas or exit codes change.

The current accepted implementation pin is `v0.4.9` from
`https://github.com/agentsmith-project/jvs/releases/tag/v0.4.9`. For
`linux/amd64`, use asset `jvs-linux-amd64` with SHA-256
`0a1c6896cecf85ec2ac4e15e1c29f6e3f8cf09b9a4db48a516559604f0e7e944`.
Package or download the released binary; do not compile JVS from source as the
GA handoff path. CI must verify the checksum, verify Sigstore/cosign bundles
where supported, and smoke-test the required command surface and a minimal
`init -> save -> history -> restore preview -> restore-run -> repo clone ->
doctor --strict` flow, plus the `doctor --strict --repair-runtime` stale
repository mutation lock cleanup path after a simulated `save` `E_REPO_BUSY`.

Version checks use the pinned release asset and checksum because the CLI has no
`--version`. Deployment pipelines should verify matching
`jvs-linux-amd64.bundle` and `SHA256SUMS.bundle` assets where Sigstore/cosign
verification is supported.

## Rules

- AFSCP invokes JVS with canonical internal repo paths.
- JVS output is captured and stored with operation records only after reduction
  to safe metadata. Raw stdout/stderr, absolute paths, credentials,
  `run_command`, and `recommended_next_command` must not be stored or returned
  verbatim.
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
- Save point create may invoke JVS runtime repair only after a `save`
  `E_REPO_BUSY` response. The target command is
  `jvs --control-root <control_root_path> --workspace main doctor --strict --repair-runtime --json`;
  AFSCP must parse the `clean_locks` repair result, retry `save` only after the
  repair command succeeds, and keep the save-point operation terminal
  `failed`+retryable when repair fails or cannot prove a safe runtime repair.
- `idle` recovery status, including the JVS shape with no `restore_state` and
  empty `plans[]`, means no pending JVS side effect is present. During
  restore-preview preflight recovery AFSCP may retry the preview from the
  durable preflight marker; during timeout or command-error reconciliation it
  should persist a retryable failure rather than move the operation to manual
  intervention when the post-error status is still idle/no-pending.
- `afscp-worker --run-once` startup timeout is not a per-JVS-operation hard cap.
  It bounds pass setup such as opening the store. After AFSCP acquires an
  operation lease, long JVS work is governed by durable lease renewal plus
  process/parent cancellation. The worker must renew the same operation ID and
  owner before expiry through `RenewOperationLease`; renewal failure cancels the
  operation context so JVS exits and executor reconciliation can use
  lease-fenced commits. If the worker process dies, renewal stops and another
  worker can reclaim only after the durable lease expires. The runner must
  preserve `context.Canceled` and `context.DeadlineExceeded` through `errors.Is`
  for executor reconciliation.
- `stale_restore_preview` during restore-preview recovery defaults to AFSCP
  `operator_intervention_required` unless a caller explicitly drives the
  matching restore discard flow. During restore-run for the matching durable
  pending plan, it is a typed `RESTORE_PREVIEW_STALE` failure; AFSCP persists
  `RestorePlan.stale=true` and a `restore_preview_stale` blocker, then leaves
  the plan pending for discard.
- The durable `RestorePlan` entity, not `OperationRecord` top-level DTO fields,
  is the lifecycle source of truth. The runner parses JVS preview `plan_id`;
  AFSCP normalizes it to `restore_plan_id` and uses the matching JVS plan ID for
  restore-run and discard commands while persisting only safe operation
  summaries.

## Required Commands

- init
- save
- history/list
- restore preview
- restore-run
- restore discard
- recovery status/resume/rollback or explicit operator-intervention state
- repo clone
- doctor, including strict runtime repair for stale repository mutation locks

Repo lifecycle support for GA is provided by the accepted AFSCP tombstone/purge
lifecycle contract. JVS lifecycle commands are optional helpers only after
their external-control-root behavior is pinned to this contract.

## GA Freeze Matrix

Storage handlers that depend on JVS must keep this required-command freeze
matrix recorded and aligned:

| Command | Required Freeze |
| --- | --- |
| `init` | argv template, success JSON fields, existing path behavior, doctor verification |
| `save` | argv template, non-empty message behavior, save point ID field, `newest_save_point`, and `unsaved_changes` reporting |
| `history/list` | argv template, complete-history behavior, JSON schema, truncated-output handling |
| `restore preview` | argv template, dirty-state reporting, retry safety |
| `restore-run` | argv template, recovery marker behavior, dirty-state default, resume/rollback or operator-intervention mapping |
| `restore discard` | argv template, discard confirmation, recovery marker behavior, operator-intervention mapping |
| `repo clone` | argv template, target empty/missing behavior, `--target-control-root`, `--save-points` mode, new repo identity field |
| `doctor --strict` | argv template, `ok` field, failure mapping, post-operation verification expectations |
| `doctor --strict --repair-runtime` | argv template, `clean_locks` repair parsing, success proof, failure mapping, save retry guard |

The JVS release version, asset names, checksums, and packaged binary paths must
remain recorded in an ADR or in this contract as part of endpoint and worker
acceptance.

Both the API and worker runtimes require the pinned JVS binary when
`AFSCP_JVS_READY=true`. Workers execute mutating JVS operations, and the
internal API constructs the JVS-backed save-point history reader used by list
and projection routes. A deployment image that reports JVS ready but omits
`AFSCP_JVS_BINARY_PATH`, the accepted checksum, clean JVS CWD, or volume-root
mapping must fail startup/readiness instead of accepting create/list/restore
traffic with a partially wired runtime.

## JVS v0.4.9 Command Matrix

The v0.4.9 runner contract is self-contained for worker implementation. All
commands must use the pinned release binary, request `--json`, capture stdout
and stderr, and map non-zero exit or malformed JSON to stable AFSCP operation
errors. For external-control-root repos, commands must run from a clean,
controlled CWD that is not inside another JVS repo; target selection comes only
from explicit `--control-root <control_root_path> --workspace main`, never from
CWD discovery.

| Capability | Argv Template | JSON Fields AFSCP Must Parse | Fail-Closed Behavior |
| --- | --- | --- | --- |
| init | `jvs init <payload_root_path> --control-root <control_root_path> --workspace main --json` | repo/workspace identity such as `repo_id` or equivalent stable repo identifier, workspace name, status/ok field | Reject if payload/control roots already contain incompatible data, if workspace is not `main`, if control metadata is created under payload, or if JSON identity/status is missing. |
| save | `jvs --control-root <control_root_path> --workspace main save --message <message> --json` | `save_point_id`, `workspace`, `message`, `created_at`, `newest_save_point`, `unsaved_changes` | Require non-empty audited message; reject missing save point id, missing newest pointer, `newest_save_point != save_point_id`, missing/non-boolean `unsaved_changes`, malformed JSON, or output that does not identify the saved point. `unsaved_changes:true` is reported to upper layers rather than rejected by the runner primitive. |
| history | `jvs --control-root <control_root_path> --workspace main history --limit 0 --json` | `workspace`, `save_points[].save_point_id`, `newest_save_point` when history is non-empty, `truncated`, `limit`, `current_pointer`, timestamp/message fields when present | Request complete history; treat malformed output as failure; reject entries missing stable IDs; allow empty history without `newest_save_point`; fail closed on `truncated:true` so callers do not silently consume an incomplete history. |
| restore preview | `jvs --control-root <control_root_path> --workspace main restore <save_point_id> --json` | `mode:"preview"`, `plan_id`, `source_save_point`, `expected_newest_save_point`, `history_head`, `expected_folder_evidence`, `managed_files`, `run_command`, `files_changed:false`, `history_changed:false`, `workspace` | This is the real preview command shape in external-control-root mode. Do not require `restore_state` on success; that belongs to recovery status. Fail closed on missing plan/source/revision metadata/folder evidence/managed-files summary/run command, changed files/history, malformed JSON, or dirty-state ambiguity. `managed_files` buckets may omit `samples` when `count` is zero; normalize that to an empty sample list, but keep failing closed for positive counts without display-safe samples. Parse `run_command` only to verify shape or redact; persist safe metadata instead of the verbatim command. |
| restore-run | `jvs --control-root <control_root_path> --workspace main restore --run <plan_id> --json` | `mode:"run"`, `plan_id`, `source_save_point` or `restored_save_point`, `files_changed`, `history_changed:false`, `unsaved_changes:false`, `workspace` | Run only for a recorded pending preview plan under the repo lock after recovery status reports exactly one pending plan matching the stored plan ID. AFSCP marks the durable plan `consuming` before this command. Do not require `restore_state` on success; fail closed on missing plan/source, unexpected history changes, unsaved changes, malformed JSON, or dirty-state ambiguity. |
| restore discard | `jvs --control-root <control_root_path> --workspace main restore discard <plan_id> --json` | `mode:"discard"`, `plan_id`, `plan_discarded:true`, `files_changed:false`, `history_changed:false` | Cleanup pending preview state with the JVS command; never delete private control-root files directly. Missing discard confirmation or changed files/history is operator-visible recovery state. |
| recovery status | `jvs --control-root <control_root_path> --workspace main recovery status --json` | `restore_state` object with `state`, `blocking`, `plan_id`, optional `recovery_plan_id`, `message`, `recommended_next_command`, plus `plans[]`; no `restore_state` with empty `plans[]` means idle; no `restore_state` with a single active `plans[]` entry means `active_recovery` | If a restore is pending/blocking, do not start unrelated mutations. Missing/malformed status, unsafe plan IDs, multiple active recovery plans, mismatched plan IDs, or unknown states map to operator intervention unless the matching discard flow is running. Matching `stale_restore_preview` during restore-run maps to typed `RESTORE_PREVIEW_STALE` and durable plan stale/blocker persistence. Runner summaries must not store or expose `recommended_next_command` verbatim. Known preview recovery states include `pending_restore_preview` and `stale_restore_preview`; `restore_state.recovery_plan_id` is optional. |
| repo clone | `jvs --control-root <source_control_root_path> --workspace main repo clone <target_payload_root_path> --target-control-root <target_control_root_path> --save-points main --json` | envelope `repo_root` resolves to `<target_control_root_path>` after clone; data includes `source_repo_id`, `target_repo_id`, `target_folder`, `target_control_root`, `save_points_mode`, `save_points_copied_count`, `runtime_state_copied` | GA uses `--save-points main`. AFSCP prepares the managed parent directory for the target payload/control roots before invoking JVS, but must not pre-create or populate the target roots themselves. Reject non-empty target payload/control roots, missing target identity, source dirty ambiguity, envelope root not matching target control root, missing/non-boolean `runtime_state_copied`, `runtime_state_copied:true`, or any attempt to clone by CWD discovery. |
| doctor --strict | `jvs --control-root <control_root_path> --workspace main doctor --strict --json` | `ok`, plus health/findings/healthy fields where present | Ordinary validation doctor uses `doctor --strict --json`. Any missing/false ok, error-severity finding, malformed JSON, or non-zero exit fails the operation or marks recovery/operator intervention. |
| doctor runtime repair | `jvs --control-root <control_root_path> --workspace main doctor --strict --repair-runtime --json` | `ok`, `healthy:true`, repo/workspace identity, and `repairs[]` containing successful `clean_locks` with optional non-negative `cleaned` | Used only after save returns `E_REPO_BUSY`. Treat malformed output, missing/failed `clean_locks`, unhealthy result, or non-zero exit as unable to repair; the save point operation remains terminal `failed` and retryable. AFSCP must not delete JVS lock files directly. |

## Restore Preview Recovery Contract

Before invoking restore preview, the worker persists a preflight marker showing
JVS recovery status was idle. On worker restart after the marker, if recovery
status is still idle/no-pending, AFSCP retries the preview from the durable
marker. If timeout or command-error reconciliation also finds idle/no-pending,
AFSCP records a retryable restore-preview failure because there is no JVS side
effect to adopt or discard. After preview side effects are present, AFSCP may
adopt or discard a single pending JVS plan only when AFSCP remains the exclusive
ordinary JVS executor and the recovering operation is the earliest same-repo
non-terminal restore preview or JVS mutation. Missing markers, competing
operations, multiple plans, mismatched plan IDs, unknown blocking state, or
malformed recovery status require AFSCP operator intervention unless the
matching discard operation is explicitly running. Matching
`stale_restore_preview` during restore-run is the typed `RESTORE_PREVIEW_STALE`
path and must persist stale/blockers on the durable restore plan before strong
preview metadata validation can turn it into a mismatch.

## Resource Locks

JVS and filesystem mutations must use ordered locks to avoid deadlocks.

- Save, restore-preview, restore-run, and restore discard: exclusive lock on the
  repo.
- Restore-preview creates a JVS pending plan. The active durable AFSCP plan then
  blocks unrelated same-repo JVS mutations while allowing ordinary file IO; only
  the matching restore-run or matching discard may proceed.
- History: shared/read gate on the repo.
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
