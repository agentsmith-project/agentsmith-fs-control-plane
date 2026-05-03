# Contract: Storage Repo Path V1

Status: draft

AFSCP resolves repo paths from structured IDs.

## Inputs

- `tenant_workspace_id`
- `storage_repo_id`
- `repo_kind`
- `filesystem_id`
- `storage_pool_id`

## Outputs

- `repo_path`
- `payload_subdir`
- `jvs_control_path`

## Rules

- Callers do not pass raw paths.
- Display names do not affect paths.
- Paths are stable for repo lifetime unless a lifecycle operation explicitly moves them.
- `.jvs` is never exposed through user/export payload paths.
- Workspace path prefix mismatch is an authorization boundary violation.
