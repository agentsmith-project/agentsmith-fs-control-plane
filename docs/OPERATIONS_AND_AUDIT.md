# Operations And Audit

AFSCP executes long-running and mutating storage operations. These operations must be durable, recoverable, and auditable.

## Operation Record

Recommended GA fields:

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
- repo archive, restore-archived, delete, restore-tombstoned, and purge
- save point create
- direct restore to save point
- template create/clone
- export create/revoke
- export expiry/reconciliation when it changes session terminal state
- workload mount binding generation, status update, heartbeat, release, and revoke
- orchestrator mount plan issuance if it provisions external resources
- migration cutover in future tooling

Product display-name rename and catalog detach are caller-owned metadata
operations. AFSCP repo lifecycle operations affect storage availability and
retention and therefore require operation records.

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
- repo lifecycle archive, restore, delete, tombstone, purge, denial, and intervention
- direct restore active writer-session denials
- migration cutover in future tooling
- operator break-glass overrides

GA must use an append-only or outbox-style audit sink with documented retention. Credentials and secrets must be redacted.

## GA Operator Minimum Surface

GA internal API operation inspection has one stable contract surface:
`GET /internal/v1/operations/{operationId}` returns a redacted operation record
by ID. GA does not add internal API operations list/search endpoints or
audit/fence aggregation endpoints. Broader inspection is still required for
operations, but it belongs to runbooks, read-only database queries,
observability dashboards, or deployment-side operator tooling.

Operators must be able to inspect:

- operation by ID through the stable internal API
- operation state by correlated resource through runbook or operator tooling
- operations requiring intervention through runbook or operator tooling
- volume health
- namespace binding and status
- repo/template/export/mount binding status
- stale workload mount leases
- held writer-session fences
- held repo lifecycle fences
- audit delivery lag or outbox failures

Operator actions must be audited. Any action that can release a fence, mark an
operation terminal, revoke a session, rotate a Secret, or accept residual risk
must require an operator role and a reason.

## Audit Delivery And Retention

- Audit delivery must be append-only or outbox-backed.
- Delivery failures must be visible to operators.
- Replay or re-delivery semantics must be documented.
- Retention must cover security investigation and caller audit projection needs.
- Logs and audit payloads must redact credential material, Secret values,
  metadata URLs, access keys, and WebDAV passwords.
- Denied events must be retained with enough context to investigate caller
  confused-deputy, path traversal, capability, and namespace mismatch failures.

## Recovery

AFSCP should reconcile operations left in `running` after process restart.

Recovery behavior must be explicit per operation type:

| Operation | Recovery Strategy |
| --- | --- |
| repo_create | inspect allocated path, JVS identity, and doctor result |
| repo_archive | inspect lifecycle status, session terminal state, retained storage, and audit state |
| repo_restore_archived | inspect lifecycle status and repo health |
| repo_delete | inspect lifecycle status, session terminal state, tombstone state, retained storage, and audit state |
| repo_restore_tombstoned | inspect tombstone status, retention policy, and repo health |
| repo_purge | inspect purge marker and absence of retained storage |
| save_point_create | inspect JVS save point existence before retry |
| restore | inspect operation phase, requested save point ID, writer-session fence, active or uncertain writer sessions, redacted direct JVS restore evidence, and explicit diagnostics when needed |
| template_create | inspect source save point, clone history mode, and target template path |
| template_clone | inspect target repo path and JVS identity |
| export_create | synchronous durable boundary commits operation, export session, and succeeded audit event; replay returns the existing session without reissuing credential secret |
| export_revoke | idempotently move session to `revoking`/drain; terminal revoke depends on gateway or reconcile confirmation |
| export_session_reconcile | inspect gateway state; terminal only after no future access for lifecycle and no future writes for direct restore |
| mount_binding_create | inspect binding state and issued orchestrator plan state |
| mount_binding_status_update | inspect orchestrator-reported terminal state and runtime access guarantee |
| mount_binding_heartbeat | idempotently extend lease or reject terminal bindings |
| mount_binding_release | idempotently mark released and unblock restore |
| mount_binding_revoke | keep releasing until runtime is confirmed unmounted or unable to write |
| migration_cutover | require operator decision if source/target generations are ambiguous |

If deterministic recovery is impossible, mark `operator_intervention_required` with the phase, external resource IDs, and recommended runbook.
