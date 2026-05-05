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
- `operator_intervention_required`

## Deployment

GA deployment:

- One AFSCP API/worker Deployment.
- WebDAV gateway may be same pod sidecar or same image subprocess.
- Dedicated ServiceAccount.
- Dedicated Secrets for JuiceFS root credentials.
- Internal-only Service.

Future deployment options:

- Split export gateway pool if WebDAV load grows.
- Split worker queue if JVS operations become heavy.
- Add multiple volume controllers if sharding grows.

## Rollout Strategy

1. Deploy AFSCP with no external caller traffic.
2. Register a default volume.
3. Add namespace volume binding.
4. Add and validate the AFSCP WebDAV policy gateway.
5. Add JVS save/restore with pinned binary smoke tests and recovery handling.
6. Add repo templates and same-namespace clone.
7. Route only feature-flagged callers or namespaces to AFSCP after their ordinary client path no longer depends on direct JuiceFS credentials.
8. Route new repos to AFSCP only after export access is validated for that caller.
9. Add workload mount binding/orchestrator integration only after the JVS external-control/payload-only mount contract is implemented; otherwise return capability errors.
10. Keep legacy repos on old paths.
11. Plan legacy migration as a separate release.

## Legacy Migration

Legacy repos may have per-resource metadata DB and bucket. Do not migrate implicitly.

Explicit migration flow should be:

1. Verify source legacy repo health.
2. Put the source into maintenance/read-only mode or establish a delta-sync capable migration plan.
3. Create target repo through AFSCP under the namespace volume binding, using the separated control/payload layout.
4. Copy/import payload data into the target payload root.
5. Record manifest counts, hashes where feasible, source generation, and copy timestamp.
6. Run delta sync until the final cutover window.
7. Freeze writes for final sync.
8. Verify external JVS control metadata or import JVS metadata and create a migration baseline save point.
9. Run `jvs doctor --strict`.
10. Switch the calling product's backend reference.
11. Preserve rollback metadata and cutover timestamp.
12. Archive legacy backend only after verification and operator approval.

## Restore Safety

Ordinary concurrent reads and writes are allowed for normal file IO. Restore-run is different: it changes repo version state.

GA restore-run must reject active or uncertain read-write WebDAV export and workload mount sessions by default. It should:

- run restore preview
- acquire the per-repo writer-session fence to block new read-write export/mount issuance
- inspect active and uncertain read-write sessions
- reject if active or uncertain read-write sessions exist
- acquire repo-level restore lock
- execute restore
- validate with `jvs doctor --strict`
- emit audit events
- release the writer-session fence after terminal success/failure handling

Future work may add operator break-glass restore with explicit approval, session revoke/drain, richer user warnings, and rollback runbooks.

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
