# ADR 0007: Use PostgreSQL Operation Store And Audit Outbox

Status: accepted for development handoff

## Context

AFSCP mutates durable storage and may run long operations such as repo create,
restore, lifecycle drain, export revoke, mount revoke, and template clone. It
must survive process restart without losing idempotency, locks, fences, or audit
events.

## Decision

Use PostgreSQL as the GA control-plane metadata store for:

- resource metadata
- namespace bindings
- repo lifecycle state
- export sessions
- workload mount bindings
- durable operations
- operation phase and lease ownership
- writer-session fences
- repo lifecycle fences
- audit outbox

Operation workers acquire work through database leases. Every mutating endpoint
creates or reuses an operation record before external effects. The operation
record stores phase, request hash, idempotency scope, caller context, resource
IDs, redacted command output, verification result, and terminal error code.

Audit events are written through an outbox table in the same transaction as the
state transition when possible. Delivery failures must not hide the event from
operator inspection.

Idempotency scope:

```text
caller_service + namespace_id + operation_type + idempotency_key
```

Same scope and request hash returns the existing operation/result. Same scope
with different request hash returns `IDEMPOTENCY_CONFLICT`.

## Consequences

Positive:

- One recovery source of truth.
- Straightforward restart reconciliation and operator inspection.
- Transactional coupling between resource state and audit outbox.

Tradeoffs:

- PostgreSQL becomes a required deployment dependency.
- Database migrations are a precondition for endpoint handlers.
- Heavy JVS operations must store redacted summaries rather than unbounded logs.

