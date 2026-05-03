# Operations And Migration

## Operation Store

AFSCP should not rely on in-memory state for mutating operations.

The operation store should record:

- operation ID
- idempotency key
- request hash
- correlation ID
- caller service
- authorized actor type
- authorized actor ID
- namespace ID
- repo ID or template ID
- operation type
- phase
- attempt
- lease owner/expiry
- input summary
- state
- external resource IDs
- started/finished timestamps
- JVS JSON output
- verification result
- compensation status
- error details
- audit event IDs

Operation states should support at least:

- `queued`
- `running`
- `succeeded`
- `failed`
- `cancel_requested`
- `cancelled`

## Deployment

MVP:

- One AFSCP API/worker Deployment.
- WebDAV gateway may be same pod sidecar or same image subprocess.
- Dedicated ServiceAccount.
- Dedicated Secrets for JuiceFS root credentials.
- Internal-only Service.

P1:

- Split export gateway pool if WebDAV load grows.
- Split worker queue if JVS operations become heavy.
- Add multiple volume controllers if sharding grows.

## Rollout Strategy

1. Deploy AFSCP with no external caller traffic.
2. Register a default volume.
3. Add namespace volume binding.
4. Route only feature-flagged callers or namespaces to AFSCP.
5. Route new repos to AFSCP.
6. Keep legacy repos on old paths.
7. Add WebDAV export for AFSCP-backed repos.
8. Add workload mount binding/orchestrator integration.
9. Add JVS save/restore.
10. Add repo templates and same-namespace clone.
11. Plan legacy migration as a separate release.

## Legacy Migration

Legacy repos may have per-resource metadata DB and bucket. Do not migrate implicitly.

Explicit migration flow should be:

1. Verify source legacy repo health.
2. Put the source into maintenance/read-only mode or establish a delta-sync capable migration plan.
3. Create target repo through AFSCP under the namespace volume binding.
4. Copy/import payload data.
5. Record manifest counts, hashes where feasible, source generation, and copy timestamp.
6. Run delta sync until the final cutover window.
7. Freeze writes for final sync.
8. Initialize or import JVS metadata and create a migration baseline save point.
9. Run `jvs doctor --strict`.
10. Switch the calling product's backend reference.
11. Preserve rollback metadata and cutover timestamp.
12. Archive legacy backend only after verification and operator approval.

## Restore Safety

Ordinary concurrent reads and writes are allowed for normal file IO. Restore-run is different: it changes repo version state.

P0 restore-run must reject active read-write WebDAV export and workload mount sessions by default. It should:

- run restore preview
- acquire the per-repo writer-session fence to block new read-write export/mount issuance
- inspect active sessions
- reject if active read-write sessions exist
- acquire repo-level restore lock
- execute restore
- validate with `jvs doctor --strict`
- emit audit events
- release the writer-session fence after terminal success/failure handling

P1 may add operator break-glass restore with explicit approval, session revoke/drain, richer user warnings, and rollback runbooks.

## Backup And Recovery

AFSCP operators must be able to recover:

- operation store
- namespace volume binding records
- JuiceFS credential references
- repo path records
- JVS metadata
- export session state
- workload mount binding lease/status state

AFSCP should be restart-safe. Reconciliation should handle operations left in `running` state after process death.
