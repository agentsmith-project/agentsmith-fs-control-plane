# Contract: Export Access WebDAV V1

Status: GA implementation-baseline contract. FINAL GA is governed by
`docs/GA_RELEASE_GATES.md`, `docs/READINESS_EVIDENCE.md`, and
`scripts/verify-ga-release.sh`.

`ExportAccessCredential` replaces ordinary direct JuiceFS mount access for clients. AFSCP also stores a durable `ExportSession` for revocation, TTL, and audit.

`POST /internal/v1/repos/{repoId}/exports` is a synchronous durable boundary:
the operation row, export session row, and succeeded audit event are committed
together before the response is returned.

## ExportSession Fields

- `export_id`
- `namespace_id`
- `repo_id`
- `protocol`
- `mode`
- `status`
- `created_by_caller_service`
- `created_by_actor`
- `created_at`
- `expires_at`
- `revoked_at`
- `last_accessed_at`
- `active_request_count`
- `active_write_count`
- `last_observed_at`
- `last_gateway_heartbeat_at`
- `gateway_heartbeat_expires_at`
- `write_drained_at`
- `terminal_observed_at`
- `status_reason`

`created_by_actor` uses the common `Actor` object. `revoked_at` and
`last_accessed_at` are durable fields that are present on every `ExportSession`
and may be `null` until revocation or first observed gateway access.
The runtime accounting fields are backed by a dedicated
`export_runtime_requests` ledger, not by per-request operation rows or per-file
records. Each admitted WebDAV request uses one durable runtime request ID:
begin inserts an open ledger row and increments active counts atomically,
heartbeat refreshes that same row, and end closes only that open row and
decrements counts idempotently. Positive begins are admitted only when the
session is still `active` and unexpired at the DB boundary.

## Credential View

- `export_id`
- `protocol`
- `url`
- `auth.type`
- `auth.username`
- `auth.password`
- `mode`
- `expires_at`

The credential view is returned only on the first successful create for an
idempotency key. Idempotent replay returns the existing operation/session
without `access`. `GET /internal/v1/exports/{exportId}` returns only the
redacted `ExportSession` and never returns the WebDAV password again.

## Rules

- `protocol` is `webdav` for GA.
- `mode` is `read_only` or `read_write`.
- Credentials are short-lived and revocable. The default TTL is 3600 seconds,
  the minimum accepted TTL is 60 seconds, and the maximum TTL comes from
  namespace export policy (`max_session_seconds`).
- Expired or revoked credentials must fail future requests. Read-write exports remain active or uncertain writer sessions until the gateway confirms no future writes are possible and any active write-capable requests are closed, expired and reconciled, or terminal.
- Any export, read-only or read-write, blocks repo archive/delete/purge lifecycle drain until the gateway confirms no future access is possible and active requests are closed, expired and reconciled, or terminal.
- Exports are rooted at the repo payload root and never expose the JVS control root.
- Root-level `.jvs` access or creation attempts are denied for every WebDAV method as defense-in-depth.
- Read-only exports allow `OPTIONS`, `HEAD`, `GET`, and `PROPFIND` only, with root-level `.jvs` still denied.
- Read-only exports deny `PUT`, `DELETE`, `MKCOL`, `MOVE`, `COPY`, `PROPPATCH`, `LOCK`, and `UNLOCK` unless the gateway implements a no-op lock required for read-only client compatibility.
- AFSCP must enforce this through its own WebDAV policy gateway or an equivalent wrapper. Stock `juicefs webdav` alone is not the GA policy boundary.
- The current `afscp-export-gateway --serve` path enforces Basic auth, active
  and unexpired session admission, mode/method policy, source path and
  `Destination` policy for `MOVE`/`COPY`, payload no-follow filesystem access,
  and durable runtime request ledger accounting.
- No JuiceFS metadata URL, bucket URL, object store credential, raw mount command, or Secret reference appears in the response.
- Export create, credential issuance, revoke, expiry, and denied path attempts are audited.

## Credential Lifecycle

GA freezes:

- default TTL: 3600 seconds
- minimum TTL: 60 seconds
- maximum TTL: namespace export policy `max_session_seconds`; policy values
  below 60 seconds are invalid
- no credential reissue endpoint and no repeat display behavior
- credential secrets are returned only in the first successful create response
- idempotent create replay omits `access`
- stored credential material is persisted only as verifier material, not as the
  raw WebDAV password
- revoke moves the session into `revoking` for gateway drain; terminal
  `revoked`, `expired`, or `failed` requires gateway/reconcile confirmation
- terminal reconcile atomically commits `export_session_reconcile`, the terminal
  session update, and the audit outbox event. The current reconcile runner first
  recovers stale open runtime request rows whose heartbeat expiry has elapsed,
  verifies aggregate counts can cover those rows, subtracts the recovered
  counts, then terminalizes zero-count `revoking -> revoked` and zero-count
  expired `active -> expired` sessions only when no open runtime request rows
  remain; drift where aggregate counts cannot cover stale ledger rows fails
  closed
- no per-request WebDAV operation rows; runtime request rows are a dedicated
  gateway ledger and must not store password, verifier, host path, or `.jvs`
  path material
- expiration reconciliation behavior
- access log fields and redaction rules

Read-write export sessions count as active or uncertain writer sessions until
the gateway confirms no future writes are possible and any active write-capable
requests are closed, expired and reconciled, or terminal. A control-plane revoke
record alone does not unblock direct restore. Read-only exports do not block
direct restore, but still must respect namespace disable, credential expiry, path
policy, audit, and repo lifecycle drain.
