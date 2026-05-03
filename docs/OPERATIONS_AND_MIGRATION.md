# Operations And Migration

## Operation Store

AFSCP should not rely on in-memory state for mutating operations.

The operation store should record:

- operation ID
- idempotency key
- caller service identity
- tenant workspace ID
- storage repo ID
- operation type
- input summary
- state
- started/finished timestamps
- JVS JSON output
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
- Add multiple storage pool controllers if sharding grows.

## Rollout Strategy

1. Deploy AFSCP with no user traffic.
2. Register a default storage pool.
3. Add AgentSmith workspace storage profile support.
4. Route only new workspaces or feature-flagged workspaces to AFSCP.
5. Route new file libraries to AFSCP.
6. Keep legacy file libraries on old path.
7. Add Desktop/WebDAV export for AFSCP-backed libraries.
8. Add sandbox binding v2 for AFSCP-backed libraries.
9. Add JVS save/restore.
10. Add templates and same-workspace clone.
11. Plan legacy migration as a separate release.

## Legacy Migration

Legacy libraries may have per-library metadata DB and bucket. Do not migrate implicitly.

Explicit migration flow should be:

1. Verify source library is healthy.
2. Create target repo through AFSCP under the workspace storage profile.
3. Copy/import payload data.
4. Initialize or import JVS metadata.
5. Run `jvs doctor --strict`.
6. Switch AgentSmith file library backend record.
7. Preserve rollback metadata.
8. Archive legacy backend only after verification.

## Restore Safety

MVP may allow restore while ordinary writers exist, but the product should clearly document that AgentSmith does not merge concurrent writes.

P1 should add stricter restore fencing:

- notify active sandbox sessions
- pause or drain WebDAV write sessions
- acquire repo-level restore lock
- run restore preview
- execute restore
- validate with `jvs doctor --strict`
- release sessions

## Backup And Recovery

AFSCP operators must be able to recover:

- operation store
- storage profile projections
- JuiceFS credential references
- repo path records
- JVS metadata
- export session state

AFSCP should be restart-safe. Reconciliation should handle operations left in `running` state after process death.
