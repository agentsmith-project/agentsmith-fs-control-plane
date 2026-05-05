# Migrations

PostgreSQL migrations for the control-plane persistence boundary live in this
directory.

The first migration defines only durable primitives for operations,
idempotency, audit outbox delivery, and repo-scoped fences. It deliberately does
not implement a store adapter, endpoint handler, or storage-side action.

## Files

- `0001_control_plane_persistence.sql`: operation records, atomic idempotency
  uniqueness, audit outbox rows, and repo fence lifecycle fields.

## Contract

The migration contract is verified by `internal/store` tests. Keep schema
changes structural and explicit so future adapters cannot rely on
list-then-create idempotency checks, omit audit outbox rows, or bypass active
fence uniqueness.
