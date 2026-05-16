# Operator Repair Contract v1

This GA implementation-baseline contract defines the minimal GA operator repair write path. It is a
shared API, storage, audit, and release-evidence contract, not an operator
platform and not an approval gate. Current readiness and release governance
remain in `docs/READINESS_EVIDENCE.md` and `scripts/verify-ga-release.sh`.

## Entry

- API entry: `POST /internal/v1/operations/{operationId}:repair`
- Required role: `operator_admin`
- Route class: operation/operator inspection boundary; no namespace header is
  required.
- Product callers with `operation_inspector` or namespace-scoped roles can
  inspect only their authorized operation records and cannot repair.

## Allowlist

The only v1 repair action is
`terminalize_unsupported_intervention_as_failed`.

The action may terminalize an operation from
`operator_intervention_required` to `failed` only when all preconditions hold:

- the operation error code is `OPERATION_RECOVERY_REQUIRED`
- the operation error or verification details carry an unsupported/disabled
  recovery marker such as `unsupported_operation_recovery`
- no active lease is present
- no session fence or other held fence ambiguity is present
- the phase is not a committed, writer-fenced, consuming, or discarding phase
- the request includes `reason`, `evidence_ref`, and `affected_ids`

The response and audit payload include `action`, `operation_id`,
`before_state`, `after_state`, `repair_outcome`, `reason`, `evidence_ref`,
`affected_ids`, and `audit_event_id`. All fields are redacted before they are
returned or persisted.

## Durable Boundary

The store implementation must use one atomic compare-and-set mutation that:

- selects the eligible `operator_intervention_required` operation with the
  unsupported recovery precondition
- updates only the operation terminal state to `failed`
- appends the audit outbox event in the same transaction boundary
- returns the redacted before/after repair result

The implementation must fail closed when the operation is missing, terminal,
not in `operator_intervention_required`, lacks the unsupported recovery marker,
has an active lease, has a session/fence ambiguity, or the CAS predicate finds
no row.

## Forbidden

The repair surface must not expose arbitrary SQL, generic state rewrite,
generic JSON patch, arbitrary operation-state transitions, fence release,
session mutation, restore mutation, repo/storage/JVS mutation, workload
mutation, export session mutation, or purge/break-glass behavior.

Additional repair actions require a new allowlist entry, preconditions,
before/after evidence, audit shape, tests, and release evidence.
