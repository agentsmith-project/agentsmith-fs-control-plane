# WebDAV Export

Desktop and Web access should use controlled exports. P0 export protocol is WebDAV.

## Current Problem

The ordinary Desktop path can receive JuiceFS direct mount information such as `metadata_url`. This exposes backend details and makes it hard to use one shared JuiceFS filesystem safely.

## Target Flow

```text
Desktop/Web -> AgentSmith API -> AFSCP -> WebDAV export runtime
```

1. Client requests access through AgentSmith API.
2. AgentSmith checks product authorization.
3. AgentSmith asks AFSCP to create an export for a repo.
4. AFSCP returns a short-lived `ExportAccess`.
5. AgentSmith returns `ExportAccess` to the client.

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
- Chroot export to repo payload directory.
- Hide or block `.jvs`.
- Reject path traversal.
- Redact credentials from logs.

## P1 Options

- SMB/NFS export.
- Export gateway pool.
- Better Desktop reconnect and diagnostics.
- Admin/debug raw JuiceFS mount behind feature flag.
