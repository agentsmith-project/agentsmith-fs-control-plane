# Sandbox Binding V2

Sandbox binding v2 should move new libraries away from caller-provided `metadata_url` and toward AFSCP-generated mount specs.

## Current Problem

Existing sandbox binding contracts can include:

- `file_library_id`
- `filesystem_name`
- `metadata_url`
- `subdir`

This leaks too much storage implementation detail into AgentSmith provisioning and sandbox-manager.

## Target Contract

New AFSCP-backed libraries should use a v2 mount spec with structured IDs and an AFSCP-resolved subdirectory.

Draft:

```json
{
  "tenant_workspace_id": "ws_123",
  "storage_repo_id": "repo_123",
  "filesystem_id": "jfs_default",
  "storage_pool_id": "pool_default",
  "payload_subdir": "/agentsmith/workspaces/ws_123/repos/repo_123/workspace",
  "mount_path": "/workspace",
  "read_only": false,
  "secret_ref": {
    "namespace": "sandbox-system",
    "name": "juicefs-pool-default"
  }
}
```

The final field names should be agreed with sandbox-manager owners.

## Responsibilities

AFSCP:

- resolves `payload_subdir`
- chooses filesystem/storage pool
- chooses Secret reference
- validates workspace/repo consistency

sandbox-manager:

- creates or updates Secret/PV/PVC/Pod mount
- reports binding status
- does not make product authorization decisions

workload Pod:

- sees only mounted payload path
- receives no JuiceFS root credentials
- should run non-root by default

## Compatibility

Binding v1 should remain for legacy libraries during migration. New AFSCP-backed libraries should use v2.
