# Operations And Audit

AFSCP executes long-running and mutating storage operations. These operations must be durable and auditable.

## Operation Record

Recommended fields:

- `operation_id`
- `idempotency_key`
- `correlation_id`
- `requesting_actor_type`
- `requesting_actor_id`
- `calling_service_identity`
- `tenant_workspace_id`
- `storage_repo_id`
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

- repo create
- repo archive/delete
- save point create
- restore preview/run
- repo clone
- template clone
- export create/revoke
- sandbox mount spec generation if it triggers provisioning
- lifecycle move/rename/detach

## Audit

AFSCP should emit low-level audit events to AgentSmith. AgentSmith should produce user-visible audit summaries.

`requesting_actor_type` and `requesting_actor_id` must identify the authorized end actor supplied by AgentSmith, such as the user or system job that requested the operation. The internal service credential only identifies the calling service and should be recorded separately as `calling_service_identity`.

Each event should include:

- workspace ID
- repo/template ID
- actor
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
