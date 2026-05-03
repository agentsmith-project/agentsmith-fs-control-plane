# Review Checklist

Use this checklist before beginning implementation.

## Product

- [ ] Workspace storage profile behavior is accepted by AgentSmith product owners.
- [ ] Cross-workspace template clone remains explicitly forbidden.
- [ ] Ordinary concurrent read/write behavior is accepted without merge semantics.
- [ ] Desktop ordinary path uses WebDAV/export rather than raw JuiceFS.
- [ ] Legacy file library compatibility and migration sequencing are accepted.

## Architecture

- [ ] AFSCP is deployed independently from AgentSmith API.
- [ ] AFSCP has its own ServiceAccount and Secret access.
- [ ] AgentSmith API remains the user authorization authority.
- [ ] AFSCP owns storage execution and operation records.
- [ ] sandbox-manager remains Kubernetes execution layer.
- [ ] No direct dependency on `agentsmith-oss` exists.

## Security

- [ ] No ordinary client response contains JuiceFS metadata URL or object store credentials.
- [ ] Sandbox workloads receive no JuiceFS root credentials.
- [ ] WebDAV blocks `.jvs`.
- [ ] Path resolver rejects traversal and workspace mismatch.
- [ ] Template clone is checked in both AgentSmith API and AFSCP.
- [ ] Mutating JVS operations use durable operation records and locks.

## Handoff

- [ ] Runtime language decision is captured in a new ADR.
- [ ] Internal API contract is reviewed with AgentSmith API owners.
- [ ] Sandbox binding v2 is reviewed with sandbox-manager owners.
- [ ] Desktop `ExportAccess` contract is reviewed with Desktop owners.
- [ ] Operational rollback plan is written before storage mutations go live.
