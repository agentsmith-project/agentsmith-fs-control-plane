# Product Requirements

## Problem

Applications need durable filesystem workspaces with versioned save/restore, cloneable templates, workload mounts, and controlled client exports. Existing product-specific storage flows tend to leak backend details, duplicate storage infrastructure, and couple business workflows to low-level storage credentials.

AFSCP should provide a generic storage control plane that manages shared or dedicated volumes, namespaces, JVS repos, repo templates, exports, and workload mount specs without knowing any caller's business model.

## Users

- Platform admins: configure volumes, credentials, namespace policies, quota hooks, and export policies.
- Calling application services: create repos, templates, exports, and mount specs after their own authorization checks.
- Workloads: read and write mounted repo files without seeing backend credentials.
- Operators: inspect operations, logs, audit events, and recovery state.

## Core Requirements

- Admins can register and manage volumes backed by JuiceFS.
- Admins or trusted callers can bind namespaces to volumes.
- Different namespaces can use different volumes, isolation classes, quota defaults, and export policies.
- New repos are created under a namespace and volume selected by policy.
- Repos are JVS-managed filesystem roots with stable IDs and controlled paths.
- AFSCP runs JVS operations for save points, restore, history, repo clone, and lifecycle.
- AFSCP manages namespace-scoped repo templates.
- Same-namespace template clone creates an independent repo with a new JVS repo identity.
- Cross-namespace template clone is rejected by default in P0.
- Clients can access authorized repos through controlled exports, initially WebDAV.
- Workloads can mount authorized repos through controlled mount specs.
- Ordinary clients and workloads must not see JuiceFS root credentials.
- Ordinary concurrent reads and writes are allowed. AFSCP does not enforce a product-level single-writer model.
- Version merge and conflict resolution are out of scope.

## Non-Goals

- Product-specific authorization.
- Product workflow orchestration.
- Caller-specific job/task lifecycle.
- Caller-specific catalog UI.
- Real-time collaboration.
- Git remote, push, pull, origin, or merge workflows.
- Global template marketplace.
- Cross-namespace template sharing in P0.
- Per-file ACL management UI.
- Per-user NAS account management.
- Raw JuiceFS direct mount for ordinary users.
- Automated legacy migration in P0.
- Multi-region replication, billing, tiering, or retention automation in P0.

## MVP Acceptance Criteria

1. A volume can be registered, health-checked, and used for new repos.
2. A namespace can be bound to a volume without exposing raw filesystem paths to callers.
3. New repos are created through AFSCP and initialized as JVS repos.
4. New repos use a managed shared volume by default instead of creating one JuiceFS DB/bucket per repo.
5. Workload mount specs mount the repo root without exposing JuiceFS root credentials to the workload.
6. Client export flow returns `ExportAccess` rather than JuiceFS direct mount credentials.
7. JVS save point, history, restore preview, and restore run work through AFSCP.
8. A source repo can be saved/cloned into a namespace-scoped repo template.
9. A template can be cloned within the same namespace into an independent repo with a new JVS repo identity.
10. Cross-namespace template clone is rejected by default.
11. WebDAV/export cannot access `.jvs`.
12. Writable workload mounts cannot read or write `.jvs`.
13. All mutating operations produce durable operation records and audit events.
