# ADR 0004: Do Not Enforce Ordinary Single-Writer Locks

Status: accepted for handoff

## Context

The same repo may be accessed by clients, file APIs, and workloads. The product requirement is to avoid constraining ordinary simultaneous reads and writes.

## Decision

AFSCP will not enforce a single-writer model for ordinary file IO. JuiceFS is responsible for filesystem-level consistency and locking semantics.

AFSCP will serialize mutating JVS operations per repo, including save, direct restore, and clone.

Direct restore to a save point is not ordinary file IO. For GA it must acquire a per-repo writer-session fence, block new read-write export/workload mount issuance, and reject active or uncertain read-write export or workload mount sessions by default. A future operator break-glass flow may revoke/drain active sessions with explicit audit, but ordinary direct restore should not race active writers.

Version merge and conflict resolution are out of scope.

## Consequences

Positive:

- Simpler user model.
- Better fit for filesystem semantics.
- Avoids unnecessary workflow restrictions in calling products.

Tradeoffs:

- Last-writer-wins behavior may occur at file level.
- Save points can capture partially written files.
- Direct restore may be rejected while active read-write sessions exist; callers must surface this as a storage safety condition, not as a merge/conflict feature.
