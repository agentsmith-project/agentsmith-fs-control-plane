# Internal Packages

Initial neutral application packages are present. They define the guardrails
needed before real handlers and storage mutation work:

- `api`: neutral HTTP shell, health/readiness responses, route metadata,
  capability-denied fallback, standard errors, and operation envelope DTOs.
- `auth`: caller kinds, role policy, namespace mismatch helpers, and route class
  tests.
- `config`: environment-backed config and capability gates.
- `observability`: structured JSON logging and redaction helpers.
- `operations`: operation state, lease decisions, idempotency, redaction, and
  typed operation record boundaries.
- `store`: interfaces for durable operation records, idempotency, and audit
  sinks. PostgreSQL schema migration exists; the first PostgreSQL adapter slice
  covers operation reader/writer, idempotency create-or-reuse, audit outbox
  append plus DB-only at-least-once delivery primitive, and minimal repo fence
  held read/create/active release.
- `audit`: audit event typing, redaction expectations, and pure outbox state
  transitions.
- `contractcheck`: contract verifier for OpenAPI/schema/docs/Go DTO guardrails.
- `fences`: pure repo fence model, held-state semantics, and acquisition checks.
- `inspection`: recovery inspection and pure read-only recovery classification
  primitives.
- `pathresolver`: path safety helpers, denial tests, and shared resolver corpus.

Still intentionally absent: real endpoint handlers, real external audit delivery
worker/sink integration, repo/resource metadata adapters, recovery loop, JVS
execution, WebDAV export serving, workload mount issuance, repo/template
lifecycle mutation, storage mutation implementations, and fence enforcement
beyond the minimal repo fence adapter slice.

Use [docs/DEVELOPER_HANDOFF.md](../docs/DEVELOPER_HANDOFF.md) for the current
handoff and next development order.
