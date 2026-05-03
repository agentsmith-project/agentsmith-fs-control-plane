# WebDAV Export

Client access should use controlled exports. P0 export protocol is WebDAV.

## Current Problem

Ordinary client flows should not receive JuiceFS direct mount information such as `metadata_url`. This exposes backend details and makes shared volumes difficult to protect.

## Target Flow

```text
Client -> Calling Product -> AFSCP -> WebDAV export runtime
```

1. Client requests access through a calling product.
2. Calling product checks product authorization.
3. Calling product asks AFSCP to create an export for a repo.
4. AFSCP creates an `ExportSession` and returns short-lived access credentials.
5. Calling product returns the credential view to the client.

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

Do not return JuiceFS metadata URL, bucket URL, access key, or secret key.

## P0 Requirements

- Support read-only and read-write modes.
- Support TTL and revoke.
- Log access with correlation/export IDs.
- Chroot export to repo root.
- Hide or block `.jvs`.
- Reject path traversal.
- Apply the same resolver/filter policy to GET, PUT, DELETE, MKCOL, MOVE, COPY, PROPFIND, PROPPATCH, LOCK, and UNLOCK.
- Reject creation, rename, copy, move, or propfind of root-level `.jvs`.
- Reject encoded or double-encoded path traversal and `.jvs` bypass attempts.
- Prevent symlink escape from the exported repo root.
- Expired or revoked credentials must fail future requests and close or reject active sessions where supported.
- Redact credentials from logs.
- Audit export create, credential issuance, revoke, expiry, and denied path attempts.

## Method Policy

All methods use the same path resolver and `.jvs` filter.

| Method | read_only | read_write |
| --- | --- | --- |
| `OPTIONS` | allow | allow |
| `HEAD` | allow | allow |
| `GET` | allow | allow |
| `PROPFIND` | allow except `.jvs` | allow except `.jvs` |
| `PUT` | deny | allow except `.jvs` |
| `DELETE` | deny | allow except `.jvs` |
| `MKCOL` | deny | allow except `.jvs` |
| `MOVE` | deny | allow except `.jvs` source or destination |
| `COPY` | deny | allow except `.jvs` source or destination |
| `PROPPATCH` | deny | allow except `.jvs` |
| `LOCK` | deny unless gateway requires no-op read lock | allow except `.jvs` |
| `UNLOCK` | deny unless paired with allowed no-op lock | allow except `.jvs` |

Denied mutating method attempts on read-only exports must be audited with export ID, method, normalized path, actor/caller context, and reason.

## P1 Options

- SMB/NFS export.
- Export gateway pool.
- Better client reconnect and diagnostics.
- Admin/debug raw JuiceFS mount behind break-glass controls.
