# Operations And Audit

AFSCP executes long-running and mutating storage operations. These operations must be durable, recoverable, and auditable.

## Operation Record

Recommended P0 fields:

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

## Operation States

Minimum:

- `queued`
- `running`
- `succeeded`
- `failed`
- `cancel_requested`
- `cancelled`
- `operator_intervention_required`

## Mutating Operations

Require durable operation records:

- volume ensure/update
- namespace create/disable and binding update
- repo create
- save point create
- restore preview/run
- repo clone
- template create/clone
- export create/revoke
- workload mount binding generation
- orchestrator mount plan issuance if it provisions external resources
- migration cutover in future tooling

Repo archive/delete/rename/detach are P1 lifecycle operations and should not be implemented before a drain/recovery contract exists.

## Audit

AFSCP should emit low-level audit events to callers or an event sink. Calling products can project those events into user-visible audit records.

`authorized_actor_type` and `authorized_actor_id` identify the authorized end actor supplied by the trusted caller, such as the user or system job that requested the operation. `caller_service` identifies the authenticated internal service invoking AFSCP.

Each event should include:

- event ID
- event type
- namespace ID
- repo/template/export/mount binding ID
- authorized actor
- caller service
- operation ID, when one exists
- correlation ID
- result
- stable error code
- timestamp

Audit events are required for:

- caller authorization denials
- namespace/resource mismatch denials
- path resolver denials
- volume capability denials
- export credential issuance and revoke
- mount binding and orchestrator plan issuance
- mount binding heartbeat, release, revoke, expiry, and stale-lease reconciliation
- namespace binding changes
- restore-run active session denials
- migration cutover in future tooling
- operator break-glass overrides

P0 should use an append-only or outbox-style audit sink with documented retention. Credentials and secrets must be redacted.

## Recovery

AFSCP should reconcile operations left in `running` after process restart.

Recovery behavior must be explicit per operation type:

| Operation | Recovery Strategy |
| --- | --- |
| repo_create | inspect allocated path, JVS identity, and doctor result |
| save_point_create | inspect JVS save point existence before retry |
| restore_preview | retry from request input |
| restore_run | inspect restore state, hold writer-session fence, block new read-write sessions, verify no active read-write sessions, run doctor |
| template_create | inspect source save point, clone history mode, and target template path |
| template_clone | inspect target repo path and JVS identity |
| export_create | inspect session and credential state; revoke partial credential on failure |
| export_revoke | idempotently mark revoked and invalidate credential |
| mount_binding_create | inspect binding state and issued orchestrator plan state |
| mount_binding_heartbeat | idempotently extend lease or reject terminal bindings |
| mount_binding_release | idempotently mark released and unblock restore |
| migration_cutover | require operator decision if source/target generations are ambiguous |

If deterministic recovery is impossible, mark `operator_intervention_required` with the phase, external resource IDs, and recommended runbook.
