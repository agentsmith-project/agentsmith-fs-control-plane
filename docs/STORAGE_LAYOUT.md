# Storage Layout

AFSCP should manage one or more JuiceFS filesystems through storage pools. MVP should bootstrap a default shared JuiceFS filesystem/storage pool for new file libraries.

## Logical Layout

Suggested layout under the JuiceFS root:

```text
/agentsmith/
  workspaces/
    <tenant_workspace_id>/
      repos/
        <storage_repo_id>/
          workspace/
          .jvs/
      templates/
        <template_repo_id>/
          workspace/
          .jvs/
      trash/
        <storage_repo_id>/
```

`workspace/` is the payload directory mounted into sandbox workloads and exported to Desktop/Web.

`.jvs/` is control metadata owned by AFSCP/JVS and must not be visible or writable through ordinary user paths.

## StorageRepo Fields

Required fields:

- `tenant_workspace_id`
- `storage_repo_id`
- `filesystem_id`
- `storage_pool_id`
- `repo_kind`
- `repo_path`
- `payload_subdir`
- `jvs_repo_id`
- `status`

`repo_path` should be treated as internal implementation state. Product APIs should use IDs.

## Path Resolver Rules

AFSCP should have one canonical path resolver used by:

- JVS operations
- WebDAV/export
- sandbox mount spec generation
- Web file APIs, if added later
- migration/import tools

The resolver must reject:

- caller-provided absolute paths
- `..` traversal
- symlink escape
- display-name-derived paths
- mismatched workspace/repo IDs
- direct access to `.jvs` from user/export contexts

## Storage Pool Sharding

MVP should not create one JuiceFS DB/bucket per task.

Future sharding can use:

- `filesystem_id`
- `storage_pool_id`
- workspace storage profile
- region/compliance policy

This keeps the data model flexible without reverting to per-task infrastructure.
