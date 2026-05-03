# Architecture

## Target Shape

```text
Calling Product / Admin Job / Operator Tool
        |
        | service auth, namespace, resource IDs, actor, idempotency
        v
+-----------------------------+
| AFSCP                       |
| storage control plane       |
| - volume manager            |
| - namespace policy          |
| - repo path allocator       |
| - JVS operation runner      |
| - repo template manager     |
| - export manager            |
| - workload mount adapter    |
| - operation store           |
+------+---------------+------+
       |               |
       | JVS/JuiceFS   | mount spec
       v               v
+--------------+   +----------------------+
| Volume       |   | External orchestrator |
| JuiceFS pool |   | PV/PVC/Pod or similar |
+--------------+   +----------------------+
```

## Component Responsibilities

### Calling Product

The calling product remains the business authority. It authenticates users, checks product permissions, owns product catalog records, and calls AFSCP only after authorization succeeds.

It should not run `juicefs` or `jvs`, hold JuiceFS root credentials, or expose storage credentials to ordinary clients.

### AFSCP

AFSCP is the storage execution authority. It receives authorized internal requests from trusted callers and performs storage operations.

Internal modules:

- `api`: internal API and service authentication.
- `volumes`: JuiceFS filesystem/pool bootstrap, health, credential references.
- `namespaces`: namespace-to-volume binding, isolation checks, quota hooks.
- `repos`: repo creation, archive/delete, path resolver, lifecycle hooks.
- `jvs`: CLI wrapper or library adapter, JSON parsing, operation lock.
- `templates`: namespace-scoped repo template clone executor.
- `exports`: WebDAV export, short-lived credentials, `.jvs` filtering.
- `mounts`: workload mount spec builder.
- `operations`: operation store, idempotency, retry, audit/event outbox.

### External Orchestrator

The orchestrator, such as a Kubernetes sandbox-manager, consumes AFSCP workload mount specs. It creates or updates K8s Secret/PV/PVC/Pod mounts or equivalent runtime mounts and reports status.

It should not make product authorization decisions.

### Client Connector

A client connector consumes export access returned by the calling product after product authorization. It should not receive or mount raw JuiceFS for ordinary users.

## Storage Model

MVP uses a managed shared JuiceFS-backed volume for new repos unless namespace policy selects a different volume.

Suggested path shape:

```text
/afscp/
  namespaces/
    <namespace_id>/
      repos/
        <repo_id>/
          .jvs/
          <user files>
      templates/
        <template_id>/
          .jvs/
          <template files>
```

`<repo_id>/` is the JVS `main` workspace real folder. Callers must never pass raw filesystem paths. AFSCP resolves paths from structured IDs and namespace context.

## Data Authority

| Data | Owner |
| --- | --- |
| Product permissions and product catalog | Calling product |
| Namespace-to-volume policy | AFSCP, optionally configured by admin/trusted caller |
| Volume runtime state | AFSCP |
| JuiceFS root credentials | AFSCP/K8s Secret |
| Repo path and JVS repo ID | AFSCP |
| Repo template path and JVS repo ID | AFSCP |
| JVS operation status | AFSCP |
| Workload runtime mount status | External orchestrator |
| User-visible audit projection | Calling product, using AFSCP events |

## Concurrency Model

Ordinary file reads and writes are not serialized by AFSCP. JuiceFS provides filesystem-level consistency and locking semantics. AFSCP must serialize mutating JVS operations per repo, such as save, restore, clone, archive, and delete.

No version merge behavior should be added in MVP.
