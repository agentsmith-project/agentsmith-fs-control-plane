# Contract: Export Access WebDAV V1

Status: GA pre-dev review draft

`ExportAccessCredential` replaces ordinary direct JuiceFS mount access for clients. AFSCP also stores a durable `ExportSession` for revocation, TTL, and audit.

## ExportSession Fields

- `export_id`
- `namespace_id`
- `repo_id`
- `protocol`
- `mode`
- `status`
- `created_by_caller_service`
- `authorized_actor_type`
- `authorized_actor_id`
- `expires_at`
- `revoked_at`
- `last_accessed_at`

## Credential View

- `export_id`
- `protocol`
- `url`
- `auth.type`
- `auth.username`
- `auth.password`
- `mode`
- `expires_at`

## Rules

- `protocol` is `webdav` for GA.
- `mode` is `read_only` or `read_write`.
- Credentials are short-lived and revocable.
- Expired or revoked credentials must fail future requests. Read-write exports remain active or uncertain writer sessions until the gateway confirms no future writes are possible and any active write-capable requests are closed, expired and reconciled, or terminal.
- Any export, read-only or read-write, blocks repo archive/delete/purge lifecycle drain until the gateway confirms no future access is possible and active requests are closed, expired and reconciled, or terminal.
- Exports are rooted at the repo payload root and never expose the JVS control root.
- Root-level `.jvs` access or creation attempts are denied for every WebDAV method as defense-in-depth.
- Read-only exports allow `OPTIONS`, `HEAD`, `GET`, and `PROPFIND` only, with root-level `.jvs` still denied.
- Read-only exports deny `PUT`, `DELETE`, `MKCOL`, `MOVE`, `COPY`, `PROPPATCH`, `LOCK`, and `UNLOCK` unless the gateway implements a no-op lock required for read-only client compatibility.
- AFSCP must enforce this through its own WebDAV policy gateway or an equivalent wrapper. Stock `juicefs webdav` alone is not the GA policy boundary.
- No JuiceFS metadata URL, bucket URL, object store credential, raw mount command, or Secret reference appears in the response.
- Export create, credential issuance, revoke, expiry, and denied path attempts are audited.

## Credential Lifecycle

GA must freeze:

- default TTL and maximum TTL
- whether credential secrets are returned only at create time or can be reissued
- hashing or encryption at rest for stored credential material
- revoke behavior for future requests and active requests
- when a read-write export counts as an active writer session
- expiration reconciliation behavior
- access log fields and redaction rules

Read-write export sessions count as active or uncertain writer sessions until
the gateway confirms no future writes are possible and any active write-capable
requests are closed, expired and reconciled, or terminal. A control-plane revoke
record alone does not unblock restore-run. Read-only exports do not block
restore-run, but still must respect namespace disable, credential expiry, path
policy, audit, and repo lifecycle drain.
