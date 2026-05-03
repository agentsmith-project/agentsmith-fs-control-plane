# Workload Mounts

AFSCP should generate generic workload mount bindings and orchestrator mount plans. It should not know the caller's workload product or runtime workflow.

## Current Problem

Product-specific systems often pass raw filesystem names, metadata URLs, or storage credentials into runtime managers. That makes storage isolation depend on application code and leaks backend details.

## Target Contract

New AFSCP-backed repos should use a two-layer contract.

Product callers receive a caller-visible mount binding:

```json
{
  "mount_binding_id": "wmb_123",
  "namespace_id": "ns_123",
  "repo_id": "repo_123",
  "volume_id": "vol_default",
  "mount_path": "/workspace",
  "read_only": false,
  "status": "issued",
  "lease_expires_at": "2026-05-03T13:00:00Z"
}
```

Only the dedicated orchestrator receives the privileged mount plan:

```json
{
  "mount_binding_id": "wmb_123",
  "volume_id": "vol_default",
  "volume_subdir": "afscp/namespaces/ns_123/repos/repo_123",
  "mount_path": "/workspace",
  "read_only": false,
  "secret_ref": {
    "namespace": "storage-system",
    "name": "juicefs-vol-default"
  },
  "security_policy": {
    "run_as_non_root": true,
    "allow_privileged": false,
    "drop_capabilities": ["CAP_DAC_OVERRIDE"],
    "jvs_metadata_protected": true
  }
}
```

The final field names should be agreed with the orchestrator that consumes this plan. `volume_subdir` is relative to the JuiceFS filesystem root and has no leading slash. The AFSCP-managed subroot is `afscp/`, so repo subdirs include that prefix.

Any shape that returns an absolute payload path and Secret reference to an ordinary product caller is rejected for P0 because it mixes product authorization with platform mount assembly.

## Responsibilities

AFSCP:

- resolves `volume_subdir`
- chooses volume
- chooses Secret reference for orchestrator-only plan
- validates namespace/repo consistency
- ensures `.jvs` is protected before enabling workload mounts
- audits mount binding and orchestrator plan issuance
- tracks lease/status for active read-write mount sessions
- blocks new read-write bindings when a repo restore fence is held

External orchestrator:

- creates or updates Secret/PV/PVC/Pod mount or equivalent runtime mount
- reports binding status
- sends heartbeat for live bindings
- releases bindings when runtime mounts end
- does not make product authorization decisions
- is the only ordinary service allowed to see JuiceFS Secret references

Workload:

- sees only mounted repo payload path
- receives no JuiceFS root credentials
- should run non-root by default
- must not be able to read or write `.jvs`

## Binding Lifecycle

P0 bindings are lease-based.

Statuses:

- `issued`
- `pending`
- `active`
- `releasing`
- `released`
- `revoked`
- `expired`
- `failed`

Rules:

- The orchestrator updates status when it starts, completes, releases, or fails a runtime mount.
- The orchestrator heartbeats before `lease_expires_at`.
- A read-write binding in `issued`, `pending`, `active`, or `releasing` with a live lease counts as an active writer session.
- An expired read-write binding still blocks restore-run until reconciliation marks it `expired`, `released`, `revoked`, or `failed`.
- AFSCP can revoke a binding; the orchestrator must unmount or stop using it and report final status.

## `.jvs` Protection

P0 workload mounts must not expose `.jvs` for read or write, even when the mount is read-only.

Permission-only protection on `.jvs` is not sufficient by itself because a writable parent directory may still allow rename/unlink/link/chmod/chown attempts against the entry. P0 requires a filtered view or equivalent filesystem/runtime gate that blocks lookup, read, write, create, rename, unlink, chmod, chown, and hardlink/symlink operations targeting root-level `.jvs`.

## Compatibility

Caller-specific compatibility adapters can translate from legacy business contracts to this generic mount binding outside AFSCP core.
