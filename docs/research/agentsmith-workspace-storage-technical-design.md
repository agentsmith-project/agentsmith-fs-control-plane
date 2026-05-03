# AgentSmith Workspace Storage（统一 JuiceFS + JVS）技术方案

日期：2026-05-03
状态：handoff draft v2
适用范围：当前 `agentsmith`、`agentsmith-desktop`、`mbos-sandbox-v1`、`jvs`

> 说明：本方案不使用 `agentsmith-oss`。该仓库是旧版本，不进入本次现状判断和改造设计。

## 1. 方向变更结论

这版方案替换上一版里的两个重要假设：

1. **不再推荐每个 file library / task 创建独立 JuiceFS metadata DB 和 bucket。**
   MVP 推荐由一个 AgentSmith 文件系统控制面创建并管理一个默认共享 JuiceFS filesystem，所有新用户文件库、notebook task、template repo 默认放在这个共享 filesystem 的受控目录树下。数据模型保留 `filesystem_id/storage_pool_id`，后续可按 tenant、region、合规等级 shard；这不是回到每 task 一个 DB/bucket。

2. **不再对普通文件读写做单写者限制。**
   AgentSmith 不拦截用户 PC、agent sandbox、Web 文件 API 对同一文件库的并发读写。JuiceFS 负责 POSIX 文件系统层的一致性和锁语义；AgentSmith 明确不做版本合并、冲突合并、多人协作编辑语义。

保留的硬边界：

- 用户、Desktop、普通 sandbox 容器仍然不能拿到底层 JuiceFS metadata DB、S3/MinIO bucket、access key、secret key。
- AgentSmith 控制用户只能访问自己有权限的 file library / repo / template。
- AgentSmith workspace 是租户级 storage policy 的归属点；管理员可在创建 workspace 时选择默认文件库控制面和 storage pool。
- JVS 操作只由 AgentSmith 文件系统控制面执行，普通用户和 agent 不直接运行 JVS。
- JVS repo lifecycle、save point、restore、repo clone 是产品语义层；JuiceFS snapshot 仍只作为基础设施能力，不直接等同用户 save point。

推荐新增一个独立容器/服务：**AgentSmith FS Control Plane（AFSCP）**。它替代上一版文档里的 WSS 名称，更贴近用户现在想要的“AgentSmith 文件系统控制面”。

AFSCP 负责：

- 创建和管理默认共享 JuiceFS filesystem / storage pool。
- 管理共享 filesystem 下的目录布局、quota、权限、JVS repo。
- 执行 JVS `init/save/history/restore/repo clone/repo lifecycle`。
- 给 sandbox-manager 生成受控 subdir mount spec。
- 给 Desktop/用户 PC 生成 WebDAV/NAS export。
- 保证底层凭据只留在服务端和 K8s Mount Pod 边界内。

## 2. 产品边界

用户真正要的是：

- 一个长期存在的“文件库 / repo”，可被 notebook task 使用。
- 管理员在创建 AgentSmith workspace 时，为该租户选择默认文件库控制面 / storage pool。
- agent 可以在里面工作，多个 task 可以复用同一个 repo。
- 用户 PC 可以访问同一个 repo。
- 用户可以创建 save point、restore、clone repo。
- 用户可以把一个 notebook task 的完整工作结果保存为 workspace 内模板，并分享给同一 AgentSmith workspace 内的其他用户。
- 同一 workspace 内的其他用户基于模板得到自己的独立 repo 副本，而不是共同写同一个模板目录。
- 不允许跨 AgentSmith workspace 共享 repo template 或 clone template。

MVP 不做：

- 不做实时协同编辑。
- 不做版本 merge。
- 不做 Git remote/push/pull/origin。
- 不做每个用户一个 NAS 账号体系。
- 不做复杂文件级 ACL UI。
- 不做跨区域复制、计费、自动 tiering。
- 不把 JVS CLI 暴露给普通用户或 agent。

## 3. 当前实现问题

当前 AgentSmith 的 File Library 架构已能创建 JuiceFS backend，但隔离方式过重：

- File library backend 里有 `filesystem_name`、PostgreSQL metadata database/user、MinIO bucket 等映射。
- Notebook task 通过 `workspace_file_library_id` 绑定工作目录。
- Desktop 当前通过 `/desktop-mount-access` 获取 `metadata_url`，本机执行 `juicefs mount`。
- sandbox-manager 当前 binding request 仍以 `file_library_id/filesystem_name/metadata_url/subdir` 为中心。

这个模型如果被用于“每个 notebook task 一个 file library”，会导致：

- metadata DB 数量膨胀。
- bucket 数量膨胀。
- bucket policy / credential / Secret 数量膨胀。
- 本地 Desktop mount 暴露底层 metadata URL。
- JVS repo clone/template 不能自然复用同一底层 filesystem 的 fast path。

新方案应把“存储隔离”从“物理 JuiceFS filesystem 隔离”改成“统一 JuiceFS + AgentSmith 目录授权 + subdir mount/export 隔离”。

## 4. 目标架构

```text
User Browser / Desktop
        |
        | AgentSmith API, authz, template/catalog UI
        v
+----------------------------+
| AgentSmith API             |
| product control plane      |
+-------------+--------------+
              |
              | internal API / service token
              v
+----------------------------+
| AgentSmith FS Control Plane|
| AFSCP                      |
| JuiceFS root credentials   |
| JVS operation runner       |
| WebDAV/export manager      |
+------+------+--------------+
       |      |
       |      +----------------------+
       |                             |
       v                             v
Unified JuiceFS filesystem       sandbox-manager
metadata DB + object bucket      JuiceFS CSI / Pod
```

组件职责：

| 组件 | 职责 |
| --- | --- |
| AgentSmith API | 用户权限、项目权限、file library/repo/template 产品模型、任务编排、审计汇总 |
| AFSCP | 统一 JuiceFS 创建/挂载/目录管理、JVS 操作、WebDAV export、sandbox mount spec、底层凭据保管 |
| sandbox-manager | K8s Secret/PV/PVC/Pod 交付，把指定 subdir mount 到 workload |
| Desktop | 使用 AgentSmith export access 连接 WebDAV/NAS，不再运行普通 JuiceFS mount |
| JVS | repo lifecycle、save point、restore、repo clone、doctor/recovery |
| JuiceFS | 统一 POSIX-like 分布式文件系统 |

数据权威：

| 数据 | 权威 |
| --- | --- |
| 用户权限、repo/template 可见性、产品状态 | AgentSmith API |
| workspace 级 storage profile、默认 storage pool、配额策略 | AgentSmith 管理员配置 |
| 统一 JuiceFS credentials、root mount、目录真实路径、JVS operation 状态 | AFSCP |
| JVS repo_id、save point、clone manifest、lifecycle journal | JVS repo control data |
| workload Pod/PVC/PV 状态 | sandbox-manager |
| 用户可见审计 | AgentSmith 汇总 AFSCP/JVS/sandbox 事件 |

### 4.1 是否需要新应用模块

需要。建议新增一个独立后台应用模块 / service：`agentsmith-fs-control-plane`，简称 AFSCP。

原因：

- 它持有 JuiceFS metadata URL、object store credential、root mount 权限，不应和普通 AgentSmith API 进程混在一起。
- 它要执行 JVS `save/restore/repo clone/lifecycle` 等 mutating operation，需要 operation journal、重试、故障恢复和审计。
- 它要管理服务端 WebDAV export、路径 chroot、`.jvs` 过滤和短期凭据。
- 它要和 sandbox-manager 交付 CSI mount spec / secret ref / payload subdir。
- 它可能需要 FUSE/CSI/mount 权限，运行时安全边界和普通 API 服务不同。

不建议第一版拆成多个新服务。MVP 只需要一个 AFSCP 服务镜像，内部按模块拆分；WebDAV gateway 可以作为同镜像子进程或 sidecar，等负载上来再拆 gateway pool。

### 4.2 模块边界

| 模块 | 所属应用 | 职责 |
| --- | --- | --- |
| Workspace admin / storage profile UI | AgentSmith Web/API | 管理员创建 workspace 时选择 AFSCP、storage pool、quota、export/template policy |
| File library catalog | AgentSmith API | 用户可见 file library 元数据、权限、项目绑定、审计投影 |
| Template catalog | AgentSmith API | workspace-scoped template 元数据和授权；拒绝跨 workspace clone |
| Storage pool manager | AFSCP | bootstrap/health/check 默认共享 JuiceFS filesystem 和后续 storage pool |
| Repo path allocator | AFSCP | 生成 repo/template 目录，规范化路径，拒绝 traversal 和任意绝对路径 |
| JVS operation runner | AFSCP | 执行 `jvs init/save/history/restore/repo clone/lifecycle --json` |
| Operation store | AFSCP | 记录 operation id、状态、JVS JSON、错误、重试、审计事件 |
| Export manager | AFSCP | 最小 WebDAV export、短期 Basic credential、`.jvs` 过滤、访问日志 |
| Sandbox mount adapter | AFSCP + sandbox-manager | AFSCP 生成 mount spec；sandbox-manager 兑现 Secret/PV/PVC/Pod mount |
| Desktop connector | agentsmith-desktop | 消费 AgentSmith 返回的 ExportAccess，不再执行普通 JuiceFS mount |

AgentSmith API 不直接运行 `juicefs` 或 `jvs` 命令；它只做产品权限、请求编排和用户可见投影。AFSCP 不直接决定用户是否有权限；它只接受 AgentSmith 已授权的内部请求。

### 4.3 AFSCP 内部结构

MVP 内部结构：

```text
agentsmith-fs-control-plane
  api/
    internal HTTP API, service auth
  storage-pools/
    JuiceFS bootstrap, mount, health, credential refs
  repos/
    create repo, archive/delete repo, path resolver
  jvs/
    CLI wrapper, JSON parser, operation lock
  templates/
    repo clone executor, workspace-boundary checks
  exports/
    WebDAV export, Basic credential, path filtering
  sandbox/
    mount spec builder for sandbox-manager
  operations/
    operation store, retry, event outbox
```

部署建议：

- P0：一个 Deployment 跑 AFSCP API + worker；WebDAV gateway 可同 pod sidecar 或同镜像子进程。
- P0：AFSCP 使用单独 service account 和 Secret 权限，AgentSmith API 不读取 JuiceFS root credential。
- P1：如果 WebDAV/export 压力大，再拆 `afscp-export-gateway`。
- P1：如果 JVS operation 很重，再拆 worker queue。

### 4.4 内部 API 草案

AgentSmith API 调 AFSCP：

```http
POST /internal/v1/storage-pools/{poolId}:ensure
GET  /internal/v1/storage-pools/{poolId}/health

POST /internal/v1/repos
GET  /internal/v1/repos/{repoId}
POST /internal/v1/repos/{repoId}/save-points
GET  /internal/v1/repos/{repoId}/save-points
POST /internal/v1/repos/{repoId}/restore-preview
POST /internal/v1/repos/{repoId}/restore-run

POST /internal/v1/templates/{templateId}:publish-from-repo
POST /internal/v1/templates/{templateId}:clone-to-repo

POST /internal/v1/repos/{repoId}/exports
DELETE /internal/v1/exports/{exportId}

POST /internal/v1/repos/{repoId}:sandbox-mount-spec
GET  /internal/v1/operations/{operationId}
```

所有 mutating API 必须带：

- `tenant_workspace_id`
- `project_id` when applicable
- `requesting_user_id` or system actor
- `idempotency_key`
- `correlation_id`

AFSCP 必须校验 `tenant_workspace_id` 与 repo/template 归属一致，尤其是 template clone：source template 和 target repo 必须属于同一个 AgentSmith workspace。

## 5. 统一 JuiceFS filesystem / storage pool

### 5.1 推荐决策

MVP 使用一个默认共享 JuiceFS filesystem：

```text
filesystem_name = agentsmith-global
metadata DB     = agentsmith_juicefs_global
object bucket   = agentsmith-global
```

不再为每个 task 或 file library 创建 metadata DB / bucket。为了避免把生产部署锁死成“永远只有一个全局 volume”，数据模型中保留 `filesystem_id/storage_pool_id`：

- **MVP 默认**：一个 deployment/region 一个共享 JuiceFS filesystem。
- **P1 shard**：按 tenant、region、合规等级或超大容量客户创建第二个共享 filesystem。
- **明确不回退**：不按 notebook task 或普通 file library 创建独立 metadata DB/bucket。

因此“统一”指统一控制面、统一 API、统一授权审计、统一默认 storage pool；不是要求所有未来 tenant 永远挤在唯一一个无法拆分的 volume 中。

### 5.2 目录布局

默认共享 filesystem 的目录树：

```text
/agentsmith/
  system/
    exports/
    afscp-runtime/

  tenants/
    <tenant_workspace_id>/
      users/
        <user_id>/
          libraries/
            <file_library_id>/
              repo/                 # JVS repo root + 用户文件根
                .jvs/               # JVS control data，服务端控制，export 过滤
                .codex/
                .mbos/
                .artifacts/
                ... user files ...

      projects/
        <project_id>/
          tasks/
            <task_id> -> optional metadata, not a new JuiceFS FS

  templates/
    <template_id>/
      repo/                         # published template JVS repo
```

原则：

- `file_library_id/repo` 是用户和 agent 的文件库根，也是 JVS repo root。
- `.jvs` 不进入 save point payload，但物理上位于 repo root。AFSCP 必须保护它。
- WebDAV/NAS export 必须过滤 `.jvs`。
- sandbox 普通容器应以非 root 用户运行，`.jvs` 目录归 AFSCP 控制 UID，权限建议 `0700`。如果 sandbox 必须允许 root 任意写 repo root，那么“保护 `.jvs`”会成为 P0 blocker，需要先做 filtered mount/view。
- 目录名使用不可猜测 ID，不使用用户输入的 display name 直接拼路径。

### 5.3 共享 FS 的安全边界

共享 FS 的代价是 blast radius 变大，因此安全边界必须前移到 AgentSmith/AFSCP：

- 用户 PC 只拿 WebDAV/NAS export，不拿 JuiceFS credentials。
- 普通 sandbox 容器只看到指定 repo subdir，不拿 JuiceFS Secret。
- K8s JuiceFS Secret 只给 CSI/Mount Pod 使用，不注入 workload env。
- AFSCP 是唯一拥有 root mount / metadata URL / bucket credential 的服务。
- AFSCP 所有路径操作必须通过 path allocator，不接受客户端传入任意绝对路径。
- WebDAV、sandbox mount、Web 文件 API 都必须通过同一个 `repo_path_resolver`，不能各自拼路径。

### 5.4 Quota

统一 FS 下不再靠 bucket/DB 自然隔离容量。建议：

- MVP 至少保留目录级 quota 的设计入口。
- P1 开启 JuiceFS directory quota，按 file library/repo 目录设置容量和 inode 限制。
- 用户/组 quota 可作为后续增强，不作为第一版权限模型。

JuiceFS 官方支持 filesystem quota、directory quota、UID/GID quota；directory quota 是统一 FS 下最贴合 file library 的手段。

### 5.5 Storage pool 选择

AFSCP 创建 repo 时负责选择 storage pool。选择不应散落在 task/file library 创建流程里，而应优先来自 AgentSmith workspace 的管理员配置。

```ts
type StoragePoolSelection = {
  filesystem_id: string;
  storage_pool_id: string;
  reason: 'default' | 'tenant_policy' | 'region_policy' | 'compliance_policy';
};
```

选择顺序：

1. `AgentSmithWorkspaceStorageProfile.default_storage_pool_id`
2. project/file-library 显式 override，只有管理员或具备权限的 owner 可用
3. deployment default storage pool

这给后续 shard 留出口，同时不影响第一版去掉 per-task DB/bucket overhead 的核心目标。

### 5.6 Workspace 级 storage profile

这是推荐的租户级配置点。管理员创建 AgentSmith workspace 时选择一个 storage profile：

```ts
type AgentSmithWorkspaceStorageProfile = {
  tenant_workspace_id: string;
  afscp_endpoint_id: string;
  default_storage_pool_id: string;
  default_filesystem_id: string;
  path_prefix: string;          // tenants/<tenant_workspace_id>
  default_quota?: {
    library_capacity_gib?: number;
    library_inodes?: number;
  };
  export_policy: {
    webdav_enabled: boolean;
    max_export_ttl_seconds: number;
  };
  template_policy: {
    allow_workspace_templates: boolean;
  };
  status: 'ready' | 'disabled' | 'degraded';
};
```

产品语义：

- 不同 AgentSmith workspace 可以绑定不同 storage pool，甚至不同 AFSCP 实例。
- 同一 workspace 下的新 file library 默认使用该 workspace 的 storage profile。
- repo template 只在同一 AgentSmith workspace 内可见和可 clone。
- 不支持跨 workspace template 分享；不同 workspace 即使绑定同一 storage pool，也不能互相 clone template。
- 修改 workspace storage profile 只影响新建 file library；已有 library 迁移必须走显式 migration flow。

## 6. 并发读写策略

### 6.1 普通读写不加产品级单写者限制

新决策：

- agent task、用户 PC、Web 文件 API 可以同时读写同一个 file library。
- AgentSmith 不做“谁持有 write lease 谁独占”的限制。
- Desktop read-write export 不阻塞 agent task。
- agent task 不阻塞用户 PC read-write export。
- 多个 agent task 可以同时使用同一个 repo，前提是用户或产品流程允许它们选择同一个 repo。

产品语义：

- JuiceFS 提供文件系统层的 close-to-open 一致性、atomic metadata operations 和 POSIX/BSD locking 能力。
- 如果两个写入者同时写同一个文件，AgentSmith 不保证自动合并。
- 如果应用需要文件级互斥，应由应用使用文件锁或自己的写入协议。
- AgentSmith UI 只提示“并发写入可能产生最后写入覆盖或应用级冲突”，不阻止。

### 6.2 仍然需要的锁

取消普通写入限制，不等于取消所有锁。仍必须保留：

| 操作 | 是否需要锁 | 原因 |
| --- | --- | --- |
| 普通文件读写 | 不需要 AgentSmith 单写锁 | 交给 JuiceFS/POSIX 和应用 |
| JVS save | 需要 JVS workspace mutation lock | 防止多个 JVS save/restore/lifecycle 同时改 JVS metadata |
| JVS restore | 需要 JVS operation lock | 防止多个 restore/save/lifecycle 同时运行 |
| JVS repo clone | 需要源 repo JVS 读/operation gate | clone 需要一致读取 JVS metadata 和 save points |
| JVS repo move/rename/detach/delete workspace | 需要 lifecycle lock | 这是 JVS 控制面 mutation |
| 底层目录 delete/archive | 需要 AFSCP operation lock | 防止目录被同时挂载/导出/删除 |

### 6.3 Restore 的产品口径

用户明确不要求版本合并。因此 restore 不解决并发 merge。

MVP 推荐：

- restore preview/run 可以在有普通写入者时执行，但 UI/API 必须提示：运行期间其他写入者可能继续修改文件，最终结果按文件系统实际写入顺序决定。
- JVS restore 仍串行化其他 JVS operation。
- 不做自动冲突合并。
- 不默认停止 sandbox 或撤销 PC export。

P1 可增加 `strict_restore=true`：

- AFSCP 请求 sandbox-manager 停止相关 workload，并 drain WebDAV 写 session。
- restore 完成后再允许恢复写入。
- 这是确定性恢复选项，不是默认 MVP 行为。

## 7. JVS 集成

### 7.1 当前可用的新能力

本地 `jvs` 已新增并文档化：

- `jvs repo clone <target-folder> [--save-points all|main] [--dry-run]`
- `jvs repo move`
- `jvs repo rename`
- `jvs repo detach`
- `jvs workspace move`
- `jvs workspace rename`
- `jvs workspace delete`
- lifecycle operation journal
- repo clone manifest / imported save point protection

关键语义：

- `repo clone` 不是 Git clone，不做 remote/push/pull。
- clone 生成新的 `repo_id`。
- clone 只创建目标 `main` workspace。
- `--save-points all` 复制 durable save point history，并需要 imported history cleanup protection。
- clone 不复制 source 的 runtime state、locks、restore plans、cleanup plans、open views。
- lifecycle move/rename/detach 使用 preview/run 或 journal/recovery，doctor 只诊断，不自动推进 lifecycle mutation。

### 7.2 AFSCP 中的 JVS 使用方式

AFSCP 是唯一 JVS executor：

```text
AgentSmith API
 -> AFSCP internal API
 -> jvs --repo <repo_path> ... --json
 -> parse JSON
 -> persist operation result
 -> emit event back to AgentSmith
```

推荐先用 JVS CLI `--json` 做集成，因为 repo clone/lifecycle 的 CLI contract 更新较快。等 Go facade 覆盖完整 preview/run/clone/lifecycle 后，再替换为 library 调用。

### 7.3 File library 与 JVS repo 映射

```text
FileLibrary / StorageRepo
  id = flib_x / srepo_x
  path = /agentsmith/tenants/<tenant>/users/<user>/libraries/<id>/repo
  jvs_repo_id = repo-...
```

创建 file library：

1. AgentSmith 创建产品记录。
2. AFSCP 在统一 JuiceFS 中创建 repo path。
3. AFSCP 设置目录权限和可选 quota。
4. AFSCP 执行 `jvs init <repo_path> --json`。
5. AFSCP 创建 baseline save point 或标记 unsaved 初始状态。

### 7.4 Save / history / restore

Save：

- AFSCP 执行 `jvs save -m ... --json`。
- 不阻塞普通文件写入者。
- 如果用户需要强一致 checkpoint，产品后续可提供“先暂停 task 再 save”的模式。

History：

- AgentSmith 展示 JVS history 投影。

Restore：

- AFSCP 执行 JVS preview/run。
- 不做 merge。
- 默认不停止普通写入者。
- 输出必须明确是否为 online restore。

### 7.5 Notebook task 保存为模板并共享

这是新方案里最自然的产品能力。

流程：

1. 用户在 notebook task 上点击“保存为模板”。
2. AgentSmith 确认 task 使用的 file library/repo。
3. AFSCP 可先创建一个 save point，例如 `template: <name>`。
4. AFSCP 执行：

```bash
jvs --repo <source_repo_path> repo clone <template_repo_path> --save-points all --json
```

5. AgentSmith 创建 workspace-scoped `Template` 产品记录：
   - template_id
   - source repo/task
   - template repo path
   - visibility: private/workspace
   - owner
   - description/tags
6. 同一 AgentSmith workspace 内的其他用户使用模板时，AFSCP 再执行：

```bash
jvs --repo <template_repo_path> repo clone <target_user_repo_path> --save-points all --json
```

7. 目标用户得到自己的独立 file library/repo，后续修改不会影响模板。

MVP 边界：

- 模板分享是 clone-copy，不是多人共同编辑。
- 模板分享边界固定为同一个 AgentSmith workspace。
- API 必须拒绝跨 workspace template clone，即使请求者同时属于两个 workspace。
- 模板 repo 默认只读，只有 owner/admin 可以更新或重新发布。
- 不做 template diff/merge。
- 不做远程 JVS 协议。

## 8. Sandbox 挂载

### 8.1 目标

sandbox-manager 不再为每个 file library 处理不同 JuiceFS filesystem credential，而是复用统一 filesystem credential，并只把指定 subdir 交付给 workload。

目标 binding：

```json
{
  "api_version": "workspace-binding/v2",
  "global_filesystem_id": "agentsmith-global",
  "storage_repo_id": "flib_123",
  "filesystem_name": "agentsmith-global",
  "payload_subdir": "agentsmith/tenants/ws/users/u/libraries/flib_123/repo",
  "mount_path": "/workspace",
  "read_only": false
}
```

安全要求：

- `payload_subdir` 必须由 AFSCP 生成，不接受调用方自定义。
- workload Pod 不能看到 JuiceFS Secret。
- workload Pod 默认只 mount repo subdir，不 mount filesystem root。
- `WORKSPACE_PATH=/workspace`。
- 如果启用 `$HOME=/workspace`，shell config、cache、dotfiles 都会进入 repo；这是产品可见行为。

### 8.2 PV/PVC 方案选择

MVP 推荐保守方案：

- 仍按 file library / repo binding 创建稳定 PV/PVC。
- 这些 PV/PVC 使用同一个 JuiceFS Secret。
- 每个 PV 用 `subdir=<repo_path>` mount option 指向 repo 目录。
- 这样不再创建 DB/bucket，但 sandbox-manager 改动较小。

P1 优化：

- 使用共享 root PVC + `volumeMounts.subPath` 挂不同 repo subdir。
- JuiceFS CSI 文档说明，多 Pod 挂同一 filesystem 的不同子目录时，`volumeMounts.subPath` 可减少 Mount Pod 资源。
- 这属于 Mount Pod 资源优化，不阻塞 MVP。

## 9. 用户 PC / Desktop 访问

Desktop 不再做普通 JuiceFS mount。

产品优先级：

- P0：AFSCP 提供最小 WebDAV export，满足“用户 PC 可访问文件库且不暴露 JuiceFS credentials”。
- P1：Desktop 做完整 OS mount 管理、断线重连、状态 UI、诊断体验。
- admin/debug：raw JuiceFS mount 只保留给管理员诊断。

流程：

1. 用户选择 file library。
2. AgentSmith 校验权限。
3. AFSCP 创建 WebDAV export，root 为 repo path。
4. AFSCP 过滤 `.jvs` 和系统目录。
5. Desktop 获取短期 Basic credential，使用 OS WebDAV 能力连接。

`ExportAccess`：

```ts
type ExportAccess = {
  export_id: string;
  file_library_id: string;
  protocol: 'webdav';
  mode: 'read_only' | 'read_write';
  url: string;
  auth: {
    type: 'basic';
    username: string;
    password: string;
    expires_at: string;
  };
};
```

并发策略：

- read-write WebDAV export 不阻塞 agent task。
- 多个 read-write export 可以存在。
- 不做冲突合并。
- WebDAV 操作审计到 user/export/path/op/status。

## 10. 数据模型调整

### 10.1 新增 AgentsmithFilesystem

```ts
type AgentsmithFilesystem = {
  id: string;                 // afs_global, afs_us_west_team_a
  filesystem_name: string;    // agentsmith-global
  storage_pool_id: string;
  scope: 'deployment_default' | 'tenant' | 'region' | 'compliance';
  status: 'creating' | 'ready' | 'degraded' | 'failed';
  metadata_backend_ref: string;
  object_store_ref: string;
  root_path: '/agentsmith';
  created_at: string;
  updated_at: string;
};
```

### 10.2 WorkspaceStorageProfile

```ts
type WorkspaceStorageProfileRecord = {
  tenant_workspace_id: string;
  afscp_endpoint_id: string;
  default_storage_pool_id: string;
  default_filesystem_id: string;
  path_prefix: string;
  status: 'ready' | 'disabled' | 'degraded';
  created_by_user_id: string;
  created_at: string;
  updated_at: string;
};
```

### 10.3 FileLibraryBackend 改造

从“每 library 一个 backend”改为“每 library 一个 logical repo path”：

```ts
type FileLibraryBackendRecord = {
  library_id: string;
  tenant_workspace_id: string;
  filesystem_id: string;
  storage_pool_id: string;
  repo_path: string;
  jvs_repo_id: string;
  quota?: {
    capacity_gib?: number;
    inodes?: number;
  };
  provisioning_status: 'creating' | 'ready' | 'degraded' | 'failed';
  last_error?: string;
};
```

废弃普通用户响应中的：

- `metadata_url`
- `storage_bucket_url`
- per-library database
- per-library bucket

### 10.4 Template

```ts
type NotebookTemplate = {
  id: string;
  tenant_workspace_id: string;
  source_task_id: string;
  source_library_id: string;
  template_repo_path: string;
  template_jvs_repo_id: string;
  visibility: 'private' | 'workspace';
  created_by_user_id: string;
  created_at: string;
  updated_at: string;
};
```

## 11. API 改造

保留现有 file library 产品入口，但改变后端语义。

新增或调整：

```http
POST /api/v1/admin/workspaces
GET  /api/v1/admin/workspaces/{tenant}/storage-profile
PUT  /api/v1/admin/workspaces/{tenant}/storage-profile

POST /api/v1/workspaces/{tenant}/projects/{project}/file-libraries
GET  /api/v1/workspaces/{tenant}/projects/{project}/file-libraries

POST /api/v1/workspaces/{tenant}/projects/{project}/file-libraries/{libraryId}/save-points
GET  /api/v1/workspaces/{tenant}/projects/{project}/file-libraries/{libraryId}/save-points
POST /api/v1/workspaces/{tenant}/projects/{project}/file-libraries/{libraryId}/restore-preview
POST /api/v1/workspaces/{tenant}/projects/{project}/file-libraries/{libraryId}/restore-run

POST /api/v1/workspaces/{tenant}/projects/{project}/file-libraries/{libraryId}/exports
DELETE /api/v1/workspaces/{tenant}/projects/{project}/exports/{exportId}

POST /api/v1/workspaces/{tenant}/projects/{project}/notebook-tasks/{taskId}/templates
POST /api/v1/workspaces/{tenant}/projects/{project}/templates/{templateId}/clone
```

管理员创建 workspace 时的最小 storage 配置：

```json
{
  "name": "Acme",
  "storage_profile": {
    "afscp_endpoint_id": "afscp_default",
    "default_storage_pool_id": "afs_us_west_shared",
    "default_quota": {
      "library_capacity_gib": 100
    },
    "export_policy": {
      "webdav_enabled": true,
      "max_export_ttl_seconds": 86400
    }
  }
}
```

旧 API：

- `/desktop-mount-access` 返回 `ExportAccess`，不返回 JuiceFS direct mount access。
- `/storage-credential-exchange` 普通用户路径关闭；仅保留 admin/debug feature flag。
- `workspace_file_library_id` 可继续作为兼容字段，内部逐步引入 `file_library_id` / `storage_repo_id`。

## 12. 迁移策略

### 12.1 新模块落地范围

本项目需要新增一个独立应用模块，但 MVP 不强制第一步拆成独立 Git repo。

推荐先在当前 `agentsmith` / `mbos` 工作区内落成独立 package 或 service 目录，例如：

```text
services/agentsmith-fs-control-plane/
```

等内部 API、operation store、权限模型和运维边界稳定后，再视发布节奏拆成独立 repo。

推荐技术形态：

- 独立 Docker image。
- 独立 Kubernetes Deployment / Service / ServiceAccount。
- 内部 HTTP API，只允许 AgentSmith API / 管理任务访问。
- 自己的 operation store，可以先用 AgentSmith 现有 Postgres/JsonDocStore，也可以独立 Postgres schema；不要只靠内存。
- 内置 JVS CLI binary 或将 JVS 作为镜像依赖。
- 挂载或访问服务端 JuiceFS root。
- 持有 JuiceFS root credential Secret。

不建议第一版把 AFSCP 写成 AgentSmith API 里的 helper module。原因是安全边界不一样：AgentSmith API 面向产品请求，AFSCP 面向底层存储执行；后者需要更强的凭据隔离、operation recovery 和运行时权限。

现有 repo 改造边界：

| Repo / 模块 | 改造 |
| --- | --- |
| `agentsmith` | 增加 workspace storage profile、file library backend 新模型、template catalog、AFSCP client、关闭 direct credential 普通路径 |
| `agentsmith-fs-control-plane` | 新增；实现 storage pool、repo path、JVS、export、sandbox mount spec |
| `mbos-sandbox-v1` | 扩展 binding v2，接受统一 filesystem + `payload_subdir` / secret ref |
| `agentsmith-desktop` | 从 JuiceFS mount client 改为 WebDAV/export client |
| `jvs` | 不嵌入业务权限；作为 AFSCP 的 repo lifecycle/save/restore/clone 执行引擎 |

### Phase 0：止血和验证

- 停止为新 notebook task 自动创建独立 DB/bucket。
- 普通用户禁用 direct JuiceFS credential exchange。
- AFSCP bootstrap 默认共享 JuiceFS filesystem。
- 管理员创建 AgentSmith workspace 时必须选择或继承 storage profile。
- 验证 sandbox-manager 可用同一 filesystem secret + 不同 subdir 挂载多个 repo。
- 验证 WebDAV export 可以 chroot/filter `.jvs`。
- 验证 JVS `repo clone --save-points all` 在统一 FS 中的性能和 doctor 结果。

### Phase 1：新库走统一 FS

- 新建 file library 都落入该 AgentSmith workspace 绑定的 storage profile。
- 新 notebook task 默认选择或创建该 workspace storage profile 下的 file library/repo。
- Desktop 走 WebDAV export。
- sandbox 走统一 FS subdir mount。
- Save/history/restore 走 AFSCP + JVS。
- 模板功能先支持“保存 task 为 workspace 内模板”和“同 workspace 用户从模板 clone 到自己的库”。

### Phase 2：迁移旧库

对已有 per-library filesystem：

1. 保持旧库可读写，短期作为 legacy backend。
2. 用户或管理员触发迁移。
3. AFSCP 把旧库 root copy 到统一 FS 的新 repo path。
4. 在新 repo path 执行 `jvs init`。
5. 创建 `migration-baseline` save point。
6. AgentSmith 把旧 `file_library_id` 指向新的 backend record。
7. 旧 DB/bucket 进入归档和延迟删除窗口。

不要在迁移中试图保留旧 Desktop direct mount。迁移完成后只提供 WebDAV export。

### Phase 3：硬化

- Directory quota。
- shared PVC + `volumeMounts.subPath` 优化。
- strict restore。
- JVS repo lifecycle move/rename/detach 的 UI/管理端入口。
- template catalog 搜索、版本化发布。
- filesystem shard policy：按 region/tenant/compliance 创建第二个共享 FS。

## 13. P0/P1 边界

P0 必须：

- AFSCP 独立服务/容器。
- 一个默认共享 JuiceFS filesystem bootstrap。
- AgentSmith workspace 创建时支持配置 storage profile / storage pool。
- 新 file library 在 workspace 绑定的 storage pool 下创建 JVS repo。
- sandbox 挂载指定 repo subdir。
- 最小 WebDAV export，不暴露 JuiceFS credentials。
- 普通读写不做单写者限制。
- JVS save/history/restore。
- JVS repo clone 支持 template 保存/分享/clone。
- template clone 必须限制在同一个 AgentSmith workspace 内。
- `.jvs` 在 WebDAV 中不可见，在 sandbox 中有权限保护。
- 旧 direct credential API 对普通用户关闭。

P1 才做：

- 严格 restore fencing。
- directory quota 自动化。
- shared PVC + subPath mount pod 优化。
- SMB/NFS。
- workspace 内 template catalog 高级能力。
- 多 filesystem shard / storage pool policy。
- 复杂 lifecycle UI。

## 14. 验收标准

功能：

1. 新建 100 个 file library，不产生 100 个 metadata DB 或 bucket。
2. 创建 AgentSmith workspace 时，管理员可以选择 storage profile。
3. 不同 AgentSmith workspace 可以绑定不同 storage pool，新 file library 落到各自 pool。
4. 两个用户的 library 默认位于同一 JuiceFS filesystem 时，API/WebDAV/sandbox 均不能越权访问。
5. 同一 library 上 agent task 和 Desktop read-write export 可同时写入。
6. 同一文件并发写不做 merge，结果按文件系统实际写入顺序呈现。
7. 创建 save point、history、restore 可用。
8. task 保存为模板后，同一 AgentSmith workspace 内另一个用户可 clone 得到自己的独立 library。
9. clone 后目标 repo 有新的 JVS repo_id，不影响模板 repo。
10. 跨 AgentSmith workspace clone template 被拒绝。
11. Desktop API 响应不包含 `metadata_url`、bucket credential、S3 key。
12. sandbox workload env 不包含 JuiceFS credential。
13. WebDAV 无法访问 `.jvs`。

安全：

1. AFSCP 是唯一持有统一 FS root credential 的服务。
2. AFSCP path resolver 拒绝任意绝对路径和 `..` traversal。
3. K8s Secret 只给 CSI/Mount Pod 使用。
4. sandbox 普通容器默认非 root；如必须 root，需先实现 filtered view。
5. 所有 save/restore/clone/export/template 操作有审计。

JVS：

1. `jvs repo clone --save-points all` 在统一 FS 下通过 `doctor --strict`。
2. clone 不复制 runtime state。
3. template clone 后 cleanup 不删除 imported history。
4. restore preview/run JSON 可被 AFSCP 稳定解析。

## 15. 风险和取舍

### 15.1 共享 FS blast radius

共享 FS 降低成本，但提高底层 credential 的事故半径。MVP 接受默认共享 FS，前提是：

- 不把 credential 给用户/agent。
- 所有外部访问都走 AFSCP/sandbox-manager 的 subdir/chroot。
- 管理面有审计和最小权限。

需要强隔离的企业/region，未来用 shard filesystem/storage pool，而不是回到每 task 一个 DB/bucket。

### 15.2 `.jvs` 保护

JVS repo root 下有 `.jvs`。如果用户或 agent 破坏 `.jvs`，会破坏版本状态。

MVP 处理：

- WebDAV 过滤。
- sandbox 非 root。
- `.jvs` 目录 AFSCP control UID + `0700`。
- AFSCP 定期或操作后运行 `jvs doctor --strict`。

如果实际 agent runtime 必须以 root 运行且可写 repo root，那么需要先做 filtered mount/view，这会成为 P0 blocker。

### 15.3 并发写入

不限制普通并发写入会带来最后写入覆盖、半写文件被 save 捕获、restore 与 active writer 交错等风险。

这是产品选择：AgentSmith 不解决 merge，只在 UI/API 文案和审计里明确。

### 15.4 JVS clone 的 dirty gate

JVS repo clone 默认会拒绝 source workspace 有 unsaved changes。对于“保存 task 为模板”，AFSCP 应先创建 save point，再 clone。这样模板语义清楚。

## 16. 参考资料

本地资料：

- `scratch.md`
- `/home/percy/works/mbos-v1/agentsmith/docs/contracts/juicefs-file-libraries-architecture.md`
- `/home/percy/works/mbos-v1/mbos-sandbox-v1/docs/JUICEFS_CSI_WORKSPACE_MODEL.md`
- `/home/percy/works/mbos-v1/mbos-sandbox-v1/docs/contracts/agentsmith-integration-contract-v2.md`
- `/home/percy/works/mbos-v1/jvs/docs/02_CLI_SPEC.md`
- `/home/percy/works/mbos-v1/jvs/docs/24_REPO_CLONE_PRODUCT_PLAN.md`
- `/home/percy/works/mbos-v1/jvs/docs/25_REPO_WORKSPACE_LIFECYCLE_PRODUCT_PLAN.md`
- `/home/percy/works/mbos-v1/jvs/docs/06_RESTORE_SPEC.md`
- `/home/percy/works/mbos-v1/jvs/docs/09_SECURITY_MODEL.md`

外部资料：

- JuiceFS CSI PV / credentials / RWX: https://juicefs.com/docs/csi/guide/pv/
- JuiceFS CSI subdirectory mount: https://juicefs.com/docs/csi/guide/configurations/
- JuiceFS WebDAV server: https://juicefs.com/docs/community/deployment/webdav/
- JuiceFS quota: https://juicefs.com/docs/community/guide/quota/
- JuiceFS POSIX compatibility: https://juicefs.com/docs/community/posix_compatibility/
- JuiceFS cache and close-to-open consistency: https://juicefs.com/docs/community/guide/cache/
- JVS GitHub: https://github.com/agentsmith-project/jvs
