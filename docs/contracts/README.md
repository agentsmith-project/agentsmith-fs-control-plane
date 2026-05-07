# Contracts

These documents are GA implementation-baseline contracts for current review,
contract verification, generated artifacts, endpoint handlers, and storage
behavior.

- [afscp-internal-api-v1.md](afscp-internal-api-v1.md)
- [namespace-volume-binding-v1.md](namespace-volume-binding-v1.md)
- [repo-path-contract-v1.md](repo-path-contract-v1.md)
- [repo-lifecycle-v1.md](repo-lifecycle-v1.md)
- [jvs-runner-contract-v1.md](jvs-runner-contract-v1.md)
- [export-access-webdav-v1.md](export-access-webdav-v1.md)
- [workload-mount-binding-v1.md](workload-mount-binding-v1.md)
- [operation-state-machine-v1.md](operation-state-machine-v1.md)

The implementation baseline, generated JSON schemas, and internal OpenAPI now
exist. Continue new or changed endpoint handler and storage behavior only when
the relevant contract, generated artifact, and readiness evidence remain
aligned. FINAL GA acceptance remains governed by `docs/READINESS_EVIDENCE.md`;
owner, security, generated-client, operations, runbook drill, and human
sign-off entries must be complete before the applicable readiness gate is
closed.

Relevant acceptance evidence includes:

- internal auth and caller-service authorization are accepted
- request/response/error schemas under `api/schemas/` remain aligned
- internal OpenAPI under `api/openapi/` remains aligned with generated clients
- workload mount binding/orchestrator plan split is accepted by platform/runtime contract reviewers
- JVS external-control/payload-only mount gate is accepted
- repo lifecycle transition, session drain, tombstone, restore, retention, purge approval-reference, and recovery semantics are accepted
- WebDAV gateway policy boundary is accepted; stock `juicefs webdav` alone is not the GA policy gate
- JVS release binary/version/checksum pin, required command smoke tests, and clean-CWD runner behavior are accepted
- writer-session fence and mount binding lifecycle are accepted
- revoke-request versus confirmed-unmounted terminal semantics are accepted
- operation recovery matrix is accepted

## Contract Governance

- Each contract has an owning reviewer role listed in `docs/DEVELOPMENT_GOVERNANCE.md`.
- Breaking changes require a new contract version or an explicit migration plan.
- Generated JSON schemas and OpenAPI must match these narrative contracts.
- Stable error codes are part of the contract.
- Orchestrator-only examples may include Secret references; ordinary product caller examples must not.
- Contract review evidence is required when storage handlers add or change
  behavior that depends on a contract.
