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
- `resources`: pure control-plane metadata models and validation for volumes,
  namespaces, namespace volume bindings, and repo/repo lifecycle metadata.
- `store`: interfaces for durable operation records, idempotency, and audit
  sinks, resource metadata store contracts, and read-only repo recovery
  inspection contracts. PostgreSQL schema migration exists; the first
  PostgreSQL adapter slice
  covers operation reader/writer, DB-only operation lease
  claim/reclaim/recover/finalize/renew plus lease-fenced worker
  progress/terminal update primitives, idempotency create-or-reuse, audit
  outbox append plus DB-only at-least-once delivery primitive, and minimal repo
  fence held read/create/active release. The PostgreSQL resource metadata adapter
  covers volumes, namespaces, namespace volume bindings, repo/repo lifecycle
  metadata, lifecycle candidate repo reads, and all-held repo fence reads as
  control-plane records only, including internal template storage identity.
  RepoTemplate publication lifecycle and handlers remain unimplemented.
- `audit`: audit event typing, redaction expectations, and pure outbox state
  transitions.
- `contractcheck`: contract verifier for OpenAPI/schema/docs/Go DTO guardrails.
- `fences`: pure repo fence model, held-state semantics, and acquisition checks.
- `inspection`: operation inspection, recovery classification, and read-only
  repo recovery inspection composition primitives.
- `volumeexec`: minimal recovery executor for metadata-only `volume_ensure`;
  it commits volume metadata, the terminal operation update, and the audit event
  through the dedicated volume ensure store boundary.
- `namespaceexec`: minimal recovery executor for `namespace_upsert`; it commits
  namespace metadata, the terminal operation update, and the audit event through
  the dedicated namespace upsert store boundary.
- `namespacebindingexec`: minimal recovery executor for `namespace_volume_binding_put`;
  it commits binding metadata, the terminal operation update, and the audit
  event through the dedicated namespace volume binding store boundary.
- `jvsrunner`: minimal JVS v0.4.8 CLI runner abstraction for fixed
  external-control-root `init` and `doctor --strict` JSON commands only; it is
  not wired into repo workers yet.
- `workerapp`: production `afscp-worker --run-once` bootstrap for the
  opt-in namespace metadata operation recovery runner.
- `pathresolver`: path safety helpers, denial tests, shared resolver corpus, and
  canonical internal repo root resolution from trusted volume roots plus repo
  IDs.

Still intentionally absent: repo/JVS/WebDAV/mount endpoint handlers, real
external audit delivery worker/sink integration, repo lifecycle
workers/recovery loop, JVS execution, WebDAV export serving, workload mount
issuance, repo/template lifecycle mutation, storage mutation implementations,
and fence enforcement beyond the minimal repo fence adapter slice. The worker
app currently wires only
`volume_ensure`, `namespace_upsert`, and `namespace_volume_binding_put`
operation recovery when explicitly enabled.

Use [docs/DEVELOPER_HANDOFF.md](../docs/DEVELOPER_HANDOFF.md) for the current
handoff and next development order.
