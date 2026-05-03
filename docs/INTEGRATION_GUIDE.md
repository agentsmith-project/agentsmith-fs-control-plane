# Integration Guide

## AgentSmith API

AgentSmith API must add workspace storage profiles and become the only product-facing entrypoint for storage operations.

Required changes:

- Add workspace admin UI/API for storage profile configuration.
- Add file library backend records that reference `filesystem_id`, `storage_pool_id`, `storage_repo_id`, `jvs_repo_id`, and AFSCP endpoint.
- Route new file library provisioning through AFSCP.
- Route save, history, restore, export, and template clone operations through AFSCP.
- Replace direct Desktop mount responses with `ExportAccess`.
- Reject cross-workspace template clone before calling AFSCP.
- Keep legacy per-library JuiceFS backend compatibility during migration.

Current paths to inspect:

- `/home/percy/works/mbos-v1/agentsmith/packages/contracts/src/index.ts`
- `/home/percy/works/mbos-v1/agentsmith/packages/api-entry-node/src/internal-agent-workspace-provisioner.ts`
- `/home/percy/works/mbos-v1/agentsmith/docs/contracts/juicefs-file-libraries-architecture.md`

## AgentSmith Desktop

Desktop should stop ordinary raw JuiceFS mount flows.

Required changes:

- Consume AgentSmith `ExportAccess`.
- Mount or open WebDAV endpoints using short-lived credentials.
- Hide JuiceFS metadata URL and bucket details from ordinary users.
- Keep raw JuiceFS direct mount only behind an admin/debug feature flag if needed.

Current paths to inspect:

- `/home/percy/works/mbos-v1/agentsmith-desktop/src/lib/mounts/service.ts`
- `/home/percy/works/mbos-v1/agentsmith-desktop/src/lib/mounts/tauri-backend.ts`

## Sandbox-Manager

Sandbox-manager should accept a binding v2 shape that does not trust raw caller-provided storage URLs.

Required changes:

- Add a v2 binding endpoint or compatible evolution.
- Accept `filesystem_id`, `storage_repo_id`, `payload_subdir`, `mount_path`, and `read_only`.
- Stop accepting arbitrary `metadata_url` as the business API contract for new libraries.
- Continue v1 compatibility for legacy libraries until explicit migration.
- Ensure workload Pods do not receive JuiceFS credentials.
- Keep non-root workload defaults and service account token restrictions.

Current paths to inspect:

- `/home/percy/works/mbos-v1/mbos-sandbox-v1/manager-service/internal/workspacebinding/handler.go`
- `/home/percy/works/mbos-v1/mbos-sandbox-v1/docs/JUICEFS_CSI_WORKSPACE_MODEL.md`
- `/home/percy/works/mbos-v1/mbos-sandbox-v1/docs/contracts/agentsmith-integration-contract-v2.md`

## JVS

AFSCP should execute JVS rather than reimplementing versioning.

Required capabilities:

- `jvs init`
- save point creation and history
- restore preview and restore
- repo clone
- repo lifecycle management
- `doctor --strict` for validation

Current paths to inspect:

- `/home/percy/works/mbos-v1/jvs/docs/02_CLI_SPEC.md`
- `/home/percy/works/mbos-v1/jvs/docs/06_RESTORE_SPEC.md`
- `/home/percy/works/mbos-v1/jvs/docs/09_SECURITY_MODEL.md`
- `/home/percy/works/mbos-v1/jvs/docs/24_REPO_CLONE_PRODUCT_PLAN.md`
- `/home/percy/works/mbos-v1/jvs/docs/25_REPO_WORKSPACE_LIFECYCLE_PRODUCT_PLAN.md`

## Integration Sequence

1. Add AFSCP service skeleton and operation store.
2. Add workspace storage profile in AgentSmith.
3. Provision new file libraries through AFSCP.
4. Add sandbox binding v2.
5. Add WebDAV export flow for Desktop/Web.
6. Add JVS save/history/restore.
7. Add workspace-scoped template save and clone.
8. Gate new behavior behind workspace/profile feature flags.
9. Plan legacy migration separately.
