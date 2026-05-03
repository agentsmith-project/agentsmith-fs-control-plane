# Security Policy

This project will hold and operate privileged storage credentials. Treat security boundaries as product requirements, not implementation details.

## Supported Versions

No released version exists yet.

## Reporting a Vulnerability

For now, report vulnerabilities through the maintainers in the `agentsmith-project` GitHub organization.

## P0 Security Requirements

- AFSCP is the only ordinary service that can read JuiceFS root metadata and object store credentials.
- Calling products must not read or return JuiceFS root credentials.
- Ordinary clients must not receive `metadata_url`, bucket URL, access key, or secret key.
- Workload containers must not receive JuiceFS root credentials in env vars, mounted files, or service account tokens.
- WebDAV/export must hide or block `.jvs`.
- Workload mounts must prevent read/write access to `.jvs`.
- AFSCP path resolution must reject absolute paths, `..` traversal, symlink escape, and caller-provided raw filesystem paths.
- Template clone must be rejected across namespaces by default in P0.
- JVS mutating operations require operation records, idempotency keys, audit logs, and per-repo operation locks.
