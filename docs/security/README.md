# Security

Start with [threat-model.md](threat-model.md).

The P0 security review must cover:

- JuiceFS credential boundary.
- Workspace path isolation.
- `.jvs` protection.
- WebDAV chroot/filter behavior.
- Sandbox non-root and credential restrictions.
- Cross-workspace template clone rejection.
- Operation store recovery behavior.
