# Contract: Workspace Storage Profile V1

Status: draft

Workspace storage profile is owned by AgentSmith API and executed by AFSCP.

## Required Fields

- `tenant_workspace_id`
- `afscp_endpoint_id`
- `default_filesystem_id`
- `default_storage_pool_id`
- `quota_bytes_default`
- `export_policy`
- `template_policy`
- `status`

## Rules

- A profile is selected when creating new file libraries.
- Changing a profile affects new repos only.
- Existing repos require explicit migration.
- AgentSmith API must not provide an authoritative raw filesystem path; AFSCP computes and owns the canonical workspace root for each storage pool.
- `allow_cross_workspace_clone` must not exist in P0.
- Template policy may enable workspace templates but not cross-workspace templates.
