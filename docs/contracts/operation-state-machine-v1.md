# Contract: Operation State Machine V1

Status: draft

AFSCP mutations are durable operations.

## States

- `queued`
- `running`
- `succeeded`
- `failed`
- `cancel_requested`
- `cancelled`

## Requirements

- Mutating requests require idempotency keys.
- Operation IDs are returned immediately or after synchronous completion.
- Operation records include correlation IDs.
- Operation records include the authorized end actor supplied by AgentSmith.
- Operation records separately identify the calling service identity.
- Retry behavior is explicit per operation type.
- Audit events reference operation IDs.
