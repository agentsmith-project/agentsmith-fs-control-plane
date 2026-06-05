# Product Requirements

## Problem

Applications need durable filesystem repos with versioned save/restore, cloneable templates, workload mounts, and controlled client exports. Existing product-specific storage flows tend to leak backend details, duplicate storage infrastructure, and couple business workflows to low-level storage credentials.

AFSCP should provide an independently runnable, independently releasable shared filesystem control plane that manages shared or dedicated volumes, namespaces, JVS repos, repo templates, exports, workload mount bindings, and orchestrator mount plans without knowing any caller's business model.

## Actors And Access Model

- Platform admins: configure volumes, credentials, namespace policies, quota hooks, and export policies through trusted admin access.
- Calling application services: call AFSCP directly after their own product authorization checks and map product objects to AFSCP namespaces, repos, templates, exports, and mount bindings.
- Dedicated workload orchestrator: calls only orchestrator-scoped APIs, consumes mount plans, executes runtime mounts, and reports binding status.
- Migration jobs: call migration/admin APIs only when an explicit audited migration is approved.
- Client connectors and desktop apps: never call AFSCP directly; they consume calling-product issued WebDAV export credentials.
- Workloads: never call AFSCP directly; they read and write only mounted repo payload roots without backend credentials.
- Operators: inspect operations, logs, audit events, health, recovery state, and approved intervention state.
- End users: authenticate and authorize through the calling product, not AFSCP.

## Core Requirements

- Admins can register and manage volumes backed by JuiceFS.
- Admins or trusted callers can create namespaces and bind namespaces to volumes.
- Namespace bindings define which caller services and roles may operate in that namespace.
- Different namespaces can use different volumes, isolation classes, quota defaults, export policies, lifecycle retention policies, and purge policies.
- New repos are created under a namespace and volume selected by policy.
- Repos support storage lifecycle operations for archive, restore-from-archive, delete request, restore-from-tombstone, and purge.
- Repo lifecycle uses common product semantics: archive is retained but unavailable, delete is recoverable tombstone/trash during retention, and purge is permanent deletion.
- Trusted callers and operators can inspect repo storage projections by ID and by namespace/lifecycle status for reconciliation. These projections are not product catalog listings and contain no display names or raw paths.
- Repos are JVS-managed payload workspaces with external control roots, stable IDs, and controlled paths.
- AFSCP runs direct JVS operations for save point create/list and durable restore, plus repo init/clone helpers.
- AFSCP manages namespace-scoped immutable repo templates for GA.
- Same-namespace template clone creates an independent repo with a new JVS repo identity.
- Cross-namespace template clone is rejected by default.
- Clients can access authorized repos through controlled exports, initially WebDAV.
- Workloads can mount authorized repos through controlled mount bindings and orchestrator-only mount plans.
- Ordinary clients and workloads must not see JuiceFS root credentials.
- Workload mounts expose only the repo payload root. JVS control metadata must stay outside that mounted/exported root.
- Ordinary concurrent reads and writes are allowed. AFSCP does not enforce a product-level single-writer model.
- Restore-run is a version mutation and must use a writer-session fence to reject active or uncertain read-write export/workload sessions and block new read-write sessions unless an explicit audited operator break-glass flow is implemented.
- Restore-run dirty-state behavior is fail-closed by default. A caller-visible dirty-state option such as `discard_unsaved` or `save_first` requires a reviewed contract, stable error codes, and audit semantics.
- Repo lifecycle operations must block new exports, workload mounts, save, restore, and template operations while lifecycle drain or deletion is in progress.
- Repo delete must drain or revoke existing exports and workload mounts, read-only or read-write, before tombstoning or purging storage. If sessions cannot be confirmed stopped, deletion fails closed or enters operator intervention.
- Repo delete may apply to active or archived repos. Restore-from-tombstone returns the repo to the recorded pre-delete accessibility state unless a separate reviewed contract adds an explicit target-state option.
- Repo purge is irreversible and must require an explicit request, retention-policy check, operation record, and audit event.
- Purge requests must include a caller-side confirmation or approval reference and reason; retention override requires an approved break-glass policy.
- `quota_bytes_default` is a GA policy record and enforcement hook. It is not enforced unless the selected volume capability `directory_quota` supports directory quota enforcement and the corresponding volume integration explicitly enables directory quota enforcement.
- Version merge and conflict resolution are out of scope.
- First or reference consumer adoption, handoff, integration sequencing, caller
  application methods, and caller business logic are out of scope for AFSCP GA
  release gates. Consumers may provide requirements and compatibility feedback,
  but AFSCP GA release closure is decided only by the repo-local
  selector-driven gate `bash scripts/verify-ga-release.sh`. Product, security,
  platform/runtime, operations, contract, schema/OpenAPI, and
  generated-client-relevant compatibility checks count only when represented as
  repo-local evidence covered by that command.

## Non-Goals

- Product-specific authorization.
- Product workflow orchestration.
- Caller-specific job/task lifecycle.
- Caller-specific catalog UI.
- Real-time collaboration.
- Git remote, push, pull, origin, or merge workflows.
- Global template marketplace.
- Cross-namespace template sharing.
- Per-file ACL management UI.
- Per-user NAS account management.
- Raw JuiceFS direct mount for ordinary users.
- Automated legacy migration.
- Product display-name rename, product catalog detach, and product-specific lifecycle vocabulary inside AFSCP.
- Creating repo templates from older historical save points.
- Multi-region replication, billing, tiering, or retention automation.
- Namespace delete.

## GA Admission Criteria

1. A volume can be registered, health-checked, and used for new repos.
2. A namespace can be created/bound to a volume with caller-service authorization and without exposing raw filesystem paths to callers.
3. New repos are created through AFSCP and initialized as JVS repos.
4. New repos use a managed shared volume by default instead of creating one JuiceFS DB/bucket per repo.
5. Repo archive rejects new ordinary access and mutations while retaining recoverable storage state.
6. Repo restore-from-archive reactivates an archived repo without changing repo identity.
7. Repo delete drains/revokes existing sessions, tombstones retained storage, and returns stable lifecycle status.
8. Repo restore-from-tombstone reactivates retained deleted storage only before purge and within retention policy.
9. Repo purge permanently removes tombstoned storage only after retention/policy checks and explicit authorization.
10. Workload mount bindings mount the repo payload root, not the JVS control root or repo container directory, without exposing JuiceFS root credentials or Secret references to the workload or ordinary product caller.
11. Client export flow returns WebDAV export credentials rather than JuiceFS direct mount credentials.
12. JVS save point create/list and durable direct restore work through AFSCP; direct restore blocks new read-write sessions and rejects active or uncertain read-write sessions by default.
13. A source repo can be saved/cloned into a namespace-scoped immutable repo template.
14. A template can be cloned within the same namespace into an independent repo with a new JVS repo identity.
15. Cross-namespace template clone is rejected by default.
16. WebDAV/export cannot access JVS control metadata and rejects root-level `.jvs` access/creation attempts.
17. Workload mounts do not contain root-level `.jvs` for AFSCP-managed repos; legacy embedded-control repos are rejected until migrated or protected by a verified filtered view.
18. All mutating operations produce durable operation records and audit events.
19. Workload mount is available only when the orchestrator contract supports payload-only mount plans, heartbeat, release, revoke, and confirmed-unmounted terminal semantics; otherwise AFSCP returns a stable capability error instead of issuing unsafe bindings.
20. Export sessions define TTL default/max, credential reissue rules, revoke behavior, active read-write session accounting, and generic client connector responsibilities.
21. Restore-run returns stable errors for active writer sessions, dirty-state rejection, JVS failure, doctor failure, idempotency conflict, and namespace/capability denial.
22. Operators can inspect operations, audit events, repo lifecycle projections, volume/namespace health, stale leases, held lifecycle fences, and operator-intervention records without accessing forbidden credentials.
23. Namespace disable behavior is defined for new operations and active or uncertain sessions.
24. Product display rename and catalog detach are handled by the calling product; AFSCP repo identity remains stable.
25. All GA-blocking risks in `docs/RISK_REGISTER.md` are closed only by repo-local automated evidence covered by `scripts/verify-ga-release.sh`. Non-waivable GA blockers cannot be bypassed by manual approval or subjective risk exception.
