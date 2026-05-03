# Operations And Migration

## Operation Store

AFSCP should not rely on in-memory state for mutating operations.

The operation store should record:

- operation ID
- idempotency key
- correlation ID
- caller service identity
- authorized actor type
- authorized actor ID
- namespace ID
- repo ID or template ID
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
- Add multiple volume controllers if sharding grows.

## Rollout Strategy

1. Deploy AFSCP with no external caller traffic.
2. Register a default volume.
3. Add namespace volume binding.
4. Route only feature-flagged callers or namespaces to AFSCP.
5. Route new repos to AFSCP.
6. Keep legacy repos on old paths.
7. Add WebDAV export for AFSCP-backed repos.
8. Add workload mount spec integration.
9. Add JVS save/restore.
10. Add repo templates and same-namespace clone.
11. Plan legacy migration as a separate release.

## Legacy Migration

Legacy repos may have per-resource metadata DB and bucket. Do not migrate implicitly.

Explicit migration flow should be:

1. Verify source legacy repo health.
2. Create target repo through AFSCP under the namespace volume binding.
3. Copy/import payload data.
4. Initialize or import JVS metadata.
5. Run `jvs doctor --strict`.
6. Switch the calling product's backend reference.
7. Preserve rollback metadata.
8. Archive legacy backend only after verification.

## Restore Safety

MVP may allow restore while ordinary writers exist, but callers should clearly document that AFSCP does not merge concurrent writes.

P1 should add stricter restore fencing primitives:

- notify active mount/export sessions
- pause or drain WebDAV write sessions
- acquire repo-level restore lock
- run restore preview
- execute restore
- validate with `jvs doctor --strict`
- release sessions

## Backup And Recovery

AFSCP operators must be able to recover:

- operation store
- namespace volume binding records
- JuiceFS credential references
- repo path records
- JVS metadata
- export session state

AFSCP should be restart-safe. Reconciliation should handle operations left in `running` state after process death.
