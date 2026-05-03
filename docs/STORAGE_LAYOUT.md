# Storage Layout

AFSCP should manage one or more JuiceFS-backed volumes. MVP should bootstrap a default shared volume for new repos, while preserving the ability to bind namespaces to different volumes later.

## Logical Layout

Suggested layout under each JuiceFS filesystem root:

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

`<repo_id>/` is both the internal AFSCP `repo_path` and the JVS `main` workspace real folder. AFSCP runs `jvs init <repo_path>` and `jvs --repo <repo_path> ...` against this directory.

Workload mounts and WebDAV exports expose the same repo root as the user payload root. `.jvs/` is JVS control metadata inside that root. WebDAV/export and workload mounts must hide or block it for read and write.

Do not add an extra `workspace/` directory between `repo_path` and user files in P0. JVS registers the initialized folder itself as workspace `main`; adding a separate payload folder would make save, restore, and repo clone operate on the wrong level unless JVS later adds explicit support for out-of-root control metadata.

## Repo Fields

Required fields:

- `namespace_id`
- `repo_id`
- `volume_id`
- `repo_kind`
- `repo_path`
- `volume_subdir`
- `jvs_repo_id`
- `status`

`repo_path` is the internal canonical JVS repo root and the `main` workspace real path. `volume_subdir` is the AFSCP-generated JuiceFS subdirectory relative to the JuiceFS filesystem root that a privileged orchestrator may consume; in P0 it points at the same directory as `repo_path`, not `repo_path/workspace`.

For the layout above, `volume_subdir` includes the managed subroot prefix:

```text
afscp/namespaces/<namespace_id>/repos/<repo_id>
```

`repo_path` and `volume_subdir` should be treated as implementation state. Ordinary product APIs should use IDs. Only the privileged orchestrator plan may include `volume_subdir`, and it must never include a leading slash or full host path.

## Path Resolver Rules

AFSCP should have one canonical path resolver used by:

- JVS operations
- WebDAV/export
- workload mount plan generation
- file APIs, if added later
- migration/import tools

The resolver must reject:

- caller-provided absolute paths
- `..` traversal
- symlink escape
- display-name-derived paths
- mismatched namespace/repo IDs
- direct access to `.jvs` from user/export/workload contexts
- percent-encoded separators or double-decoded traversal
- root-level `.jvs` create, rename, copy, move, or propfind attempts

Filesystem implementations should use dirfd/openat-style traversal with symlink protection where possible. The same resolver test corpus should cover WebDAV, migration/import, file APIs, and workload mount plan generation.

## `.jvs` Protection Gate

P0 exports and workload mounts must pass a `.jvs` protection gate before enablement.

Acceptable P0 strategies:

- protocol gateway filters `.jvs` for WebDAV and rejects all methods that target it
- filtered mount/view hides `.jvs` from workload containers
- equivalent filesystem/runtime gate blocks lookup, read, write, create, rename, unlink, chmod, chown, hardlink, and symlink operations targeting root-level `.jvs`

Permission-only controls on `.jvs` are not sufficient by themselves because the repo root is writable user payload. If the runtime cannot enforce one of the strategies above, AFSCP must reject workload mounts for that volume/namespace until a filtered view is implemented.

## Volume Sharding

MVP should not create one JuiceFS DB/bucket per repo.

Future sharding can use:

- `volume_id`
- namespace policy
- region/compliance policy
- isolation class

This keeps the data model flexible without reverting to per-task or per-repo infrastructure.
