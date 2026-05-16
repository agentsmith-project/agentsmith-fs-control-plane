# WebDAV Export

Client access should use controlled exports. GA export protocol is WebDAV.

## Current Problem

Ordinary client flows should not receive JuiceFS direct mount information such as `metadata_url`. This exposes backend details and makes shared volumes difficult to protect.

## Target Flow

```text
Client -> Calling Product -> AFSCP -> WebDAV export runtime
```

1. Client requests access through a calling product.
2. Calling product checks product authorization.
3. Calling product asks AFSCP to create an export for a repo.
4. AFSCP atomically commits the succeeded operation, `ExportSession`, and audit
   event, then returns short-lived access credentials only for the first
   successful create for that idempotency key.
5. Calling product returns the one-time credential view to the client. A replay
   returns the existing redacted session without `access`.

## Export Session And Access Credential

```json
{
  "export_id": "export_123",
  "namespace_id": "ns_123",
  "repo_id": "repo_123",
  "protocol": "webdav",
  "mode": "read_write",
  "status": "active",
  "expires_at": "2026-05-03T12:00:00Z",
  "access": {
    "url": "https://files.example.com/e/export_123/",
    "auth": {
      "type": "basic",
      "username": "export_123",
      "password": "short-lived-secret"
    }
  }
}
```

`GET /internal/v1/exports/{exportId}` returns only the redacted
`ExportSession`; it does not include `access` or the WebDAV password. Do not
return JuiceFS metadata URL, bucket URL, access key, or secret key.

## GA Requirements

- Support read-only and read-write modes.
- Support TTL and revoke.
- Log access with correlation/export IDs.
- Chroot export to the repo payload root.
- Never expose JVS control metadata.
- Reject path traversal.
- Apply the same resolver/filter policy to GET, PUT, DELETE, MKCOL, MOVE, COPY, PROPFIND, PROPPATCH, LOCK, and UNLOCK.
- Reject creation, rename, copy, move, or propfind of root-level `.jvs` as defense-in-depth and legacy safety.
- Reject encoded or double-encoded path traversal and `.jvs` bypass attempts.
- Prevent symlink escape from the exported payload root.
- Expired or revoked credentials must fail future requests. Read-write exports remain active or uncertain writer sessions until the gateway confirms no future writes are possible and any active write-capable requests are closed, expired and reconciled, or terminal.
- Any export, read-only or read-write, blocks repo archive/delete/purge lifecycle drain until the gateway confirms no future access is possible and active requests are closed, expired and reconciled, or terminal.
- Redact credentials from logs.
- Audit export create, credential issuance, revoke, expiry, and denied path attempts.
- Default TTL is 3600 seconds, minimum TTL is 60 seconds, and maximum TTL is the
  namespace export policy `max_session_seconds`.
- Credential secrets are returned only in the first successful create response;
  there is no reissue endpoint and idempotent replay omits `access`.
- Define when read-write exports count as active writer sessions for direct restore to a save point and the writer-session fence.
- Store credential material hashed or encrypted according to the accepted security contract.

## Gateway Requirement

GA WebDAV export must be served by an AFSCP-controlled policy gateway. Stock `juicefs webdav` can be a reference or backend capability, but it is not enough by itself because AFSCP must apply the canonical path resolver, payload-root chroot, and method policy across every method, including `MOVE` and `COPY` destination paths.

Do not rely on a directory listing deny list, a reverse proxy path prefix check, or a method allowlist as the complete policy boundary.

Current implementation: `afscp-export-gateway --serve` serves the WebDAV policy
gateway. It enforces Basic auth against durable export credential verifier
material, admits only active and unexpired WebDAV sessions, applies read-only
or read-write mode policy per method, validates both source paths and
`Destination` paths for `MOVE`/`COPY`, serves only the payload root through a
no-follow filesystem boundary, and records runtime request ledger entries
durably.

## Method Policy

All methods use the same payload-root resolver. Root-level `.jvs` source or destination paths are denied even though AFSCP-managed payload roots should not contain `.jvs`.

| Method | read_only | read_write |
| --- | --- | --- |
| `OPTIONS` | allow | allow |
| `HEAD` | allow | allow |
| `GET` | allow | allow |
| `PROPFIND` | allow except root `.jvs` | allow except root `.jvs` |
| `PUT` | deny | allow except root `.jvs` |
| `DELETE` | deny | allow except root `.jvs` |
| `MKCOL` | deny | allow except root `.jvs` |
| `MOVE` | deny | allow except root `.jvs` source or destination |
| `COPY` | deny | allow except root `.jvs` source or destination |
| `PROPPATCH` | deny | allow except root `.jvs` |
| `LOCK` | deny unless gateway requires no-op read lock | allow except root `.jvs` |
| `UNLOCK` | deny unless paired with allowed no-op lock | allow except root `.jvs` |

Denied mutating method attempts on read-only exports must be audited with export ID, method, normalized path, actor/caller context, and reason.

## Credential And Session Semantics

- Credentials are short-lived. Default TTL is 3600 seconds, minimum TTL is 60
  seconds, and maximum TTL is the namespace export policy
  `max_session_seconds`; policy values below 60 seconds are invalid.
- Secret-bearing credentials are returned only at first create. Idempotent
  replay and `GET /internal/v1/exports/{exportId}` return only the redacted
  `ExportSession`.
- Credential reissue is not supported for GA.
- Revoked or expired credentials fail new requests.
- Revoke moves the session to `revoking` for gateway drain; terminal `revoked`,
  `expired`, or `failed` requires gateway or reconcile confirmation.
- New request admission is guarded at the DB runtime-request boundary:
  durable request begin is accepted only while the session is still `active`
  and `expires_at` is in the future. This prevents revoke/expiry TOCTOU between
  gateway credential lookup and active-count accounting.
- Active write-capable requests after revoke must be closed or allowed to reach a terminal state before the export stops counting as an active or uncertain writer.
- Read-write exports count as active or uncertain writer sessions until the gateway confirms no future writes are possible and any active write-capable requests are closed, expired and reconciled, or terminal.
- A control-plane revoke record alone does not unblock direct restore.
- Read-only exports do not block direct restore, but namespace disable and credential revoke/expiry still apply.
- Read-only exports do block repo archive/delete/purge lifecycle drain until gateway reconciliation confirms there is no ongoing or future access through that export.
- Gateway runtime state is stored as aggregate active request/write counts plus
  a dedicated `export_runtime_requests` ledger. Each admitted WebDAV request
  receives one durable runtime request ID; begin inserts an open ledger row and
  increments aggregate counts atomically, heartbeat refreshes that same row,
  and end closes only that open row and decrements counts idempotently. GA does
  not persist per-file locks, per-request operation rows, or WebDAV operation
  records.
- Terminal export reconcile is the GA terminal boundary: it commits the
  `export_session_reconcile` operation, terminal session update, and audit
  outbox event atomically. The current reconcile runner first recovers stale
  open runtime request rows whose heartbeat expiry has elapsed, verifies the
  aggregate counts can cover those rows, subtracts the recovered counts, then
  terminalizes zero-count `revoking -> revoked` and zero-count expired
  `active -> expired` sessions only when no open runtime request rows remain.
  Drift where aggregate counts cannot cover the stale ledger rows fails closed.
  Bare status updates are not the GA terminal boundary.
- Access logs must include export ID, correlation ID when available, method, normalized path, result, and denial reason without credential material.

## Future Options

- SMB/NFS export.
- Export gateway pool.
- Better client reconnect and diagnostics.
- Admin/debug raw JuiceFS mount behind break-glass controls.
