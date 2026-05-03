# Security Policy

This project will hold and operate privileged storage credentials. Treat security boundaries as product requirements, not implementation details.

## Supported Versions

No released version exists yet.

## Reporting a Vulnerability

For now, report vulnerabilities through the AgentSmith project maintainers in the `agentsmith-project` GitHub organization.

## P0 Security Requirements

- AFSCP is the only ordinary service that can read JuiceFS root metadata and object store credentials.
- AgentSmith API must not read or return JuiceFS root credentials.
- Desktop must not receive `metadata_url`, bucket URL, access key, or secret key for ordinary mounts.
- Sandbox workloads must not receive JuiceFS root credentials in env vars, mounted files, or service account tokens.
- WebDAV/export must hide or block `.jvs`.
- AFSCP path resolution must reject absolute paths, `..` traversal, symlink escape, and caller-provided raw filesystem paths.
- Template clone must be rejected across AgentSmith workspaces at both AgentSmith API and AFSCP path-boundary layers.
- JVS mutating operations require operation records, idempotency keys, audit logs, and per-repo operation locks.
