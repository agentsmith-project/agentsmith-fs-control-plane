# ADR 0002: Use A Default Shared JuiceFS Pool For New Repos

Status: accepted for handoff

## Context

Creating a separate JuiceFS metadata DB and bucket per notebook task is too expensive operationally. It increases database, bucket, policy, Secret, and mount management overhead.

## Decision

New file libraries should default to a shared JuiceFS filesystem/storage pool managed by AFSCP. Isolation should be provided through AgentSmith authorization, AFSCP path resolution, controlled exports, and sandbox subdirectory mounts.

The data model must keep `filesystem_id` and `storage_pool_id` so future sharding by workspace, tenant, region, or compliance profile remains possible.

## Consequences

Positive:

- Much lower provisioning overhead.
- JVS repo clone/template flows can stay within one filesystem.
- Easier Desktop/WebDAV and sandbox integration.

Tradeoffs:

- Larger blast radius for root storage credentials.
- Stronger AFSCP security boundary is required.
- Path resolver and `.jvs` protection become P0 requirements.
