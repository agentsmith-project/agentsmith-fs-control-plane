# Commands

The repo now has neutral Go command entrypoints:

- `afscp-api`: validates config, can build or serve the neutral API shell, and
  exposes `/healthz`, `/readyz`, route metadata fallback, standard error
  envelopes, request logging, capability-denied guardrails, metadata-only
  namespace handlers, repo create intake, repo lifecycle intake/admission,
  namespace-bound repo read handlers, plus operation inspection. It does not
  implement repo lifecycle workers/storage mutation, JVS lifecycle, WebDAV,
  mount, save/restore, or template handlers yet.
- `afscp-worker`: bounded async worker entrypoint. `--run-once` defaults to
  fail-closed unless at least one worker gate is enabled. Export session
  terminal reconcile is enabled by
  `AFSCP_EXPORT_SESSION_RECONCILE_ENABLED=true`; it uses
  `AFSCP_EXPORT_SESSION_RECONCILE_POSTGRES_DSN`, falling back to
  `AFSCP_POSTGRES_DSN` and then `AFSCP_DATABASE_URL`, plus
  `AFSCP_EXPORT_SESSION_RECONCILE_OWNER` and
  `AFSCP_EXPORT_SESSION_RECONCILE_LIMIT`. In a run-once pass, export session
  reconcile runs before operation recovery. Operation recovery is enabled by
  `AFSCP_WORKER_OPERATION_RECOVERY_ENABLED=true`; when enabled it wires
  PostgreSQL operation recovery for the minimal
  `volume_ensure`, `namespace_upsert`, `namespace_volume_binding_put`, and
  explicit-gated `repo_create` plus `repo_archive`/`repo_restore_archived`/
  `repo_delete`/`repo_restore_tombstoned` executors, plus separately gated
  `repo_purge` recovery for destructive AFSCP-managed retained storage removal.
  Audit delivery is independently enabled by
  `AFSCP_WORKER_AUDIT_DELIVERY_ENABLED=true`; it runs stale outbox recovery then
  HTTP JSON at-least-once delivery, and the external sink must dedupe by
  `audit_event_id`.
  A run-once pass emits its summary, then exits nonzero if export session
  reconcile fails or reports failed records, if operation recovery reports
  unsupported, manual, or failed records, if audit stale recovery itself fails,
  or if audit delivery cannot mark a record delivered/failed or records a
  concrete delivery failure. Successful stale recovery that schedules
  `retry_wait` replay is not itself a run-once failure.
- `afscp-export-gateway`: WebDAV export policy gateway. `--dry-run` validates
  gateway configuration; `--serve` serves WebDAV under the configured prefix
  using Basic auth, export session credential/admission checks, mode/method
  policy, source and `Destination` path policy, payload no-follow filesystem
  access, and durable runtime observation.
- `afscp-contract-verify`: verifies selected OpenAPI, schema, docs, and Go DTO
  contract guardrails.

Useful checks:

```bash
bash scripts/verify-ga-baseline.sh
```
