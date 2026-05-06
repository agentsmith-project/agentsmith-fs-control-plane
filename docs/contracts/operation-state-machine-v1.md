# Contract: Operation State Machine V1

Status: GA pre-dev review draft

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
Rows in the current GA restore slice, including `restore_preview_discard`, are
represented in the machine-readable OpenAPI/schema, operation-type fixtures,
routes, and generated contracts used by implementation.

Minimum GA matrix:

| Operation | Resource Lock | Recovery Requirement |
| --- | --- | --- |
| volume_ensure | volume metadata row | `validate_volume_ensure` claim/retry/reclaim atomically commits volume metadata, succeeded operation, and succeeded audit event; this is metadata-only and does not provision JuiceFS or perform health checks |
| namespace_upsert | namespace metadata row | `validate_namespace_upsert` claim/retry/reclaim atomically commits active namespace metadata, succeeded operation, and succeeded audit event; `cancel_requested` may be lease-finalized to `cancelled` within the namespace_upsert scope without touching non-namespace operations |
| namespace_volume_binding_put | namespace volume binding row | `validate_namespace_volume_binding_put` claim/retry/reclaim atomically commits namespace volume binding metadata, succeeded operation, and succeeded audit event after verifying active namespace and active default volume; `cancel_requested` may be lease-finalized to `cancelled` within the namespace_volume_binding_put scope without touching non-binding operations |
| repo_create | target repo exclusive create | inspect allocated path, JVS identity, and doctor result |
| repo_archive | repo lifecycle exclusive plus session drain | inspect lifecycle status, session terminal state, and retained storage |
| repo_restore_archived | repo lifecycle exclusive | inspect lifecycle status and repo health |
| repo_delete | repo lifecycle exclusive plus session drain | inspect tombstone status, session terminal state, and retained storage |
| repo_restore_tombstoned | repo lifecycle exclusive | inspect tombstone status, retention policy, and repo health |
| repo_purge | repo lifecycle exclusive plus session drain | inspect purge marker and absence of retained storage |
| save_point_create | repo JVS exclusive | inspect JVS output/save point existence before retry |
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
`source_save_point_id` is also stored on the durable plan. These values must
not be added as top-level `OperationRecord` DTO fields unless the
OpenAPI/schema, Go structs, and migrations are updated in the same change.

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

Valid restore-run input requires a preview operation and plan in the same
namespace, repo, and resource boundary. The preview operation must have type
`restore_preview`, be `succeeded`, and contain durable `restore_plan_id` and
`source_save_point_id`. The plan must be `pending`, not consumed, discarded,
discarding, or consuming, and not already referenced by a succeeded or
non-terminal restore-run. Cross-namespace or cross-repo preview references use
`OPERATION_NOT_FOUND` or the existing non-leaking equivalent.

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
2. Preflight JVS recovery status and require exactly one pending plan matching
   stored `restore_plan_id`.
3. Acquire the writer-session fence.
4. Reject active or uncertain read-write sessions.
5. Mark the plan `consuming`.
6. Invoke JVS restore-run.
7. Run `jvs doctor --strict`.
8. Verify JVS recovery status is idle.
9. Atomically commit operation success, audit success, writer-fence release, and
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
  WebDAV gateway admission and DB-backed runtime observation, terminal
  reconcile, and repo lifecycle worker drain checks; workload mount issuance and
  restore-run execution remain separate.
- restore-run acquires the fence before checking active writer sessions
- while the fence is held, new read-write exports and workload mount bindings are rejected with `WRITER_SESSION_FENCE_HELD`
- read-only exports and read-only mount bindings do not count as writer sessions, but still respect namespace status and capability policy
- read-write exports count as active until revoked, expired and reconciled, or terminal
- export runtime accounting is aggregate DB delta accounting. Request start is
  `+1` active request and mutating start is also `+1` active write; request end
  is `-1`; heartbeat is `0/0`. Positive start deltas require the session to
  still be `active` and unexpired at the DB admission boundary.
- if a gateway crashes after a positive start delta commits and before its
  matching end delta, active counts may remain conservatively positive; current
  GA docs do not claim per-request operation recovery for that edge.
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
- archive, delete, and purge require all export and workload mount sessions, read-only or read-write, to reach confirmed terminal non-accessing state before storage is tombstoned or purged
- lifecycle fence acquisition must reject or wait for active storage mutations on the same repo; uncertain in-flight mutations fail closed or require operator intervention
- uncertain sessions fail closed with `STALE_SESSION_BLOCKS_LIFECYCLE` or enter `operator_intervention_required`
- purge is irreversible and must verify retention policy or approved break-glass purge before physical removal
- process restart must recover held lifecycle fences from durable operation/repo/session state

Lifecycle fence acquisition, drain, tombstone, purge, release, and recovery must
be covered by operation recovery tests before GA.
