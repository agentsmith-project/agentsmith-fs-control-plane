# AgentSmith Changes for AFSCP Integration

Status: external-team handoff draft.

Audience: AgentSmith product and platform developers.

This document records the AgentSmith-side changes required for AFSCP integration. It is intentionally scoped to AgentSmith responsibilities so the AFSCP team can continue AFSCP-only development without owning product-layer changes.

## Summary

AgentSmith should remain the product authority for users, workspaces, projects, file libraries, product catalog state, UI lifecycle, and user-facing authorization.

AFSCP should become the storage-control authority for storage credentials, namespace boundaries, repo lifecycle, JVS operations, WebDAV export sessions, workload mount bindings, and storage audit.

The target integration is:

1. AgentSmith authorizes the user and product action.
2. AgentSmith maps product resources to AFSCP resource IDs.
3. AgentSmith calls AFSCP with actor, idempotency, correlation, and namespace context.
4. AgentSmith stores opaque AFSCP IDs, not raw JuiceFS credentials or source paths.
5. AgentSmith presents product-facing state while AFSCP owns storage execution state.

## Current Behavior to Retire

The current AgentSmith file library flow directly owns JuiceFS backend provisioning and access:

- File library catalog/backend/mount-access records include JuiceFS filesystem and mount access data.
- API-side provisioning creates per-library PostgreSQL/MinIO resources and runs `juicefs format`.
- Some authorized mount access flows return `metadata_url`, storage bucket URL, or recommended native mount commands.
- Web file APIs use an API-managed JuiceFS gateway/client path.
- Internal agent workspace provisioning sends JuiceFS metadata information to sandbox-manager.

These flows must not be used for new AFSCP-backed repositories because they bypass AFSCP TTL, revoke, audit, writer-session fencing, and credential boundaries.

## Required AgentSmith Changes

1. Replace file library storage provisioning with AFSCP repo lifecycle calls.

   AgentSmith should create or attach file libraries by calling AFSCP repository APIs after product authorization. AgentSmith should persist the returned AFSCP `namespace_id`, `repo_id`, and related opaque IDs as product metadata.

2. Replace raw mount access with AFSCP export sessions.

   Desktop/Web access should request AFSCP WebDAV exports or another AFSCP-approved access descriptor. AgentSmith must not return raw JuiceFS `metadata_url`, root object-store credentials, bucket credentials, or native mount commands for AFSCP-backed repositories.

3. Replace direct sandbox-manager storage binding calls with AFSCP workload mount bindings.

   AgentSmith should request an AFSCP workload mount binding and pass only the caller-visible binding information into the workload orchestration path. It must not pass JuiceFS metadata URLs, Kubernetes Secret names, PV names, PVC names, or source filesystem paths as product API data.

4. Keep lifecycle semantics product-facing but delegate storage effects.

   Product-only rename/archive visibility may remain in AgentSmith catalog state. Storage-affecting operations must call AFSCP:

   - storage archive -> AFSCP repo archive
   - trash/delete -> AFSCP repo delete or tombstone
   - restore -> AFSCP restore-tombstoned lifecycle operation
   - permanent delete -> AFSCP purge with confirmation

5. Keep product authorization outside AFSCP.

   AgentSmith must decide whether the user may act on a workspace, project, file library, template, or task. AFSCP should receive caller-service identity and actor context, but should not need to understand AgentSmith business objects.

6. Add compatibility guards during transition.

   AgentSmith should distinguish legacy direct-JuiceFS libraries from AFSCP-backed repos. New AFSCP-backed resources must fail closed if the required AFSCP capability or external orchestrator contract is unavailable.

## Data Model Expectations

AgentSmith should retain product metadata:

- workspace/project/file-library ownership
- display name and UI status
- product lifecycle state
- product audit projection
- mapping to AFSCP `namespace_id`, `repo_id`, `repo_template_id`, `export_id`, or `mount_binding_id`

AgentSmith should not persist or return these for AFSCP-backed repos:

- JuiceFS metadata URLs
- object-store access keys or secret keys
- root bucket credentials
- Kubernetes Secret names or Secret values
- PV/PVC names as product-facing authorization material
- absolute host/node/source filesystem paths

## Acceptance Criteria

The AgentSmith integration is ready for AFSCP-backed repos when:

- New file libraries can be created without AgentSmith running `juicefs format`.
- Product APIs expose file library state using AFSCP opaque IDs rather than raw JuiceFS access data.
- Desktop/Web access obtains short-lived AFSCP export credentials.
- Workload execution receives only an AFSCP mount binding and workload destination path.
- Direct metadata URL/native JuiceFS mount access is disabled for new AFSCP-backed repos.
- Storage lifecycle operations route to AFSCP and respect AFSCP operation status.
- Audit events include product actor/correlation IDs without leaking storage credentials.

## References

- `docs/INTEGRATION_GUIDE.md`
- `docs/PRODUCT_BOUNDARY.md`
- `docs/SECURITY_AND_TENANCY.md`
- `docs/contracts/export-access-webdav-v1.md`
- `docs/contracts/repo-lifecycle-v1.md`
- `docs/contracts/workload-mount-binding-v1.md`
