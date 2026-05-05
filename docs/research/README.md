# Research

This directory contains copied planning material from `/home/percy/works/mbos-v1/improve-agentsmith-fs`.

The root-level handoff docs are now authoritative. Treat files in this
directory as research snapshots and historical review notes, not active scope.

Known superseding decisions in the root docs:

- AFSCP core is product-agnostic; AgentSmith-specific notebook task, file library, project, and workspace semantics belong only in integration mapping.
- `namespace` is the AFSCP isolation concept. AgentSmith may map one workspace to one namespace, but AFSCP does not store AgentSmith workspace semantics.
- GA repo layout uses JVS external control root mode: AFSCP stores control metadata under a private `control/` root and exposes only the separate `payload/` root.
- Product callers receive workload mount bindings; only the dedicated orchestrator receives Secret-bearing mount plans.
- GA restore-run rejects active or uncertain read-write export/workload sessions by default.
- Repo archive, restore-archived, delete, restore-tombstoned, and purge are GA storage lifecycle APIs. Product display-name rename and catalog detach remain caller-owned metadata operations.
- Namespace volume binding does not provide an authoritative raw `path_prefix`; AFSCP computes canonical paths from structured IDs and volume configuration.
- This GitHub repository is the AFSCP implementation home.

- [agentsmith-workspace-storage-technical-design.md](agentsmith-workspace-storage-technical-design.md)
- [scratch.md](scratch.md)
- [team-review-summary.md](team-review-summary.md)

`scratch.md` is historical discussion context and may include earlier assumptions that were later revised.
