# Contract: Repo Path V1

Status: draft

AFSCP resolves repo paths from structured IDs.

## Inputs

- `namespace_id`
- `repo_id`
- `repo_kind`
- `volume_id`

## Outputs

- `repo_path`
- `payload_subdir`
- `jvs_control_path`

## Rules

- Callers do not pass raw paths.
- Display names do not affect paths.
- Paths are stable for repo lifetime unless a lifecycle operation explicitly moves them.
- `repo_path` is the JVS `main` workspace real folder.
- In P0, `payload_subdir` is the same directory as `repo_path`.
- `.jvs` is never exposed through user/export payload paths.
- Canonical namespace root mismatch is an authorization boundary violation.
