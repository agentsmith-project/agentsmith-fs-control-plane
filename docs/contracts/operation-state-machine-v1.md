# Contract: Operation State Machine V1

Status: GA implementation-baseline contract. FINAL GA is governed by
`docs/GA_RELEASE_GATES.md`, `docs/READINESS_EVIDENCE.md`, and
`scripts/verify-ga-release.sh`.

AFSCP mutations are durable operations. The operation store is the recovery source of truth after process restart.

## API Shape Boundary

Resource mutation endpoints return the flat `OperationEnvelope` response with
`operation_id`, `operation_state`, `resource`, `result`, and `error`. That
response is a caller-facing acknowledgement or terminal result, not the durable
operation-store record.

The durable store and operation inspection API use `OperationRecord`. `GET
/internal/v1/operations/{operationId}` returns the redacted `OperationRecord`
directly after stored-namespace or operator authorization. It must not be
wrapped in an `OperationEnvelope`, and mutation handlers must not return
`OperationRecord` where the OpenAPI response is `OperationEnvelope`.

Operation inspection does not require `X-AFSCP-Namespace-Id` because
`OperationRecord.namespace_id` may be null. The inspection handler resolves by
`operationId`, then enforces authorization against the stored namespace when
present or operator/global policy when absent.

## States

- `queued`
- `running`
- `succeeded`
- `failed`
- `cancel_requested`
- `cancelled`
- `operator_intervention_required`

## Required Record Fields

- `operation_id`
- `operation_type`
- `operation_state`
- `phase`
- `attempt`
- `lease_owner`
- `lease_expires_at`
- `idempotency_scope`
- `idempotency_key`
- `request_hash`
- `correlation_id`
- `caller_service`
- `authorized_actor`
- `resource`
- `namespace_id`
- `repo_id`
- `template_id`
- `export_id`
- `mount_binding_id`
- `session_fence_id`
- `external_resource_ids`
- `input_summary`
- `jvs_json_output`
- `verification_result`
- `compensation_status`
- `error`
- `created_at`
- `started_at`
- `finished_at`

`lease_owner`, `lease_expires_at`, resource-specific IDs, `session_fence_id`,
`jvs_json_output`, `verification_result`, `compensation_status`, `error`,
`started_at`, and `finished_at` are required nullable DTO fields. They must be
present as `null` when the operation has not reached the phase that populates
them.

## Idempotency

- Scope is `caller_service + namespace_id + operation_type + idempotency_key`.
- Same scope and same request hash returns the original operation or terminal result.
- Same scope and different request hash returns conflict.
- Operation IDs are returned immediately for async mutations or after synchronous completion with the same envelope.

## Recovery Rules

Each operation type must define deterministic recovery by `phase`.
Rows in the current GA restore slice, including direct `restore` and
`restore_preview_discard`, are represented in the machine-readable
OpenAPI/schema, operation-type fixtures, routes, and generated contracts used by
implementation.

## Operation Type Inventory

| operation_type | caller API admission | worker execution | worker recovery | capability | default GA posture |
| --- | --- | --- | --- | --- | --- |
| `volume_ensure` | yes | yes | yes | `volume_preflight` | default positive |
| `namespace_upsert` | yes | yes | yes | `namespace_binding` | default positive |
| `namespace_disable` | yes | yes | yes | `namespace_binding` | default positive |
| `namespace_volume_binding_put` | yes | yes | yes | `namespace_binding` | default positive |
| `repo_create` | yes | yes | yes | `repo_create` | default positive |
| `repo_archive` | yes | yes | yes | `repo_lifecycle_retained` | default positive |
| `repo_restore_archived` | yes | yes | yes | `repo_lifecycle_retained` | default positive |
| `repo_delete` | yes | yes | yes | `repo_lifecycle_retained` | default positive |
| `repo_restore_tombstoned` | yes | yes | yes | `repo_lifecycle_retained` | default positive |
| `repo_purge` | optional-gated | optional-gated | yes | `repo_purge` | default denied; optional positive only by selector |
| `save_point_create` | yes | yes | yes | `jvs_save_restore` | default positive |
| `restore` | yes | yes | yes | `jvs_save_restore` | default positive direct restore mutation |
| `restore_preview` | yes | yes | yes | `jvs_save_restore` | default positive durable restore-plan mutation |
| `restore_preview_discard` | yes | yes | yes | `jvs_save_restore` | default positive cleanup mutation |
| `restore_run` | yes | yes | yes | `jvs_save_restore` | default positive |
| `template_create` | optional-gated | optional-gated | yes | `repo_template` | default denied; optional positive only by selector |
| `template_clone` | optional-gated | optional-gated | yes | `repo_template` | default denied; optional positive only by selector |
| `export_create` | yes | yes | yes | `webdav_export` | default positive |
| `export_revoke` | yes | yes | yes | `webdav_export` | default positive |
| `export_session_reconcile` | no caller API admission | yes | yes | `operation_recovery` | internal recovery terminalization |
| `mount_binding_create` | optional-gated | optional-gated | yes | `workload_mount_binding` | default denied; optional positive only by selector |
| `mount_binding_status_update` | optional-gated | optional-gated | yes | `workload_mount_binding` | default denied; optional positive only by selector |
| `mount_binding_heartbeat` | optional-gated | optional-gated | yes | `workload_mount_binding` | default denied; optional positive only by selector |
| `mount_binding_release` | optional-gated | optional-gated | yes | `workload_mount_binding` | default denied; optional positive only by selector |
| `mount_binding_revoke` | optional-gated | optional-gated | yes | `workload_mount_binding` | default denied; optional positive only by selector |
| `migration_cutover` | no caller API admission | conditional | recovery-only | `operation_recovery` | conditional unsupported unless migration tooling is explicitly enabled |

## Side Effect And Replay Boundary

| operation_type | side_effect_boundary | idempotent_replay |
| --- | --- | --- |
| `volume_ensure` | metadata-only volume row, operation, and audit commit | same idempotency scope returns committed volume metadata |
| `namespace_upsert` | namespace row, operation, and audit commit | same request returns active namespace metadata |
| `namespace_disable` | namespace disabled state, operation, and audit commit | same request returns disabled namespace metadata without re-disabling side effects |
| `namespace_volume_binding_put` | namespace binding row, operation, and audit commit | same request returns binding metadata |
| `repo_create` | repo metadata, JVS identity, operation, and audit boundary | same request returns original repo operation/result |
| `repo_archive` | lifecycle archive state plus session-drain predicate | same request returns archive terminal result |
| `repo_restore_archived` | lifecycle restore state plus health predicate | same request returns restore terminal result |
| `repo_delete` | tombstone state plus session-drain predicate | same request returns delete terminal result |
| `repo_restore_tombstoned` | tombstone restore state plus retention predicate | same request returns restore terminal result |
| `repo_purge` | irreversible purge marker and retained storage absence | same request returns original purge terminal result; disabled default denies before side effects |
| `save_point_create` | JVS save point plus operation/audit boundary | same request returns original save point metadata |
| `restore` | writer-session fence, JVS direct restore, doctor, operation, and audit boundary | same request returns original direct restore operation/result without creating a preview plan |
| `restore_preview` | durable restore plan, preflight idle marker, operation, and audit boundary | same request returns original preview plan metadata without creating a second plan |
| `restore_preview_discard` | matching restore plan discard state, operation, and audit boundary | same request returns discarded plan result |
| `restore_run` | matching plan consume state, writer fence, JVS run, doctor, operation, and audit boundary | same request returns original restore-run terminal result |
| `template_create` | source save point and target template create boundary | same request returns original template operation/result; disabled default denies before side effects |
| `template_clone` | target repo create from template boundary | same request returns original clone operation/result; disabled default denies before side effects |
| `export_create` | export session, generated credential, operation, and audit boundary | same request returns existing session without credential reissue |
| `export_revoke` | session revoking state, operation, and audit boundary | same request returns existing revoke operation/session state |
| `export_session_reconcile` | session terminal state, runtime ledger counts, operation, and audit boundary | replay scans durable session/ledger state and never creates caller credentials |
| `mount_binding_create` | binding issuance state, operation, and audit boundary | same request returns existing binding without reissuing runtime material |
| `mount_binding_status_update` | orchestrator status observation, operation, and audit boundary | same request returns existing status update result |
| `mount_binding_heartbeat` | binding lease extension boundary | same request returns existing heartbeat result |
| `mount_binding_release` | release requested/confirmed terminal boundary | same request returns existing release state |
| `mount_binding_revoke` | revoke requested/confirmed terminal boundary | same request returns existing revoke state |
| `migration_cutover` | migration generation handoff if tooling is enabled | recovery-only replay requires operator decision when source/target generation is ambiguous |

## Failed vs Operator Intervention Decision

| operation_type | failed | operator_intervention_required |
| --- | --- | --- |
| `volume_ensure` | validation or durable metadata conflict before external ambiguity | ambiguous_external_state or storage recovery uncertainty |
| `namespace_upsert` | invalid namespace input or durable constraint rejection | ambiguous_external_state after partial commit uncertainty |
| `namespace_disable` | invalid namespace state or durable constraint rejection | ambiguous_external_state after partial disable uncertainty |
| `namespace_volume_binding_put` | inactive namespace/volume or durable constraint rejection | ambiguous_external_state after binding commit uncertainty |
| `repo_create` | validation failure before repo/JVS side effects | ambiguous_external_state, JVS runtime unavailable after queued recovery, or path/JVS uncertainty |
| `repo_archive` | stable session/lifecycle denial before mutation | ambiguous_external_state or uncertain retained lifecycle/storage transition |
| `repo_restore_archived` | stable lifecycle/health denial before mutation | ambiguous_external_state or uncertain restore transition |
| `repo_delete` | stable session/lifecycle denial before mutation | ambiguous_external_state or uncertain tombstone transition |
| `repo_restore_tombstoned` | stable retention/lifecycle denial before mutation | ambiguous_external_state or uncertain tombstone restore transition |
| `repo_purge` | capability_disabled_or_unsupported or approval/retention denial before side effects | ambiguous_external_state after possible irreversible deletion |
| `save_point_create` | validation/JVS preflight denial before save side effects | ambiguous_external_state or uncertain JVS save state |
| `restore` | confirmation, writer-session, or JVS direct restore denial before confirmed mutation | ambiguous_external_state, direct restore result mismatch, or doctor failure after restore |
| `restore_preview` | stable dirty-state or active-plan denial before preview side effects | ambiguous_external_state, mismatched pending plan, or uncertain JVS recovery state |
| `restore_preview_discard` | missing/mismatched pending plan before discard side effects | ambiguous_external_state or uncertain discard state |
| `restore_run` | stable writer-session/fence denial before JVS run | ambiguous_external_state, doctor failure after run, or uncertain recovery state |
| `template_create` | capability_disabled_or_unsupported or validation denial before side effects | ambiguous_external_state after save/clone uncertainty |
| `template_clone` | capability_disabled_or_unsupported or validation denial before side effects | ambiguous_external_state after clone uncertainty |
| `export_create` | capability/session validation denial before credential creation | ambiguous_external_state around persisted credential/session boundary |
| `export_revoke` | stable missing/terminal session denial | ambiguous_external_state when runtime access may remain |
| `export_session_reconcile` | stable terminalization denial when counts are provably nonzero | ambiguous_external_state when ledger/session counts disagree |
| `mount_binding_create` | capability_disabled_or_unsupported or validation denial before issuance | ambiguous_external_state when runtime mount material may have been issued |
| `mount_binding_status_update` | stable invalid transition denial | ambiguous_external_state for conflicting orchestrator terminal evidence |
| `mount_binding_heartbeat` | stable terminal/missing binding denial | ambiguous_external_state for lease/store uncertainty |
| `mount_binding_release` | stable missing/terminal binding denial | ambiguous_external_state when runtime access may remain |
| `mount_binding_revoke` | stable missing/terminal binding denial | ambiguous_external_state when runtime access may remain |
| `migration_cutover` | capability_disabled_or_unsupported when migration tooling is not enabled | ambiguous_external_state or generation mismatch requires recovery-only operator decision |

Minimum GA matrix:

| Operation | Resource Lock | Recovery Requirement |
| --- | --- | --- |
| volume_ensure | volume metadata row | `validate_volume_ensure` claim/retry/reclaim atomically commits volume metadata, succeeded operation, and succeeded audit event; this is metadata-only and does not provision JuiceFS or perform health checks |
| namespace_upsert | namespace metadata row | `validate_namespace_upsert` claim/retry/reclaim atomically commits active namespace metadata, succeeded operation, and succeeded audit event; `cancel_requested` may be lease-finalized to `cancelled` within the namespace_upsert scope without touching non-namespace operations |
| namespace_disable | namespace metadata row | `validate_namespace_disable` claim/retry/reclaim atomically commits disabled namespace metadata, succeeded operation, and succeeded audit event; existing exports and workload mounts remain governed by their own terminalization contracts |
| namespace_volume_binding_put | namespace volume binding row | `validate_namespace_volume_binding_put` claim/retry/reclaim atomically commits namespace volume binding metadata, succeeded operation, and succeeded audit event after verifying active namespace and active default volume; `cancel_requested` may be lease-finalized to `cancelled` within the namespace_volume_binding_put scope without touching non-binding operations |
| repo_create | target repo exclusive create | inspect allocated path, JVS identity, and doctor result |
| repo_archive | repo lifecycle exclusive plus session drain | inspect lifecycle status, session terminal state, and retained storage |
| repo_restore_archived | repo lifecycle exclusive | inspect lifecycle status and repo health |
| repo_delete | repo lifecycle exclusive plus session drain | inspect tombstone status, session terminal state, and retained storage |
| repo_restore_tombstoned | repo lifecycle exclusive | inspect tombstone status, retention policy, and repo health |
| repo_purge | repo lifecycle exclusive plus session drain | inspect purge marker and absence of retained storage |
| save_point_create | repo JVS exclusive | inspect JVS output/save point existence before retry |
| restore | repo JVS exclusive direct restore mutation plus writer-session fence | validate direct restore evidence, gate writer sessions, run doctor, and release or retain fence based on terminal outcome |
| restore_preview | repo JVS exclusive restore-plan mutation | inspect durable restore plan, preview preflight idle marker, and JVS recovery status before retry or intervention |
| restore_preview_discard | repo JVS exclusive matching active plan | inspect durable restore plan and JVS discard state before retry or intervention |
| restore_run | repo JVS exclusive matching active plan plus writer-session fence | validate preview plan, preflight matching pending JVS plan, gate writer sessions, run doctor, verify recovery idle, and retain fence on ambiguity |
| template_create | source repo exclusive during save phase, then source read gate plus target template exclusive create | inspect source save point, clone history mode, and target template path |
| template_clone | template read gate plus target repo exclusive create | inspect target repo path and JVS identity |
| export_create | export session lock | synchronous durable boundary commits operation, export session, and succeeded audit event; replay returns the existing session without reissuing credential secret |
| export_revoke | export session lock | idempotently move session to `revoking`/drain; terminal revoke depends on gateway or reconcile confirmation |
| export_session_reconcile | export session lock | terminalize zero-count `revoking -> revoked` and zero-count expired `active -> expired` sessions by atomically committing the operation, session update, and audit event; nonzero or uncertain gateway state fails closed |
| mount_binding_create | repo mount lock | inspect binding record and orchestrator issuance state |
| mount_binding_status_update | mount binding lock | inspect orchestrator-reported terminal state and runtime access guarantee |
| mount_binding_heartbeat | mount binding lock | extend only non-terminal live bindings |
| mount_binding_release | mount binding lock | terminal only after runtime access has ended |
| mount_binding_revoke | mount binding lock | keep releasing until runtime is confirmed unmounted or unable to write |
| migration_cutover, if migration tooling is enabled | migration exclusive lock | require operator decision if source/target generation is ambiguous |

## Audit

Audit events reference operation IDs. Denied authorization, path, namespace, and capability checks must also emit audit events even when no mutation operation is created.

## Record Redaction

Operation records, operator inspection APIs, standard error envelopes, logs, and
stored command output must not contain credential material. This includes
JuiceFS metadata URLs, bucket credentials, access keys, secret keys, Secret
values, WebDAV passwords, raw mount commands containing secrets, and bearer
tokens.

`external_resource_ids`, `input_summary`, `jvs_json_output`, `error.message`,
`error.details`, and `verification_result` must be redacted before persistence
or response. If a JVS or platform command emits secret material, AFSCP stores a
redacted copy and records that redaction occurred.

JVS restore preview `run_command` and recovery `recommended_next_command` must
not be stored or returned verbatim because they may contain internal paths.
Persist only safe metadata such as command kind, plan ID, source save point ID,
normalized recovery state, and redaction flags.

## Restore Plan Lifecycle

Restore preview creates durable restore plan state. It is not a read-only
operation, and its success operation record is not the source of truth for
whether a repo has an active pending restore.

The durable `RestorePlan` table/entity is the source of truth for restore plan
lifecycle. `restore_plan_id` is the AFSCP-safe identifier normalized from the
JVS preview `plan_id`; workers use the matching JVS plan ID when invoking
`jvs restore --run <plan_id>` and `jvs restore discard <plan_id>`.
`source_save_point_id`, `base_revision`, `head_revision`, `generation`,
`fence_marker`, `summary_json`, `stale`, and `blockers_json` are also stored
on the durable plan. These values must not be added as top-level
`OperationRecord` DTO fields unless the OpenAPI/schema, Go structs, and
migrations are updated in the same change.

`OperationRecord` carries only safe restore metadata in existing structured
containers: `external_resource_ids.restore_plan_id` and
`external_resource_ids.source_save_point_id` when that container is approved for
safe external IDs; otherwise the redacted `jvs_json_output` or
`verification_result` safe summary records the plan metadata. Request linkage
belongs in `input_summary.preview_operation_id`. Recovery evidence belongs in
safe summaries such as `verification_result.restore_plan_match`,
`verification_result.recovery_status`, and `verification_result.doctor`.

Required GA plan statuses:

- `pending`: preview succeeded and the plan may be consumed or discarded.
- `consuming`: restore-run has passed writer-session gating and is about to
  invoke or has invoked JVS restore-run.
- `consumed`: restore-run, doctor, recovery-idle verification, audit, and fence
  release completed atomically.
- `discarding`: caller-triggered discard is about to invoke or has invoked JVS
  restore discard.
- `discarded`: JVS discard and audit completed.
- `operator_intervention_required`: plan state cannot be proven safe by worker
  recovery.

`pending`, `consuming`, `discarding`, and
`operator_intervention_required` are active states. `consumed` and `discarded`
are terminal. Each repo may have at most one active restore plan. Active plans
block unrelated same-repo JVS mutations, including save, restore preview,
unrelated restore-run, template create, and template clone, but do not block
ordinary file IO. The only allowed same-repo JVS mutations while a plan is
active are the matching restore-run and matching discard operation.
`stale` and `blockers_json` are durable plan source-of-truth fields. A stale
pending plan is not consumable; it remains pending only for matching discard or
for a typed restore-run failure that returns `RESTORE_PREVIEW_STALE`.

Valid restore-run input requires a preview operation and plan in the same
namespace, repo, and resource boundary. The preview operation must have type
`restore_preview`, be `succeeded`, and contain durable `restore_plan_id` and
`source_save_point_id`. The plan must be `pending` and `stale=false` to be
consumed, must not be consumed, discarded, discarding, or consuming, and must
not already be referenced by a succeeded or non-terminal restore-run. A pending
plan with `stale=true` may only drive the typed `RESTORE_PREVIEW_STALE`
failure or matching discard. Cross-namespace or cross-repo preview references
use `OPERATION_NOT_FOUND` or the existing non-leaking equivalent.

Restore preview recovery must persist a preflight idle marker before invoking
JVS preview. After a crash, worker recovery may adopt a single pending JVS plan
only when AFSCP-exclusive-control assumptions hold and the current operation is
the earliest same-repo non-terminal restore preview or JVS mutation. Missing
markers, mismatched plan IDs, multiple pending plans, unsafe plan IDs,
competing operations, `stale_restore_preview`, or unknown recovery states move
the operation or plan to `operator_intervention_required` unless a caller
explicitly uses the discard flow.

Restore-run phases are ordered:

1. Validate the preview operation and durable plan.
2. If the durable plan is already `stale=true`, fail the restore-run operation
   with `RESTORE_PREVIEW_STALE` before JVS or writer fence.
3. Preflight JVS recovery status and require exactly one pending plan matching
   stored `restore_plan_id`. If JVS reports matching `stale_restore_preview`,
   persist `RestorePlan.stale=true` plus a `restore_preview_stale` blocker,
   fail typed with `RESTORE_PREVIEW_STALE`, and leave the plan `pending` for
   discard.
4. Acquire the writer-session fence.
5. Reject active or uncertain read-write sessions.
6. Mark the plan `consuming`.
7. Invoke JVS restore-run.
8. Run `jvs doctor --strict`.
9. Verify JVS recovery status is idle.
10. Atomically commit operation success, audit success, writer-fence release, and
   plan `consumed`.

Writer/session denial before JVS invocation releases the writer-session fence,
records a stable writer error, and leaves the plan `pending`. Any JVS
restore-run failure, doctor failure, or recovery-status ambiguity keeps the
writer-session fence held and moves the operation and/or plan to
`operator_intervention_required`.

Restore preview discard is a caller-triggerable cleanup operation. It validates
the matching pending preview plan, marks the plan `discarding`, invokes
`jvs restore discard <plan_id>` through the runner, and atomically commits
operation success, audit success, and plan `discarded`. It must never delete
private `.jvs` files directly. Cancelled preview UX must call this flow instead
of becoming operator filesystem cleanup.

`restore_preview_discard` is included in the current GA restore slice. The
machine-readable endpoint, route, operation type, request/response schema, and
contract fixtures expose it for handlers and generated clients.

## Writer-Session Fence

The writer-session fence is the shared safety contract between restore-run,
read-write export creation, and read-write workload mount binding creation.

Required GA behavior:

- the session substrate pure model exists for restore-run writer gating and
  lifecycle drain decisions. Export sessions are wired to API create/get/revoke,
  WebDAV gateway admission and durable runtime request ledger accounting, terminal
  reconcile, and repo lifecycle worker drain checks; workload mount issuance and
  restore-run execution remain separate.
- restore-run acquires the fence before checking active writer sessions
- while the fence is held, new read-write exports and workload mount bindings are rejected with `WRITER_SESSION_FENCE_HELD`
- read-only exports and read-only mount bindings do not count as writer sessions, but still respect namespace status and capability policy
- restore-run writer gating treats read-write WebDAV exports as active writers
  unless the export is terminal/reconciled or has durable write-drained evidence:
  `active_write_count=0` and nonzero `write_drained_at`. Revoking or
  heartbeat-expired non-terminal exports with that evidence do not block
  restore-run writer gating.
- export runtime accounting uses the durable `export_runtime_requests` ledger.
  Request begin inserts an open runtime request row and increments aggregate
  counts in the same DB boundary; heartbeat refreshes the same row; request end
  closes only an open row and decrements counts. Begin requires the session to
  still be `active` and unexpired at the DB admission boundary.
- stale open runtime request recovery runs before terminal export reconcile. It
  closes expired open ledger rows and subtracts their counts only when
  aggregate counts can cover the recovered rows; aggregate/ledger drift fails
  closed. Runtime request rows are not per-request WebDAV operation rows.
- read-write mount bindings in `issued`, `pending`, `active`, or `releasing` count as active when their lease is live
- expired read-write mount bindings still count as uncertain writers until reconciliation marks a terminal non-writing state
- restore-run with active or uncertain writers fails closed with `ACTIVE_WRITER_SESSIONS` or `STALE_WRITER_SESSION_UNCERTAIN`
- if restore-run enters `operator_intervention_required`, the fence remains held until an operator recovery action releases it or completes rollback
- process restart must recover held fences from durable operation/session state

Fence acquisition, release, and recovery must be covered by operation recovery
tests before GA.

## Repo Lifecycle Fence

The repo lifecycle fence protects archive, restore-archived, delete, tombstone
restore, and purge. It is stronger than the writer-session fence.

Required GA behavior:

- archive, restore-archived, delete, restore-tombstoned, and purge acquire the lifecycle fence before changing repo status
- while held, new exports, workload mount bindings, save point creation, restore-run, template create, and template clone into the repo are rejected with `REPO_LIFECYCLE_FENCE_HELD`
- archive, delete, and purge require all export and workload mount sessions,
  read-only or read-write, to reach confirmed terminal non-accessing state
  before storage is tombstoned or purged; WebDAV export write-drained evidence
  used by restore-run writer gating is not sufficient for lifecycle drain
- lifecycle fence acquisition must reject or wait for active storage mutations on the same repo; uncertain in-flight mutations fail closed or require operator intervention
- uncertain sessions fail closed with `STALE_SESSION_BLOCKS_LIFECYCLE` or enter `operator_intervention_required`
- purge is irreversible and must verify retention policy or approved break-glass purge before physical removal
- process restart must recover held lifecycle fences from durable operation/repo/session state

Lifecycle fence acquisition, drain, tombstone, purge, release, and recovery must
be covered by operation recovery tests before GA.
