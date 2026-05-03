# Operations And Audit

AFSCP executes long-running and mutating storage operations. These operations must be durable and auditable.

## Operation Record

Recommended fields:

- `operation_id`
- `idempotency_key`
- `correlation_id`
- `caller_service_identity`
- `authorized_actor_type`
- `authorized_actor_id`
- `namespace_id`
- `repo_id`
- `template_id`
- `operation_type`
- `operation_state`
- `input_summary`
- `jvs_json_output`
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

## Mutating Operations

Require durable operation records:

- volume ensure/update
- namespace binding update
- repo create
- repo archive/delete
- save point create
- restore preview/run
- repo clone
- template create/clone
- export create/revoke
- workload mount spec generation if it triggers provisioning
- lifecycle move/rename/detach

## Audit

AFSCP should emit low-level audit events to callers or an event sink. Calling products can project those events into user-visible audit records.

`authorized_actor_type` and `authorized_actor_id` must identify the authorized end actor supplied by the trusted caller, such as the user or system job that requested the operation. The internal service credential only identifies the calling service and should be recorded separately as `caller_service_identity`.

Each event should include:

- namespace ID
- repo/template ID
- authorized actor
- caller service
- operation ID
- correlation ID
- result
- timestamp

## Recovery

AFSCP should reconcile operations left in `running` after process restart.

Recovery behavior must be explicit per operation type:

- retry safe
- inspect and mark succeeded
- inspect and mark failed
- require operator intervention
