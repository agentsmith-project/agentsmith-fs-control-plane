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
          .jvs/
          <user files and directories>
      templates/
        <template_repo_id>/
          .jvs/
          <template user files and directories>
      trash/
        <storage_repo_id>/
```

`<storage_repo_id>/` is both the AFSCP `repo_path` and the JVS `main` workspace real folder. AFSCP runs `jvs init <repo_path>` and `jvs --repo <repo_path> ...` against this directory.

Sandbox workloads and Desktop/Web exports mount or export the same `repo_path` as the user payload root. `.jvs/` is JVS control metadata inside that root. WebDAV/export must hide or block it; sandbox mounts must at minimum prevent workload containers from reading or writing it, and should hide it if the chosen mount/filter technology supports that.

Do not add an extra `workspace/` directory between `repo_path` and user files in P0. JVS registers the initialized folder itself as workspace `main`; adding a separate payload folder would make save, restore, and repo clone operate on the wrong level unless JVS later adds explicit support for out-of-root control metadata.

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

`repo_path` is the canonical JVS repo root and the `main` workspace real path. `payload_subdir` is the AFSCP-generated JuiceFS subdirectory that sandbox-manager mounts; in P0 it is the same directory as `repo_path`, not `repo_path/workspace`.

`repo_path` and `payload_subdir` should be treated as internal implementation state. Product APIs should use IDs.

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
