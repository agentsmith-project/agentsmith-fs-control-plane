# Integration Guide

This document describes how the first expected caller, AgentSmith, can integrate with AFSCP without coupling AFSCP to AgentSmith business concepts.

Core rule: AgentSmith owns product workflow and authorization. AFSCP owns generic storage primitives.

## Concept Mapping

| AgentSmith concept | AFSCP primitive |
| --- | --- |
| AgentSmith workspace / tenant boundary | `namespace` |
| configured storage profile | namespace volume binding |
| file library backend | `repo` |
| notebook task working home | product workflow that selects or creates a `repo` |
| AgentSmith template catalog record | product metadata pointing to AFSCP `repo_template` |
| Desktop/Web file access | `export` |
| sandbox workspace binding | `workload_mount_binding` plus orchestrator-only mount plan |
| AgentSmith audit entry | projection of AFSCP operation/audit events |

AFSCP should store the right-hand concepts only. Left-hand concepts stay in AgentSmith.

## AgentSmith API Responsibilities

AgentSmith API should:

- authenticate users
- authorize workspace/project/file-library/template operations
- own AgentSmith catalog records and display names
- choose or create an AFSCP namespace for each AgentSmith workspace
- configure namespace volume bindings through AFSCP admin/internal APIs
- map file library records to AFSCP repo IDs
- map template catalog records to AFSCP template IDs
- call AFSCP with authorized actor, namespace ID, correlation ID, and idempotency key
- reject cross-workspace template clone before calling AFSCP
- render user-visible audit from AFSCP events

AgentSmith API should not:

- run `juicefs` or `jvs`
- hold JuiceFS root credentials
- pass raw filesystem paths to AFSCP
- require AFSCP to know notebook task IDs or file library IDs

Current paths to inspect:

- `/home/percy/works/mbos-v1/agentsmith/packages/contracts/src/index.ts`
- `/home/percy/works/mbos-v1/agentsmith/packages/api-entry-node/src/internal-agent-workspace-provisioner.ts`
- `/home/percy/works/mbos-v1/agentsmith/packages/api-entry-node/src/file-library-model.ts`
- `/home/percy/works/mbos-v1/agentsmith/packages/api-entry-node/src/file-library-runtime.ts`
- `/home/percy/works/mbos-v1/agentsmith/packages/api-entry-node/src/project-file-library-routes.ts`
- `/home/percy/works/mbos-v1/agentsmith/packages/api-entry-node/src/sandbox-manager-client.ts`

## AgentSmith Desktop Mapping

Desktop should stop ordinary raw JuiceFS mount flows.

Required changes:

- Consume AgentSmith-returned WebDAV access credentials, backed by AFSCP export sessions.
- Mount or open WebDAV endpoints using short-lived credentials.
- Hide JuiceFS metadata URL and bucket details from ordinary users.
- Keep raw JuiceFS direct mount only behind an admin/debug feature flag if needed.

Current paths to inspect:

- `/home/percy/works/mbos-v1/agentsmith-desktop/src/lib/mounts/service.ts`
- `/home/percy/works/mbos-v1/agentsmith-desktop/src/lib/mounts/tauri-backend.ts`
- `/home/percy/works/mbos-v1/agentsmith-desktop/src/lib/mounts/api.ts`
- `/home/percy/works/mbos-v1/agentsmith-desktop/src/types.ts`

## Sandbox/Orchestrator Mapping

AFSCP should generate a generic workload mount binding for product callers and an orchestrator-only mount plan for the sandbox-manager. AgentSmith's sandbox-manager can consume that plan through a compatibility adapter or v2 binding endpoint.

Required changes:

- Add a v2 binding endpoint or compatible evolution.
- Accept `mount_binding_id`, `namespace_id`, `repo_id`, `volume_id`, `volume_subdir`, `mount_path`, `read_only`, and orchestrator-only `secret_ref`.
- Stop accepting arbitrary `metadata_url` as the business API contract for new repos.
- Continue v1 compatibility for legacy file libraries until explicit migration.
- Ensure workload Pods do not receive JuiceFS credentials.
- Ensure AgentSmith API services do not receive or reference JuiceFS root Secrets.
- Keep non-root workload defaults and service account token restrictions.

Current paths to inspect:

- `/home/percy/works/mbos-v1/mbos-sandbox-v1/manager-service/internal/workspacebinding/handler.go`
- `/home/percy/works/mbos-v1/mbos-sandbox-v1/docs/JUICEFS_CSI_WORKSPACE_MODEL.md`
- `/home/percy/works/mbos-v1/mbos-sandbox-v1/docs/contracts/agentsmith-integration-contract-v2.md`

## JVS

AFSCP executes JVS generically. AgentSmith should not invoke JVS directly.

Required capabilities:

- `jvs init`
- save point creation and history
- restore preview and restore
- repo clone
- repo lifecycle management only in P1/future flows
- `doctor --strict` for validation

Current paths to inspect:

- `/home/percy/works/mbos-v1/jvs/docs/02_CLI_SPEC.md`
- `/home/percy/works/mbos-v1/jvs/docs/06_RESTORE_SPEC.md`
- `/home/percy/works/mbos-v1/jvs/docs/09_SECURITY_MODEL.md`
- `/home/percy/works/mbos-v1/jvs/docs/24_REPO_CLONE_PRODUCT_PLAN.md`
- `/home/percy/works/mbos-v1/jvs/docs/25_REPO_WORKSPACE_LIFECYCLE_PRODUCT_PLAN.md`

## Integration Sequence

1. Add AFSCP service skeleton and operation store.
2. Add AgentSmith mapping from workspace to AFSCP namespace.
3. Configure namespace volume binding for selected AgentSmith workspaces.
4. Provision new file library backends as AFSCP repos.
5. Add workload mount binding/orchestrator adapter for sandbox-manager.
6. Add WebDAV export flow for Desktop/Web.
7. Route save/history/restore through AFSCP.
8. Save notebook task result by asking AFSCP to clone source repo into a repo template, then let AgentSmith store catalog metadata.
9. Clone template by asking AFSCP to clone same-namespace template into a new repo.
10. Gate new behavior behind AgentSmith workspace/profile feature flags.
11. Plan legacy migration separately.
