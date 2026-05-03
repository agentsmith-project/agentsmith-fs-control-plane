# Architecture

## Target Shape

```text
User Browser / Desktop
        |
        | AgentSmith API, authz, catalog, template UI
        v
+-----------------------------+
| AgentSmith API              |
| product control plane       |
+--------------+--------------+
               |
               | internal service auth
               v
+-----------------------------+
| AFSCP                       |
| storage control plane       |
| - storage pool manager      |
| - repo path allocator       |
| - JVS operation runner      |
| - export manager            |
| - sandbox mount adapter     |
| - operation store           |
+------+---------------+------+
       |               |
       | JVS/JuiceFS   | mount spec
       v               v
+--------------+   +------------------+
| JuiceFS FS   |   | sandbox-manager  |
| shared pool  |   | PV/PVC/Pod mount |
+--------------+   +------------------+
```

## Component Responsibilities

### AgentSmith API

AgentSmith API remains the product authority. It authenticates users, checks workspace/project/file-library permissions, owns catalog records, and calls AFSCP only after authorization succeeds.

It should not run `juicefs` or `jvs`, hold JuiceFS root credentials, or expose storage credentials to ordinary clients.

### AFSCP

AFSCP is the storage execution authority. It receives authorized internal requests from AgentSmith API and performs storage operations.

Internal modules:

- `api`: internal HTTP API and service authentication.
- `storage-pools`: JuiceFS filesystem/pool bootstrap, health, credential references.
- `repos`: repo creation, archive/delete, path resolver, lifecycle hooks.
- `jvs`: CLI wrapper or library adapter, JSON parsing, operation lock.
- `templates`: workspace-bound clone executor.
- `exports`: WebDAV export, short-lived credentials, `.jvs` filtering.
- `sandbox`: sandbox mount spec builder.
- `operations`: operation store, idempotency, retry, audit/event outbox.

### Sandbox-Manager

Sandbox-manager remains the Kubernetes execution layer. It should receive a v2 binding spec that no longer includes arbitrary `metadata_url` from AgentSmith API. It creates or updates K8s Secret/PV/PVC/Pod mounts and reports status.

### Desktop

Desktop consumes `ExportAccess` from AgentSmith API. It should not receive or mount raw JuiceFS for ordinary users.

## Storage Model

MVP uses a default shared JuiceFS filesystem/storage pool for new repos.

Suggested path shape:

```text
/agentsmith/
  workspaces/
    <tenant_workspace_id>/
      repos/
        <storage_repo_id>/
          .jvs/
          <user files>
      templates/
        <template_repo_id>/
          .jvs/
          <template files>
```

`<storage_repo_id>/` is the JVS `main` workspace real folder. Callers must never pass raw filesystem paths. AFSCP resolves paths from structured IDs and workspace context.

## Data Authority

| Data | Owner |
| --- | --- |
| Workspace storage profile product config | AgentSmith API |
| User/file-library/template permissions | AgentSmith API |
| File library catalog | AgentSmith API |
| Template catalog | AgentSmith API |
| Storage pool runtime state | AFSCP |
| JuiceFS root credentials | AFSCP/K8s Secret |
| Repo path and JVS repo ID | AFSCP, projected to AgentSmith |
| JVS operation status | AFSCP |
| Sandbox PV/PVC/Pod status | sandbox-manager |
| User-visible audit | AgentSmith API, using AFSCP and sandbox events |

## Concurrency Model

Ordinary file reads and writes are not serialized by AgentSmith. JuiceFS provides filesystem-level consistency and locking semantics. AFSCP must serialize mutating JVS operations per repo, such as save, restore, clone, archive, and delete.

No version merge behavior should be added in MVP.
