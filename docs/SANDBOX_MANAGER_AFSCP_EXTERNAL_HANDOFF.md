# sandbox-manager Changes for AFSCP Integration

Status: external-team handoff draft.

Audience: sandbox-manager and runtime platform developers.

This document records the sandbox-manager-side changes required for AFSCP integration. It is intentionally scoped to runtime orchestration responsibilities so the AFSCP team can continue AFSCP-only development without owning sandbox-manager implementation.

## Summary

sandbox-manager should act as the external workload mount orchestrator for AFSCP-backed repositories.

In the target design, sandbox-manager does not receive storage credentials from AgentSmith and does not make product authorization decisions. It consumes an AFSCP orchestrator-only mount plan, creates or updates the required Kubernetes/runtime mount resources, and reports binding state back to AFSCP.

The target integration is:

1. AgentSmith asks AFSCP for a workload mount binding after product authorization.
2. AFSCP returns caller-visible binding data to AgentSmith.
3. sandbox-manager, using an orchestrator-only service identity, fetches the AFSCP `OrchestratorMountPlan`.
4. sandbox-manager turns the plan into Secret/PV/PVC/Pod mount or equivalent runtime mount state.
5. sandbox-manager reports status, heartbeat, release, revoke, and confirmed-unmounted state to AFSCP.

## Current Behavior to Retire

The current sandbox-manager workspace binding flow accepts storage details from AgentSmith and creates Kubernetes resources directly from that product request:

- request payload includes JuiceFS metadata information such as `metadata_url`
- sandbox-manager creates or reuses Kubernetes Secret/PV/PVC
- response exposes storage implementation resource names
- workload pod uses a PVC-derived `/workspace` mount
- binding deletion and workload deletion do not provide the full AFSCP writer-drain and confirmed-unmounted semantics

This is not acceptable for new AFSCP-backed repositories because ordinary product callers must not carry JuiceFS credentials, Kubernetes Secret references, object-store credentials, or source filesystem paths.

## Required sandbox-manager Changes

1. Add an AFSCP orchestrator v2 path.

   The new path should accept or discover an AFSCP `mount_binding_id`, then fetch the corresponding AFSCP `OrchestratorMountPlan` using a service identity authorized for the `orchestrator_mount` role.

2. Stop accepting product-supplied storage credentials for AFSCP-backed repos.

   For AFSCP-backed repos, sandbox-manager must not accept `metadata_url`, object-store endpoint, bucket credentials, Secret values, or source subdir values from AgentSmith or any ordinary product caller.

3. Treat AFSCP as the source of storage mount truth.

   sandbox-manager should derive runtime mount state only from AFSCP plan fields such as:

   - `mount_binding_id`
   - `volume_id`
   - `payload_volume_subdir`
   - `mount_path`
   - `read_only`
   - `secret_ref`
   - `security_policy`

4. Mount only the repository payload root.

   The runtime mount must expose only the AFSCP-provided payload directory. It must not mount the repo container directory if that would expose control metadata such as JVS state.

5. Implement binding status and lease callbacks.

   sandbox-manager must report:

   - issued or pending execution state
   - active mount confirmation
   - periodic heartbeat
   - release result
   - revoke result
   - failed state with redacted error details
   - terminal status with evidence-bearing meaning: use `released`/`revoked` only after the runtime mount is confirmed unmounted or non-accessing; use `expired`/`failed` for observed terminal or uncertain outcomes without unmount proof. Future callback extensions may add explicit evidence for unable-to-write-but-still-mounted states.

6. Implement confirmed-unmounted semantics.

   AFSCP may only treat a write-capable binding as terminal when sandbox-manager confirms the runtime mount is unmounted or otherwise non-accessing. This also proves the workload can no longer write. Deleting or patching a Secret/PV/PVC is not enough if a running pod can still access an existing mount.

   Bare terminal status alone is not sufficient evidence. In the current GA callback contract, `released` and `revoked` are evidence-bearing non-accessing terminal statuses, while `expired` and `failed` are observed terminal statuses without proof of unmount, non-access, or unable-to-write. Restore-run can proceed past a read-write binding only after `released`/`revoked`; archive, delete, and purge require the same confirmed non-accessing evidence. If a future runtime needs restore-run evidence for unable-to-write-but-still-mounted/readable, it must add an explicit evidence field or status rather than reusing `released`/`revoked`.

7. Enforce read-only and runtime policy.

   The `read_only` flag and `security_policy` from AFSCP must be reflected in Pod volume mounts, runtime policy, and any equivalent mount mechanism. The workload must not receive JuiceFS root credentials.

8. Avoid deterministic-name collision risks.

   Runtime resource names derived from IDs should be collision-resistant after Kubernetes name normalization and length limits. Truncation without a stable hash suffix is not sufficient.

## Acceptance Criteria

The sandbox-manager integration is ready for AFSCP-backed repos when:

- AFSCP-backed workload mounts no longer require AgentSmith to send `metadata_url`.
- Only the sandbox-manager orchestrator identity can fetch AFSCP mount plans.
- Product callers never receive Kubernetes Secret names, Secret values, JuiceFS metadata URLs, object-store credentials, or source filesystem paths.
- Runtime mounts expose `payload_volume_subdir` only.
- Read-only bindings are actually read-only inside the workload.
- Heartbeat/release/revoke callbacks are implemented and tested.
- AFSCP can distinguish active, released, revoked, expired, failed, and uncertain writer states.
- Revoke tests prove that a workload cannot keep writing after sandbox-manager reports terminal state.

## References

- `docs/WORKLOAD_MOUNTS.md`
- `docs/contracts/workload-mount-binding-v1.md`
- `docs/adr/0011-workload-orchestrator-contract.md`
- `docs/SECURITY_AND_TENANCY.md`
- `docs/TECHNICAL_FEASIBILITY_REVIEW_2026-05-03.md`
