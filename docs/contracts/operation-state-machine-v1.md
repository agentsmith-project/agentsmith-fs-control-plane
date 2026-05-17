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
Rows in the current GA restore slice, including direct `restore`, are
represented in the machine-readable OpenAPI/schema, operation-type fixtures,
routes, and generated contracts used by implementation.

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
| `restore` | writer-session fence, JVS direct restore output, operation, audit, and recovery boundary | same request returns original direct restore operation/result without creating a planning artifact |
| `template_create` | source save point and target template create boundary | same request returns original template operation/result; disabled default denies before side effects |
| `template_clone` | target repo create from template boundary | same request returns original clone operation/result; disabled default denies before side effects |
| `export_create` | export session, generated credential, operation, and audit boundary | same request returns existing session without credential reissue |
| `export_revoke` | session revoking state, operation, and audit boundary | same request returns existing revoke operation/session state |
| `export_session_reconcile` | session terminal state, runtime ledger counts, operation, and audit boundary | replay scans durable session/ledger state and never creates caller credentials |
| `mount_binding_create` | binding issuance state, operation, and audit boundary | same request returns existing binding without reissuing runtime material |
| `mount_binding_status_update` | orchestrator status observation, operation, audit, and terminal release evidence boundary; `released`/`revoked` require non-accessing runtime plus completed storage flush/durable and export-visible boundaries | same request returns existing status update result |
| `mount_binding_heartbeat` | binding lease extension boundary | same request returns existing heartbeat result |
| `mount_binding_release` | release requested boundary; terminal `released` is recorded only through status evidence after runtime, durable, and export visibility are complete | same request returns existing release state |
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
| `restore` | writer-session, dirty-state, or JVS direct restore denial before confirmed mutation | ambiguous_external_state, direct restore result mismatch, explicit diagnostic/recovery evidence requiring repair, or uncertain restore commit recovery |
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
| restore | repo JVS exclusive direct restore mutation plus writer-session fence | validate direct restore output, gate writer sessions, and release or retain fence based on durable operation/audit/recovery outcome |
| template_create | source repo exclusive during save phase, then source read gate plus target template exclusive create | inspect source save point, clone history mode, and target template path |
| template_clone | template read gate plus target repo exclusive create | inspect target repo path and JVS identity |
| export_create | export session lock | synchronous durable boundary commits operation, export session, and succeeded audit event; replay returns the existing session without reissuing credential secret |
| export_revoke | export session lock | idempotently move session to `revoking`/drain; terminal revoke depends on gateway or reconcile confirmation |
| export_session_reconcile | export session lock | terminalize zero-count `revoking -> revoked` and zero-count expired `active -> expired` sessions by atomically committing the operation, session update, and audit event; nonzero or uncertain gateway state fails closed |
| mount_binding_create | repo mount lock | inspect binding record and orchestrator issuance state |
| mount_binding_status_update | mount binding lock | inspect orchestrator-reported terminal state, runtime access guarantee, storage flush/durable barrier, and export-visible boundary |
| mount_binding_heartbeat | mount binding lock | extend only non-terminal live bindings |
| mount_binding_release | mount binding lock | request release/teardown; terminal status is only after runtime access has ended and durable/export-visible evidence is reported |
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

JVS direct restore output and verification material must not store raw commands,
absolute paths, stdout/stderr, credentials, `plan_id`, `restore_plan_id`,
`run_command`, or recovery-command material. Persist only safe metadata such as
requested save point, restored save point, previous/new head IDs, writer-gate
result, redaction flags, and direct restore mode.

## Direct Restore Lifecycle

Direct restore is a single durable `restore` operation. It does not create a
planning artifact, secondary run operation, cleanup operation, or safety save
point. The request body contains `save_point_id` only; caller UI confirmation
and idempotent submission express user intent.

Direct restore phases are ordered:

1. Validate the queued operation, namespace/repo boundary, caller context, and
   `input_summary.save_point_id`.
2. Acquire the writer-session fence.
3. Reject active or uncertain read-write sessions.
4. Invoke JVS direct restore for the save point.
5. Reduce the `jvs.afscp.direct.v1` restore JSON to safe operation evidence
   such as restored save point, previous/new head IDs, and redaction flags.
6. Atomically commit operation success, audit success, and writer-fence release.

Writer/session denial before JVS invocation releases the writer-session fence
and records a stable writer error. JVS direct restore failure before confirmed
mutation records a terminal failed operation. Direct restore output mismatch,
missing/mismatched restored save point or history head evidence, recovery-required
metadata, or uncertain operation/fence commit recovery keeps the operation in
`operator_intervention_required` until an operator repair path resolves it.
`jvs afscp status --json` and `jvs afscp doctor --json` are explicit
metadata-only diagnostics for recovery, operator investigation, or smoke
validation; they are not called by default in the direct restore hot path.

## Writer-Session Fence

The writer-session fence is the shared safety contract between direct restore,
read-write export creation, and read-write workload mount binding creation.

Required GA behavior:

- the session substrate pure model exists for direct restore writer gating and
  lifecycle drain decisions. Export sessions are wired to API create/get/revoke,
  WebDAV gateway admission and durable runtime request ledger accounting, terminal
  reconcile, and repo lifecycle worker drain checks; workload mount issuance and
  direct restore execution remain separate.
- direct restore acquires the fence before checking active writer sessions
- while the fence is held, new read-write exports and workload mount bindings are rejected with `WRITER_SESSION_FENCE_HELD`
- read-only exports and read-only mount bindings do not count as writer sessions, but still respect namespace status and capability policy
- direct restore writer gating treats read-write WebDAV exports as active writers
  unless the export is terminal/reconciled or has durable write-drained evidence:
  `active_write_count=0` and nonzero `write_drained_at`. Revoking or
  heartbeat-expired non-terminal exports with that evidence do not block
  direct restore writer gating.
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
- direct restore with active or uncertain writers fails closed with `ACTIVE_WRITER_SESSIONS` or `STALE_WRITER_SESSION_UNCERTAIN`
- if direct restore enters `operator_intervention_required`, the fence remains held until an operator recovery action releases it or completes rollback
- process restart must recover held fences from durable operation/session state

Fence acquisition, release, and recovery must be covered by operation recovery
tests before GA.

## Repo Lifecycle Fence

The repo lifecycle fence protects archive, restore-archived, delete, tombstone
restore, and purge. It is stronger than the writer-session fence.

Required GA behavior:

- archive, restore-archived, delete, restore-tombstoned, and purge acquire the lifecycle fence before changing repo status
- while held, new exports, workload mount bindings, save point creation, direct restore, template create, and template clone into the repo are rejected with `REPO_LIFECYCLE_FENCE_HELD`
- archive, delete, and purge require all export and workload mount sessions,
  read-only or read-write, to reach confirmed terminal non-accessing state
  before storage is tombstoned or purged; WebDAV export write-drained evidence
  used by direct restore writer gating is not sufficient for lifecycle drain
- lifecycle fence acquisition must reject or wait for active storage mutations on the same repo; uncertain in-flight mutations fail closed or require operator intervention
- uncertain sessions fail closed with `STALE_SESSION_BLOCKS_LIFECYCLE` or enter `operator_intervention_required`
- purge is irreversible and must verify retention policy or approved break-glass purge before physical removal
- process restart must recover held lifecycle fences from durable operation/repo/session state

Lifecycle fence acquisition, drain, tombstone, purge, release, and recovery must
be covered by operation recovery tests before GA.
