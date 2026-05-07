# Integration Guide

Status: external adoption notes, not an AFSCP release gate.

This document describes how the first expected caller, AgentSmith, can integrate with AFSCP without coupling AFSCP to AgentSmith business concepts. These notes are consumer guidance only: they may inform compatibility work, but AgentSmith, sandbox-manager, and sibling-repo changes are not final AFSCP GA or release blockers.

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
| Storage-affecting file library delete/archive | AFSCP repo lifecycle operation plus AgentSmith catalog state |
| AgentSmith file library rename | AgentSmith catalog metadata only |
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
- call AFSCP repo lifecycle APIs when a file library archive/delete/restore/purge affects storage state
- keep the file library catalog record and AFSCP repo ID while tombstoned storage remains restorable
- pass a product confirmation or approval reference and reason when requesting AFSCP purge
- call AFSCP with authorized actor, namespace ID, correlation ID, and idempotency key
- reject cross-workspace template clone before calling AFSCP
- render user-visible audit from AFSCP events

AgentSmith API should not:

- run `juicefs` or `jvs`
- hold JuiceFS root credentials
- pass raw filesystem paths to AFSCP
- require AFSCP to know notebook task IDs or file library IDs
- use AFSCP repo lifecycle APIs for display-name rename or product-only catalog detach

## AgentSmith Lifecycle Mapping

| AgentSmith file library state | AFSCP state/change | Catalog rule |
| --- | --- | --- |
| active | repo `active` | keep ordinary catalog record |
| product-only archived/hidden | no AFSCP storage change | keep repo `active`; hide or group in AgentSmith catalog |
| storage archived/cold | call AFSCP `archive` | keep catalog record and repo ID; show restore/unarchive action |
| deleted/trash | call AFSCP `delete` | keep tombstoned catalog record and repo ID until purge or retention expiry |
| restored from trash | call AFSCP `restore-tombstoned` | restore catalog visibility according to product policy and AFSCP lifecycle result |
| permanently deleted | call AFSCP `purge` only after product confirmation and policy approval | keep only product audit marker; no storage restore is possible |

AgentSmith display-name rename and product-only catalog detach remain catalog
metadata changes. They must not call AFSCP repo lifecycle APIs unless storage
availability or retention state changes.

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

Current direct JuiceFS Desktop flows are not compatible with AFSCP-backed repos because they bypass export TTL, revoke, audit, and restore writer-session fencing. The Desktop integration should be treated as a WebDAV/export rewrite, not a thin field rename.

Current paths to inspect:

- `/home/percy/works/mbos-v1/agentsmith-desktop/src/lib/mounts/service.ts`
- `/home/percy/works/mbos-v1/agentsmith-desktop/src/lib/mounts/tauri-backend.ts`
- `/home/percy/works/mbos-v1/agentsmith-desktop/src/lib/mounts/api.ts`
- `/home/percy/works/mbos-v1/agentsmith-desktop/src/types.ts`

## Sandbox/Orchestrator Mapping

AFSCP should generate a generic workload mount binding for product callers and an orchestrator-only mount plan for the sandbox-manager. AgentSmith's sandbox-manager can consume that plan through a compatibility adapter or v2 binding endpoint.

Required changes:

- Add a v2 binding endpoint or compatible evolution.
- Accept `mount_binding_id`, `namespace_id`, `repo_id`, `volume_id`, `payload_volume_subdir`, `mount_path`, `read_only`, and orchestrator-only `secret_ref`.
- Stop accepting arbitrary `metadata_url` as the business API contract for new repos.
- Continue v1 compatibility for legacy file libraries until explicit migration.
- Ensure workload Pods do not receive JuiceFS credentials.
- Ensure AgentSmith API services do not receive or reference JuiceFS root Secrets.
- Keep non-root workload defaults and service account token restrictions.
- Add mount binding heartbeat, release, revoke, and reconciliation semantics. Pod keepalive is not the same as a storage mount binding lease.
- Consume only AFSCP payload-only mount plans for AFSCP-backed workload homes. Stock JuiceFS CSI subdirectory mounting is acceptable only when the selected subdir is the repo payload root and JVS control metadata is outside that mounted subtree.

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
- repo lifecycle management for archive/delete/restore/purge storage state
- `doctor --strict` for validation

Current paths to inspect:

- `/home/percy/works/mbos-v1/jvs/docs/02_CLI_SPEC.md`
- `/home/percy/works/mbos-v1/jvs/docs/06_RESTORE_SPEC.md`
- `/home/percy/works/mbos-v1/jvs/docs/09_SECURITY_MODEL.md`
- `/home/percy/works/mbos-v1/jvs/docs/24_REPO_CLONE_PRODUCT_PLAN.md`
- `/home/percy/works/mbos-v1/jvs/docs/25_REPO_WORKSPACE_LIFECYCLE_PRODUCT_PLAN.md`

## External Adoption Sequence

This sequence is for the sibling repositories. It is not an AFSCP core GA
closure checklist.

1. Add AFSCP service skeleton and operation store.
2. Add AgentSmith mapping from workspace to AFSCP namespace.
3. Configure namespace volume binding for selected AgentSmith workspaces.
4. Add AgentSmith mapping from file-library records to AFSCP repo IDs without exposing AFSCP to file-library vocabulary.
5. Provision new file library backends as AFSCP repos.
6. Add WebDAV export flow for Desktop/Web and disable ordinary direct JuiceFS access for AFSCP-backed repos.
7. Add workload mount binding/orchestrator adapter for sandbox-manager after the external-control/payload-only mount contract is accepted.
8. Route save/history/restore through AFSCP.
9. Save notebook task result by asking AFSCP to clone source repo into a repo template, then let AgentSmith store catalog metadata.
10. Clone template by asking AFSCP to clone same-namespace template into a new repo.
11. Route file library archive/delete/restore/purge storage state changes through AFSCP repo lifecycle APIs.
12. Gate new behavior behind AgentSmith workspace/profile feature flags.
13. Plan legacy migration separately.
