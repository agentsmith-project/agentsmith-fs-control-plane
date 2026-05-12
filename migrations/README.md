# Migrations

PostgreSQL migrations for the control-plane persistence boundary live in this
directory.

The first migration defines only durable primitives for operations,
idempotency, audit outbox delivery, and repo-scoped fences. It deliberately does
not implement a store adapter, endpoint handler, or storage-side action.

## Files

- `0001_control_plane_persistence.sql`: operation records, atomic idempotency
  uniqueness, audit outbox rows, and repo fence lifecycle fields.
- `0002_export_sessions_terminal_zero_counts.sql`: backfill/upgrade guard that
  adds the terminal export-session zero-count check for environments that
  already applied `0001`.
- `0003_export_runtime_request_ledger.sql`: dedicated WebDAV export runtime
  request ledger for durable begin/heartbeat/end accounting and stale open
  request recovery without creating per-request operation rows.
- `0004_restore_reconciliation.sql`: restore reconciliation run, target, and
  observation tables for operator-visible storage mismatch closure.
- `0005_restore_plan_preview_metadata.sql`: upgrade guard for environments that
  already applied an older `0001`; adds durable restore-plan preview metadata,
  stale marker, and blockers source-of-truth columns.
- `0006_save_point_repo_busy_terminalization.sql`: terminalizes legacy save
  point create operations that were left in operator intervention for JVS
  `E_REPO_BUSY`, preserving a complete retryable operation error envelope.

## Contract

The migration contract is verified by `internal/store` tests. Keep schema
changes structural and explicit so future adapters cannot rely on
list-then-create idempotency checks, omit audit outbox rows, or bypass active
fence uniqueness.
