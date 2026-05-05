# ADR 0006: Use Service Identity Plus Namespace Roles

Status: accepted for development handoff

## Context

AFSCP is internal only. End users, desktop clients, and workloads must not call
it directly. AFSCP still needs storage-control authorization so one trusted
service cannot operate on the wrong namespace or fetch orchestrator-only Secret
references.

## Decision

Use authenticated service identity plus namespace-scoped roles.

Authentication:

- Primary production mechanism is mTLS/SPIFFE-style service identity, or an
  equivalent deployment service principal.
- A signed service token may be accepted only when deployment policy maps it to
  the same canonical `caller_service`.
- `X-AFSCP-Caller-Service` must match the authenticated principal or a configured
  alias; callers cannot self-assert arbitrary service names.

Required request context:

- `Idempotency-Key` for mutating calls.
- `X-Correlation-Id`.
- `X-AFSCP-Namespace-Id` where resource access is namespace-bound.
- `X-AFSCP-Actor-Type` and `X-AFSCP-Actor-Id` for mutating calls.

Authorization sources:

- `NamespaceVolumeBinding.allowed_callers`.
- deployment-level admin/operator allowlists.
- deployment-level migration allowlist.
- dedicated orchestrator allowlist for mount plan and mount status APIs.

Role split:

- Ordinary repo administration does not imply purge or lifecycle authority.
- `repo_lifecycle_admin` is required for archive/delete/restore lifecycle calls.
- Purge also requires namespace lifecycle policy approval and product
  confirmation/approval reference.
- `orchestrator_mount` must never be merged into ordinary product caller roles.
- `migration_admin` is a dedicated migration role and must never be merged into
  ordinary product caller roles.
- `volume_admin` is deployment/global volume policy, not an ordinary namespace
  binding product role.
- `break_glass_admin` is explicit and audited.

Denied checks are audited even when no durable mutation operation is created.

## Consequences

Positive:

- Prevents confused-deputy namespace access.
- Keeps product authorization outside AFSCP while preserving storage authority.
- Gives the development team a concrete role matrix for endpoint guards.

Tradeoffs:

- Calling products must pass actor and correlation context consistently.
- Deployment must provide a reliable principal-to-caller mapping.
- Tests must cover both authenticated principal and caller-service alias behavior.
