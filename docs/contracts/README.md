# Contracts

These documents are draft contracts for cross-repo review before endpoint implementation.

- [afscp-internal-api-v1.md](afscp-internal-api-v1.md)
- [namespace-volume-binding-v1.md](namespace-volume-binding-v1.md)
- [repo-path-contract-v1.md](repo-path-contract-v1.md)
- [jvs-runner-contract-v1.md](jvs-runner-contract-v1.md)
- [export-access-webdav-v1.md](export-access-webdav-v1.md)
- [workload-mount-binding-v1.md](workload-mount-binding-v1.md)
- [operation-state-machine-v1.md](operation-state-machine-v1.md)

Service skeleton work may begin while contracts are reviewed. Endpoint implementation should wait until:

- internal auth and caller-service authorization are accepted
- request/response/error schemas are added under `api/schemas/`
- P0 OpenAPI is generated under `api/openapi/`
- workload mount binding/orchestrator plan split is accepted by orchestrator owners
- `.jvs` mount protection gate is accepted
- WebDAV gateway policy boundary is accepted; stock `juicefs webdav` alone is not the P0 policy gate
- JVS commit/binary pin, required command smoke tests, and clean-CWD runner behavior are accepted
- writer-session fence and mount binding lifecycle are accepted
- revoke-request versus confirmed-unmounted terminal semantics are accepted
- operation recovery matrix is accepted
