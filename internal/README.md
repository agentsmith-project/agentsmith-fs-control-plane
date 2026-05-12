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
  gateway runtime request ledger accounting, terminal reconcile, and read-only repo recovery
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
  durable runtime request ledger accounting, and terminal reconcile, including internal
  template storage identity.
  RepoTemplate create/clone intake, recovery store boundaries, and gated worker
  executors are implemented.
- `audit`: audit event typing, redaction expectations, and pure outbox state
  transitions.
- `contractcheck`: contract verifier for OpenAPI/schema/docs/Go DTO guardrails.
- `fences`: pure repo fence model, held-state semantics, and acquisition checks.
- `repoaccess`: pure repo access admission model used by repo lifecycle
  intake/admission and save/restore, export, workload mount, and template
  control-plane admission/gating. It validates stored repo/binding/fence invariants and
  returns stable error-family decisions.
- `sessionstate`: pure export and workload-mount session substrate for
  restore-run writer gating and repo lifecycle drain gating. Export session
  state is now used by API export create/get/revoke, WebDAV gateway admission
  and runtime request ledger accounting, terminal reconcile, and opt-in repo archive/delete
  recovery drain checks. Workload mount issuance remains separate.
- `exportaccess`: WebDAV export session, credential, durable runtime request
  models, and terminal reconcile request/result models.
- `exportgateway`: WebDAV policy gateway handler and no-follow payload
  filesystem boundary. It enforces Basic auth, active/unexpired WebDAV session
  admission, mode/method policy, source and `Destination` path policy, payload
  no-follow traversal, and DB-backed runtime request ledger accounting.
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
- `jvsrunner`: JVS v0.4.9 CLI runner abstraction for fixed
  external-control-root JSON commands, including `init`, `doctor --strict`,
  strict doctor runtime repair, save/history/restore primitives, recovery
  status, and repo clone. Repo, save point, restore preview/discard/run, and
  template recovery workers wire these primitives behind explicit worker gates.
- `repoexec`: opt-in repo recovery executors. `repo_create` resolves metadata,
  acquires the create fence, runs JVS `init`/`doctor --strict`, and commits repo
  metadata, terminal operation, audit, and fence release through dedicated store
  boundaries. The repo lifecycle executor currently covers `repo_archive`,
  `repo_restore_archived`, `repo_delete`, and `repo_restore_tombstoned` behind
  an explicit worker gate with dedicated lifecycle store boundaries. `repo_purge`
  uses a separate explicit worker gate, dedicated store boundary, and storage
  purger for destructive AFSCP-managed retained storage removal.
- `workerapp`: production `afscp-worker --run-once` bootstrap for explicitly
  gated export session terminal reconcile, workload mount stale lease scan,
  restore reconciliation, operation recovery, audit stale recovery, and HTTP
  JSON audit delivery in the same order as the command entrypoint.
- `pathresolver`: path safety helpers, denial tests, shared resolver corpus, and
  canonical internal repo root resolution from trusted volume roots plus repo
  IDs.

Repo create intake, repo lifecycle operation intake/admission, save point,
restore preview/discard/run, repo template, export create/get/revoke, workload
mount binding, namespace-bound repo read handlers, operation inspection, and
the WebDAV export gateway exist. Still intentionally absent: host/orchestrator
mount application beyond control-plane workload mount binding issuance, real
external audit delivery integration beyond the HTTP JSON at-least-once sink,
repo/template lifecycle mutation beyond the implemented lifecycle workers,
per-request WebDAV operation records outside the dedicated runtime ledger, and
fence enforcement beyond the minimal repo fence adapter slice.
The worker app currently wires export session terminal reconcile when
`AFSCP_EXPORT_SESSION_RECONCILE_ENABLED=true`, workload mount stale lease scans
when `AFSCP_WORKLOAD_MOUNT_STALE_LEASE_RECONCILE_ENABLED=true`, restore
reconciliation when `AFSCP_RESTORE_RECONCILIATION_ENABLED=true`, then operation
recovery when `AFSCP_WORKER_OPERATION_RECOVERY_ENABLED=true`. Operation recovery
includes the metadata executors, workload mount binding recovery, and opt-in
JVS/storage-backed repo lifecycle, save point, restore, and template recovery
executors behind their corresponding gates. It also wires audit outbox stale
recovery and HTTP JSON delivery when
`AFSCP_WORKER_AUDIT_DELIVERY_ENABLED=true`; delivery is at-least-once and the
external sink must dedupe by `audit_event_id`.

Use [docs/DEVELOPER_HANDOFF.md](../docs/DEVELOPER_HANDOFF.md) for the current
handoff and next development order.
