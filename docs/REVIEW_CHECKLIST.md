# Review Checklist

Use this checklist before beginning implementation.

## Product Boundary

- [ ] AFSCP core docs use generic storage concepts: volume, namespace, repo, template, export, mount, operation.
- [ ] Caller-specific concepts stay in integration docs only.
- [ ] Cross-namespace template clone remains rejected by default in P0.
- [ ] Ordinary concurrent read/write behavior is accepted without merge semantics.
- [ ] Ordinary client access uses controlled exports rather than raw JuiceFS.
- [ ] Legacy compatibility and migration sequencing are accepted as caller integration work.
- [ ] P0 repo layout is accepted: `repo_path` is the JVS `main` workspace real folder, with no child `workspace/` payload directory.

## Architecture

- [ ] AFSCP is deployed independently from calling products.
- [ ] AFSCP has its own ServiceAccount and Secret access.
- [ ] Calling products remain product authorization authorities.
- [ ] AFSCP owns storage execution and operation records.
- [ ] External orchestrators remain runtime mount execution layers.
- [ ] No direct dependency on `agentsmith-oss` exists.
- [ ] Namespace volume binding does not contain an authoritative raw filesystem path supplied by a caller.

## Security

- [ ] No ordinary client response contains JuiceFS metadata URL or object store credentials.
- [ ] Workload containers receive no JuiceFS root credentials.
- [ ] WebDAV blocks `.jvs`.
- [ ] Workload mounts prevent read/write access to `.jvs`.
- [ ] Path resolver rejects traversal and namespace mismatch.
- [ ] Template clone is checked by namespace/resource consistency in AFSCP.
- [ ] Mutating JVS operations use durable operation records and locks.
- [ ] Mutating AFSCP requests include the authorized end actor, separate from the calling service identity.

## Handoff

- [ ] Runtime language decision is captured in a new ADR.
- [ ] Generic internal API contract is reviewed with the first calling product team.
- [ ] Workload mount spec is reviewed with orchestrator owners.
- [ ] ExportAccess contract is reviewed with client connector owners.
- [ ] Operational rollback plan is written before storage mutations go live.
