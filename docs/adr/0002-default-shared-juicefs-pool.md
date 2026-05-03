# ADR 0002: Use A Default Shared JuiceFS Volume For New Repos

Status: accepted for handoff

## Context

Creating a separate JuiceFS metadata DB and bucket per product resource is too expensive operationally. It increases database, bucket, policy, Secret, and mount management overhead.

## Decision

New repos should default to a shared JuiceFS-backed volume managed by AFSCP. Isolation should be provided through caller authorization, AFSCP namespace boundaries, path resolution, controlled exports, workload mount bindings, and orchestrator-only mount plans.

The data model must keep `volume_id` and namespace bindings so future sharding by tenant, region, or compliance profile remains possible.

## Consequences

Positive:

- Much lower provisioning overhead.
- JVS repo clone/template flows can stay within one volume.
- Easier WebDAV/export and workload mount integration.

Tradeoffs:

- Larger blast radius for root storage credentials.
- Stronger AFSCP security boundary is required.
- Path resolver and `.jvs` protection become P0 requirements.
