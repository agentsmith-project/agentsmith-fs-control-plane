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
aligned. FINAL GA is governed by `docs/GA_RELEASE_GATES.md`,
`docs/READINESS_EVIDENCE.md`, and `scripts/verify-ga-release.sh`.

Relevant automated evidence includes:

- internal auth and caller-service authorization are contract and test guarded
- request/response/error schemas under `api/schemas/` remain aligned
- internal OpenAPI under `api/openapi/` remains aligned with schemas and contracts
- workload mount binding/orchestrator plan split is contract and test guarded
- JVS external-control/payload-only mount behavior is contract and test guarded
- repo lifecycle transition, session drain, tombstone, restore, retention, purge approval-reference, and recovery semantics are contract and test guarded
- WebDAV gateway policy boundary is guarded; stock `juicefs webdav` alone is not the GA policy boundary
- JVS release binary/version/checksum pin, required command smoke tests, and clean-CWD runner behavior are recorded and test guarded
- writer-session fence and mount binding lifecycle are test guarded
- revoke-request versus confirmed-unmounted terminal semantics are test guarded
- operation recovery matrix is test guarded

## Contract Governance

- Each contract has an owner role listed in `docs/DEVELOPMENT_GOVERNANCE.md`.
- Breaking changes require a new contract version or an explicit migration plan.
- Generated JSON schemas and OpenAPI must match these narrative contracts.
- Stable error codes are part of the contract.
- Orchestrator-only examples may include Secret references; ordinary product caller examples must not.
- Repo-local contract evidence is required when storage handlers add or change
  behavior that depends on a contract.
