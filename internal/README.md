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
  sinks, resource metadata store contracts, WebDAV export create/get/revoke,
  gateway runtime observation, terminal reconcile, and read-only repo recovery
  inspection contracts. PostgreSQL schema migration exists; the first
  PostgreSQL adapter slice
  covers operation reader/writer, DB-only operation lease
  claim/reclaim/recover/finalize/renew plus lease-fenced worker
  progress/terminal update primitives, idempotency create-or-reuse, audit
  outbox append plus DB-only at-least-once delivery primitive, and minimal repo
  fence held read/create/active release. The PostgreSQL resource metadata adapter
  covers volumes, namespaces, namespace volume bindings, repo/repo lifecycle
  metadata, lifecycle candidate repo reads, all-held repo fence reads, and
  read-only export session/workload mount binding state as control-plane
  records, WebDAV export session create/get/revoke, gateway credential lookup,
  atomic runtime delta accounting, and terminal reconcile, including internal
  template storage identity.
  RepoTemplate publication lifecycle and handlers remain unimplemented.
- `audit`: audit event typing, redaction expectations, and pure outbox state
  transitions.
- `contractcheck`: contract verifier for OpenAPI/schema/docs/Go DTO guardrails.
- `fences`: pure repo fence model, held-state semantics, and acquisition checks.
- `repoaccess`: pure repo access admission model used by repo lifecycle
  intake/admission handlers and intended for later save/restore, export, mount,
  and template handlers. It validates stored repo/binding/fence invariants and
  returns stable error-family decisions.
- `sessionstate`: pure export and workload-mount session substrate for
  restore-run writer gating and repo lifecycle drain gating. Export session
  state is now used by API export create/get/revoke, WebDAV gateway admission
  and runtime observation, terminal reconcile, and opt-in repo archive/delete
  recovery drain checks. Workload mount issuance remains separate.
- `exportaccess`: WebDAV export session, credential, runtime observation, and
  terminal reconcile request/result models.
- `exportgateway`: WebDAV policy gateway handler and no-follow payload
  filesystem boundary. It enforces Basic auth, active/unexpired WebDAV session
  admission, mode/method policy, source and `Destination` path policy, payload
  no-follow traversal, and DB-backed runtime delta observations.
- `exportreconcile`: terminal export session reconcile runner for zero-count
  `revoking -> revoked` and zero-count expired `active -> expired` updates.
- `operationinspect`: operation-only inspection authorization service used by
  the API without importing repo recovery/fence inspection code.
- `inspection`: recovery classification and read-only repo recovery inspection
  composition primitives.
- `volumeexec`: minimal recovery executor for metadata-only `volume_ensure`;
  it commits volume metadata, the terminal operation update, and the audit event
  through the dedicated volume ensure store boundary.
- `namespaceexec`: minimal recovery executor for `namespace_upsert`; it commits
  namespace metadata, the terminal operation update, and the audit event through
  the dedicated namespace upsert store boundary.
- `namespacebindingexec`: minimal recovery executor for `namespace_volume_binding_put`;
  it commits binding metadata, the terminal operation update, and the audit
  event through the dedicated namespace volume binding store boundary.
- `jvsrunner`: JVS v0.4.8 CLI runner abstraction for fixed
  external-control-root JSON commands, including `init`, `doctor --strict`,
  save/history/restore primitives, recovery status, and repo clone. Only
  `init`/`doctor` are wired through current repo workers; save/restore/template
  workers and handlers remain absent.
- `repoexec`: opt-in repo recovery executors. `repo_create` resolves metadata,
  acquires the create fence, runs JVS `init`/`doctor --strict`, and commits repo
  metadata, terminal operation, audit, and fence release through dedicated store
  boundaries. The repo lifecycle executor currently covers `repo_archive`,
  `repo_restore_archived`, `repo_delete`, and `repo_restore_tombstoned` behind
  an explicit worker gate with dedicated lifecycle store boundaries. `repo_purge`
  uses a separate explicit worker gate, dedicated store boundary, and storage
  purger for destructive AFSCP-managed retained storage removal.
- `workerapp`: production `afscp-worker --run-once` bootstrap for explicitly
  gated export session terminal reconcile, the opt-in metadata operation
  recovery runner, and the independent audit outbox stale-recovery plus HTTP
  JSON delivery runner. Export session reconcile runs before operation recovery
  when both are enabled.
- `pathresolver`: path safety helpers, denial tests, shared resolver corpus, and
  canonical internal repo root resolution from trusted volume roots plus repo
  IDs.

Repo create intake, repo lifecycle operation intake/admission, export
create/get/revoke handlers, namespace-bound repo read handlers, operation
inspection, and the WebDAV export gateway exist. Still intentionally absent:
JVS save/restore/template execution, workload mount issuance, save/restore and
template endpoint handlers beyond intake/admission, real external audit
delivery integration beyond the HTTP JSON at-least-once sink, repo/template
lifecycle mutation beyond the implemented lifecycle workers, per-request WebDAV
operation records or complex crash recovery for gateway runtime counts, and
fence enforcement beyond the minimal repo fence adapter slice.
The worker app currently wires export session terminal reconcile when
`AFSCP_EXPORT_SESSION_RECONCILE_ENABLED=true`, then
`volume_ensure`, `namespace_upsert`, `namespace_volume_binding_put`, and opt-in
`repo_create` plus `repo_archive`/`repo_restore_archived`/`repo_delete`/
`repo_restore_tombstoned`, plus separately gated `repo_purge` operation recovery
when explicitly enabled. It also wires audit outbox stale recovery and HTTP JSON
delivery when `AFSCP_WORKER_AUDIT_DELIVERY_ENABLED=true`; delivery is
at-least-once and the external sink must dedupe by `audit_event_id`.

Use [docs/DEVELOPER_HANDOFF.md](../docs/DEVELOPER_HANDOFF.md) for the current
handoff and next development order.
