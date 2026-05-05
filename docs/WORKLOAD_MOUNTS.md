# Workload Mounts

AFSCP should generate generic workload mount bindings and orchestrator mount plans. It should not know the caller's workload product or runtime workflow.

## Current Problem

Product-specific systems often pass raw filesystem names, metadata URLs, or storage credentials into runtime managers. That makes storage isolation depend on application code and leaks backend details.

## Target Contract

New AFSCP-backed repos must use a two-layer contract.

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
  "payload_volume_subdir": "afscp/namespaces/ns_123/repos/repo_123/payload",
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
    "jvs_control_outside_payload": true
  }
}
```

This example assumes an AFSCP-managed repo created with JVS external control root mode. The orchestrator mounts only the payload subdir. It does not receive the JVS control root.

The final field names should be agreed with the orchestrator that consumes this plan. `payload_volume_subdir` is relative to the JuiceFS filesystem root and has no leading slash. The AFSCP-managed subroot is `afscp/`, so repo payload subdirs include that prefix.

Any shape that returns an absolute payload path and Secret reference to an ordinary product caller is rejected for GA because it mixes product authorization with platform mount assembly.

## Responsibilities

AFSCP:

- resolves `payload_volume_subdir`
- chooses volume
- chooses Secret reference for orchestrator-only plan
- validates namespace/repo consistency
- ensures the repo uses JVS external control root mode before enabling workload mounts
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
- must not see JVS control metadata

## Binding Lifecycle

GA bindings are lease-based.

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
- An expired read-write binding still blocks restore-run until reconciliation marks it `expired`, `released`, confirmed-unmounted `revoked`, or `failed`.
- Any binding, read-only or read-write, blocks repo archive/delete/purge lifecycle drain until AFSCP has a confirmed terminal non-accessing state.
- AFSCP can revoke a binding; the orchestrator must unmount or stop using it and report final status.
- `revoked` is terminal only after the orchestrator confirms that the runtime can no longer write. A revoke request waiting for runtime teardown remains `releasing` and continues to block restore-run.

## JVS Control Metadata Protection

GA workload mounts expose only the JVS payload root. For AFSCP-managed new repos, JVS control metadata is in the external control root and is outside the mounted subtree.

Permission-only protection on embedded `.jvs` is not sufficient because a writable parent directory may still allow rename/unlink/link/chmod/chown attempts against the entry. If a legacy embedded-control repo is encountered, AFSCP must reject workload mounts until the repo is migrated to external control root mode or protected by a verified filtered view.

## JuiceFS CSI Feasibility Note

JuiceFS CSI can mount a selected filesystem subdirectory as the volume root, and Kubernetes `volumeMounts.subPath` can mount a subpath from an already-bound volume. AFSCP uses that capability to mount the `payload/` subdir.

Do not mount the repo container directory. It contains both `control/` and `payload/`, and would expose the control root. The orchestrator must map `payload_volume_subdir` to a JuiceFS CSI-supported form, such as `mountOptions: ["subdir=afscp/.../payload"]` or a controlled Kubernetes `subPath`.

Do not assume `volumeAttributes["subdir"]` is portable without verifying the pinned CSI driver version.

## GA Enablement Rule

AFSCP must not issue workload mount bindings for a repo/runtime unless the
orchestrator contract supports:

- orchestrator-only mount plan retrieval
- payload-only subdir mounting
- Secret RBAC that excludes ordinary product callers and workloads
- heartbeat before lease expiry
- idempotent release
- revoke request followed by confirmed-unmounted or confirmed-unable-to-write terminal status
- stale lease reconciliation

If any requirement is missing, AFSCP returns a stable capability error instead
of issuing a read-only or read-write degraded binding.

## Compatibility

Caller-specific compatibility adapters can translate from legacy business contracts to this generic mount binding outside AFSCP core.
