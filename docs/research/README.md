# Research

This directory contains copied planning material from `/home/percy/works/mbos-v1/improve-agentsmith-fs`.

The root-level handoff docs are now authoritative. Treat files in this directory as research snapshots and historical review notes.

Known superseding decisions in the root docs:

- `repo_path` is the JVS `main` workspace real folder; P0 does not add a child `workspace/` payload directory.
- AgentSmith workspace storage profile does not provide an authoritative raw `path_prefix`; AFSCP computes canonical paths from structured IDs and storage pool configuration.
- This GitHub repository is the AFSCP implementation home.

- [agentsmith-workspace-storage-technical-design.md](agentsmith-workspace-storage-technical-design.md)
- [scratch.md](scratch.md)
- [team-review-summary.md](team-review-summary.md)

`scratch.md` is historical discussion context and may include earlier assumptions that were later revised.
