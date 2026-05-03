# ADR 0001: Create AgentSmith FS Control Plane

Status: accepted for handoff

## Context

AgentSmith needs persistent file libraries for notebooks, agent sandboxes, Desktop, and templates. Current storage responsibilities are spread across AgentSmith API, Desktop, and sandbox-manager, and ordinary mount contracts can include JuiceFS metadata and bucket details.

## Decision

Create a new internal application module named `agentsmith-fs-control-plane` (AFSCP).

AFSCP owns storage execution:

- JuiceFS root credentials.
- Storage pool management.
- Repo path allocation.
- JVS operation execution.
- Export runtime.
- Sandbox mount spec generation.
- Operation store and retries.

AgentSmith API remains the product and authorization authority.

## Consequences

Positive:

- Clear credential boundary.
- Cleaner workspace storage profile model.
- Easier migration away from per-task JuiceFS DB/bucket provisioning.
- Dedicated operation recovery for JVS and storage mutations.

Tradeoffs:

- New service to deploy and operate.
- New internal API contract.
- Requires cross-repo integration with AgentSmith API, Desktop, sandbox-manager, and JVS.
