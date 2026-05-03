# Product Requirements

## Problem

Applications need durable filesystem repos with versioned save/restore, cloneable templates, workload mounts, and controlled client exports. Existing product-specific storage flows tend to leak backend details, duplicate storage infrastructure, and couple business workflows to low-level storage credentials.

AFSCP should provide a generic storage control plane that manages shared or dedicated volumes, namespaces, JVS repos, repo templates, exports, workload mount bindings, and orchestrator mount plans without knowing any caller's business model.

## Users

- Platform admins: configure volumes, credentials, namespace policies, quota hooks, and export policies.
- Calling application services: create repos, templates, exports, and workload mount bindings after their own authorization checks.
- Workloads: read and write mounted repo files without seeing backend credentials.
- Operators: inspect operations, logs, audit events, and recovery state.

## Core Requirements

- Admins can register and manage volumes backed by JuiceFS.
- Admins or trusted callers can create namespaces and bind namespaces to volumes.
- Namespace bindings define which caller services and roles may operate in that namespace.
- Different namespaces can use different volumes, isolation classes, quota defaults, and export policies.
- New repos are created under a namespace and volume selected by policy.
- Repos are JVS-managed filesystem roots with stable IDs and controlled paths.
- AFSCP runs JVS operations for save points, restore preview/run, history, and repo clone.
- AFSCP manages namespace-scoped immutable repo templates in P0.
- Same-namespace template clone creates an independent repo with a new JVS repo identity.
- Cross-namespace template clone is rejected by default in P0.
- Clients can access authorized repos through controlled exports, initially WebDAV.
- Workloads can mount authorized repos through controlled mount bindings and orchestrator-only mount plans.
- Ordinary clients and workloads must not see JuiceFS root credentials.
- Workload mounts must not expose `.jvs`; if the runtime cannot hide or block `.jvs`, workload mounts are rejected.
- Ordinary concurrent reads and writes are allowed. AFSCP does not enforce a product-level single-writer model.
- Restore-run is a version mutation and must use a writer-session fence to reject active read-write export/workload sessions and block new read-write sessions in P0 unless an explicit audited operator break-glass flow is implemented.
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
- Repo archive/delete/rename/detach lifecycle APIs in P0.
- Creating repo templates from older historical save points in P0.
- Multi-region replication, billing, tiering, or retention automation in P0.

## MVP Acceptance Criteria

1. A volume can be registered, health-checked, and used for new repos.
2. A namespace can be created/bound to a volume with caller-service authorization and without exposing raw filesystem paths to callers.
3. New repos are created through AFSCP and initialized as JVS repos.
4. New repos use a managed shared volume by default instead of creating one JuiceFS DB/bucket per repo.
5. Workload mount bindings mount the repo root without exposing JuiceFS root credentials or Secret references to the workload or ordinary product caller.
6. Client export flow returns WebDAV export credentials rather than JuiceFS direct mount credentials.
7. JVS save point, history, restore preview, and restore-run work through AFSCP; restore-run blocks new read-write sessions and rejects active read-write sessions by default.
8. A source repo can be saved/cloned into a namespace-scoped immutable repo template.
9. A template can be cloned within the same namespace into an independent repo with a new JVS repo identity.
10. Cross-namespace template clone is rejected by default.
11. WebDAV/export cannot access `.jvs`.
12. Workload mounts cannot read or write `.jvs`, or are rejected until a filtered/protected view is available.
13. All mutating operations produce durable operation records and audit events.
