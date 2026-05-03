# References

## Local Planning Material

- `/home/percy/works/mbos-v1/improve-agentsmith-fs`
- `/home/percy/works/mbos-v1/improve-agentsmith-fs/agentsmith-workspace-storage-technical-design.md`
- `/home/percy/works/mbos-v1/improve-agentsmith-fs/scratch.md`

Copied into this repo:

- `docs/research/agentsmith-workspace-storage-technical-design.md`
- `docs/research/scratch.md`

## Current AgentSmith Paths

Do not use `agentsmith-oss`.

- `/home/percy/works/mbos-v1/agentsmith/packages/contracts/src/index.ts`
- `/home/percy/works/mbos-v1/agentsmith/packages/api-entry-node/src/internal-agent-workspace-provisioner.ts`
- `/home/percy/works/mbos-v1/agentsmith/packages/api-entry-node/src/file-library-model.ts`
- `/home/percy/works/mbos-v1/agentsmith/packages/api-entry-node/src/file-library-runtime.ts`
- `/home/percy/works/mbos-v1/agentsmith/packages/api-entry-node/src/project-file-library-routes.ts`
- `/home/percy/works/mbos-v1/agentsmith/packages/api-entry-node/src/sandbox-manager-client.ts`
- `/home/percy/works/mbos-v1/agentsmith/docs/contracts/juicefs-file-libraries-architecture.md`

## Current Desktop Paths

- `/home/percy/works/mbos-v1/agentsmith-desktop/src/lib/mounts/service.ts`
- `/home/percy/works/mbos-v1/agentsmith-desktop/src/lib/mounts/tauri-backend.ts`
- `/home/percy/works/mbos-v1/agentsmith-desktop/src/lib/mounts/api.ts`
- `/home/percy/works/mbos-v1/agentsmith-desktop/src/types.ts`

## Current Sandbox Paths

- `/home/percy/works/mbos-v1/mbos-sandbox-v1/manager-service/internal/workspacebinding/handler.go`
- `/home/percy/works/mbos-v1/mbos-sandbox-v1/docs/JUICEFS_CSI_WORKSPACE_MODEL.md`
- `/home/percy/works/mbos-v1/mbos-sandbox-v1/docs/contracts/agentsmith-integration-contract-v2.md`

## Current And Proposed Local API Paths

Existing AgentSmith routes to replace or restrict for ordinary users:

- `POST /api/v1/workspaces/{workspaceId}/projects/{projectId}/file-libraries/{libraryId}/desktop-mount-access`
- `POST /api/v1/workspaces/{workspaceId}/projects/{projectId}/file-libraries/{libraryId}/storage-credential-exchange`

Existing sandbox-manager local target:

- `http://localhost:8080`
- `PUT /v1/workspaces/{wsId}/projects/{projId}/workspace-bindings/{bindingId}`
- `PUT /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}`

Suggested AFSCP local targets:

- `http://localhost:8090/internal/v1`
- `http://localhost:8091/exports/{exportId}/`

## JVS

- GitHub: https://github.com/agentsmith-project/jvs
- `/home/percy/works/mbos-v1/jvs/docs/02_CLI_SPEC.md`
- `/home/percy/works/mbos-v1/jvs/docs/06_RESTORE_SPEC.md`
- `/home/percy/works/mbos-v1/jvs/docs/09_SECURITY_MODEL.md`
- `/home/percy/works/mbos-v1/jvs/docs/24_REPO_CLONE_PRODUCT_PLAN.md`
- `/home/percy/works/mbos-v1/jvs/docs/25_REPO_WORKSPACE_LIFECYCLE_PRODUCT_PLAN.md`

## JuiceFS

- CSI PV and credentials: https://juicefs.com/docs/csi/guide/pv/
- CSI configuration and subdirectory mount options: https://juicefs.com/docs/csi/guide/configurations/
- WebDAV server: https://juicefs.com/docs/community/deployment/webdav/
- Quota: https://juicefs.com/docs/community/guide/quota/
- POSIX compatibility: https://juicefs.com/docs/community/posix_compatibility/
- Cache and close-to-open consistency: https://juicefs.com/docs/community/guide/cache/
