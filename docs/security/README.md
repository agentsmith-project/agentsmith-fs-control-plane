# Security

Start with [threat-model.md](threat-model.md).

The GA security review must cover:

- JuiceFS credential boundary.
- Namespace path isolation.
- JVS control metadata protection.
- WebDAV chroot/filter behavior.
- Workload non-root and credential restrictions.
- Cross-namespace template clone rejection.
- Operation store recovery behavior.
