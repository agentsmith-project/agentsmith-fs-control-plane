# Development Governance

Status: active governance contract.

AFSCP holds privileged storage credentials and mutates durable user data. The
development process must make product, security, contract, and operations
decisions visible before implementation depends on them.

## Governance Principles

- Build directly toward the GA contract in `docs/GA_PRE_DEV_READINESS.md`.
- Keep AFSCP product-agnostic; caller-specific behavior belongs in integration docs or sibling repos.
- Prefer small, reviewable changes, but do not define staged product semantics unless a contract requires them.
- Treat security boundaries, idempotency, audit, and recovery as product requirements.
- Do not implement endpoint handlers or storage mutations from narrative drafts when schemas and OpenAPI are missing.
- Do not expose raw JuiceFS credentials, Secret references, or host paths to ordinary callers, clients, or workloads.

## Decision Records

Required ADRs before storage mutation implementation:

- runtime language, framework, packaging, and test command
- service authentication and caller-service authorization model
- operation store and audit/outbox persistence
- repo lifecycle fence, retention, tombstone, restore, and purge policy
- JVS release binary/version/checksum pin and runner contract
- WebDAV gateway policy and credential lifecycle
- workload orchestrator mount-plan contract
- writer-session fence and restore safety behavior
- break-glass direct mount policy, if any break-glass direct mount capability exists

ADR lifecycle:

- `proposed`: under review and not implementation-binding
- `accepted`: implementation-binding
- `superseded`: replaced by a newer ADR
- `rejected`: recorded for traceability, not binding

Every ADR must include context, decision, rejected alternatives, security impact,
operational impact, contract impact, and rollback or supersession conditions.

## Contract Ownership

| Contract Area | Required Reviewers |
| --- | --- |
| Product boundary and GA scope | AFSCP product owner, API consumer-contract/generated-client compatibility reviewer |
| Internal API and schemas | AFSCP maintainer, API consumer-contract/generated-client compatibility reviewer, operator/tooling owner |
| Service auth and caller roles | AFSCP maintainer, security owner, API consumer-contract/generated-client compatibility reviewer |
| JVS runner | AFSCP maintainer, JVS owner |
| Repo lifecycle | AFSCP maintainer, AFSCP product owner, operations owner, security owner |
| WebDAV export | AFSCP maintainer, security owner, API consumer-contract/generated-client compatibility reviewer |
| Workload mount plan | AFSCP maintainer, platform/runtime contract reviewer, security owner |
| Operation/audit/recovery | AFSCP maintainer, operations owner, security owner |
| Deployment and Secret access | AFSCP maintainer, platform owner, security owner |

Named people can be added later, but the role must be clear in every review.

## Review Gates

The following gates are blocking for endpoint handlers and storage mutation
logic:

- schemas and OpenAPI are generated and reviewed
- standard error envelope and stable error families are reviewed
- credential boundary review passes
- JVS runner pin and smoke evidence are present
- workload mount platform/runtime contract is accepted
- repo lifecycle contract is accepted for archive/delete/restore/purge storage state
- WebDAV gateway contract is accepted for exports
- writer-session fence contract is accepted for restore/export/mount interactions
- operation recovery and audit retention semantics are accepted
- GA-blocking risks are closed or have approved residual-risk acceptance under this governance contract

Every gate needs:

- owner role
- approving reviewer role
- evidence link or file path
- blocking status
- waiver decision, if any

Gate closure is recorded in `docs/READINESS_EVIDENCE.md`. A gate is not closed
because a document says it should be done; it is closed only when the ledger
links to the reviewed evidence.

Waivers and residual-risk acceptance are the same controlled process. They are
allowed only for risks that cannot plausibly cause credential exposure, tenant
isolation failure, user data loss, irrecoverable operation ambiguity, or a
caller-visible contract break. Those risk classes are non-waivable for GA.

Any accepted residual risk must include:

- product, security, and operations approver roles
- expiration or review date
- compensation controls
- evidence link
- residual risk statement
- rollback or disablement condition

## Contract Versioning

- Public internal contracts use versioned filenames or versioned OpenAPI paths.
- Breaking changes require a new contract version or explicit migration plan.
- Schema/OpenAPI drift is blocking once generated artifacts exist.
- Contract examples must not include forbidden credential material outside explicitly orchestrator-only examples.
- Stable error codes are part of the contract and require review before renaming or removal.

## Pull Request Expectations

Every implementation PR that touches a GA boundary must state:

- which contract or ADR it implements
- security boundary impact
- operation/audit impact
- test evidence
- whether generated schemas/OpenAPI changed
- whether docs need to change

PRs must not introduce caller-specific product model names into AFSCP core
packages.

## Risk Governance

`docs/RISK_REGISTER.md` is the live risk source. A risk is GA-blocking when it
can plausibly cause credential exposure, tenant isolation failure, data loss,
irrecoverable operation ambiguity, or a caller-visible contract break.

Risk closure requires:

- mitigation decision
- evidence requirement
- owner role
- residual risk statement
- link to updated docs, tests, runbooks, or ADRs

Risk decisions are tracked in `docs/RISK_REGISTER.md`; readiness evidence is
tracked in `docs/READINESS_EVIDENCE.md`.

## Operational Readiness

Before GA, operations owners must have:

- health and readiness checks
- structured logs with secret redaction
- metrics and alerts for operation failure, stale leases, restore rejection, export credential misuse, JVS doctor failure, audit outbox lag, and volume health
- alert severity, paging thresholds, and recovery targets for held fences, stale leases, lifecycle drain, purge failure, and audit outbox lag
- backup and restore plan for operation store and metadata records
- Secret rotation runbook
- recovery runbooks rehearsed against representative failure cases
- on-call escalation path for `operator_intervention_required`
