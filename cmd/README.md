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
  fail-closed unless at least one worker gate is enabled. Operation recovery is
  enabled by `AFSCP_WORKER_OPERATION_RECOVERY_ENABLED=true`; when enabled it
  wires PostgreSQL operation recovery for the minimal
  `volume_ensure`, `namespace_upsert`, `namespace_volume_binding_put`, and
  explicit-gated `repo_create` plus `repo_archive`/`repo_restore_archived`/
  `repo_delete`/`repo_restore_tombstoned` executors, plus separately gated
  `repo_purge` recovery for destructive AFSCP-managed retained storage removal.
  Audit delivery is independently enabled by
  `AFSCP_WORKER_AUDIT_DELIVERY_ENABLED=true`; it runs stale outbox recovery then
  HTTP JSON at-least-once delivery, and the external sink must dedupe by
  `audit_event_id`.
  A run-once pass emits its summary, then exits nonzero if operation recovery
  reports unsupported, manual, or failed records, if audit stale recovery itself
  fails, or if audit delivery cannot mark a record delivered/failed or records a
  concrete delivery failure. Successful stale recovery that schedules
  `retry_wait` replay is not itself a run-once failure.
- `afscp-export-gateway`: versioned placeholder entrypoint for the WebDAV
  gateway. It has no WebDAV file access or export-session enforcement yet.
- `afscp-contract-verify`: verifies selected OpenAPI, schema, docs, and Go DTO
  contract guardrails.

Useful checks:

```bash
go test -count=1 ./...
go run ./cmd/afscp-contract-verify \
  -openapi api/openapi/internal-v1.openapi.yaml \
  -schema api/schemas/afscp-internal-v1.schema.json \
  -api-contract docs/contracts/afscp-internal-api-v1.md \
  -api-draft docs/API_CONTRACT_DRAFT.md
```
