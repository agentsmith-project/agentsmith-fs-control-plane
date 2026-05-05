# Review Checklist

Use this checklist before beginning implementation.

## Product Boundary

- [ ] AFSCP core docs use generic storage concepts: volume, namespace, repo, template, export, mount, operation.
- [ ] Caller-specific concepts stay in integration docs only.
- [ ] Cross-namespace template clone remains rejected by default in P0.
- [ ] Ordinary concurrent read/write behavior is accepted without merge semantics.
- [ ] Restore-run is treated as a version mutation, blocks new read-write sessions, and rejects active read-write sessions in P0.
- [ ] Ordinary client access uses controlled exports rather than raw JuiceFS.
- [ ] Legacy compatibility and migration sequencing are accepted as caller integration work.
- [ ] P0 repo layout is accepted: each repo has an AFSCP-only JVS external control root and a separate payload root mounted/exported to clients and workloads.

## Architecture

- [ ] AFSCP is deployed independently from calling products.
- [ ] AFSCP has its own ServiceAccount and Secret access.
- [ ] Calling products remain product authorization authorities.
- [ ] AFSCP owns storage execution and operation records.
- [ ] External orchestrators remain runtime mount execution layers and do not decide product authorization.
- [ ] No direct dependency on `agentsmith-oss` exists.
- [ ] Namespace volume binding does not contain an authoritative raw filesystem path supplied by a caller.
- [ ] Namespace binding includes caller-service authorization policy.

## Security

- [ ] No ordinary client response contains JuiceFS metadata URL or object store credentials.
- [ ] Workload containers receive no JuiceFS root credentials.
- [ ] Ordinary product callers never receive JuiceFS Secret references.
- [ ] Orchestrator-only mount plans are protected by a dedicated caller role.
- [ ] WebDAV is served by an AFSCP-controlled policy gateway or equivalent wrapper; stock `juicefs webdav` alone is not accepted.
- [ ] WebDAV exposes only payload roots, never JVS control roots, and rejects root-level `.jvs` access/creation attempts as defense-in-depth.
- [ ] Workload mounts expose only `payload_volume_subdir`; embedded-control repos are rejected until migrated or protected by a verified filtered view.
- [ ] Mount binding lease/status lifecycle defines revoke-request versus confirmed-unmounted terminal states and is integrated with restore-run.
- [ ] Path resolver rejects traversal and namespace mismatch.
- [ ] Template clone is checked by namespace, resource, and P0 volume rules in AFSCP.
- [ ] Mutating JVS operations use durable operation records, phases, and resource locks.
- [ ] Mutating AFSCP requests include the authorized end actor, separate from the calling service identity.
- [ ] Denied authorization and path checks emit audit events.

## Handoff

- [ ] Runtime language decision is captured in a new ADR.
- [ ] JVS commit/binary is pinned, required command smoke tests pass, and the runner CWD cannot affect repo resolution.
- [ ] Generic internal API contract is reviewed with the first calling product team.
- [ ] Workload mount binding/orchestrator plan split is reviewed with orchestrator owners.
- [ ] Writer-session fence is reviewed with export and orchestrator owners.
- [ ] Export session/access credential contract is reviewed with client connector owners.
- [ ] Operational rollback plan is written before storage mutations go live.
