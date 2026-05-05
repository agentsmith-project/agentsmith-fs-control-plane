# Security Policy

This project will hold and operate privileged storage credentials. Treat security boundaries as product requirements, not implementation details.

## Supported Versions

No released version exists yet.

## Reporting a Vulnerability

For now, report vulnerabilities through the maintainers in the `agentsmith-project` GitHub organization.

## GA Security Requirements

- AFSCP is the only ordinary service that can read JuiceFS root metadata and object store credentials.
- Calling products must not read or return JuiceFS root credentials.
- Ordinary clients must not receive `metadata_url`, bucket URL, access key, or secret key.
- Workload containers must not receive JuiceFS root credentials in env vars, mounted files, or service account tokens.
- AFSCP-managed repos must keep JVS control metadata outside the workload/export payload root.
- WebDAV/export and workload mounts must expose only payload roots, and payload roots must not contain `.jvs`.
- AFSCP path resolution must reject absolute paths, `..` traversal, symlink escape, and caller-provided raw filesystem paths.
- Template clone must be rejected across namespaces by default for GA.
- JVS mutating operations require operation records, idempotency keys, audit logs, and per-repo operation locks.
- Restore-run must acquire the writer-session fence, block new read-write sessions, and reject active or uncertain read-write export/workload sessions by default.
- Break-glass raw JuiceFS direct mount must be disabled by default and must not become an ordinary user or workload access path.
