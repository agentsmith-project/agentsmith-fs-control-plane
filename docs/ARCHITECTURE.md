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
       | JVS/JuiceFS   | mount plan
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

- `api`: internal API, service authentication, caller-service authorization.
- `volumes`: JuiceFS filesystem/pool bootstrap, health, credential references, capability checks.
- `namespaces`: namespace-to-volume binding, allowed caller policy, isolation checks, quota hooks.
- `repos`: repo creation, path resolver, archive/delete/tombstone/purge lifecycle.
- `jvs`: CLI wrapper or library adapter, JSON parsing, resource locks.
- `templates`: namespace-scoped repo template clone executor.
- `exports`: WebDAV export, short-lived credentials, payload-root chroot, and defense-in-depth path filtering.
- `mounts`: workload mount binding and orchestrator-only plan builder.
- `operations`: operation store, idempotency, retry, audit/event outbox.

### External Orchestrator

The orchestrator consumes AFSCP workload mount plans. It creates or updates K8s Secret/PV/PVC/Pod mounts or equivalent runtime mounts and reports status.

It should not make product authorization decisions.

It is the only ordinary integration component allowed to see JuiceFS Secret references, and only through the orchestrator-specific API role.

### Client Connector

A client connector consumes export access returned by the calling product after product authorization. It should not receive or mount raw JuiceFS for ordinary users.

### Direct Access Matrix

| Actor | Direct AFSCP API Access | Notes |
| --- | --- | --- |
| Calling product control plane | yes | Product authorization must already be complete. |
| Admin job/operator tool | yes | Restricted by admin/operator roles. |
| Migration job | yes | Restricted by migration role and explicit migration contract. |
| Dedicated workload orchestrator | yes | Orchestrator role only; no product permission decisions. |
| Client connector/desktop app | no | Consumes calling-product issued export credentials. |
| Workload container | no | Consumes mounted payload only. |
| End user | no | Uses the calling product. |

## Storage Model

GA uses a managed shared JuiceFS-backed volume for new repos unless namespace policy selects a different volume.

Suggested path shape:

```text
/afscp/
  namespaces/
    <namespace_id>/
      repos/
        <repo_id>/
          control/
            .jvs/
          payload/
            <user files>
      templates/
        <template_id>/
          control/
            .jvs/
          payload/
            <template files>
```

`control/` is the JVS external control root. `payload/` is the JVS `main` workspace and the only subtree exposed to workloads or WebDAV. Callers must never pass raw filesystem paths. AFSCP resolves paths from structured IDs and namespace context.

Workload mounts use the payload subdir only. They do not mount the repo container directory or JVS control root.

## Data Authority

| Data | Owner |
| --- | --- |
| Product permissions and product catalog | Calling product |
| Namespace-to-volume and allowed caller policy | AFSCP, optionally configured by admin/trusted caller |
| Volume runtime state | AFSCP |
| JuiceFS root credentials | AFSCP/K8s Secret |
| Repo control/payload paths and JVS repo ID | AFSCP |
| Repo lifecycle status, tombstone, and purge state | AFSCP |
| Repo template control/payload paths and JVS repo ID | AFSCP |
| JVS operation status | AFSCP |
| Workload runtime mount status and Secret/PV/PVC execution | External orchestrator |
| User-visible audit projection | Calling product, using AFSCP events |

## Concurrency Model

Ordinary file reads and writes are not serialized by AFSCP. JuiceFS provides filesystem-level consistency and locking semantics. AFSCP must serialize mutating JVS operations per repo, such as save, restore-run, and clone.

Restore-run is not ordinary file IO. GA restore-run must acquire a per-repo writer-session fence, block new read-write export or workload mount issuance, and reject existing active or uncertain read-write sessions by default. This preserves ordinary concurrent file access while preventing version mutations from racing active writers.

No version merge behavior should be added in GA.

## GA Functional Architecture Decisions

- Workload mounts require an accepted orchestrator contract with payload-only subdir mapping, Secret RBAC, heartbeat, release, revoke, and confirmed-unmounted semantics. Without that closure, AFSCP must return a capability error instead of issuing unsafe bindings.
- WebDAV exports require an AFSCP-controlled policy gateway. Stock `juicefs webdav` is not the policy boundary.
- Restore-run dirty-state behavior is fail-closed unless an explicit reviewed API option models and audits a supported JVS choice.
- Namespace disable rejects new mutating operations, exports, and mount bindings. Existing active or uncertain sessions require revoke, expiry/reconciliation, or operator action before restore or destructive operational activity can proceed.
- Repo lifecycle archive, restore-archived, delete, restore-tombstoned, and purge are GA storage-control operations. They acquire a lifecycle fence, block new sessions and mutations, drain or revoke all existing export and workload mount sessions where required, and audit state transitions.
- Product display-name rename and catalog detach remain caller-owned metadata operations. AFSCP repo IDs are stable and immutable.
- Break-glass direct mount is disabled by default and requires a separate reviewed operational contract if ever enabled.
