# Contracts

These documents are GA pre-dev contracts for cross-repo review before endpoint implementation.

- [afscp-internal-api-v1.md](afscp-internal-api-v1.md)
- [namespace-volume-binding-v1.md](namespace-volume-binding-v1.md)
- [repo-path-contract-v1.md](repo-path-contract-v1.md)
- [repo-lifecycle-v1.md](repo-lifecycle-v1.md)
- [jvs-runner-contract-v1.md](jvs-runner-contract-v1.md)
- [export-access-webdav-v1.md](export-access-webdav-v1.md)
- [workload-mount-binding-v1.md](workload-mount-binding-v1.md)
- [operation-state-machine-v1.md](operation-state-machine-v1.md)

Service skeleton work may begin while contracts are reviewed. Endpoint implementation must wait until:

- internal auth and caller-service authorization are accepted
- request/response/error schemas are added under `api/schemas/`
- internal OpenAPI is generated under `api/openapi/`
- workload mount binding/orchestrator plan split is accepted by orchestrator owners
- JVS external-control/payload-only mount gate is accepted
- repo lifecycle transition, session drain, tombstone, restore, retention, purge confirmation, and recovery semantics are accepted
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
- Contract review evidence is required before storage handlers depend on a contract.
