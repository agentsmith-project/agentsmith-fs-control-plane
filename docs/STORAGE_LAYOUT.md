# Storage Layout

AFSCP should manage one or more JuiceFS-backed volumes. GA should bootstrap a default shared volume for new repos, while preserving the ability to bind namespaces to different volumes later.

## Logical Layout

Suggested layout under each JuiceFS filesystem root:

```text
/afscp/
  namespaces/
    <namespace_id>/
      repos/
        <repo_id>/
          control/
            .jvs/
          payload/
            <user files and directories>
      templates/
        <template_id>/
          control/
            .jvs/
          payload/
            <template user files and directories>
      trash/
        <repo_id>/
```

AFSCP must use JVS external control root mode for new repos.

`control/` is the JVS external control root selected with `--control-root`. It contains JVS control metadata and is never mounted into workload containers or exported to clients.

`payload/` is the JVS `main` workspace folder and the only subtree exposed through WebDAV and workload mounts. It must not contain `.jvs/`.

AFSCP initializes a repo with:

```bash
jvs init <payload_root_path> --control-root <control_root_path> --workspace main --json
```

AFSCP runs later JVS commands with:

```bash
jvs --control-root <control_root_path> --workspace main <command> --json
```

The older embedded-control layout, where `.jvs/` sits inside the mounted workspace root, is not the GA layout for AFSCP-managed repos. Legacy embedded-control repos require migration or a verified filtered view before workload mounting.

## Repo Fields

Required fields:

- `namespace_id`
- `repo_id`
- `volume_id`
- `repo_kind`
- `control_volume_subdir`
- `payload_volume_subdir`
- `jvs_repo_id`
- `status`

`control_root_path` is the absolute JVS external control root used only by the AFSCP/JVS runner. It is resolved at runtime from the worker's trusted volume root map plus `control_volume_subdir`; it is not persisted in DB, operation, or audit data.

`payload_root_path` is the absolute JVS `main` workspace folder. It is resolved at runtime from the worker's trusted volume root map plus `payload_volume_subdir`; it is not persisted in DB, operation, or audit data.

`payload_volume_subdir` is the AFSCP-generated JuiceFS subdirectory relative to the JuiceFS filesystem root that a privileged orchestrator may consume. It points at `payload/`, not the repo container directory and not the control root.

For the layout above, subdirs include the managed subroot prefix:

```text
control_volume_subdir = afscp/namespaces/<namespace_id>/repos/<repo_id>/control
payload_volume_subdir = afscp/namespaces/<namespace_id>/repos/<repo_id>/payload
```

These paths and subdirs should be treated as implementation state. Ordinary product APIs should use IDs. Only the privileged orchestrator plan may include `payload_volume_subdir`, and it must never include a leading slash or full host path. Absolute `control_root_path` and `payload_root_path` values are worker-runtime values only; `control_root_path` and `control_volume_subdir` must never be returned to ordinary product callers, workloads, or client connectors.

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
- direct access to control roots or `.jvs` from user/export/workload contexts
- percent-encoded separators or double-decoded traversal
- root-level `.jvs` create, rename, copy, move, or propfind attempts

Filesystem implementations should use dirfd/openat-style traversal with symlink protection where possible. The same resolver test corpus should cover WebDAV, migration/import, file APIs, and workload mount plan generation.

## Control Metadata Protection Gate

GA exports and workload mounts must expose `payload/` only.

Required GA checks:

- `payload_root_path` must not contain root-level `.jvs`.
- `control_root_path` and `payload_root_path` must be distinct and must not contain each other.
- WebDAV and workload mounts use `payload_root_path` / `payload_volume_subdir`, never the repo container directory or control root.
- WebDAV still rejects attempts to create or access root-level `.jvs` as defense-in-depth and for legacy/migration safety.

Permission-only controls on embedded `.jvs` are not sufficient. AFSCP-managed new repos avoid that problem by using JVS external control roots. If a legacy embedded-control repo is encountered, AFSCP must reject workload mounts until the repo is migrated to separated control metadata or a verified filtered view is implemented.

## Volume Sharding

GA should not create one JuiceFS DB/bucket per repo.

Future sharding can use:

- `volume_id`
- namespace policy
- region/compliance policy
- isolation class

This keeps the data model flexible without reverting to per-task or per-repo infrastructure.

## Lifecycle Storage

Repo delete and purge must not be implemented as an untracked filesystem delete.
GA lifecycle uses durable repo lifecycle state plus an AFSCP-controlled
tombstone/trash location or equivalent retained-storage marker.

Lifecycle rules:

- archive keeps control and payload storage retained but unavailable for ordinary access
- delete moves or marks both control and payload storage as tombstoned after all export and workload mount sessions are confirmed terminal
- restore-tombstoned returns retained control and payload storage to the canonical repo location only while retention policy allows
- purge permanently removes both control and payload storage only after retention, product confirmation, and authorization checks pass
- raw paths for trash or tombstone locations remain internal and are never returned to ordinary product callers
