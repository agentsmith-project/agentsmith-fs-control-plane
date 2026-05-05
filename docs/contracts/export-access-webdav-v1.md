# Contract: Export Access WebDAV V1

Status: P0 review draft

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

- `protocol` is `webdav` for P0.
- `mode` is `read_only` or `read_write`.
- Credentials are short-lived and revocable.
- Expired or revoked credentials must fail future requests and close or reject active sessions where supported by the gateway.
- Exports are rooted at the repo payload root and never expose the JVS control root.
- Root-level `.jvs` access or creation attempts are denied for every WebDAV method as defense-in-depth.
- Read-only exports allow `OPTIONS`, `HEAD`, `GET`, and `PROPFIND` only, with root-level `.jvs` still denied.
- Read-only exports deny `PUT`, `DELETE`, `MKCOL`, `MOVE`, `COPY`, `PROPPATCH`, `LOCK`, and `UNLOCK` unless the gateway implements a no-op lock required for read-only client compatibility.
- AFSCP must enforce this through its own WebDAV policy gateway or an equivalent wrapper. Stock `juicefs webdav` alone is not the P0 policy boundary.
- No JuiceFS metadata URL, bucket URL, object store credential, raw mount command, or Secret reference appears in the response.
- Export create, credential issuance, revoke, expiry, and denied path attempts are audited.
