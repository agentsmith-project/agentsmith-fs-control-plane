# AFSCP 用户指南

本文面向三类读者：

- 集成方：把自己的控制面、后台任务或平台工具接入 AFSCP。
- 平台操作者：负责部署、配置、readiness、release gate、运行期排障。
- 调用方工程师：通过内部 API 创建 repo、保存版本、恢复版本、发放导出或挂载授权。

本文只解释 AFSCP 的通用文件系统控制面能力。具体请求体、响应体、错误码和字段名以
[API_CONTRACT_DRAFT](API_CONTRACT_DRAFT.md)、[contracts](contracts/README.md) 和
`api/openapi/internal-v1.openapi.yaml` 为准。

## AFSCP 是什么

AFSCP 是一个独立的共享文件系统控制面。它负责管理受控 volume、namespace、repo、JVS
保存/恢复、repo lifecycle、template、WebDAV export、workload mount binding、operation
和 audit。

AFSCP 的职责是执行已经授权的存储控制动作，并把存储边界做牢：namespace 归属、repo
路径解析、JVS 控制目录隔离、导出和挂载的 payload-only 访问、异步操作记录、审计和恢复。

AFSCP 独立运行、独立演进、独立 release gate。调用方系统可以把它作为内部存储控制面
使用，但调用方的业务对象、业务权限和业务流程不属于 AFSCP。

## AFSCP 不是什么

AFSCP 不是用户可见的业务应用，不是业务权限系统，不是产品目录，也不是工作流引擎。

AFSCP 不应该包含任何具体业务应用名称、业务对象名称或业务逻辑。它不会决定“用户在某个
业务场景里是否应该看到按钮”，也不会维护调用方的展示名、目录、项目、任务、模板市场或
终端客户端体验。

AFSCP 也不是普通 NAS 的直接暴露层。普通用户、桌面客户端和 workload 容器不应该直接调用
AFSCP 内部 API，也不应该拿到 JuiceFS root credential、metadata URL、bucket credential、
Kubernetes Secret reference、raw mount command 或 `.jvs` 控制目录。

## 系统设计原则

- 产品无关：AFSCP 只暴露通用存储概念，如 volume、namespace、repo、save point、export、
  mount binding、operation 和 audit。业务权限、业务目录和展示文案由调用方系统负责。
- Repo-local gate：AFSCP release readiness 由本仓库脚本、契约、schema、OpenAPI、测试和
  runbook 证据决定，不依赖外部部署验收或其他仓库状态。
- 默认安全/失败关闭：缺少能力、缺少配置、namespace disabled、volume unhealthy、writer
  session 不确定、JVS 状态不明确、路径不安全时，AFSCP 默认拒绝 mutation 或进入
  `operator_intervention_required`，而不是猜测继续执行。
- 异步 durable operation：会改变存储状态的动作先落 durable operation 记录，再由 worker
  执行或恢复。调用方用 operation ID 追踪结果，重试依赖 idempotency key。
- 最小权限与密钥隔离：普通调用方只看到业务上需要的受控结果。Secret reference 只给专用
  orchestrator 角色；WebDAV 密码只在创建成功的首次响应中出现；原始路径和 credential 默认
  脱敏。
- JVS 外部控制：repo 的 JVS control root 与 workload/WebDAV 可见的 payload root 分开。
  普通访问只进入 payload，不暴露 `.jvs`。
- 普通 IO 与控制面 mutation 区分：普通文件读写不由 AFSCP 串行化；save、restore-run、
  template create、lifecycle、export/mount issuance 等控制面 mutation 需要 operation、fence、
  policy 和 audit。template clone 通过 template read gate 和 target repo exclusive create 管理
  template 读取与目标 repo 创建冲突。

## 架构概览

AFSCP 的主要边界如下：

- API：内部 HTTP API，负责服务认证、caller-service 授权、namespace/resource 校验、请求
  intake、标准响应 envelope 和稳定错误码。
- Worker：消费 durable operation，执行 repo create、save、restore、template、lifecycle、
  export revoke/reconcile、mount binding 状态推进等存储 mutation。
- Export gateway：AFSCP 控制的 WebDAV policy gateway。它负责短期访问、路径过滤、请求账本、
  revoke/drain 语义和审计边界；不能把裸 `juicefs webdav` 当作 GA policy boundary。
- Postgres operation/audit store：保存 operation、resource metadata、idempotency、fence、
  restore plan、export/mount session 和 audit outbox 等 durable 状态。
- JVS runner：受控执行已 pin 的 JVS 命令，解析 JSON 输出，运行 doctor/recovery 检查，并避免
  把 raw command、raw path 或 secret 泄露给普通响应。
- Managed volume/namespace/repo/path resolver：AFSCP 从结构化 ID 和 namespace context 解析受控
  路径。调用方不得传权威 raw filesystem path。
- External orchestrator boundary：外部编排器消费 orchestrator mount plan，创建或清理运行时挂载。
  它不做业务授权，只按 AFSCP 的 payload-only plan 执行。

## 功能说明

### Volumes

Volume 是 AFSCP 管理的底层文件系统/存储池，当前基线以 JuiceFS-backed volume 为核心。Volume
记录 capability、health 和 credential reference，但 credential 本身是部署内部配置，不进入普通
调用方响应。

Volume health 不是单纯查 metadata。健康结果需要 durable volume metadata、所需 capability 和
backend probe 一起通过。

### Namespaces

Namespace 是隔离和策略边界。它绑定默认 volume，声明 allowed callers、角色、export policy、
lifecycle policy、mount policy 和 template policy。

Quota hook 和 `quota_bytes_default` 是策略记录和集成启用语义；它们不表示目录配额已经默认被底层存储强制执行。

Namespace disabled 后，新 mutation、新 export、新 mount binding 默认拒绝。授权的 operator
inspection 或特定 cleanup 例外以契约为准。

### Repos

Repo 是 namespace 内的 JVS-managed filesystem root。AFSCP repo ID 稳定且不可变。调用方可以在
自己的系统里改展示名或目录关系，但那不是 AFSCP repo rename。

普通 repo 响应只应暴露 ID、namespace、volume、状态、lifecycle 等安全字段；control root、payload
root、`.jvs` 路径、JuiceFS root 细节属于内部状态。

### Lifecycle

Repo lifecycle 覆盖 archive、restore-archived、delete、restore-tombstoned 和 purge。

这些是存储控制操作，不是业务目录操作。Archive 保留数据但停止普通访问；delete 进入可恢复
tombstone；restore-tombstoned 在 retention 和 policy 允许时恢复；purge 是不可逆删除，需要明确
确认/审批引用并审计。

Lifecycle 操作会与 export、mount session、JVS mutation 和 lifecycle fence 协调。无法确认安全状态时
失败关闭或进入 operator intervention。

### Save/Restore

Save point 是 JVS 管理的版本标记。创建 save point 是 durable operation。

Restore 分成 preview、run 和 discard。Preview 会创建 durable restore plan，不只是“读一下历史”。
Restore-run 必须引用同 namespace、同 repo、同资源边界内有效的 preview/plan，并在执行前取得
writer-session fence，拒绝 active 或 uncertain read-write export/workload mount session。

普通文件 IO 可以继续发生，但 same-repo JVS mutation 会被 active restore plan 或 JVS lock 阻挡。
Dirty state 默认失败关闭。

### Templates

Repo template 是 namespace-scoped 的已发布快照 repo。GA 基线中 template 创建会从 source repo 创建
新的 source save point，再生成 template；template clone 创建新的独立 repo。

跨 namespace clone 默认拒绝。若 template 所在 volume 与 namespace 当前默认 volume 不匹配，clone
必须拒绝并提示需要 import 类流程，而不是静默跨 volume 创建。

### WebDAV Export

WebDAV export 是短期、受控的普通客户端访问方式。AFSCP 创建 export session，并在首次成功创建时
返回一次性 access credential。后续 get 或同 idempotency replay 不应再次返回密码。

Revoke 是 durable 边界：请求会把 session 推入 revoking/drain 流程，只有 gateway 或 reconcile 确认
不会继续访问后，才进入 terminal revoked。

### Workload Mount Binding 和 Orchestrator Plan

Workload mount binding 是调用方可见的挂载授权记录。它给调用方一个 opaque binding ID，用于编排
流程。

Orchestrator mount plan 是给专用 orchestrator 角色的特权视图，可能包含 Secret reference 和
payload volume subdir。普通调用方和 workload 不应该看到该 plan。计划必须只指向 payload，不暴露
control root。

Read-write mount binding 在 live lease 或 uncertain 状态下会阻挡 restore-run。Revoke 只有在编排器
确认 runtime mount 已停止或无法写入后才真正 terminal。

### Operations

Mutation 通过 durable operation 表达。标准 mutation 响应返回 flat `OperationEnvelope`；operation
inspection 通过 `GET /internal/v1/operations/{operationId}` 返回 redacted `OperationRecord`。

Operation 支持 idempotency、lease、phase、resource IDs、safe metadata、error code、audit 关联和
recovery。无法确定是否安全重试时，应进入 operator intervention，而不是重复执行外部副作用。

### Audit

AFSCP 发出低层审计事件，调用方系统可以把它投影成自己的用户可见审计。审计需要区分
`caller_service` 和 `authorized_actor`：前者是调用 AFSCP 的内部服务，后者是调用方确认过的最终
用户、系统任务或管理员。

审计必须覆盖授权拒绝、namespace/resource mismatch、path denial、capability denial、export credential
issuance/revoke、mount plan issuance、lifecycle、restore denial、operator action 等关键事件，并默认
脱敏 credential、Secret、raw path 和 backend 细节。

### Inspection/Readiness

Readiness 是服务运行状态，不是 caller 授权。`/readyz` 报告 capability readiness、bootstrap/runtime
health 和 default/optional gating 状态，但它不能代替 API admission、角色授权或 release gate。

Volume health 通过 volume health endpoint 查看。按 ID 查询 operation 是稳定的内部 API 检查入口。
更宽的 operator 视图，如 correlated lookup、fence/session/audit lag，需要按 runbook、只读查询、
observability 或部署侧 operator tooling 执行。

## 使用方式

`docs/READINESS_EVIDENCE.md` 是 readiness 和 release evidence 的 `current implementation baseline` 证据台账。

### 如何读 readiness

平台操作者通常先确认部署使用正确 readiness profile。GA candidate deployment 应显式设置
`AFSCP_READINESS_PROFILE=ga`，使 `/readyz` 按完整 GA capability set 判断服务 readiness。

读取 `/readyz` 时重点看三件事：

- 服务是否 ready：这表示当前实例的控制面依赖、capability 配置和 runtime store health 是否满足
  readiness 要求。
- 哪些能力是 default、optional 或 disabled：readiness 可以说明能力状态，但不会授予任何 caller
  权限。
- 是否有稳定 finding code：缺少 backend probe、store 不可用、配置不完整等都应表现为稳定原因，
  不应泄露 raw path、secret 或 backend 细节。

Volume 本身的健康通过 `GET /internal/v1/volumes/{volumeId}/health` 读取。字段和状态值以 OpenAPI
为准。

### 如何运行 release gate

AFSCP 的权威 GA release gate 是：

```bash
bash scripts/verify-ga-release.sh
```

该脚本读取 `docs/release-evidence/ga-release-selector.json`，并按 selector 决定运行 seed/convergence
还是 final candidate 验证。最终是否通过只看这个 repo-local 脚本的退出码。不要用手工 review、外部
部署状态、会议结论或其他仓库状态替代该 gate。

当前 selector 文件中 `claimed_optional_capabilities` 字段为 `[]`。这表示未选择的 optional/future gaps
不阻塞 default GA；required/default gaps 仍会阻塞。一旦 selector 声明某个 optional capability，
该 optional 对应的未闭合 gap 就会导致机器检查失败，并让 final mode 返回非零。

### 如何用 internal API 的通用流程

内部 API 只给可信服务、admin/operator job、migration job、operator tool 和专用 workload orchestrator
使用。普通最终用户、客户端和 workload 容器不直接调用。

通用流程：

1. 调用方系统先完成自己的业务认证和业务授权。
2. 使用部署规定的 service credential 调用 AFSCP；`X-AFSCP-Caller-Service` 必须能映射到已认证的
   service principal 或别名。
3. Namespace-bound 请求带 `X-AFSCP-Namespace-Id`。如果 path、query 或 body 也带 namespace，所有
   namespace 值必须一致。
4. Mutation 请求带 `Idempotency-Key`、`X-Correlation-Id`、`X-AFSCP-Actor-Type` 和
   `X-AFSCP-Actor-Id`。Actor 是调用方已经授权的最终发起者，不是 AFSCP 自己推断的用户。
5. 请求体按 OpenAPI 请求体填写。不要传 raw filesystem path、Secret reference、JuiceFS credential、
   `.jvs` 路径或业务应用字段。
6. Mutation 返回 `OperationEnvelope`。如果状态不是 terminal，调用方按契约读取
   `GET /internal/v1/operations/{operationId}`，直到 operation 进入 succeeded、failed、cancelled 或
   `operator_intervention_required`。
7. 对 `IDEMPOTENCY_CONFLICT`、`CALLER_NOT_ALLOWED`、`NAMESPACE_DISABLED`、`CAPABILITY_DENIED`、
   `ACTIVE_WRITER_SESSIONS`、`OPERATION_RECOVERY_REQUIRED` 等稳定错误码做产品侧映射。不要解析错误
   message 来驱动业务逻辑。

字段、状态、错误码、HTTP 状态码和响应 envelope 以 OpenAPI/schema/contract 为准。

## Tutorial

下面流程是通俗路径，不是完整 API reference。每一步的请求体都按 OpenAPI 请求体填写。

### 1. 创建并读取 repo

前置条件：平台已准备好 volume、namespace volume binding 和 caller role。调用方已完成业务授权。

1. 调用 `POST /internal/v1/repos` 创建 repo，请求体按 OpenAPI `CreateRepoRequest`。
2. 从响应的 `OperationEnvelope` 取得 operation ID。
3. 用 `GET /internal/v1/operations/{operationId}` 查看 operation 进度。若已 succeeded，读取其中安全
   结果或继续查 repo。
4. 调用 `GET /internal/v1/repos/{repoId}` 读取 repo projection。
5. 如需列出 namespace 内 repo，调用 `GET /internal/v1/repos?namespace_id={namespaceId}`；请求 header
   中的 namespace 必须与 query namespace 一致。

注意：repo projection 不是业务目录。不要期望它返回业务展示名、业务对象、raw path 或 credential。

### 2. 创建 save point 并执行 restore preview/run

前置条件：repo 处于可进行 JVS mutation 的状态；没有 active restore plan 阻挡；调用方具备相关角色。

1. 调用 `POST /internal/v1/repos/{repoId}/save-points` 创建 save point，请求体按 OpenAPI。
2. 通过 operation inspection 等待 save point 创建成功。
3. 调用 `POST /internal/v1/repos/{repoId}/restore-preview`，按 OpenAPI 请求体指定要预览的 save point。
4. 等待 preview operation succeeded。Preview 成功代表 AFSCP 持久化了一个 pending restore plan。
5. 若决定不恢复，调用 `POST /internal/v1/repos/{repoId}/restore-preview:discard`，请求体按 OpenAPI。
6. 若决定恢复，调用 `POST /internal/v1/repos/{repoId}/restore-run`，请求体按 OpenAPI 引用有效的
   preview operation。
7. Restore-run 会取得 writer-session fence，并拒绝 active 或 uncertain read-write export/workload
   mount session。若收到 `ACTIVE_WRITER_SESSIONS`、`WRITER_SESSION_FENCE_HELD` 或相关稳定错误码，先按
   runbook 处理 session，再重试或 discard。

注意：restore-run 是控制面版本 mutation，不是普通文件读取。它会运行 JVS restore 和 doctor，并在状态
不明确时失败关闭。

### 3. 创建 WebDAV export 并 revoke

前置条件：namespace export policy 允许 WebDAV，runtime 已配置有效 public base URL，调用方具备
export role。

1. 调用 `POST /internal/v1/repos/{repoId}/exports` 创建 export，请求体按 OpenAPI `CreateExportRequest`。
2. 首次成功创建会在 operation result 中返回 redacted export session 和一次性 access credential。调用方
   可以把该 credential 交给已经通过业务授权的连接器或用户流程。
3. 不要依赖重放请求再次取回密码。同一 idempotency key 的 replay 应返回 session 但不重新发放
   access secret。
4. 如需查看 session 状态，调用 `GET /internal/v1/exports/{exportId}`。该接口只返回 redacted session。
5. 如需撤销，调用 `DELETE /internal/v1/exports/{exportId}`。请求会进入 revoke/drain 流程。
6. 继续读取 export session 或相关 operation，直到 gateway/reconcile 确认 terminal revoked。

注意：WebDAV export 是短期受控访问，不是裸 JuiceFS mount，也不是把 AFSCP credential 交给普通用户。

### 4. Repo lifecycle：archive/delete/restore/purge

前置条件：repo 所在 namespace 的 lifecycle policy 允许对应动作；调用方具备相关角色；没有需要先 drain 的
export、mount 或 JVS mutation 状态。

1. 需要临时停止普通访问时，调用 archive endpoint，按 OpenAPI 填写请求体，并等待 operation 进入 terminal。
2. 需要恢复 archived repo 时，调用 restore-archived endpoint，并通过 operation inspection 确认结果。
3. 需要进入可恢复删除状态时，调用 delete endpoint。Delete 是 repo lifecycle mutation，不是业务目录隐藏。
4. 若 retention 和 policy 允许恢复 tombstone，调用 restore-tombstoned endpoint。
5. 只有在明确确认不可逆删除、满足 retention/policy/runbook 要求时，才调用 purge endpoint。

注意：lifecycle mutation 会和 session drain、lifecycle fence、audit、worker recovery 协调。请求字段、
确认字段和错误码以 OpenAPI 为准。

### 5. Template create/clone

前置条件：

- Template create：namespace template policy 允许；source repo 可进行 JVS mutation；source repo
  通过 writer-fence、dirty-source 和 session 检查。
- Template clone：namespace template policy 允许；template 对调用方可读；namespace/volume contract
  允许创建目标 repo；目标 repo 创建 admission 没有资源冲突。

1. 调用 template create endpoint，从 source repo 生成 namespace-scoped template；请求体按 OpenAPI。
2. 等待 operation succeeded。创建过程会在 source repo 内生成新的 save point，再形成 template。
3. 调用 template clone endpoint，从 template 创建新的独立 repo。
4. Clone 完成后，按 repo 读取流程读取新 repo projection，并由调用方系统维护自己的业务目录关系。

注意：template 不是全局市场对象。跨 namespace 或跨 volume 行为以 contract 和 OpenAPI 为准，
默认不要假设会自动导入。

### 6. Workload mount binding 和 orchestrator plan

前置条件：namespace mount policy 允许；调用方具备 mount issuance 角色；专用 orchestrator 服务已接入。

1. 调用 workload mount binding create endpoint，创建 caller-visible 的 opaque binding。
2. 普通调用方只保存 binding ID 和可见状态，不读取 Secret reference、raw path 或 mount command。
3. 专用 orchestrator 角色调用 orchestrator plan endpoint，取得 payload-only plan。
4. Orchestrator 在自己的运行时创建或清理挂载，并按 contract 回报 heartbeat、release、revoke 或
   confirmed-unmounted 状态。
5. 调用方通过 binding/status 或 operation inspection 观察状态变化。

注意：orchestrator plan 是特权视图，只服务运行时挂载编排；业务授权和用户体验仍由调用方系统负责。

## 在哪里继续阅读

- [README](../README.md)：项目定位、核心模型和 release gate 入口。
- [ARCHITECTURE](ARCHITECTURE.md)：组件边界、数据 authority 和并发模型。
- [API_CONTRACT_DRAFT](API_CONTRACT_DRAFT.md)：内部 API narrative、endpoint group、响应 envelope 和错误码。
- [DEVELOPER_GUIDE](DEVELOPER_GUIDE.md)：开发团队入口，包含本地开发、测试和 evidence 维护约定。
- [contracts](contracts/README.md)：各功能面的 GA implementation-baseline contract。
- [GA_RELEASE_GATES](GA_RELEASE_GATES.md)：selector-driven release gate 规则。
- [READINESS_EVIDENCE](READINESS_EVIDENCE.md)：repo-local readiness evidence ledger。
- [runbooks](runbooks/README.md)：平台操作和故障处理入口。
