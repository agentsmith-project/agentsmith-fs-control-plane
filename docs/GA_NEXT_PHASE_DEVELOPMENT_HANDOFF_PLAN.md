# AFSCP 下一阶段开发交接计划

Status: development handoff plan for direct GA convergence.

本文档交给下一轮开发团队使用。它基于
`docs/research/afscp-product-architecture-review.md` 的问题清单，以及现有
`docs/GA_CONVERGENCE_WORK_PLAN.md`、`docs/DEVELOPER_HANDOFF.md`、
`docs/GA_RELEASE_GATES.md`、`docs/READINESS_EVIDENCE.md` 和
`scripts/verify-ga-release.sh` 的当前口径。

目标不是再做一张复杂路线图，而是把下一阶段工作压成可以直接开工、直接验收的
5 个开发包。开发团队应围绕这些包收敛到 GA，不扩大产品面，不引入外部业务项目，
不把主观 review 或会议当作 gate。

## Scope alignment / source of truth

本计划是下一阶段开发交接的执行口径，专门用来化解当前
`docs/PRODUCT_REQUIREMENTS.md`、`docs/ARCHITECTURE.md` 与既有 GA 计划之间的
默认边界冲突。PRD/Architecture 中已经描述的能力不等于默认 GA 全部启用；下一阶段
开发应按本文的默认 GA 闭环与 optional-gated 规则实现、验收和补证。

后续必须交付 doc-sync，但 doc-sync 是开发包完成后的对齐工作，不应在本轮先大范围
改 PRD/Architecture。doc-sync 的目标是把 PRD/Architecture 中“GA storage-control
operations”“workload mount available”“template GA”等容易被读成默认开启的表述，
统一改成：

- 默认 GA：必须在无外部业务项目、无真实部署环境依赖的 repo-local gate 中证明。
- optional-gated：代码和契约可以保留，但默认关闭；默认 GA 只证明 disabled、denied、
  recovery 安全。只有 repo-local fixture capability 显式启用且 evidence 完整时，
  才能把该 optional capability 标记为 fixture-enabled ready。
- deployment-only 风险：只能要求本仓库证明检测模型、模拟 fixture、redaction、
  path policy、runbook/escalation；不能把真实 CSI/POSIX/subPath 部署状态作为本仓库
  release gate。

## 目标

下一阶段只做一件事：把 AFSCP 收敛成产品中立、可独立发布、可自动证明的共享文件系统控制面 GA 闭环。

完成后必须满足：

- 默认 GA 能力边界固定、可证明、可自动验收。
- 默认 GA 用户闭环前置：trusted caller 可以创建/查看 namespace-scoped repo，执行
  save/history/restore-preview/restore-run/discard，通过 WebDAV export/gateway/revoke
  访问 payload，并能用 operation/audit/recovery 追踪结果。
- 高风险能力可以保留在代码和契约中，但默认必须 capability-gated。
- API admission、worker execution/recovery、readyz、operator inspection、release evidence 使用同一份 capability matrix。
- 新 mutation 不会在不可执行时创建永久 `queued` operation。
- 历史 operation 即使当前 capability 关闭，也会被 recovery 扫描并进入明确终态。
- operator 有最小发现、定位、干预和审计闭环，不需要把临时 SQL 当作主要修复机制。
- 每个高风险 GA 声明都有 repo-local 自动化证据，并被唯一 gate 覆盖：

```bash
bash scripts/verify-ga-release.sh
```

## 非目标

本轮不做以下事情：

- 不引入任何业务项目名、调用方业务概念或兄弟 repo 依赖。
- 不依赖人工审批、会议、主观 review、owner sign-off、consumer adoption 作为 GA gate。
- 不做 UI、business catalog、业务生命周期、namespace delete、template marketplace。
- 不做多语言 client matrix；只允许 repo-local 最小 generated-client/fixture 编译证据。
- 不做通用运维搜索平台；只做 operator 必需的最小 inspection/repair 闭环。
- 不让普通 caller、client connector 或 workload 看到 raw root path、metadata URL、SecretRef、底层 credential、`.jvs` 路径或 WebDAV raw password replay。
- 不用文档声明替代高风险路径的自动化测试。

运行态安全控制不是 release gate。`operator_intervention_required`、operator repair、purge approval evidence、residual-risk acceptance 都是产品运行安全机制；它们必须被自动化测试保护，但不是人工 GA 审批流程。

## 默认 GA 能力边界

默认 GA 能力必须写死为以下闭环：

- namespace 与 managed volume binding。
- repo create/get，以及 namespace-scoped repo projection/list。
- pinned JVS save/history。
- restore-preview/restore-run/restore-discard。
- WebDAV export/gateway/revoke。
- retained repo lifecycle：archive、restore_archived、delete/tombstone、restore_tombstoned。
- operation inspection、audit outbox、worker recovery。

默认 GA 用户闭环必须在证据包前段就能被一眼追踪，而不是只埋在 evidence manifest 后段：

```text
trusted caller
  -> namespace/binding policy
  -> repo create/get/projection/list
  -> JVS save/history/restore-preview/restore-run/discard
  -> WebDAV export/gateway/revoke
  -> operation status + audit + recovery evidence
```

repo projection/list 的默认 GA 语义必须收窄：它只允许 caller 在授权 namespace 内查看 repo storage projection，带明确分页、过滤和权限边界；它不是 global search、aggregation、operator investigation 平台，也不承载业务 catalog 查询。

以下能力默认保留但必须 capability-gated：

- workload orchestrator、workload mount 与 orchestrator mount plan。
- template/clone。
- purge 与 break-glass purge。
- 超出默认 GA 的 runtime variants，例如非 pinned JVS 运行方式、替代 gateway、特殊 orchestrator、特殊 storage-plane mutation。

pinned JVS runner 支撑的 save/history/restore-preview/run/discard，以及 AFSCP WebDAV gateway 支撑的 export/revoke，属于默认 GA 必证能力；不能因为它们依赖 runtime 就写成默认可选。optional-gated 只表达默认 GA 之外的高风险或变体能力。

能力关闭、未配置、未 ready 或 namespace/volume policy 不允许时，新请求必须稳定拒绝，或在已有历史 operation 的 recovery 中终态化。不能把“能力暂不可用”表达成永久排队。

Repo lifecycle 默认边界必须显式写入 contract、schema/OpenAPI、capability matrix 和证据：

| Lifecycle action | 默认 GA 口径 | Admission default | Worker/recovery default | 说明 |
| --- | --- | --- | --- | --- |
| `archive` | 默认 GA storage-state | enabled when lifecycle capability ready | 可执行；session/fence 不确定时进入 intervention | 表达存储不可普通访问和保留，不是业务归档流程。 |
| `restore_archived` | 默认 GA storage-state | enabled when lifecycle capability ready | 可执行；是否需要 no-sessions 必须由 decision record 固定 | 不改变 repo identity。 |
| `delete` / `tombstone` | 默认 GA storage-state | enabled when lifecycle capability ready | 可执行；必须 drain/revoke 或 fail-closed/intervention | 表达 retained tombstone/trash，不是产品删除 UX。 |
| `restore_tombstoned` | 默认 GA storage-state | enabled within retention and policy | 可执行；session drain 行为必须与 contract 一致 | 不得恢复已 purge 的 repo。 |
| `purge` | optional-gated irreversible capability | default profile 永远 disabled；只有 explicit optional/fixture-enabled/deployment-enabled profile 且结构化 approval evidence 可验证时才允许 positive path | 历史 purge operation 必须被扫描并 failed/intervention；不能遗留 queued | 默认 GA 只证明 disabled/denied/recovery 安全；positive evidence 的 release manifest 必须标为非 default，且 `default_mode=false`。 |
| break-glass purge override | optional-gated break-glass | disabled by default | 只有结构化 approval evidence 可验证时才允许执行 | 不是人工 GA 审批，也不是自由字符串。 |

## 核心架构方案

下一阶段的核心架构命题是统一控制链：

```text
capability -> admission -> operation -> worker recovery -> readiness -> evidence
```

capability matrix 是以下所有判断的唯一事实源：

- API admission 是否接受新 mutation。
- worker 是否注册 executor，以及 recovery 如何处理历史 operation。
- readyz 如何表达 service-ready 与 optional gated 能力。
- operator inspection 如何展示 capability/runtime/recovery 状态。
- release evidence manifest 如何证明每条 GA 声明。

capability matrix 至少要区分：

- `operation_type`: capability 覆盖的 durable operation 类型。
- `capability_id`: 跨 API、worker、readyz、operator、evidence 共用的稳定能力 ID。
- `resource_scope`: capability 适用的 resource scope，例如 service、namespace、volume、repo。
- `supported`: 当前版本是否实现该能力。
- `configured`: runtime 是否有必要配置。
- `ready`: 当前进程/依赖是否可安全执行。
- `required_for_default_ga`: 是否属于默认 GA 闭环。
- `required_for_service_ready`: 是否影响服务基础 readyz。
- `optional_gated`: 是否为可保留但默认 gate 的高风险能力。
- `namespace_policy`: namespace 是否允许使用该能力。
- `volume_runtime_capability`: volume/runtime 是否具备执行条件。
- `denial_code`: 不可用时返回的稳定错误码。
- `runbook_ref`: operator 可定位的处理入口。
- `evidence_ref`: release evidence manifest 中的证据 ID。

capability matrix v1 rows 只允许覆盖本仓库 GA 收敛所需能力，避免演变成通用 feature-flag 平台：

| capability_id | operation_type 示例 | default_ga_required | optional_gated | service_ready 影响 | fixture-enabled 正向路径 |
| --- | --- | --- | --- | --- | --- |
| `namespace_binding` | namespace create/bind/update policy | yes | no | yes | no |
| `repo_create` | repo create | yes | no | yes | no |
| `repo_projection` | repo get/list projection | yes | no | yes | no |
| `jvs_save_restore` | save, history, restore preview/run/discard | yes | no | yes | no |
| `webdav_export` | export create/get/revoke, gateway ledger | yes | no | yes | no |
| `operation_recovery` | operation inspect/recover/terminalize | yes | no | yes | no |
| `repo_lifecycle_retained` | archive, restore archived, tombstone, restore tombstoned | yes | no | yes | no |
| `repo_purge` | purge, purge recovery | no | yes | no when disabled | yes, only with verifiable approval fixture |
| `repo_template` | template create, same-namespace same-volume clone | no | yes | no when disabled | yes, only with repo-local fixture enabled |
| `workload_mount` | mount binding, plan, heartbeat/release/revoke | no | yes | no when disabled | yes, only with repo-local orchestrator fixture enabled |

有效决策表：

| supported | configured | ready | namespace/volume policy | default_ga_required | optional_gated | 有效决策 |
| --- | --- | --- | --- | --- | --- | --- |
| false | any | any | any | any | any | New admission denied; historical operations failed or intervention with unsupported code. |
| true | false | any | any | yes | no | Service not ready; new default-GA mutation denied with config error; historical recovery still scans. |
| true | false | any | any | no | yes | Service can be ready; new optional mutation denied; historical recovery scans and terminalizes/intervenes. |
| true | true | false | any | yes | no | Service not ready for default GA; new mutation denied or retryable per error table; recovery still scans. |
| true | true | false | any | no | yes | Optional capability not ready; service ready remains true; positive path cannot be marked ready. |
| true | true | true | deny | any | any | New admission denied by policy; no operation created except idempotent replay. |
| true | true | true | allow | yes | no | Default GA admission may proceed after fence/session/lifecycle checks. |
| true | true | true | allow | no | yes | Admission may proceed only in explicit fixture-enabled or deployment-enabled mode; default GA still proves disabled/denied path. |

### API Admission 顺序

所有 mutating API 必须按以下顺序处理：

1. 认证、授权和 namespace 上下文先行。
2. 幂等 replay 优先于 capability denial：同一 idempotency key/hash 命中既有 operation 时，先返回既有结果或 operation，不因当前 capability 变化改成 denied。
3. 只有新 mutation 才进入 capability/fence/session/approval/lease/lifecycle 检查。
4. 检查不通过时 fail-closed，返回稳定 error envelope，并写入必要 denied audit。
5. 检查通过后才创建 durable operation。

禁止行为：

- capability 关闭时创建新的永久 `queued` operation。
- worker 不扫描关闭 capability 的历史 operation。
- 用后台沉默、无限 retry 或未知 handler 来表达不可执行。

API admission denial 必须表格化，并由 schema/OpenAPI、contract tests 和 audit tests 共同保护：

| Denial reason | Error family | Create operation? | Audit required? | Retry semantics |
| --- | --- | --- | --- | --- |
| authn/authz/caller role denied | `AUTH_DENIED` / `FORBIDDEN` | no | denied audit when actor/resource resolved | no until credentials/role change |
| namespace disabled or namespace policy denies capability | `NAMESPACE_DISABLED` / `POLICY_DENIED` | no | yes | retry only after policy changes |
| capability unsupported | `CAPABILITY_UNSUPPORTED` | no | yes for mutating request | no for this version |
| capability disabled/unconfigured | `CAPABILITY_DENIED` / `CAPABILITY_NOT_CONFIGURED` | no | yes | retry only after config/capability changes |
| runtime dependency not ready | `CAPABILITY_NOT_READY` | no for new mutation | yes | retryable after readyz recovers |
| lifecycle state blocks mutation | `REPO_LIFECYCLE_CONFLICT` | no | yes | retry after lifecycle terminal state |
| active/uncertain session or fence blocks mutation | `SESSION_CONFLICT` / `FENCE_HELD` | no | yes | retry after revoke/drain/recovery or operator repair |
| purge approval missing/invalid/expired/replayed | `APPROVAL_REQUIRED` / `APPROVAL_INVALID` | no | yes | retry with valid structured approval evidence |
| idempotency key/hash conflict | `IDEMPOTENCY_CONFLICT` | no new operation | yes when conflict is security-relevant | no unless caller uses correct key/hash |
| idempotency replay hit | no denial | no new operation | no new denied audit | returns existing operation/result before capability checks |

### Worker Recovery 语义

worker recovery 必须覆盖历史 operation，不受“当前 capability 是否允许新 admission”的限制。

recovery dispatcher、classifier 和 terminalizer 不应随 `ready=false` 或
`configured=false` gate 被裁掉。所有已知 `operation_type` 的 classifier 和
terminalizer 必须能发现历史 operation；`ready/configured=false` 只影响新 admission
或真正执行外部 mutation 的路径，不能让 recovery 查询范围、分类范围或终态化路径消失。

历史 operation 的处理原则：

- 能安全执行且 handler/runtime ready：按原语义继续执行。
- 当前 capability 已关闭、handler 不支持或 runtime 缺失：终态化为 `failed`，或在需要 operator 判断时进入 `operator_intervention_required`。
- fence/session/lease/storage 一致性不确定：进入 `operator_intervention_required` 并保持 blocking。
- 每次终态化都必须 idempotent，并产生 audit/evidence。

`operator_intervention_required` 是运行态安全状态，不是 GA 人工审批状态。它表示系统无法自动证明继续执行安全，因此保持阻断并等待受控 operator repair。

Worker recovery/terminalization 必须表格化：

| Historical operation condition | Recovery action | Terminal state | Audit/evidence | Operator action needed |
| --- | --- | --- | --- | --- |
| handler/runtime ready and safety predicates true | continue or retry idempotently | normal success/failure per executor | operation progress + audit | no |
| operation type known but capability now disabled | do not execute external mutation | `failed` for safe no-op cases, otherwise `operator_intervention_required` | denied/unsupported recovery audit | only when state cannot be proven safe |
| operation type known but handler unsupported in this binary | no external mutation | `failed` if no side effects started; otherwise intervention | unsupported handler audit | maybe |
| runtime config missing before side effect | no external mutation | `failed` | config-denied recovery audit | no |
| side effect may have started and runtime state unknown | keep blocking | `operator_intervention_required` | intervention record + runbook ref | yes |
| lease expired before side effect and no external mutation possible | terminalize | `failed` | lease-expired audit | no |
| fence/session/storage consistency uncertain | keep fence/session blocking | `operator_intervention_required` | intervention record + evidence requirement | yes |
| audit outbox blocked after durable mutation | preserve durable state and retry audit/recovery | not success-visible until audit policy satisfied, or intervention if policy says so | audit lag evidence | maybe |
| purged repo referenced by old operation | do not resurrect or reissue access | `failed` or intervention if storage residual exists | purge invariant audit | maybe |

## 开发包局部验收规则

这些开发包是合并单元，不是 release 阶段，也不存在 package-level GA。每个包只能声明
“局部可合并”，不能声明“该包已 GA”。局部验收必须同时满足 DoD、依赖、可合并条件和
manifest 追踪要求。

| 开发包 | 依赖 | DoD | 可合并条件 | 局部验收边界 |
| --- | --- | --- | --- | --- |
| Capability & Operation Terminalization | 现有 operation store、capability tests、release manifest seed | capability matrix v1 rows、effective decision table、admission denial table、worker terminalization table 有 contract/schema/test 覆盖 | 默认 GA 与 optional-gated 的 admission/recovery/readyz 状态一致；disabled optional 不创建永久 queued；历史 operation 可发现和终态化/intervention | 只证明 capability/admission/recovery 语义可合并，不证明 access、purge、operator repair 全闭环 GA。 |
| Access Sessions Safety | package 1 的 capability/admission 语义 | WebDAV credential first-create-only、gateway policy、ledger e2e、workload lease freshness、SecretRef redaction/RBAC、session/fence 联动测试通过 | 默认 WebDAV 正向路径有真实 Postgres ledger + repo-local fixture；workload mount 默认 disabled/denied/recovery 安全，positive 只在 fixture-enabled 模式验收 | 不实现 caller connector、真实 orchestrator 或真实部署 mount。 |
| Operator Intervention | package 1 的 recovery/intervention 状态；package 2 的 session/fence 证据 | operator-only inspection/repair contract、allowlist、safety predicate、required evidence、audit/redaction/idempotency tests 完成 | repair API/CLI/tooling contract 明确 operator-only，不暴露给普通 caller；缺证据 repair fail-closed | 只证明最小运行态修复闭环，不做通用运维平台或 dashboard。 |
| Irreversible Lifecycle Safety | package 1-3 的 capability、session、operator repair | purge approval evidence、restore consistency、repo lifecycle session-drain decision、template/quota/lifecycle wording、shared-volume residual-risk gate 完成 | purge 默认 disabled；retained lifecycle 默认 enabled 且 contract/test 一致；deployment-only 风险只以 repo-local 模拟/检测/runbook/escalation 证明 | 不做 namespace delete、template marketplace、真实部署 CSI/POSIX 验证。 |
| GA Evidence Manifest | 前四包的 claim/evidence 条目 | 扩展现有 `docs/release-evidence/ga-manifest.json`，manifest verifier 接入唯一 gate，claim/evidence mapping 完整 | 每个最终验收 bullet 可追溯到 manifest `claim_id`、`acceptance_id` 或 `subclaim_id`、evidence `id`；高风险 claim 不能 doc-only | 只做 repo-local release gate，不依赖兄弟 repo、人工审批或外部 release dashboard。 |

## 开发包 1: Capability & Operation Terminalization

### 要解决的问题

- API、worker、readyz、release evidence 可能各自判断 capability，导致 admission 与 execution 不一致。
- 新 mutation 可能进入 operation 队列，但 worker 因 gate 关闭或 handler 不存在而永远不处理。
- 历史 operation 在 capability 变化后缺少统一终态规则。

### 方案

- 实现或收敛一份 capability matrix contract，并让 API、worker、readyz、operator inspection、evidence manifest 共用。
- admission 按本文的固定顺序执行，确保 idempotent replay 优先于 capability denial。
- 每类 operation 定义 terminalization policy：`succeeded`、`failed`、`operator_intervention_required`。
- worker recovery dispatcher/classifier/terminalizer 覆盖所有已知 operation type；扫描历史 operation 时，不以当前 `ready/configured` gate 作为跳过理由。
- unsupported handler、runtime unavailable、capability now disabled、lease expired、fence uncertain、audit outbox blocked 都必须有稳定分类、错误码和审计事件。
- readyz 区分 default GA required capability 与 optional gated capability；optional gated 关闭不能让基础服务误报 not ready。

### TDD/自动验收

- 先写 capability-off admission 测试：关闭 workload/template/purge 等 optional-gated 能力时，新 mutation 不创建永久 queued operation；默认 GA 的 pinned JVS 与 WebDAV gateway 不得被当作默认可选能力跳过。
- 先写 idempotency replay 测试：同 key/hash replay 返回既有 operation，不受当前 capability 状态影响。
- 先写 worker recovery 测试：历史 queued/running operation 在 capability 关闭或 handler 不支持时会终态化或进入 intervention。
- 先写 recovery discovery 测试：`ready/configured=false` 时，已知 operation type 仍会被 classifier/terminalizer 发现并处理。
- contract tests 覆盖 API、worker、readyz 暴露的 capability 状态一致。
- operation state machine tests 覆盖 unsupported、runtime unavailable、crash/retry、audit replay、lease lost、fence uncertain。
- 精确 race/concurrency tests 覆盖同 repo save/restore/template/lifecycle 的串行化；不扩大到无关包。

### 交付物

- capability matrix contract/schema。
- stable capability denial error 与 audit event catalog。
- operation terminalization contract。
- recovery dispatcher/classifier/terminalizer coverage tests。
- API admission 与 worker recovery 测试。
- readyz/operator/evidence 读取同一 capability matrix 的证据。

### 范围防蔓延

- 不把 capability matrix 做成通用 feature flag 平台。
- 不做 UI 配置界面。
- 不把 optional gated 能力关闭解释成服务整体不可用。
- 不新增业务项目或部署侧专有名称。

## 开发包 2: Access Sessions Safety

### 要解决的问题

- WebDAV credential issuer 口径需要统一，secret replay 和 gateway policy boundary 要有真实证据。
- workload mount plan 领取必须检查 lease freshness；expired/stale binding 不能继续发普通 plan。
- SecretRef 不能由普通 caller 可见数据推导，必须来自 operator/orchestrator-only runtime config。
- restore、lifecycle、purge 在 session 不确定时必须 fail-closed。

### 方案

WebDAV：

- AFSCP 向 trusted caller 签发短期 WebDAV credential；caller relay 给 client connector。
- client connector 不直接调用 AFSCP，caller 不自行生成 WebDAV password。
- raw credential 只在 first-create response 返回；idempotent replay 和 GET export 只能返回 redacted session。
- AFSCP 存 verifier，不存 raw password。
- gateway 是 policy boundary，必须拒绝 `.jvs`、control root、raw path、path traversal、Destination escape。
- revoke/expiry 后 future request 必须失败；runtime request ledger 不存 password、host path 或敏感路径材料。

Workload access：

- plan 领取必须检查 lease freshness。
- expired/stale binding 不能返回普通 plan；只能返回 blocking 结果或 teardown-only plan。
- stale binding 进入 operator-visible inspection/intervention，并保留 fence/session blocking。
- SecretRef 来自 operator/orchestrator-only runtime config，配置有 schema、RBAC 和 redaction。
- 普通 caller 不得看到 SecretRef、mount secret、host path 或底层 credential。

### TDD/自动验收

- WebDAV first-create-only credential 测试。
- idempotent replay 不返回 raw secret 测试。
- revoke/expiry deny 测试。
- gateway path policy 测试：`.jvs`、path traversal、Destination escape、control-root access 全部拒绝。
- WebDAV e2e 使用真实 Postgres ledger 和 repo-local gateway/runtime fixture，不能只靠 mock。
- workload expired lease 不返回普通 plan。
- stale/releasing binding 只能 blocking 或 teardown-only。
- SecretRef redaction/RBAC/schema 测试。
- active/uncertain export 和 workload session 阻止 restore-run、template writer、archive/delete/purge 的危险推进。

### 交付物

- WebDAV export credential contract wording 更新。
- WebDAV gateway + ledger e2e 证据。
- workload mount binding/plan freshness contract。
- runtime config schema、RBAC、redaction tests。
- session safety 与 writer/lifecycle fence 联动测试。

### 范围防蔓延

- 不实现调用方 client connector。
- 不把 WebDAV gateway 换成外部 stock gateway 作为 GA policy boundary。
- 不绑定具体编排平台。
- 不把 mount plan API 扩展成业务工作负载管理 API。

## 开发包 3: Operator Intervention

### 要解决的问题

- operator 需要先发现问题，再定位 operation/resource；单纯 operation by ID 不足以闭环。
- `operator_intervention_required` 如果没有受控 repair，就会变成长久卡死状态。
- repair 如果靠临时 SQL，会破坏身份、reason、before/after 和 audit 证据。

### 方案

最小 operator inspection surface：

- correlated operation lookup。
- intervention queue。
- held fence/session view。
- stale mount lease view。
- audit outbox lag。
- runtime recovery status。

所有 inspection surface 都必须有分页、过滤、权限和脱敏边界。operator 可以跨 namespace 定位运行态问题；namespace-scoped caller 只能看到自己授权范围内的 redacted projection。

最小受控 repair 写路径只允许：

- terminalize operation。
- release fence。
- revoke/terminalize session。
- residual-risk acceptance。

每类 repair 都必须定义 allowed transition 和 safety predicate。没有满足对应安全谓词时，repair 请求失败并保留 blocking；不能把任意 operator 权限解释为任意状态改写。

Operator repair 必须表格化为 operator-only API/CLI/tooling contract。它不是 caller API，
不是普通 admin update，也不是通用运维平台。

| Repair action | Allowlist scope | Safety predicate | Required evidence | Audit requirement |
| --- | --- | --- | --- | --- |
| terminalize operation | known operation in non-terminal or intervention state | no unaccounted external mutation, or mutation result is independently proven | operation ID, before state, executor/recovery evidence, reason, correlation ID | before/after state, actor, reason, evidence ref, stable event ID |
| release fence | held fence tied to known operation/session | all protected sessions terminal, or specific residual-risk contract authorizes unblock | fence ID, protected resource IDs, session terminal evidence, runbook/ref | release decision, affected IDs, expiry/scope if risk acceptance used |
| revoke/terminalize session | export/workload session in active/uncertain/stale state | credential expired/revoked or orchestrator/gateway terminal evidence proves no access | session ID, runtime ledger/heartbeat/revoke evidence, actor reason | terminal state, redacted runtime evidence, no raw secret/path |
| residual-risk acceptance | explicitly named residual risk only | cannot prove full safety, but contract says bounded acceptance may unblock a named condition | scope, expiry/review point, affected IDs, risk ID, evidence available, reason | immutable acceptance audit; must not auto-clear unrelated writer/credential/mount/restore/purge blocks |

禁止 repair 类型：

- 普通 caller 可调用的 repair。
- 任意 SQL、任意状态改写、任意 raw path 修复。
- 重发 WebDAV raw secret、生成底层 storage credential、复活 purged repo。
- 把 uncertain session 直接当作 terminal，除非有对应 terminal evidence 或 bounded residual-risk contract。

每个 repair 必须记录：

- operator identity。
- reason。
- evidence reference。
- scope、expiry 和 affected IDs；没有合理 expiry 的 acceptance 必须明确说明为什么。
- before/after state。
- correlation ID。
- audit event IDs。

release fence 只能在安全谓词满足时执行；或者由具体 repair contract 明确定义带 scope、expiry、affected IDs、evidence 和 audit 的 residual-risk unblocking。residual-risk acceptance 不能自动绕过 active/uncertain writer、credential、mount、restore 或 purge 阻断。

repair 后不得重发 raw secret、复活 purged repo、把 uncertain session 当 terminal，也不得把 metadata/storage 不一致静默修成 active。无法证明安全状态时默认保持 blocking。operator repair 是运行态修复机制，不是 GA 审批机制。

### TDD/自动验收

- 只有 operator/admin role 能访问 global inspection 和 repair。
- namespace-scoped caller 不能读取 global intervention queue、held fence/session 或 audit lag。
- 缺少 identity/reason/evidence/before state 的 repair 请求失败。
- 每类 repair 的 allowed transition/safety predicate 有 contract tests。
- release fence 前必须证明关联 operation/session/runtime/audit 状态安全，或命中具体 repair contract 定义的 residual-risk unblocking。
- residual-risk acceptance 不能让 active/uncertain writer、credential、mount、restore、purge 阻断自动放行。
- repair 后不重发 raw secret、不复活 purged repo、不把 uncertain session 当 terminal。
- repair idempotency 测试：重复提交同一 repair 不产生重复外部语义。
- 所有 repair 都产生审计事件，并通过 redaction guard。

### 交付物

- operator inspection contract。
- operator repair/intervention contract。
- allowed transition 与 safety predicate decision table。
- repair request/response schema 或 CLI/tooling contract。
- authorization、audit、redaction、idempotency tests。
- runbook 与 API/CLI 契约对齐。

### 范围防蔓延

- 不做通用搜索平台。
- 不做 UI dashboard。
- 不允许任意 SQL/任意状态改写作为产品契约。
- 不把 residual-risk acceptance 当作绕过安全检查的普通开关。

## 开发包 4: Irreversible Lifecycle Safety

### 要解决的问题

- purge/break-glass 是不可逆路径，approval reference 不能只是自由字符串。
- backup/restore 需要 control-plane 与 storage-plane 一致性边界。
- 恢复后未完成 reconciliation 前，不能发新 credential、mount plan、restore-run 或 purge。
- purged repo 不得被备份恢复复活。
- template/clone 默认模式只证明 stable denied/fail-closed/recovery；若 fixture-enabled/optional capability 显式启用，正向路径只允许 same-namespace same-volume primitive。
- quota 必须暴露机器可读 enforcement status。
- archive/delete/tombstone/purge 是 storage-state，不是业务 catalog lifecycle。
- restore archived/tombstoned 是否需要 `no_sessions` 必须做一等决策并测试。
- shared managed volume 的残余风险必须显式建模和验收，不能藏在普通 capability 文案里。

### 方案

Purge approval：

- break-glass 默认关闭。
- purge default profile 永远 disabled；只有 explicit optional/fixture-enabled/deployment-enabled profile 且结构化 approval evidence 可验证时，才允许 positive path。
- release manifest 中的 purge positive evidence 必须标成非 default，且 `default_mode=false`。
- 未启用上述显式 profile 或缺少可验证 approval capability 时 fail-closed。
- approval evidence 必须结构化，至少包含 approval issuer/verifier、approver、subject、audience、policy、version、scope、repo/action、reason、expires_at、hash/correlation、replay protection。
- audit 绑定 approval 摘要或 hash，不记录敏感审批材料。
- purge 前必须证明没有 active/uncertain export/workload access session。

Backup/restore：

- 定义 restore consistency contract，覆盖 control-plane snapshot timestamp、storage generation 或等价 marker、tombstone/purge marker、reconciliation mode。
- 恢复后进入 reconciliation mode。
- reconciliation 完成前禁止新 credential、mount plan、restore-run、purge。
- 不自动重发 WebDAV credential。
- metadata 与 storage 不一致进入 intervention。
- metadata 标记 purged 但 storage residual 存在时，禁止访问并进入 intervention；不能复活 repo。

Template/quota/lifecycle：

- template/clone 默认模式只证明 stable denied/fail-closed/recovery；若 fixture-enabled/optional capability 显式启用，正向路径只允许 same-namespace same-volume primitive。
- cross-namespace、cross-volume 默认稳定拒绝。
- 如果未来需要跨 namespace 发布，另行定义受控 admin import/publish；不混入本轮。
- quota schema/OpenAPI 暴露机器可读 enforcement status，例如 policy-only、not-enforced、runtime-enforced、effective_quota_bytes。
- lifecycle wording 统一为 storage-state：archive/delete/tombstone/purge 只表达存储可访问性、保留、恢复和清理状态。
- restore archived/tombstoned 的 `no_sessions` 行为必须决策：要么契约明确需要并定义 stable error/audit，要么实现放松到只对 archive/delete/purge drain。

Shared-volume residual risk：

- 同一 managed volume 内的 namespace 隔离依赖 AFSCP 生成并校验路径；caller 不得提供 raw path。
- path traversal、double-encoded traversal、symlink escape、`.jvs` access、cross-namespace resource mismatch 必须 fail-closed。
- 普通 caller、client connector 和 workload 不得看到 raw root path、metadata URL、SecretRef、host path 或底层 credential。
- backup/restore residual data、volume-level admin 误配置、POSIX/CSI/subPath 权限漂移必须进入 threat model、operator inspection 或 residual-risk acceptance 证据；本仓库 gate 只能证明检测模型、模拟 fixture、redaction、path policy、runbook/escalation，不能要求真实部署环境。
- 当 shared-volume 隔离证据不足、合规要求需要 volume 级隔离，或 operator 无法接受残余风险时，必须升级到 dedicated-volume deployment policy。
- residual-risk acceptance 必须记录 scope、expiry/review point、reason、evidence、affected IDs 和 audit；它不能自动解除 active/uncertain session、writer fence、restore 或 purge 阻断。

### TDD/自动验收

- purge approval 缺失、过期、scope 不匹配、policy/version 不匹配、hash 不匹配、replay 全部拒绝。
- purge success 后 purged repo 不可 restore、export、mount、save、template/clone。
- purged repo 在 backup/restore 后不得复活。
- 恢复后 reconciliation mode 阻止新 credential、mount plan、restore-run、purge。
- metadata active 但 storage 缺失，进入 intervention。
- metadata purged 但 storage residual 存在，进入 intervention 并禁止访问。
- restore archived/tombstoned 的 session drain 行为有 contract/API/worker/recovery 测试。
- cross-namespace/cross-volume clone 稳定拒绝。
- quota enforcement status 进入 schema/OpenAPI/generated fixture。
- lifecycle wording 由 doc guard 或 contractcheck 防回退到业务 catalog 语义。
- shared-volume 测试覆盖 path traversal、symlink escape、cross-namespace mismatch、raw path/SecretRef redaction、backup restore residual data simulation、POSIX/CSI/subPath drift detection model fixture、dedicated-volume escalation、runbook/escalation guard、residual-risk acceptance audit；不要求真实 CSI/POSIX 部署作为 repo-local gate。

### 交付物

- purge approval evidence contract。
- restore consistency/reconciliation contract。
- repo lifecycle/session drain decision record。
- template/clone contract 更新。
- quota enforcement schema/OpenAPI 更新。
- backup/restore simulation 或 integration fixture。
- purge、restore、template、quota、lifecycle 的自动化证据。
- shared-volume residual risk threat model、dedicated-volume escalation rule、acceptance audit contract。

### 范围防蔓延

- 不做 namespace delete。
- 不做 template marketplace。
- 不做业务 catalog lifecycle。
- 不把 break-glass 开成默认能力。
- 不用人工审批记录代替可校验 approval evidence。

## 开发包 5: GA Evidence Manifest

### 要解决的问题

- 当前 `auto_verified` 颗粒度太粗，容易把 unit/text/contract baseline 误读为完整生产证据。
- 高风险项不能只有 doc guard。
- JVS provenance、真实 Postgres、WebDAV ledger e2e、generated-client、race/concurrency 等需要进入唯一 GA gate。
- 已有 `docs/release-evidence/ga-manifest.json` 是 seed/baseline，不能把 manifest 当作空白新建物并覆盖现实。

### 方案

- 保留唯一 authoritative gate：

```bash
bash scripts/verify-ga-release.sh
```

- 扩展现有 `docs/release-evidence/ga-manifest.json`，把 seed/baseline 提升为 machine-readable evidence manifest，映射：
  - GA claim。
  - 风险项。
  - capability ID。
  - evidence type。
  - 覆盖命令。
  - repo-local fixture 或 generated artifact。
  - evidence command 的 expected runtime 与 scope。
  - pass/fail 判定。
- 补齐或扩展 manifest verifier，并由 `scripts/verify-ga-release.sh` 直接或间接调用。
- manifest schema 至少扩展字段：
  - `claim_id`: 稳定 GA 声明 ID。
  - `acceptance_id` 或 `subclaim_id`: 稳定子声明 ID，用于把最终验收 bullet 拆成可追踪、可聚合的子声明。
  - `risk_id`: 对应 risk register 或本计划风险 ID。
  - `fixture_id`: 使用的 repo-local fixture ID；无 fixture 时显式为空。
  - `expected_runtime`: 证据命令预期耗时分级，例如 fast、integration、e2e、race。
  - `scope`: unit、package、service、repo-local e2e、doc guard 等覆盖范围。
  - `negative_or_positive`: negative、positive 或 both。
  - `default_mode`: 证据是否覆盖默认 GA 模式。
  - `fixture_enabled_mode`: 证据是否只在 repo-local fixture capability 启用时成立。
  - `pass_criteria`: verifier-checkable structured criteria；不能只是自由文本或“文字可检查”说明。
  - `anchors`: 源码、schema、contract、runbook 或生成物锚点。
  - `command`: repo-local 可执行命令；不能要求人工 DSN、兄弟 repo 或真实部署状态。
- evidence type 至少支持：unit、contract、schema、openapi、generated-client、integration、e2e、provenance、race、doc-guard。
- 高风险项必须有非 doc-only evidence。
- Postgres integration gate 在 clean checkout 下必须能自启动临时 Postgres，或使用 repo-local 可复现 fixture；CI service 只是 CI 中的等价自动 provisioning，不能要求人工 DSN、预配置外部 DB 或部署侧状态。
- WebDAV GA evidence 必须使用真实 Postgres ledger 和 repo-local gateway/runtime fixture。
- JVS pinned binary provenance 和最小 smoke 自动验证；如果上游缺少某类 signature/bundle，manifest 必须记录可自动验证的替代证据，不能只写说明。
- product-neutral conformance/smoke 必须区分默认能力与 optional-gated 正向验证：默认模式验证 workload/template/purge 关闭时 stable denied/fail-closed/recovery；只有启用 repo-local fixture capability 后，才验证 mount plan、template/clone、purge 的正向路径；purge positive evidence 必须来自 explicit optional/fixture-enabled/deployment-enabled profile、`default_mode=false` 且结构化 approval evidence 可验证。这不改变默认 GA 边界，也不依赖真实外部 orchestrator 或兄弟 repo。
- product-neutral happy/failure journeys 作为验收索引，覆盖默认 create/get/projection/list、save/history、restore-preview/run/discard、WebDAV export/gateway/revoke、operation/audit/recovery，以及 optional-gated denied 和 fixture-enabled positive paths。
- workflow YAML 检查只验证 repo-local 可检查事实：是否调用唯一脚本、最小权限、artifact/tag trigger 配置声明。branch protection 和真实 artifact 存在不能作为本地 gate 通过条件。

Claim/evidence mapping 必须在 manifest 或相邻 generated report 中可读：

| claim_id | Claim | Required evidence shape | Manifest requirement |
| --- | --- | --- | --- |
| `CLAIM_DEFAULT_USER_LOOP` | 默认 GA trusted caller 闭环可用 | create/get/projection/list + save/history/restore + WebDAV + operation/audit/recovery repo-local evidence | 每个子能力至少一个 `default_mode=true` positive evidence。 |
| `CLAIM_RETAINED_LIFECYCLE_DEFAULT` | retained repo lifecycle 属于默认 GA storage-state 正向能力 | archive/restore_archived/delete/tombstone/restore_tombstoned repo-local positive tests | 每个 action 至少一个 `default_mode=true` positive evidence；purge 不得归入该 claim。 |
| `CLAIM_OPTIONAL_DENIED_SAFE` | workload/template/purge 默认关闭时安全 | disabled/denied/recovery negative tests | `negative_or_positive=negative`、`default_mode=true`、不得创建永久 queued。 |
| `CLAIM_OPTIONAL_FIXTURE_READY` | optional positive path 只在 fixture-enabled 模式 ready | repo-local fixture positive tests | `fixture_enabled_mode=true`，且不能把默认模式标成 ready；purge positive evidence 必须 `default_mode=false` 且绑定结构化 approval evidence。 |
| `CLAIM_OPERATOR_REPAIR_SAFE` | operator repair 有 allowlist、safety predicate、evidence、audit | contract + auth + audit/redaction/idempotency tests | 每个 repair action 有 claim/evidence pair。 |
| `CLAIM_DEPLOYMENT_RISK_ENVELOPE` | deployment-only 风险有 repo-local检测/模拟/脱敏/升级证据 | model fixture + doc/runbook guard | 不允许要求真实 CSI/POSIX/subPath 部署 gate。 |
| `CLAIM_RELEASE_GATE_TRACEABLE` | 最终验收 bullet 可追溯 | manifest verifier + generated mapping | 每个最终验收 bullet 都引用 `claim_id`、`acceptance_id` 或 `subclaim_id`、evidence `id`。 |

### TDD/自动验收

- manifest schema validation。
- manifest verifier negative tests：缺少 required capability evidence、doc-only high-risk evidence、命令不存在、evidence type 不合法时失败。
- `scripts/verify-ga-release.sh` 覆盖 manifest verifier。
- schema/OpenAPI drift guard。
- repo-local generated-client fixture 编译。
- precise race/concurrency gate。
- Postgres migration/transaction/idempotency/lease/fence/audit outbox integration。
- product-neutral conformance/smoke 在默认模式覆盖 credential relay、operation inspection、workload/template/purge stable denied/fail-closed；在 repo-local fixture capability 启用后覆盖 orchestrator mount plan consumption、template/clone、purge 正向路径，不引入业务项目名。
- happy/failure journey index 能映射到 manifest evidence ID，防止大而泛测试。

### 交付物

- 扩展后的 `docs/release-evidence/ga-manifest.json` 和 schema。
- evidence manifest verifier。
- 更新后的 `scripts/verify-ga-release.sh` 子 gate 接入。
- release gate 文档与 readiness evidence ledger 更新。
- CI workflow hardening 检查。
- product-neutral happy/failure journeys 交付物与验收索引。

### 范围防蔓延

- 不做外部 release dashboard。
- 不依赖兄弟 repo 或外部业务 e2e。
- 不把 generated-client 扩成多语言兼容矩阵。
- 不把 branch protection、人工 artifact 检查或 GitHub 环境配置当成本地 gate。

## 开发团队接手顺序

这不是阶段路线图；它只是为了减少返工的工程接手顺序。

1. 先落 capability matrix 与 operation terminalization contract。
   这一步决定 admission、worker recovery、readyz、operator 和 evidence 的共同语言。

2. 然后补 access session safety。
   WebDAV、workload、restore、template、lifecycle 都依赖 session/fence/lease 语义；先把 credential、ledger、lease freshness、SecretRef redaction 收紧。

3. 接着补 operator intervention。
   当 recovery 无法证明安全时，需要有受控的 inspection 和 repair 写路径，否则 `operator_intervention_required` 只会变成死状态。

4. 再收 irreversible lifecycle safety。
   purge、backup/restore、template clone、quota、lifecycle wording 都会碰到不可逆或调用方误解风险，必须在 capability 和 session 安全之后收口。

5. 最后把所有证据接入 GA evidence manifest。
   每个开发包完成时都应同步补 evidence 条目；最后一步只做统一 verifier、gate 接入和缺口清零。

不存在 package-level GA。5 个开发包全部完成、全部证据进入唯一 gate，并且 `bash scripts/verify-ga-release.sh` 从干净 checkout 成功退出后，才能进入 GA 判定。

每一步都按同一方式推进：

1. 先改 contract/schema/OpenAPI/test fixture，让当前实现失败。
2. 再做最小产品中立实现。
3. 补 stable error、audit、redaction、idempotency、runbook/evidence。
4. 接入 `scripts/verify-ga-release.sh` 覆盖的 repo-local gate。

## Deployment readiness envelope

默认 GA 的 deployment readiness 只表达最小可运行配置和 readyz 语义，不把真实业务部署、
兄弟 repo、人工审批或主观 review 纳入 release gate。

最小可运行配置必须明确：

- service auth 与 trusted caller/operator/orchestrator role 配置存在且可校验。
- PostgreSQL metadata store/migration 可用，或 repo-local integration fixture 可自动启动等价依赖。
- managed volume policy 和 path resolver root 配置存在，但 raw root path 不暴露给 caller。
- pinned JVS runner 配置、provenance/smoke evidence 可由 gate 验证。
- AFSCP WebDAV gateway 作为默认 export policy boundary；不是 stock gateway 代替品。
- audit outbox HTTP JSON GA sink 或 repo-local sink fixture 配置可用，且 redaction guard 生效。
- optional-gated workload/template/purge 默认关闭；fixture-enabled mode 必须显式打开并产生单独 evidence。

readyz 必须表达：

| readyz dimension | Meaning | Default GA impact |
| --- | --- | --- |
| `service_ready` | 默认 GA 必需依赖可接收和安全处理请求 | false 时不能声明默认 GA ready。 |
| `default_capabilities_ready` | namespace/repo/JVS/WebDAV/lifecycle retained/recovery/audit 等默认能力 ready | 任一 required capability false，service-ready 必须反映。 |
| `optional_capabilities` | workload/template/purge 等 optional-gated 当前 disabled/denied/fixture-enabled 状态 | disabled 不使 service not ready；fixture-enabled positive path 必须单独显示。 |
| `recovery_ready` | classifier/terminalizer 可以发现历史 operation 并推进 failed/intervention | 不得因 optional capability disabled 从 recovery discovery 消失。 |
| `evidence_profile` | 当前进程/测试运行使用 default mode 还是 fixture-enabled mode | 用于防止 positive fixture 被误读成默认开启。 |

## 最终验收命令

从干净 checkout 运行：

```bash
bash scripts/verify-ga-release.sh
```

该命令必须自动证明：

- 默认 GA capability 都有 repo-local evidence，并可追溯到 manifest `claim_id`/evidence `id`。
- pinned JVS save/history/restore-preview/run/discard 和 WebDAV export/gateway/revoke 被自动证明为默认 GA 能力，不被 optional gate 跳过，并映射到 `CLAIM_DEFAULT_USER_LOOP`。
- retained repo lifecycle 的 archive、restore_archived、delete/tombstone、restore_tombstoned 有 repo-local positive evidence，并映射到 `CLAIM_RETAINED_LIFECYCLE_DEFAULT`；purge 不包含在 retained lifecycle 默认能力内。
- namespace-scoped repo projection/list 有分页、过滤和权限边界，不是 global search/aggregation/operator investigation 平台。
- optional gated capability 关闭时，新 mutation fail-closed，不创建永久 queued operation，并映射到 `CLAIM_OPTIONAL_DENIED_SAFE`。
- product-neutral conformance/smoke 在默认模式证明 workload/template/purge stable denied/fail-closed/recovery；启用 repo-local fixture capability 后才证明 mount plan/template/purge 正向路径，其中 template/clone 正向路径只允许 same-namespace same-volume primitive，purge positive evidence 必须来自 explicit optional/fixture-enabled/deployment-enabled profile、`default_mode=false` 且结构化 approval evidence 可验证，并映射到 `CLAIM_OPTIONAL_FIXTURE_READY`。
- idempotent replay 优先于 capability denial。
- 历史 operation 即使 capability 关闭，也会被 worker recovery 扫描并终态化或进入 `operator_intervention_required`；dispatcher/classifier/terminalizer 不因 `ready/configured=false` 从查询或分类范围消失。
- WebDAV credential first-create-only、revoke/expiry、gateway policy、redaction、ledger recovery 有真实 Postgres ledger 与 repo-local e2e 证据。
- workload plan 领取检查 lease freshness，expired/stale 只能 blocking 或 teardown-only。
- operator inspection/repair 覆盖 correlated lookup、intervention queue、held fence/session、stale lease、audit lag、runtime recovery status，并有分页/过滤/脱敏、allowed transition、safety predicate、identity/reason/evidence/before-after/audit，映射到 `CLAIM_OPERATOR_REPAIR_SAFE`。
- residual-risk acceptance 不能自动绕过 active/uncertain writer、credential、mount、restore、purge 阻断；repair 后不重发 raw secret、不复活 purged repo、不把 uncertain session 当 terminal。
- purge approval evidence 结构化、可校验、防重放，包含 issuer/verifier、subject、audience，并与 audit 绑定。
- backup/restore 后 reconciliation mode 阻止危险新动作，purged repo 不复活，metadata/storage 不一致进入 intervention。
- shared-volume residual risk 覆盖 path traversal、symlink escape、cross-namespace mismatch、raw path/SecretRef redaction、backup restore residual data simulation、POSIX/CSI/subPath drift detection model fixture、dedicated-volume escalation、runbook/escalation、residual-risk acceptance audit，并映射到 `CLAIM_DEPLOYMENT_RISK_ENVELOPE`。
- template/clone 默认模式只证明 stable denied/fail-closed/recovery；若 fixture-enabled/optional capability 显式启用，正向路径只允许 same-namespace same-volume primitive。
- quota enforcement status 机器可读。
- lifecycle vocabulary 保持 storage-state，不漂移成业务 catalog lifecycle。
- schema/OpenAPI/generated fixture 不漂移，product-neutral happy/failure journey index 能映射到 evidence manifest。
- JVS provenance/smoke、Postgres integration、WebDAV e2e、race/concurrency、doc guard 都由唯一 gate 覆盖；Postgres gate 在本地可自启动临时 Postgres 或使用 repo-local 可复现 fixture，不要求人工 DSN 或预配置外部 DB。
- generated claim/evidence report 证明每个最终验收 bullet 都可追溯到 manifest `claim_id`、`acceptance_id` 或 `subclaim_id`、evidence `id`，并映射到 `CLAIM_RELEASE_GATE_TRACEABLE`。

只有这条命令从干净 checkout 成功退出，才能认为下一阶段开发交付满足 GA 收敛要求。
