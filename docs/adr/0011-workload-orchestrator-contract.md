# ADR 0011: Split Product Mount Binding From Orchestrator Mount Plan

Status: accepted for development handoff

## Context

Workloads need mounted repo payloads. Ordinary product callers and workload
containers must not see JuiceFS root credentials, Kubernetes Secret references,
or raw host paths. A privileged orchestrator must assemble the runtime mount and
report whether access has ended.

## Decision

Use a two-layer workload mount contract.

Product callers receive:

- `mount_binding_id`
- namespace/repo/volume IDs
- requested container `mount_path`
- read-only flag
- lifecycle status and lease expiry

Only the dedicated `orchestrator_mount` role can fetch:

- `payload_volume_subdir`
- platform Secret reference
- runtime security policy

Mount bindings are lease-based. Status, heartbeat, release, and revoke are GA
APIs. `revoked` is terminal only after the orchestrator confirms unmounted or
unable to write. Read-write uncertain sessions block direct restore to a save
point through the writer-session fence. Any non-terminal export or mount blocks
archive/delete/purge lifecycle drain when the lifecycle contract requires no
access.

## Consequences

Positive:

- Keeps Secret references out of ordinary product callers and workloads.
- Makes stale lease and revoke behavior auditable.
- Gives repo lifecycle a reliable drain signal.

Tradeoffs:

- Requires orchestrator implementation work before mount bindings can be GA
  enabled.
- AFSCP must reject degraded mount behavior with `CAPABILITY_DENIED`.
- Test environments need an orchestrator fake that proves confirmed-unmounted
  semantics.
