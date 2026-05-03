# Contract: Export Access WebDAV V1

Status: draft

`ExportAccess` replaces ordinary Desktop direct JuiceFS mount access.

## Fields

- `export_id`
- `protocol`
- `url`
- `username`
- `password`
- `mode`
- `expires_at`

## Rules

- `protocol` is `webdav` for P0.
- `mode` is `read_only` or `read_write`.
- Credentials are short-lived and revocable.
- `.jvs` is hidden or blocked.
- No JuiceFS metadata URL or object store credential appears in the response.
