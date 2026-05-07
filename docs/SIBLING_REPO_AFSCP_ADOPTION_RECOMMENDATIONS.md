# 独立兄弟 Repo 接入 AFSCP 的 GA 架构建议

Status: recommendation draft.

Audience: AgentSmith、sandbox-manager、JVS 三个团队。

本文基于只读调研结论整理，目标是明确 GA 所需的架构边界和验收口径。它不是长路线图，也不替各 repo 设计内部实现。

## 一句话结论

AFSCP 应成为 AgentSmith 文件库、sandbox/workload mount、JVS 操作和存储生命周期的统一 storage-control plane。兄弟 repo 保留各自领域职责，但不得继续把 raw JuiceFS、PostgreSQL、MinIO、Kubernetes storage 细节作为 product caller 可见契约。

## 目标架构关系

| 组件 | GA 职责边界 | 与 AFSCP 的关系 |
| --- | --- | --- |
| AgentSmith | 产品授权、workspace/file library/catalog/UI lifecycle、产品审计投影 | 授权后显式映射 `ws/flib` 到 AFSCP `namespace/repo`，调用 AFSCP repo lifecycle、export、mount binding 和文件访问入口 |
| AFSCP | 存储控制面、namespace/repo 边界、凭据、export、mount binding、JVS operation、tombstone/restore/purge、审计与 fencing | 对普通 product caller 返回 opaque ID 和安全状态，不返回 raw storage material |
| sandbox-manager | 外部 workload mount orchestrator、runtime/Kubernetes mount 落地、写入者实际卸载确认 | 使用 orchestrator service identity 获取 AFSCP mount plan，执行后回报 lease/release/revoke/confirmed-unmounted/read-only 状态 |
| JVS | repo versioning engine 与 CLI/JSON 契约 | 提供稳定外部控制接口；AFSCP 只消费固定语义的 JSON、error schema、doctor 和 restore plan 行为 |

GA 的核心边界是：AgentSmith 不再直接分发存储连接信息，sandbox-manager 不再从产品请求中接收存储细节，JVS 不承担产品生命周期语义，AFSCP 不承担产品授权。

## AgentSmith 改造建议

### P0

- 将当前 owner-private file library 的存储实现从 AgentSmith 内部剥离。owner-private 可以继续是产品权限模型，但存储边界必须落在 AFSCP `namespace/repo`。
- 必须显式维护 `workspace/file_library` 到 AFSCP `namespace_id/repo_id` 的映射。该映射是产品对象和存储对象之间的唯一 GA 桥梁。
- 停止向产品调用方暴露 raw JuiceFS、PostgreSQL、MinIO、`metadata_url`、native mount command。新 AFSCP-backed library 只能返回 AFSCP opaque ID、状态和受控访问描述。
- 文件 CRUD 当前推荐由 AgentSmith 通过 AFSCP WebDAV export 做受控代理；不把 AFSCP internal file API 作为当前承诺。如未来需要 internal file API，必须另行作为受控契约设计，并满足 AFSCP 授权、路径边界、审计和凭据生命周期要求。
- sandbox/workload mount 改为 AFSCP `mount_binding` 加 orchestrator plan 模式。AgentSmith 只发起产品授权后的绑定请求，不向 sandbox-manager 传递 `metadata_url/storage/subdir`。
- 存储生命周期从同步硬删改为 AFSCP operation：trash/delete 对应 tombstone，restore 对应 restore operation，permanent delete 对应 purge operation。AgentSmith 可以保留产品 UI 状态，但不得自行删除底层 repo payload/control 数据。

### P1

- 为 legacy direct-storage file library 与 AFSCP-backed repo 增加清晰兼容边界；新建资源默认走 AFSCP，缺能力时 fail closed。
- 产品审计事件携带 actor、correlation、idempotency，但 raw storage 信息只允许留在 AFSCP 内部安全审计域。
- 将 Web/API 返回模型收敛到稳定 product DTO，避免把 AFSCP 内部 operation、worker、mount 细节变成产品长期契约。

## sandbox-manager 改造建议

### P0

- 新增 AFSCP orchestrator v2 接入面：接收或解析 `mount_binding_id`，使用 orchestrator service identity 从 AFSCP 拉取 mount plan。
- 对 AFSCP-backed workload，停止接收来自 AgentSmith 或普通产品调用方的 `metadata_url`、storage endpoint、bucket credential、Secret value、source subdir。
- runtime mount 只能来自 AFSCP plan，包括 `mount_binding_id`、`volume_id`、`payload_volume_subdir`、`mount_path`、`read_only`、`secret_ref`、`security_policy` 等受控字段。
- 补齐 lease/release/revoke/confirmed-unmounted 语义。AFSCP 只有在 sandbox-manager 确认 runtime mount 已 unmounted/non-accessing 后，才能把 write-capable binding 视为 terminal；这同时证明 workload 不再可写。
- `released`/`revoked` 是 evidence-bearing non-accessing terminal statuses，必须只在确认 unmounted/non-accessing 后使用；`expired`/`failed` 只是 observed terminal statuses，不证明 unmounted/non-accessing，也不证明 unable-to-write，不能放行 lifecycle，必须 fail closed。
- 强制 read-only 语义，并保证 runtime 只挂载 payload root，不暴露 JVS control metadata。
- 增加 collision guard。Kubernetes Secret/PV/PVC/Pod mount 资源名经过规范化和截断后仍必须抗碰撞，不能只依赖可预测前缀。

### P1

- 将 active、released、revoked、expired、failed、uncertain writer state 的状态回报做成稳定契约，并明确 `released`/`revoked` 承载 confirmed-unmounted/non-accessing 语义，`expired`/`failed` 承载 observed/uncertain 语义。未来如果要支持 unable-to-write-but-still-mounted/readable 作为 restore-run 放行证据，必须新增显式 evidence 字段/状态，不能复用 `released`/`revoked`。
- 对 revoke 后仍可写、Pod 已删但 mount 未确认、read-only 被绕过等失败模式建立回归验收。
- 对外错误信息默认 redacted，只返回可诊断的稳定错误码和 correlation 信息。

## JVS 改造建议

### P0

- JVS `v0.4.8` 基本满足 AFSCP GA 需要，但需要把外部控制模式作为稳定契约固化：AFSCP 使用 explicit external-control root，不依赖 CWD 或自动发现。
- 固化 version JSON 输出，确保 AFSCP 可在 worker 启动、smoke、operation record 中记录 JVS 版本与能力。
- 固化 error schema，并提供 golden fixtures。AFSCP 需要稳定映射 JVS 错误到 caller-visible error code，不能解析自由文本。
- 明确 `doctor --strict` 的 write probe 文档，尤其是它会写什么、验证什么、失败时哪些状态可恢复。
- 将 external-control CLI 视为 stable API，包括 `init`、save/history、restore preview/run/discard、clone、doctor、recovery status 等 AFSCP 依赖命令。
- 固化 `restore_state/plans` 语义：单 repo pending plan、consume/discard、crash recovery、operator intervention 的 JSON 状态必须稳定。

### P1

- 继续补充 JSON fixture 覆盖 truncated history、pending restore plan、failed restore、doctor failure、external-control root mismatch 等边界。
- 发布资产应带可验证 checksum/signature，方便 AFSCP 固定 runner binary。
- 对 CLI 输出中的路径、命令建议、debug 字段提供 redaction 指南，避免 AFSCP operation/audit 存入内部路径。

## AFSCP 侧结论

- AFSCP 对普通产品调用方只承诺 storage-control API，不承诺 raw storage API。
- AFSCP 必须把 `namespace/repo` 作为 AgentSmith `ws/flib` 的明确承接对象；隐式 owner-private storage 不足以成为 GA 多团队边界。
- AFSCP mount binding 与 orchestrator plan 必须分层：AgentSmith 获得 caller-visible binding，sandbox-manager 以 orchestrator 身份获取 runtime plan。
- repo lifecycle 必须是 durable operation：archive/tombstone/restore/purge 都要经过 writer drain、export revoke、mount revoke、JVS consistency check 和审计。
- AFSCP 不应把 JVS 当作产品生命周期源。JVS 负责版本与 restore mechanics；AFSCP 负责 repo availability、tombstone、restore/purge operation 和安全状态。
- AFSCP 应保留内部 worker 配置中的 volume root、Secret 引用、payload/control subdir 解析能力，但这些不是 product contract。

## E2E 验收清单

- AgentSmith 新建 file library 时不再执行 `juicefs format`，也不创建 raw PG/MinIO/JuiceFS backend 作为产品对象。
- AgentSmith 能把 `workspace/file_library` 稳定映射到 AFSCP `namespace/repo`，并只保存 opaque ID 与产品状态。
- Web 文件 CRUD 通过 AgentSmith 对 AFSCP WebDAV export 的受控代理完成，路径限制在 repo payload root；不得将 AFSCP internal file API 作为当前验收承诺。如未来引入 internal file API，须另行验收其授权、路径边界、审计、凭据生命周期契约。
- 新 AFSCP-backed library 的产品 API 不返回 `metadata_url`、MinIO credential、PG DSN、native mount command、Kubernetes Secret/PV/PVC 名称。
- AgentSmith 发起 workload mount 后，sandbox-manager 通过 AFSCP orchestrator v2 获取 mount plan，而不是从 AgentSmith 请求体读取 storage/subdir。
- workload 内 read-write binding 可被 revoke，且 sandbox-manager 在 confirmed-unmounted 前不报告 terminal success。
- read-only binding 在 workload 内真实不可写。
- runtime mount 只暴露 payload root，不暴露 JVS control root 或 `.jvs` 元数据。
- tombstone 后新 export、mount、save、restore-run 被阻断；restore operation 成功后经过 JVS `doctor --strict` 验证再恢复可访问状态。
- purge 只在确认 lifecycle fencing、mount/export drain 和审计记录后永久删除 payload/control 数据。
- JVS version JSON、error schema fixtures、restore plan JSON 和 `doctor --strict` 行为被 AFSCP smoke 覆盖。

## 不应暴露给 product caller 的 raw storage 信息

以下信息不得出现在 AgentSmith 产品 API、普通 product caller 响应、用户可见配置、workload 创建请求或跨团队普通调用契约中：

- JuiceFS `metadata_url`、volume name、format 参数、native mount command。
- PostgreSQL DSN、metadata database 名称、用户名、密码或连接参数。
- MinIO/S3 endpoint、bucket credential、access key、secret key、root bucket 权限。
- Kubernetes Secret 名称、Secret value、PV/PVC 名称、storageClass 细节。
- AFSCP worker 的 absolute volume root、host path、node path、payload/control 绝对路径。
- repo container root、JVS control root、`.jvs` 控制元数据路径。
- 未 redacted 的 JVS stdout/stderr、restore recommended command、debug path。

普通 product caller 可以接收的是产品对象 ID、AFSCP opaque ID、受控访问 URL/credential、operation ID、稳定状态、稳定错误码、correlation ID，以及不泄露底层实现的审计摘要。
