# Storage Layout

AFSCP should manage one or more JuiceFS-backed volumes. MVP should bootstrap a default shared volume for new repos, while preserving the ability to bind namespaces to different volumes later.

## Logical Layout

Suggested layout under each volume root:

```text
/afscp/
  namespaces/
    <namespace_id>/
      repos/
        <repo_id>/
          .jvs/
          <user files and directories>
      templates/
        <template_id>/
          .jvs/
          <template user files and directories>
      trash/
        <repo_id>/
```

`<repo_id>/` is both the AFSCP `repo_path` and the JVS `main` workspace real folder. AFSCP runs `jvs init <repo_path>` and `jvs --repo <repo_path> ...` against this directory.

Workload mounts and WebDAV exports mount or export the same `repo_path` as the user payload root. `.jvs/` is JVS control metadata inside that root. WebDAV/export must hide or block it; workload mounts must at minimum prevent workload containers from reading or writing it, and should hide it if the chosen mount/filter technology supports that.

Do not add an extra `workspace/` directory between `repo_path` and user files in P0. JVS registers the initialized folder itself as workspace `main`; adding a separate payload folder would make save, restore, and repo clone operate on the wrong level unless JVS later adds explicit support for out-of-root control metadata.

## Repo Fields

Required fields:

- `namespace_id`
- `repo_id`
- `volume_id`
- `repo_kind`
- `repo_path`
- `payload_subdir`
- `jvs_repo_id`
- `status`

`repo_path` is the canonical JVS repo root and the `main` workspace real path. `payload_subdir` is the AFSCP-generated JuiceFS subdirectory that an external orchestrator mounts; in P0 it is the same directory as `repo_path`, not `repo_path/workspace`.

`repo_path` and `payload_subdir` should be treated as internal implementation state. Product APIs should use IDs.

## Path Resolver Rules

AFSCP should have one canonical path resolver used by:

- JVS operations
- WebDAV/export
- workload mount spec generation
- file APIs, if added later
- migration/import tools

The resolver must reject:

- caller-provided absolute paths
- `..` traversal
- symlink escape
- display-name-derived paths
- mismatched namespace/repo IDs
- direct access to `.jvs` from user/export contexts

## Volume Sharding

MVP should not create one JuiceFS DB/bucket per repo.

Future sharding can use:

- `volume_id`
- namespace policy
- region/compliance policy
- isolation class

This keeps the data model flexible without reverting to per-task or per-repo infrastructure.
