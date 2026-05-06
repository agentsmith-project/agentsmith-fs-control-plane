# Contract: Repo Lifecycle V1

Status: GA pre-dev review draft

AFSCP owns storage lifecycle for repos. Calling products own product catalog
state, display names, user-facing deletion UX, and permission checks.

## Lifecycle Operations

GA repo lifecycle operations:

- archive repo
- restore archived repo
- delete repo
- restore tombstoned repo, when retention policy allows
- purge repo

Product display-name rename is not an AFSCP operation. AFSCP repo IDs are stable
and immutable.

Product-facing mapping:

- archive means retained storage is hidden from ordinary access but recoverable
- delete means recoverable trash/tombstone while retention policy allows
- purge means permanent deletion after retention and authorization checks
- display-name rename means caller catalog metadata only

## Repo Statuses

- `active`: ordinary repo operations may proceed when policy permits.
- `archiving`: lifecycle fence is held and sessions are draining.
- `archived`: retained storage is unavailable for ordinary export, mount, save, restore-run, template create, or clone target use.
- `restoring_archived`: archive reversal is running.
- `deleting`: lifecycle fence is held, new sessions are blocked, existing sessions are draining, and tombstone is in progress.
- `tombstoned`: caller-visible deleted state; retained storage may be restored only while retention policy allows.
- `restoring_tombstoned`: deleted repo restoration is running.
- `purging`: irreversible physical removal is running.
- `purged`: storage has been permanently removed; AFSCP retains only the minimal control-plane record needed for audit, idempotency, and authorized status inspection while record retention requires it.
- `operator_intervention_required`: lifecycle operation cannot be safely completed automatically.

## Transition Rules

| Operation | Allowed Source Status | Result |
| --- | --- | --- |
| archive | `active` | `archived` |
| restore archived | `archived` | `active` |
| delete | `active`, `archived` | `tombstoned` |
| restore tombstoned | `tombstoned` before purge and within retention | previous storage accessibility state, usually `active` or `archived` |
| purge | `tombstoned` after retention or approved break-glass | `purged` |

Deleting an archived repo records that it was archived before deletion.
Restoring a tombstoned repo returns it to the recorded pre-delete accessibility
state unless a separate reviewed contract adds an explicit target-state option.

The repo access admission pure model exists in code for shared pre-handler
decisions across future lifecycle, save/restore, export, mount, and template
handlers. It is not yet wired to those concrete endpoint handlers.
The session substrate pure model also exists for restore-run writer gating and
lifecycle drain gating over export and workload-mount sessions. Export sessions
are wired to the API create/get/revoke boundary, WebDAV gateway admission and
runtime observation, terminal reconcile, and repo lifecycle worker drain checks.
Workload-mount plan issuance and restore-run execution remain separate.

## Lifecycle Fence

Lifecycle operations acquire a per-repo lifecycle fence. The lifecycle fence is
stronger than the restore writer-session fence.

While held, AFSCP rejects:

- new exports
- new workload mount bindings
- save point creation
- restore-run
- template creation from the repo
- template clone into the repo
- additional lifecycle operations unless the operation is an idempotent retry

Lifecycle fence acquisition must also reject or wait for active storage
mutations on the same repo, according to the operation-lock contract. If AFSCP
cannot prove whether an in-flight mutation has finished safely, the lifecycle
operation fails closed or enters `operator_intervention_required`.
Lifecycle operation admission also fails closed when a held writer-session fence
already exists for the target repo.
Before wiring this pure admission model to concrete handlers, lifecycle request
handling must preserve idempotency-first behavior or pass holder-operation
context so an operation retry is not denied by its own held fence.

Archive, delete, and purge must wait for all non-terminal exports and workload
mount bindings, read-only or read-write, to reach a confirmed terminal non-accessing
state. Uncertain sessions fail closed or move the operation to
`operator_intervention_required`. For export sessions, the current terminal
reconcile runner can prove zero-count `revoking -> revoked` and zero-count
expired `active -> expired` without a fresh gateway heartbeat; nonzero counts
or stale/uncertain runtime state remain blocking until operator/runbook repair
or later recovery.

## Operation Semantics

Archive:

- blocks new sessions and mutations
- drains or revokes existing exports and mounts, read-only or read-write
- retains repo data and JVS control metadata
- ends in `archived`

Restore archived:

- reactivates an `archived` repo
- preserves repo ID and JVS identity
- must verify repo health before returning `active`

Delete:

- is a logical delete request, not a raw filesystem unlink
- blocks new sessions and mutations
- drains or revokes existing exports and mounts, read-only or read-write
- moves retained storage to an AFSCP-controlled tombstone/trash state or marks it equivalent by durable metadata
- ends in `tombstoned`

Restore tombstoned:

- is allowed only before purge and within retention policy
- evaluates retention eligibility against the restore operation accepted time:
  the operation is eligible only when its accepted time is earlier than
  `retention_expires_at`; an accepted time equal to or later than
  `retention_expires_at` is expired and must be rejected or moved to operator
  handling
- preserves repo ID and JVS identity unless a separate reviewed import contract says otherwise
- must verify repo health before returning to the recorded pre-delete accessibility state

Purge:

- is irreversible
- is allowed only for `tombstoned` repos after retention policy permits it, or with an approved operator break-glass purge
- requires a dedicated lifecycle/purge authorization policy rather than ordinary repo metadata permission alone
- permanently removes AFSCP-managed retained payload and control storage
- must not run while any active or uncertain export or mount session remains
- ends in `purged`

## Caller Contract

Calling products must:

- authorize user lifecycle intent before calling AFSCP
- provide `authorized_actor`, `correlation_id`, and `idempotency_key`
- treat lifecycle operations as asynchronous durable operations
- keep product display names and catalog state outside AFSCP
- keep the product catalog to AFSCP repo ID mapping while tombstoned storage remains restorable
- provide a product confirmation or approval reference and reason for purge requests
- handle stable lifecycle errors in user-facing UX

AFSCP must:

- audit lifecycle request, drain/revoke, tombstone, restore, purge, denial, and intervention events
- never expose raw paths or storage credentials in lifecycle responses
- preserve idempotency for repeated lifecycle requests
- return stable errors for existing sessions, lifecycle fence held, invalid state, retention denial, and purge denial

Archive, delete, and purge may stay `running` while sessions drain. A pending
drain is not a user-data failure by itself; it becomes failed or
`operator_intervention_required` only when the session state cannot be safely
confirmed.

## Stable Error Codes

- `REPO_LIFECYCLE_INVALID_STATE`
- `REPO_LIFECYCLE_FENCE_HELD`
- `ACTIVE_SESSIONS_BLOCK_LIFECYCLE`
- `STALE_SESSION_BLOCKS_LIFECYCLE`
- `REPO_ARCHIVED`
- `REPO_TOMBSTONED`
- `REPO_PURGED`
- `PURGE_CONFIRMATION_REQUIRED`
- `PURGE_RETENTION_NOT_MET`
- `PURGE_REQUIRES_OPERATOR_APPROVAL`

## Recovery

Each lifecycle operation must define phase-specific recovery before
implementation:

- fence acquired
- session drain requested
- existing sessions reconciled
- storage tombstone or restore started
- JVS or storage health verified
- terminal status recorded
- audit emitted

If AFSCP cannot prove whether data was tombstoned, restored, or purged, it must
enter `operator_intervention_required` and keep the lifecycle fence until an
operator resolves the state.
