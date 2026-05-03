# ADR 0001: Create AFSCP Storage Control Plane

Status: accepted for handoff

## Context

Applications need durable filesystem repos for workloads, clients, templates, and versioned save/restore. If storage responsibilities are embedded in one product API, backend credentials, path rules, and product workflows become tightly coupled.

## Decision

Create an independent application module named AFSCP.

AFSCP owns storage execution:

- volume credentials
- volume management
- namespace boundaries
- repo path allocation
- JVS operation execution
- repo template clone
- export runtime
- workload mount spec generation
- operation store and retries

Calling products remain product and authorization authorities.

## Consequences

Positive:

- Clear credential boundary.
- Reusable storage substrate.
- Cleaner namespace/volume model.
- Easier migration away from per-resource JuiceFS DB/bucket provisioning.
- Dedicated operation recovery for JVS and storage mutations.

Tradeoffs:

- New service to deploy and operate.
- New internal API contract.
- Requires integration adapters for each calling product and orchestrator.
