# ADR 0008: Own Repo Lifecycle In AFSCP

Status: accepted for development handoff

## Context

Calling product resources may expose complete lifecycle management, including
delete. AFSCP is the storage execution authority for the backing repo, so it
must support storage lifecycle operations rather than requiring callers to
manipulate raw paths or leave retained storage orphaned.

## Decision

AFSCP GA includes repo lifecycle:

- archive
- restore archived
- delete to tombstone
- restore tombstoned
- purge

Product display-name rename, product-only archive/hide, and catalog detach stay
in the calling product. AFSCP repo IDs are stable and immutable.

Lifecycle rules:

- Archive keeps storage retained but unavailable for ordinary access.
- Delete is recoverable tombstone/trash while retention policy allows.
- Purge is permanent deletion and requires namespace lifecycle policy approval,
  caller approval reference, role authorization, and audit.
- Delete may apply to `active` or `archived` repos.
- Restore tombstoned returns to the recorded pre-delete accessibility state.
- Tombstoned catalog mapping remains a calling-product responsibility.

AFSCP uses durable lifecycle state plus an AFSCP tombstone/purge storage
contract as the GA authority. JVS lifecycle commands are optional helpers only
after their external-control-root behavior is pinned to the same contract.

## Consequences

Positive:

- Supports complete file library lifecycle without leaking raw storage details.
- Makes permanent deletion auditable and policy-gated.
- Keeps product catalog semantics outside AFSCP.

Tradeoffs:

- Lifecycle implementation must coordinate exports, workload mounts, JVS health,
  operation recovery, and retention.
- Purge cannot be a simple filesystem unlink.
- Calling products must keep repo ID mapping while tombstoned data is restorable.
