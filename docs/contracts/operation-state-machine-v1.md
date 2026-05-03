# Contract: Operation State Machine V1

Status: P0 review draft

AFSCP mutations are durable operations. The operation store is the recovery source of truth after process restart.

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
- `authorized_actor_type`
- `authorized_actor_id`
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
- `error_code`
- `error_message`
- `created_at`
- `started_at`
- `finished_at`

## Idempotency

- Scope is `caller_service + namespace_id + operation_type + idempotency_key`.
- Same scope and same request hash returns the original operation or terminal result.
- Same scope and different request hash returns conflict.
- Operation IDs are returned immediately for async mutations or after synchronous completion with the same envelope.

## Recovery Rules

Each operation type must define deterministic recovery by `phase`.

Minimum P0 matrix:

| Operation | Resource Lock | Recovery Requirement |
| --- | --- | --- |
| repo_create | target repo exclusive create | inspect allocated path, JVS identity, and doctor result |
| save_point_create | repo JVS exclusive | inspect JVS output/save point existence before retry |
| restore_preview | repo JVS shared/read | retry safe from request input |
| restore_run | repo JVS exclusive plus writer-session fence | inspect restore marker/JVS state, block new read-write sessions, run doctor, reject active read-write sessions |
| template_create | source repo exclusive during save phase, then source read gate plus target template exclusive create | inspect source save point, clone history mode, and target template path |
| template_clone | template read gate plus target repo exclusive create | inspect target repo path and JVS identity |
| export_create | export session lock | inspect credential/session record, revoke partial credential on failure |
| export_revoke | export session lock | idempotently mark revoked and invalidate credential |
| mount_binding_create | repo mount lock | inspect binding record and orchestrator issuance state |
| migration_cutover | migration exclusive lock | require operator decision if source/target generation is ambiguous |

## Audit

Audit events reference operation IDs. Denied authorization, path, namespace, and capability checks must also emit audit events even when no mutation operation is created.
