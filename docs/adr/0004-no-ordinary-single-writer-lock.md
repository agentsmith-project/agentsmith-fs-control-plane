# ADR 0004: Do Not Enforce Ordinary Single-Writer Locks

Status: accepted for handoff

## Context

The same repo may be accessed by clients, file APIs, and workloads. The product requirement is to avoid constraining ordinary simultaneous reads and writes.

## Decision

AFSCP will not enforce a single-writer model for ordinary file IO. JuiceFS is responsible for filesystem-level consistency and locking semantics.

AFSCP will serialize mutating JVS operations per repo, including save, restore, clone, archive, delete, and lifecycle operations.

Version merge and conflict resolution are out of scope.

## Consequences

Positive:

- Simpler user model.
- Better fit for filesystem semantics.
- Avoids unnecessary workflow restrictions in calling products.

Tradeoffs:

- Last-writer-wins behavior may occur at file level.
- Save points can capture partially written files.
- Restore can interact poorly with active writers until stricter P1 fencing exists.
