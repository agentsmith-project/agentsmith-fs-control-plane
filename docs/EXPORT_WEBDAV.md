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
4. AFSCP returns a short-lived `ExportAccess`.
5. Calling product returns `ExportAccess` to the client.

## ExportAccess

```json
{
  "export_id": "export_123",
  "protocol": "webdav",
  "url": "https://files.example.com/e/export_123/",
  "username": "export_123",
  "password": "short-lived-secret",
  "mode": "read_write",
  "expires_at": "2026-05-03T12:00:00Z"
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
- Redact credentials from logs.

## P1 Options

- SMB/NFS export.
- Export gateway pool.
- Better client reconnect and diagnostics.
- Admin/debug raw JuiceFS mount behind feature flag.
