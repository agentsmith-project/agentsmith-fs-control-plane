# ADR 0010: Build An AFSCP-Controlled WebDAV Gateway

Status: accepted for development handoff

## Context

Clients need file access without raw JuiceFS credentials. Stock `juicefs webdav`
can expose files, but it does not by itself enforce AFSCP's canonical path
resolver, payload-root chroot, method policy, credential lifecycle, or audit
contract.

## Decision

Build or wrap a WebDAV gateway controlled by AFSCP.

The gateway must:

- authorize by `ExportSession`.
- expose only repo `payload/`.
- reject root-level `.jvs` access or creation attempts.
- use the shared path resolver for source and destination paths.
- reject traversal, encoded traversal, double-decoded traversal, symlink escape,
  and namespace/repo mismatch.
- enforce read-only versus read-write method policy.
- close or reconcile active write-capable requests after revoke/expiry before
  the export stops counting as an active or uncertain writer.
- confirm no future access before lifecycle drain treats an export as terminal.
- redact credentials from logs, operations, audit, and errors.

## Consequences

Positive:

- Keeps ordinary client access away from JuiceFS root credentials.
- Makes direct restore to a save point and lifecycle drain enforceable through the writer-session fence.
- Gives product clients stable TTL/revoke semantics.

Tradeoffs:

- More work than shelling out to `juicefs webdav`.
- Gateway tests must cover every WebDAV method and destination path.
- Export session reconciliation is part of correctness, not optional telemetry.
