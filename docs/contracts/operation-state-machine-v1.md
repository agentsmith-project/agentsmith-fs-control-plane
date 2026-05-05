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

Minimum GA matrix:

| Operation | Resource Lock | Recovery Requirement |
| --- | --- | --- |
| namespace_upsert | namespace metadata row | `validate_namespace_upsert` claim/retry/reclaim atomically commits active namespace metadata, succeeded operation, and succeeded audit event; `cancel_requested` may be lease-finalized to `cancelled` within the namespace_upsert scope without touching non-namespace operations |
| repo_create | target repo exclusive create | inspect allocated path, JVS identity, and doctor result |
| repo_archive | repo lifecycle exclusive plus session drain | inspect lifecycle status, session terminal state, and retained storage |
| repo_restore_archived | repo lifecycle exclusive | inspect lifecycle status and repo health |
| repo_delete | repo lifecycle exclusive plus session drain | inspect tombstone status, session terminal state, and retained storage |
| repo_restore_tombstoned | repo lifecycle exclusive | inspect tombstone status, retention policy, and repo health |
| repo_purge | repo lifecycle exclusive plus session drain | inspect purge marker and absence of retained storage |
| save_point_create | repo JVS exclusive | inspect JVS output/save point existence before retry |
| restore_preview | repo JVS shared/read | retry safe from request input |
| restore_run | repo JVS exclusive plus writer-session fence | inspect restore marker/JVS state, block new read-write sessions, run doctor, reject active or uncertain read-write sessions |
| template_create | source repo exclusive during save phase, then source read gate plus target template exclusive create | inspect source save point, clone history mode, and target template path |
| template_clone | template read gate plus target repo exclusive create | inspect target repo path and JVS identity |
| export_create | export session lock | inspect credential/session record, revoke partial credential on failure |
| export_revoke | export session lock | idempotently mark revoked and invalidate credential |
| export_session_reconcile | export session lock | inspect gateway state; terminal only after no future access for lifecycle and no future writes for restore-run |
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

## Writer-Session Fence

The writer-session fence is the shared safety contract between restore-run,
read-write export creation, and read-write workload mount binding creation.

Required GA behavior:

- restore-run acquires the fence before checking active writer sessions
- while the fence is held, new read-write exports and workload mount bindings are rejected with `WRITER_SESSION_FENCE_HELD`
- read-only exports and read-only mount bindings do not count as writer sessions, but still respect namespace status and capability policy
- read-write exports count as active until revoked, expired and reconciled, or terminal
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
