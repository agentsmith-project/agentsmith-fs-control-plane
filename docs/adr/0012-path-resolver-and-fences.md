# ADR 0012: Share Path Resolver And Repo Fences

Status: accepted for development handoff

## Context

AFSCP exposes storage through WebDAV, workload mounts, JVS runner paths, and
future file APIs. It also coordinates version restore and repo lifecycle
operations with live sessions. Separate path or fence implementations would
create security and recovery gaps.

## Decision

Implement one canonical path resolver and two repo-scoped fences.

Path resolver rules:

- callers provide IDs and logical relative paths, not filesystem paths
- reject absolute paths, traversal, encoded traversal, double-decoded traversal,
  malformed IDs, namespace mismatch, and symlink escape
- never expose `control/`, `.jvs`, full host paths, or root credentials to
  ordinary callers
- WebDAV, migration/import, JVS runner, and file APIs use the same resolver
  test corpus

Fences:

- writer-session fence protects direct restore to a save point versus
  read-write export and read-write workload mount issuance
- repo lifecycle fence protects archive, restore-archived, delete,
  restore-tombstoned, and purge versus all export/mount issuance and repo
  storage mutations
- lifecycle fence is stronger than writer-session fence
- fences recover from durable operation/session state after process restart

## Consequences

Positive:

- One test corpus catches path bypasses across APIs.
- Clear separation between ordinary concurrent IO and control-plane mutations.
- Restore and lifecycle operations fail closed on uncertain session state.

Tradeoffs:

- Endpoint handlers must be organized around shared resolver/fence packages.
- Operation recovery must understand held fences.
- Read-only sessions do not block version restore, but do block lifecycle drain
  when no further access is allowed.
