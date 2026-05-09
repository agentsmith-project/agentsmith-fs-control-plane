# Commands

The repo now has neutral Go command entrypoints:

- `afscp-api`: validates config, can build or serve the neutral API shell, and
  exposes `/healthz`, `/readyz`, route metadata fallback, standard error
  envelopes, request logging, capability-denied guardrails, metadata-only
  namespace handlers, repo create intake, repo lifecycle intake/admission,
  namespace-bound repo read handlers, operation inspection, WebDAV export
  create/get/revoke, workload mount issuance/plan/status flows, save/restore
  flows, and namespace-scoped template create/clone intake.
- `afscp-worker`: bounded async worker entrypoint. `--run-once` defaults to
  fail-closed unless at least one worker gate is enabled. A run-once pass runs
  export session reconcile, workload mount stale lease scan, restore
  reconciliation, operation recovery, audit stale recovery, and audit outbox
  delivery in that order. Export session terminal reconcile is enabled by
  `AFSCP_EXPORT_SESSION_RECONCILE_ENABLED=true`; it uses
  `AFSCP_EXPORT_SESSION_RECONCILE_POSTGRES_DSN`, falling back to
  `AFSCP_POSTGRES_DSN` and then `AFSCP_DATABASE_URL`, plus
  `AFSCP_EXPORT_SESSION_RECONCILE_OWNER` and
  `AFSCP_EXPORT_SESSION_RECONCILE_LIMIT`. Workload mount stale lease scanning is
  enabled by `AFSCP_WORKLOAD_MOUNT_STALE_LEASE_RECONCILE_ENABLED=true`; it uses
  `AFSCP_WORKLOAD_MOUNT_STALE_LEASE_RECONCILE_POSTGRES_DSN`, falling back to
  `AFSCP_POSTGRES_DSN` and then `AFSCP_DATABASE_URL`, plus
  `AFSCP_WORKLOAD_MOUNT_STALE_LEASE_RECONCILE_LIMIT`. Restore reconciliation is
  enabled by `AFSCP_RESTORE_RECONCILIATION_ENABLED=true`; it uses
  `AFSCP_POSTGRES_DSN`, falling back to `AFSCP_DATABASE_URL`, plus
  `AFSCP_RESTORE_RECONCILIATION_OWNER` and
  `AFSCP_RESTORE_RECONCILIATION_LIMIT`. Operation recovery is enabled by the
  top-level `AFSCP_WORKER_OPERATION_RECOVERY_ENABLED=true` gate and uses
  `AFSCP_POSTGRES_DSN`, falling back to `AFSCP_DATABASE_URL`, plus
  `AFSCP_WORKER_OWNER`, `AFSCP_OPERATION_RECOVERY_LIMIT`, and
  `AFSCP_OPERATION_RECOVERY_LEASE_DURATION`. When enabled, operation recovery
  always wires the PostgreSQL executors for `volume_ensure`,
  `namespace_upsert`, `namespace_disable`, `namespace_volume_binding_put`, and
  workload mount binding recovery. The JVS/storage-backed recovery executors
  are separately gated by `AFSCP_REPO_CREATE_RECOVERY_ENABLED`,
  `AFSCP_REPO_LIFECYCLE_RECOVERY_ENABLED`,
  `AFSCP_REPO_PURGE_RECOVERY_ENABLED`, `AFSCP_SAVE_POINT_RECOVERY_ENABLED`,
  `AFSCP_TEMPLATE_CREATE_RECOVERY_ENABLED`,
  `AFSCP_TEMPLATE_CLONE_RECOVERY_ENABLED`,
  `AFSCP_RESTORE_PREVIEW_RECOVERY_ENABLED`,
  `AFSCP_RESTORE_PREVIEW_DISCARD_RECOVERY_ENABLED`, and
  `AFSCP_RESTORE_RUN_RECOVERY_ENABLED`; those true gates require the pinned JVS
  path/hash/cwd and `AFSCP_VOLUME_ROOTS`. Audit delivery is independently
  enabled by `AFSCP_WORKER_AUDIT_DELIVERY_ENABLED=true`; it runs stale outbox
  recovery then HTTP JSON at-least-once delivery, and the external sink must
  dedupe by `audit_event_id`.
  A run-once pass emits its summary, then exits nonzero if export session
  reconcile fails or reports failed records, if
  workload mount stale lease scan fails or reports failed records, if operation
  recovery reports unsupported, manual, or failed records, if audit stale
  recovery itself fails, or if audit delivery cannot mark a record
  delivered/failed or records a concrete delivery failure. Successful stale
  recovery that schedules `retry_wait` replay is not itself a run-once failure.
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
