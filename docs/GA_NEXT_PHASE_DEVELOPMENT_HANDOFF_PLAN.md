# AFSCP 下一阶段开发交接计划

Status: development handoff plan for direct GA convergence.

本文档交给下一轮开发团队使用。它基于
`docs/research/afscp-product-architecture-review.md` 的问题清单，以及现有
`docs/GA_CONVERGENCE_WORK_PLAN.md`、`docs/DEVELOPER_HANDOFF.md`、
`docs/GA_RELEASE_GATES.md`、`docs/READINESS_EVIDENCE.md` 和
`scripts/verify-ga-release.sh` 的当前口径。

目标不是再做一张复杂路线图，而是把下一阶段工作压成可以直接开工、直接验收的
开发包。开发团队应围绕这些包收敛到 GA，不扩大产品面，不引入外部业务项目，
不把主观 review 或会议当作 gate。

## Current status / Handoff boundary

本文档是下一阶段开发交接计划，不是当前 GA release evidence。当前
`docs/release-evidence/ga-manifest.json` 与 `scripts/verify-ga-release.sh` 仍是
seed/baseline gate；当前 gate 通过只说明 baseline 约束通过，不表示最终 GA 或 release
验收已满足。Package 0/5 中提到的 `seed_gap_*_open` final mode、workflow hardening、
manifest final fields 都是后续开发必须实现、补证并接入唯一 gate 的工作项。

## Product Boundary / Ownership

AFSCP 是共享文件系统控制面，不是业务产品、client connector、编排平台或部署平台。
后续开发必须按下表切边界；contract、release note、runbook 和证据 manifest 都不能把
别人的责任写成 AFSCP 默认 GA 能力。

| Owner | 负责什么 | 不负责什么 |
| --- | --- | --- |
| AFSCP | namespace/managed volume binding、repo storage-state、pinned JVS save/restore、WebDAV export gateway、operation/audit/recovery、capability/admission/worker/readyz/evidence 一致性。 | 业务 catalog、产品生命周期、业务审批流、client UI、外部 orchestrator 实现、真实部署权限配置。 |
| trusted caller | 持有调用方身份和 namespace 授权；调用 AFSCP API；把 first-create WebDAV credential relay 给 client connector；维护业务侧用户、租户和 catalog 映射。 | 自行签发 AFSCP WebDAV password、看到 raw root path/SecretRef、绕过 namespace policy、把业务 lifecycle 直接塞进 AFSCP。 |
| client connector | 接收 trusted caller relay 的短期 WebDAV credential；访问 AFSCP gateway；处理 credential 过期/revoke 后的用户侧体验。 | 直接调用 AFSCP admin/caller API、拿底层 storage credential、读取 `.jvs` 或 control root、生成或重放 raw password。 |
| orchestrator | 在被授权的 orchestrator role 下消费 workload fixture/runtime plan；执行 heartbeat、release、revoke 和 terminal evidence。 | 普通 caller 可见的 mount plan、SecretRef/raw path 泄露、业务工作负载管理平台、默认 GA 正向能力。 |
| operator/admin | 注册和预检 volume；配置 namespace binding policy、role/policy readiness、path redaction；查看 intervention queue/held fence/session/audit lag；按 allowlist 做 repair 并留审计。 | 人工批准 GA、任意 SQL 修复、任意状态改写、重发 raw secret、复活 purged repo。 |
| deployment/runtime | 提供 PostgreSQL、managed volume、JVS runner、WebDAV runtime、audit sink、orchestrator runtime、CI service 等运行依赖和最小权限配置。 | 作为 repo-local GA gate 的前置条件；用真实 CSI/POSIX/subPath/branch protection/GitHub 环境状态替代本仓库自动证据。 |

## PO brief / Product Slice

下一阶段只交付一个产品切片：本仓库可独立发布的共享文件系统控制面 GA。默认首发闭环
面向 trusted caller，先完成 admin preflight，再完成正向用户路径；高风险或部署相关能力
默认关闭，只证明拒绝、恢复和证据边界安全。

首发默认正向闭环：

```text
admin/operator
  -> volume register/health/preflight
  -> namespace-volume binding policy
  -> trusted caller role/policy readiness
  -> managed volume/path resolver redaction

trusted caller
  -> repo create/get/projection/list
  -> pinned JVS save/history/restore-preview/restore-run/discard
  -> WebDAV export/gateway/revoke
  -> operation status + audit + recovery evidence
```

默认安全负路径：

- workload mount、template/clone、purge/break-glass purge 默认 disabled/denied/fail-closed。
- 关闭、未配置、未 ready 或 policy deny 时，新 mutation 不创建永久 `queued` operation。
- 历史 operation 仍被 recovery 发现，并按 side-effect 边界进入 `failed` 或
  `operator_intervention_required`。

optional fixture 正向路径：

- 只能通过 repo-local fixture capability 显式启用。
- 只能作为 `repo-local-fixture-enabled` profile 的 conformance evidence；只有 release
  明确声明某 optional capability fixture-conformant 时才 blocking。
- `deployment-runtime-support` 只声明运行态支持 envelope；不能作为 required local GA evidence。

当前 `docs/release-evidence/ga-manifest.json` 只是 baseline/seed，不代表最终 GA claim
coverage。WebDAV/JVS/default user loop 必须补齐 positive evidence 后，才能满足最终验收。

## Scope alignment / source of truth

本计划是下一阶段开发交接的执行口径，专门用来化解当前
`docs/PRODUCT_REQUIREMENTS.md`、`docs/ARCHITECTURE.md` 与既有 GA 计划之间的
默认边界冲突。PRD/Architecture 中已经描述的能力不等于默认 GA 全部启用；下一阶段
开发应按本文的默认 GA 闭环与 optional-gated 规则实现、验收和补证。

后续必须交付 doc-sync，但 doc-sync 是开发包完成后的对齐工作，不应在本轮先大范围
改 PRD/Architecture/contracts/runbooks/README。以下三档能力口径是强约束，release note、
doc-sync、contract wording、runbook 和 evidence manifest 必须全部遵守：

- default GA：必须在无外部业务项目、无真实部署环境依赖的 repo-local gate 中证明；
  release note 只能把默认 GA 正向能力写成默认可用。
- `repo-local-fixture-enabled` optional：代码和契约可以保留，但默认关闭；默认 GA 只证明
  disabled、denied、recovery 安全。只有 repo-local fixture capability 显式启用且
  evidence 完整时，才能把该 optional capability 标记为 fixture-conformant。
- `deployment-runtime-support`：只能要求本仓库证明检测模型、模拟 fixture、redaction、
  path policy、runbook/escalation；不能把真实 CSI/POSIX/subPath 部署状态作为本仓库
  release gate，也不能写进 required local GA evidence。

Doc-sync exit criterion 是清理 wording，不是在本任务修改这些文件。PRD/Architecture/
contracts/runbooks/README 中出现以下说法时，后续 doc-sync 不得判定完成：

Doc-sync 时机固定：每个开发包合并时，必须同步更新该包直接触达的
contracts/schema/OpenAPI/runbook 和 evidence manifest；PRD、Architecture、README
这类全局 wording 的系统性清理在 Package 5 doc-sync 收口，不能前置成大范围文档重写。

| 文档区域 | 必清理 wording |
| --- | --- |
| workload/template/purge | 不得把 workload/template/purge 写成默认正向可用；只能写默认 denied/recovery，fixture-enabled 才有正向验收。 |
| caller credential issuer | 不得把 caller 写成 WebDAV credential issuer；AFSCP 签发 first-create 短期 credential，caller 只 relay 给 client connector。 |
| lifecycle/catalog | 不得把 archive/delete/tombstone/restore/purge 写成业务 catalog workflow；它们只表达 storage-state mutation 和访问性/保留/清理状态。 |
| quota enforcement | 不得暗示 quota 是默认硬 enforcement；schema/OpenAPI 必须暴露机器可读 enforcement status。 |
| purge/default lifecycle | purge 与 break-glass purge 是 optional irreversible capability；retained lifecycle 才是默认 storage-state 正向能力。 |

## 目标

下一阶段只做一件事：把 AFSCP 收敛成产品中立、可独立发布、可自动证明的共享文件系统控制面 GA 闭环。

完成后必须满足：

- 默认 GA 能力边界固定、可证明、可自动验收。
- 默认 GA 用户闭环前置：admin preflight 完成后，trusted caller 可以创建/查看 namespace-scoped repo，执行
  save/history/restore-preview/restore-run/discard，通过 WebDAV export/gateway/revoke
  访问 payload，并能用 operation/audit/recovery 追踪结果。
- 高风险能力可以保留在代码和契约中，但默认必须 capability-gated。
- API admission、worker execution/recovery、readyz、operator inspection、release evidence 使用同一份 capability matrix。
- 新 mutation 不会在不可执行时创建永久 `queued` operation。
- 历史 operation 即使当前 capability 关闭，也会被 recovery 扫描并进入明确终态。
- operator 有最小发现、定位、干预和审计闭环，不需要把临时 SQL 当作主要修复机制。
- 每个高风险 GA 声明都有 repo-local 自动化证据，并被唯一 gate 覆盖；`deployment-runtime-support`
  支持声明不能替代 required local GA evidence：

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

## Actor Journeys

这些 journeys 是 capability matrix 和 manifest claim 的阅读入口。后续测试可以拆散实现，
但 release gate 必须能把证据重新聚合回这些路径。

| Actor journey | 默认/fixture | 验收要点 | Manifest trace |
| --- | --- | --- | --- |
| Day-0 Admin Bootstrap | 默认前置 | operator/admin 完成 volume register/health/preflight；namespace binding policy 机器可验证；trusted caller 与 operator/admin role/policy readiness 可验证；optional orchestrator capability 在 `default` profile 下只证明 disabled discovery/deny contract，不要求 orchestrator role/policy readiness；path resolver/redaction 不暴露 raw root path、metadata URL、SecretRef、host path、底层 credential 或 `.jvs`；生成 admin bootstrap trace 并进入 manifest。 | `CLAIM_ADMIN_BOOTSTRAP_READY` |
| trusted caller happy path | 默认正向 | admin preflight 已通过；caller 在授权 namespace 内 create/get/projection/list repo；完成 JVS save/history/restore-preview/run/discard；创建 WebDAV export，gateway 访问成功，revoke 后失效；operation/audit/recovery 可追踪。 | `CLAIM_ADMIN_BOOTSTRAP_READY`、`CLAIM_DEFAULT_USER_LOOP` |
| trusted caller failure path | 默认负向 | 越权 namespace、policy deny、capability disabled、lifecycle/session/fence 冲突、WebDAV revoked/expired/path escape 都 fail-closed；无新永久 queued operation；有稳定 error/audit。 | `CLAIM_DEFAULT_DENIAL_SAFE`、`CLAIM_OPTIONAL_DENIED_SAFE` |
| operator discovery -> repair path | 默认运行态安全 | operator 发现 intervention queue、held fence/session、stale lease、audit lag、runtime recovery status；只能用 allowlist repair；repair 需要 repo-local fixture/object evidence、safety predicate 和审计。 | `CLAIM_OPERATOR_REPAIR_SAFE` |
| orchestrator default-disabled discovery | 默认负向 | orchestrator 或非授权 actor 只能看到 workload mount capability disabled/denied/status；不能拿到 mount plan、SecretRef、raw path 或底层 credential。 | `CLAIM_OPTIONAL_DENIED_SAFE` |
| orchestrator fixture-enabled path | repo-local fixture 正向 | 显式 `repo-local-fixture-enabled` profile 下验证 plan fetch、heartbeat、release、revoke、terminal evidence 五个子声明；证据不能标成 `default_mode=true`。 | `CLAIM_WORKLOAD_FIXTURE_READY` |

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
| `restore_archived` | 默认 GA storage-state mutation，恢复访问性 | enabled only when lifecycle capability ready and absence of active/uncertain access/fence is proven | 可执行；无法证明无 active/uncertain session/fence 时 fail-closed 或进入 `operator_intervention_required` | 不改变 repo identity；为避免恢复期间重新授权不一致，默认 GA 与 archive/delete 同样受 session/fence blocking 约束。 |
| `delete` / `tombstone` | 默认 GA storage-state | enabled when lifecycle capability ready | 可执行；必须 drain/revoke 或 fail-closed/intervention | 表达 retained tombstone/trash，不是产品删除 UX。 |
| `restore_tombstoned` | 默认 GA storage-state mutation，恢复访问性 | enabled within retention/policy only when absence of active/uncertain access/fence is proven | 可执行；无法证明无 active/uncertain session/fence 时 fail-closed 或进入 `operator_intervention_required` | 不得恢复已 purge 的 repo；默认 GA 下同样受 session/fence blocking 约束。 |
| `purge` | optional-gated irreversible capability | default profile 永远 disabled；只有 `repo-local-fixture-enabled` profile 且结构化 approval evidence 可验证时，显式 fixture conformance gate 才允许执行 | 历史 purge operation 必须被扫描并 failed/intervention；不能遗留 queued | 默认 GA 只证明 disabled/denied/recovery 安全；`deployment-runtime-support` 只可声明运行态支持，不得放入 required local GA 证据集合。 |
| break-glass purge override | optional-gated break-glass | disabled by default；positive path 只能来自 `repo-local-fixture-enabled` profile，且不能作为 default GA 阻断条件 | 只有结构化 approval evidence 可验证时才允许执行 | 不是人工 GA 审批，也不是自由字符串；`deployment-runtime-support` 不计入 required local GA evidence。 |

`restore_archived` 与 `restore_tombstoned` 的产品决策已固定：它们是恢复 repo 访问性的
storage-state mutation，不是业务 catalog 恢复工作流。为了避免恢复期间重新授权不一致，
contract、API admission、worker executor、recovery classifier、operator evidence 和 manifest
必须同步执行同一约束：不能证明没有 active/uncertain export/workload session、writer fence
或访问性不一致时，新请求 fail-closed；历史 operation 则按 side-effect boundary 进入
`failed` 或 `operator_intervention_required`，不能静默恢复为 active。

## Admin / Bootstrap Acceptance

默认用户闭环的前置条件必须自动验收，不能靠 operator 口头确认。

| Acceptance | 必证内容 |
| --- | --- |
| volume register/health/preflight | managed volume 已注册、health 可读、preflight 能验证 root policy、path resolver、metadata store、audit sink、JVS/WebDAV 必需配置。 |
| namespace-volume binding policy | namespace 只能绑定允许的 managed volume；cross-namespace/cross-volume mismatch fail-closed；policy deny 有稳定 error/audit。 |
| caller/operator role/policy readiness | trusted caller 与 operator/admin 角色与权限可机器校验；caller 只能访问 namespace-scoped projection。optional orchestrator role/policy readiness 不属于默认 GA bootstrap，只能在 `repo-local-fixture-enabled` 或 `deployment-runtime-support` profile 中验证。 |
| managed volume/path resolver redaction | caller、client connector、workload 不暴露 raw root path、metadata URL、SecretRef、host path、底层 credential 或 `.jvs` 路径。 |
| manifest/admin bootstrap trace | 上述每项都有 `CLAIM_ADMIN_BOOTSTRAP_READY` 下的 `acceptance_id`、evidence command、anchors 和 pass criteria；缺任一项不能进入 default user loop 验收。 |

## Capability Discovery Surface

capability discovery 分三类 contract，不能只靠 readyz 替代：

| Surface | Reader | Scope | 必须表达 |
| --- | --- | --- | --- |
| caller capability/status | trusted caller | namespace-scoped | 默认能力可用性、policy denial、repo lifecycle/session/fence blocking、稳定 error code；不得泄露全局 runtime 或 raw path。 |
| orchestrator mount capability/plan readiness | orchestrator role | namespace/workload binding scoped | 默认 disabled discovery、fixture-enabled readiness、plan 是否可领取、heartbeat/release/revoke 状态；不得给普通 caller plan/SecretRef。 |
| operator global capability/runtime/recovery/evidence profile | operator | global with filters | capability profile、runtime dependency、recovery discovery、intervention queue、evidence profile、runbook/ref 和 redacted runtime details。 |
| readyz | platform/operator automation | service | 只表达 service-ready、default capability ready、optional disabled/fixture status、recovery ready；不能替代 caller/orchestrator API contract。 |

## Capability Profile / Evidence Profile

| Profile | Runtime meaning | Local release evidence meaning | 可计入 required local GA gate 的正向路径 |
| --- | --- | --- | --- |
| default | 默认 GA 闭环开启；optional workload/template/purge disabled。 | 必须证明 admin bootstrap、trusted caller 正向闭环、optional denied/recovery、安全负路径。 | 仅默认 GA 能力正向路径。 |
| repo-local-fixture-enabled | 在本仓库 fixture 中显式打开 optional capability。 | 用于证明 optional 正向路径的契约完整性/安全性，不是默认产品可用性声明；默认产品可用性仍只包含默认 GA 正向能力。 | 只有 release 明确声明该 optional capability fixture-conformant 时才 blocking；必须 `fixture_enabled_mode=true`、`default_mode=false`。 |
| deployment-runtime-support | 真实部署可选择启用的运行态支持声明。 | 只能证明检测模型、模拟 fixture、redaction、path policy、runbook/escalation。 | 不得放入 required local GA 证据集合。 |

这三档不能混用：

- release note 只能把 `default` profile 的正向能力写成默认可用。
- `repo-local-fixture-enabled` 正向证据必须同时标记 `fixture_enabled_mode=true` 和
  `default_mode=false`，不能让 optional positive 反向证明默认 GA。
- `deployment-runtime-support` 只能进入运行态支持说明、runbook/escalation 或
  deployment envelope evidence；不能作为 required/final GA claim 的通过依据。
- doc-sync、contract wording 和 README 必须使用同一套 profile 词汇；任何 wording 漂移都要由
  doc guard 或 manifest verifier 报出。

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

- `surface_type`: capability 暴露面类型，至少区分 `durable_operation`、
  `read_projection`、`preflight`、`discovery`、`redaction`、`runtime_support`。
- `operation_type`: 仅 `surface_type=durable_operation` 的 mutating durable operation
  必填，用于 worker execution/recovery/terminalization。
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

read/status/preflight/discovery/redaction surface 不创建 durable operation，也不走 worker
terminalization policy；它们只参与 admission、discovery、readyz 和 evidence。只有会改变
持久状态的 mutating durable operation 才有 `operation_type`，并必须映射 terminalization
policy。

capability matrix v1 rows 只允许覆盖本仓库 GA 收敛所需能力，避免演变成通用 feature-flag 平台：

| capability_id | surface_type | operation_type / surface 示例 | default_ga_required | optional_gated | service_ready 影响 | fixture-enabled 正向路径 |
| --- | --- | --- | --- | --- | --- | --- |
| `namespace_binding` | `durable_operation` | namespace create/bind/update policy | yes | no | yes | no |
| `volume_preflight` | `preflight` | volume register/health/preflight | yes | no | yes | no |
| `admin_bootstrap` | `preflight` | admin bootstrap/readiness check | yes | no | yes | no |
| `caller_policy_readiness` | `discovery` | caller/operator role/policy readiness check | yes | no | yes | no |
| `path_redaction` | `redaction` | redacted path/capability/status projection | yes | no | yes | no |
| `repo_create` | `durable_operation` | repo create | yes | no | yes | no |
| `repo_projection` | `read_projection` | repo get/list projection | yes | no | yes | no |
| `jvs_save_restore` | `durable_operation` | save, restore run/discard；history/preview are read/projection evidence | yes | no | yes | no |
| `webdav_export` | `durable_operation` | export create/revoke；get/gateway status are read/projection evidence | yes | no | yes | no |
| `operation_recovery` | `runtime_support` | recovery/terminalize；operation status read does not create a new durable operation | yes | no | yes | no |
| `repo_lifecycle_retained` | `durable_operation` | archive, restore archived, tombstone, restore tombstoned | yes | no | yes | no |
| `repo_purge` | `durable_operation` | purge, purge recovery | no | yes | no when disabled | yes, only with verifiable approval fixture |
| `repo_template` | `durable_operation` | template create, same-namespace same-volume clone | no | yes | no when disabled | yes, only with repo-local fixture enabled |
| `workload_mount` | `durable_operation` / `discovery` | mount binding, plan, heartbeat/release/revoke；default profile exposes disabled/status only | no | yes | no when disabled | yes, only with repo-local fake orchestrator fixture enabled |

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
| true | true | true | allow | no | yes | Admission may proceed only in explicit `repo-local-fixture-enabled` mode；`deployment-runtime-support` 只能作为运行态支持声明；default GA 仍证明 disabled/denied path。 |

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

下表的 Error family 是目标稳定 catalog，不只是文档标签。Package 1 必须把这些 family
新增或映射到 API error code/schema/OpenAPI 和 audit event schema；代码里的 error code
不得与本文档 catalog 分叉。

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
- 当前 capability 已关闭、handler 不支持或 runtime 缺失：按 side-effect boundary 终态化，而不是按 capability 名称粗暴分类。
- side effect 可证明未开始，或外部状态可证明安全且无需人工判断：进入 `failed` 并记录稳定 recovery audit。
- side effect 可能已经开始、外部状态未知、或继续/回滚安全性不可自动证明：进入
  `operator_intervention_required` 并保持 blocking。
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

## Research Finding Trace Matrix

这张表把 reviewer findings 收束到开发包、TDD/acceptance、manifest claim/subclaim 和 gate
形状。Finding 名称按问题域归并，但覆盖 Finding 1-18；后续 generated report 必须能按同样
维度反查 evidence `id`。Source anchor/source finding 是覆盖复核索引，不声明一一对应；
一行可以覆盖多个原始 finding，一个原始 finding 也可以被多个验收行分摊。

| Finding | Source anchor | Source finding | Package | TDD/acceptance | Manifest claim/subclaim | Gate command shape |
| --- | --- | --- | --- | --- | --- | --- |
| F1 默认 GA 与 optional 边界混淆 | `docs/research/afscp-product-architecture-review.md` Finding 1, 5 | 直接 GA 范围过宽；独立 GA 与真实可用性混在一起 | P0/P1 | profile schema、capability decision table、optional disabled admission | `CLAIM_PROFILE_BOUNDARY` | `bash scripts/verify-ga-release.sh` -> manifest verifier profile checks |
| F2 默认 trusted caller 闭环不前置 | `docs/research/afscp-product-architecture-review.md` Finding 5, 15 | 产品中立链路可用性；缺少 happy/failure journeys | P2/P5 | create/get/list + JVS + WebDAV + operation/audit/recovery journey | `CLAIM_DEFAULT_USER_LOOP` | `bash scripts/verify-ga-release.sh` -> default journey evidence |
| F3 admin/bootstrap 缺少验收 | `docs/research/afscp-product-architecture-review.md` Finding 5, 14, 15 | 真实可用性链路；调用方心智收敛；journey 验收缺口 | P1/P2 | volume preflight、namespace binding、caller role/policy、path redaction | `CLAIM_ADMIN_BOOTSTRAP_READY` | `bash scripts/verify-ga-release.sh` -> bootstrap claim evidence |
| F4 capability 判断分散 | `docs/research/afscp-product-architecture-review.md` Finding 2, 4 | API admission 与 worker gate 不一致；证据强度不足 | P1 | API/worker/readyz/operator/manifest 共用 matrix contract | `CLAIM_CAPABILITY_MATRIX_CONSISTENT` | `bash scripts/verify-ga-release.sh` -> contract/schema tests |
| F5 admission 可能创建永久 queued | `docs/research/afscp-product-architecture-review.md` Finding 2 | mutating API 与 worker capability gate 不共享判断 | P1 | capability-off mutation、idempotency replay、denial audit | `CLAIM_DEFAULT_DENIAL_SAFE`、`CLAIM_OPTIONAL_DENIED_SAFE` | `bash scripts/verify-ga-release.sh` -> admission negative evidence |
| F6 historical operation recovery 缺终态 | `docs/research/afscp-product-architecture-review.md` Finding 2, 7 | worker 不扫描关闭能力的历史 operation；repair/intervention 写路径缺口 | P1 | disabled/unsupported/unconfigured/side-effect boundary recovery | `CLAIM_OPERATION_TERMINALIZATION` | `bash scripts/verify-ga-release.sh` -> recovery terminalization evidence |
| F7 readyz 被误用为 caller/orchestrator contract | `docs/research/afscp-product-architecture-review.md` Finding 6, 14 | operator observability 面不匹配；概念暴露过多 | P1/P2 | caller status、orchestrator discovery、operator global surface | `CLAIM_DISCOVERY_SURFACES` | `bash scripts/verify-ga-release.sh` -> discovery contract evidence |
| F8 WebDAV credential/gateway 边界不清 | `docs/research/afscp-product-architecture-review.md` Finding 5, 10 | 产品中立 export 链路；credential issuer wording 不一致 | P2 | first-create-only、revoke/expiry、path policy、ledger e2e | `CLAIM_WEBDAV_DEFAULT_ACCESS` | `bash scripts/verify-ga-release.sh` -> WebDAV e2e evidence |
| F9 workload mount 正向路径过粗 | `docs/research/afscp-product-architecture-review.md` Finding 3, 5 | workload mount 安全闭环缺口；产品中立 orchestrator 链路 | P2 | plan fetch、heartbeat、release、revoke、terminal evidence | `CLAIM_WORKLOAD_FIXTURE_READY` subclaims | `bash scripts/verify-ga-release.sh` -> fixture-enabled workload evidence |
| F10 SecretRef/raw path 泄露风险 | `docs/research/afscp-product-architecture-review.md` Finding 3, 14, 18 | SecretRef 推导/暴露边界；分层心智；shared-volume residual risk | P2/P4 | RBAC/redaction、path resolver、raw path deny | `CLAIM_SECRET_PATH_REDACTION` | `bash scripts/verify-ga-release.sh` -> redaction/path policy evidence |
| F11 operator discovery/repair 不成闭环 | `docs/research/afscp-product-architecture-review.md` Finding 6, 7 | operator discovery 面不足；repair/intervention 写路径未契约化 | P3 | inspection queue、allowed repair、identity/reason/evidence/audit | `CLAIM_OPERATOR_REPAIR_SAFE` | `bash scripts/verify-ga-release.sh` -> operator repair evidence |
| F12 residual-risk acceptance 可能绕过安全 | `docs/research/afscp-product-architecture-review.md` Finding 7, 18 | operator repair safety；shared-volume residual risk threat model 不足 | P3/P4 | risk catalog、named predicate、default record-only、blocking tests | `CLAIM_RESIDUAL_RISK_CATALOG` | `bash scripts/verify-ga-release.sh` -> residual-risk negative evidence |
| F13 purge approval 只是自由字符串 | `docs/research/afscp-product-architecture-review.md` Finding 8 | purge break-glass approval reference 偏弱 | P4 | structured approval fixture、expiry/scope/hash/replay negative | `CLAIM_PURGE_APPROVAL_SAFE` | `bash scripts/verify-ga-release.sh` -> purge approval evidence |
| F14 restore reconciliation 状态边界不足 | `docs/research/afscp-product-architecture-review.md` Finding 9, 16 | control-plane/storage-plane 一致性；restore session drain contract 不一致 | P4 | mode entry/exit、read-only allowed、writes denied、不一致 intervention | `CLAIM_RESTORE_RECONCILIATION` | `bash scripts/verify-ga-release.sh` -> reconciliation evidence |
| F15 lifecycle wording 混入业务 catalog | `docs/research/afscp-product-architecture-review.md` Finding 12, 16 | lifecycle vocabulary 偏产品工作流；restore drain 口径需对齐 | P4/P5 | retained lifecycle positive、purge excluded、doc/contract guard | `CLAIM_RETAINED_LIFECYCLE_DEFAULT` | `bash scripts/verify-ga-release.sh` -> lifecycle evidence |
| F16 template/clone/quota 默认语义不稳 | `docs/research/afscp-product-architecture-review.md` Finding 11, 13 | template 心智不匹配；quota 字段误导 | P4 | default denied、same-namespace same-volume fixture、quota status | `CLAIM_TEMPLATE_QUOTA_BOUNDARY` | `bash scripts/verify-ga-release.sh` -> template/quota evidence |
| F17 deployment-only 风险混入本地 gate | `docs/research/afscp-product-architecture-review.md` Finding 5, 18 | 独立 GA 与真实部署可用性边界；shared-volume deployment residual risk | P0/P4/P5 | evidence profile check、model fixture、runbook/escalation guard | `CLAIM_DEPLOYMENT_RISK_ENVELOPE` | `bash scripts/verify-ga-release.sh` -> deployment envelope evidence |
| F18 manifest/gate 颗粒度不足 | `docs/research/afscp-product-architecture-review.md` Finding 4, 17 | evidence manifest/gate 颗粒度不足；旧 wording 与 gate 口径冲突 | P0/P5 | schema/verifier negative、claim report、high-risk non-doc-only | `CLAIM_RELEASE_GATE_TRACEABLE` | `bash scripts/verify-ga-release.sh` -> generated claim/evidence report |

## 开发包局部验收规则

这些开发包是合并单元，不是 release 阶段，也不存在 package-level GA。每个包只能声明
“局部可合并”，不能声明“该包已 GA”。局部验收必须同时满足 DoD、依赖、可合并条件和
manifest 追踪要求。package 0/1 必须先把 manifest schema/verifier seed 前移；packages
2-4 每包都必须新增 manifest claim/evidence entries，并至少补一个 verifier negative
case；package 5 只做 hardening、coverage gap 清零和 generated report，不再承载主要
功能发现。

| 开发包 | 依赖 | DoD | 可合并条件 | 局部验收边界 |
| --- | --- | --- | --- | --- |
| Package 0: Evidence Manifest Seed | 现有 manifest baseline、唯一 gate 脚本 | 最终 manifest 字段和 claim taxonomy 前置，包括 `claim_id`、`subclaim_id`、`acceptance_id`、`risk_id`、`capability_id`、`evidence_type`、`evidence_profile`、`default_mode`、`fixture_enabled_mode`、`expected_runtime`、`scope`、`command`、`anchors`、`pass_criteria`；verifier negative seed | gate 能识别缺 claim、缺 required evidence、profile 混用、doc-only high-risk、命令缺失或命令不可 repo-local 运行 | 只建立证据骨架；不声称现有 baseline 已覆盖最终 GA，也不把字段留到 Package 5 才补。 |
| Package 1: Capability & Operation Terminalization | package 0、现有 operation store、capability tests | capability matrix v1 rows、effective decision table、admission denial table、worker terminalization table、per-operation phase side-effect boundary table 有 contract/schema/test 覆盖 | 默认 GA 与 optional-gated 的 admission/recovery/readyz 状态一致；disabled optional 不创建永久 queued；历史 operation 可发现和终态化/intervention；新增 manifest entries 与 verifier negative case | 只证明 capability/admission/recovery 语义可合并，不证明 access、purge、operator repair 全闭环 GA。 |
| Package 2: Access Sessions Safety | package 1 的 capability/admission 语义 | WebDAV credential first-create-only、gateway policy、ledger e2e、workload lease freshness、SecretRef redaction/RBAC、session/fence 联动测试通过 | 默认 WebDAV 正向路径有真实 Postgres ledger + repo-local fixture；workload mount 默认 disabled/denied/recovery 安全，positive 只作为 `repo-local-fixture-enabled` conformance evidence；stale binding 只能证明进入 blocking/intervention trace，不能单独声明 closure；新增 manifest entries 与 verifier negative case | 不实现 caller connector、真实 orchestrator 或真实部署 mount；stale/intervention closure 必须由 Package 3 联合闭环。 |
| Package 3: Operator Intervention | package 1 的 recovery/intervention 状态；package 2 的 session/fence/stale binding 证据 | operator-only inspection/repair contract、allowlist、safety predicate、required fixture/object evidence、audit/redaction/idempotency tests 完成 | repair contract 明确 operator-only，不暴露给普通 caller；缺证据 repair fail-closed；能闭环 Package 2 留下的 stale/intervention queue；新增 manifest entries 与 verifier negative case | 只证明最小运行态修复闭环，不做通用运维平台或 dashboard。 |
| Package 4: Irreversible Lifecycle Safety | package 1-3 的 capability、session、operator repair | purge approval evidence、restore consistency、repo lifecycle session-drain decision、template/quota/lifecycle wording、shared-volume residual-risk gate 完成 | purge 默认 disabled；retained lifecycle 默认 enabled 且 contract/test 一致；deployment-only 风险只以 repo-local 模拟/检测/runbook/escalation 证明；新增 manifest entries 与 verifier negative case | 不做 namespace delete、template marketplace、真实部署 CSI/POSIX 验证。 |
| Package 5: Evidence Hardening & Coverage Report | 前四包的 claim/evidence 条目 | 扩展现有 manifest coverage、gap report、gate wiring、generated mapping | 每个最终验收 bullet 可追溯到 manifest `claim_id`、`acceptance_id` 或 `subclaim_id`、evidence `id`；高风险 claim 不能 doc-only | 只做 repo-local release gate hardening，不依赖兄弟 repo、人工审批或外部 release dashboard。 |

## 开发包 0: Evidence Manifest Seed

### 要解决的问题

- 当前 manifest 是 baseline/seed，容易被误读成最终 GA claim coverage。
- 如果等到最后才定义 schema/verifier，packages 2-4 的证据会缺少统一 claim/subclaim/profile 语言。

### 方案

- 在第一轮开发前扩展 manifest schema seed，最终字段必须一次前置，至少支持
  `claim_id`、`subclaim_id`、`acceptance_id`、`risk_id`、`fixture_id`、`capability_id`、
  `evidence_type`、`evidence_profile`、`default_mode`、`fixture_enabled_mode`、
  `negative_or_positive`、`expected_runtime`、`scope`、`command`、`anchors` 和
  `pass_criteria`。Package 5 只能补 coverage 和 report，不能才开始补字段。
- verifier seed 接入唯一 gate，并先提供 negative cases：缺 required evidence、把
  运行态支持 profile 误放入本地必需证据集合、high-risk doc-only、命令不存在、
  profile 标记冲突。
- 建立唯一 Claim Taxonomy / Capability Vocabulary 权威表。后续文档、manifest、
  verifier 和 generated report 只能引用这张表中的 `claim_id`，不得另起一套 claim
  名称或把 acceptance/subclaim 写成新的 claim。
- verifier 必须支持 seed mode 与 final mode：baseline 期间 `seed_gap_*_open` 只允许作为
  seed marker；final mode 下 required/final claim 不允许 open marker，必须被具体 evidence
  替代、关闭，否则 gate fail。

Claim taxonomy seed 必须先给后续包可引用的稳定 ID：

| Taxonomy | 用途 | Package 0 最低要求 |
| --- | --- | --- |
| `claim_id` | 聚合 GA 声明，例如 `CLAIM_DEFAULT_USER_LOOP` | 必须稳定、可在 generated report 中反查。 |
| `subclaim_id` | 拆分 claim 的子能力，例如 workload plan fetch/heartbeat/revoke | required claim 有多个子能力时必填。 |
| `acceptance_id` | 映射最终验收 bullet 或 journey acceptance | 每个最终验收 bullet 至少能落到一个 acceptance。 |
| `risk_id` | 映射 risk register 或本计划风险 | 高风险 claim 必填，不能只有文档说明。 |
| `capability_id` | 连接 capability matrix、API、worker、readyz 和 evidence | mutating/recovery 相关 evidence 必填。 |
| `evidence_type` | unit、contract、schema、openapi、generated-client、integration、e2e、provenance、race、doc-guard | verifier 必须拒绝未知类型。 |
| `evidence_profile` | `default`、`repo-local-fixture-enabled`、`deployment-runtime-support` | verifier 必须拒绝 profile 混用。 |
| `default_mode` / `fixture_enabled_mode` | 防止 optional positive 被写成默认可用 | optional positive 必须 `default_mode=false`。 |
| `expected_runtime` / `scope` | 说明命令耗时和覆盖范围 | 必须是 repo-local 可运行范围，不能要求人工 DSN 或真实部署状态。 |
| `command` / `anchors` / `pass_criteria` | 让证据可执行、可定位、可判定 | command 必须被唯一 gate 覆盖；pass criteria 必须结构化。关键高风险断言不能只放自由文本 assertion，必须拆成 verifier-checkable subclaim、acceptance、required selector 或 negative case；机器可判定字段优先于 prose。 |

Package 0 的唯一 claim taxonomy 如下。`CLAIM_WORKLOAD_FIXTURE_READY` 是 workload
fixture 的子能力聚合 claim，不表示默认 GA ready；所有 optional 正向 conformance 由
`CLAIM_OPTIONAL_FIXTURE_CONFORMANT` 约束 `evidence_profile=repo-local-fixture-enabled`、
`default_mode=false`。

| claim_id | Authority / meaning | Gate profile role | Capability vocabulary anchor |
| --- | --- | --- | --- |
| `CLAIM_PROFILE_BOUNDARY` | 三档 evidence profile、default/fixture/deployment 边界一致 | `default` required；拒绝 profile 混用 | all |
| `CLAIM_ADMIN_BOOTSTRAP_READY` | 默认闭环前置 admin/bootstrap 已就绪 | `default` required | `volume_preflight`、`namespace_binding`、`admin_bootstrap`、`caller_policy_readiness`、`path_redaction` |
| `CLAIM_DEFAULT_USER_LOOP` | trusted caller 默认正向闭环可用 | `default` required | `repo_create`、`repo_projection`、`jvs_save_restore`、`webdav_export`、`operation_recovery` |
| `CLAIM_DEFAULT_DENIAL_SAFE` | 默认能力负路径 fail-closed | `default` required negative | default durable operations + admission denial catalog |
| `CLAIM_OPTIONAL_DENIED_SAFE` | optional capability 默认关闭时 disabled/denied/recovery 安全 | `default` required negative | `workload_mount`、`repo_template`、`repo_purge` |
| `CLAIM_CAPABILITY_MATRIX_CONSISTENT` | API、worker、readyz、operator、manifest 共用同一 matrix | `default` required | all capability rows |
| `CLAIM_OPERATION_TERMINALIZATION` | 历史 durable operation 可扫描并终态化/intervention | `default` required | durable operation rows |
| `CLAIM_DISCOVERY_SURFACES` | caller/orchestrator/operator/readyz discovery surface 分层 | `default` required；optional 只暴露 disabled/status | discovery/read projection rows |
| `CLAIM_WEBDAV_DEFAULT_ACCESS` | 默认 WebDAV export/gateway/revoke 链路安全可用 | `default` required | `webdav_export` |
| `CLAIM_SECRET_PATH_REDACTION` | raw path、SecretRef、host path、credential 不泄漏 | `default` required | `path_redaction`、`workload_mount` discovery |
| `CLAIM_WORKLOAD_FIXTURE_READY` | workload fixture 的 plan/heartbeat/release/revoke/terminal evidence 子声明完整 | `repo-local-fixture-enabled` conformance only | `workload_mount` |
| `CLAIM_OPTIONAL_FIXTURE_CONFORMANT` | release 明确声明的 optional 正向 fixture conformance 可验证 | only when explicitly claimed；never `default_mode=true` | `workload_mount`、`repo_template`、`repo_purge` |
| `CLAIM_OPERATOR_REPAIR_SAFE` | operator repair 有 allowlist、safety predicate、evidence、audit | `default` required runtime-safety evidence | `operation_recovery` + repair surfaces |
| `CLAIM_RESIDUAL_RISK_CATALOG` | residual-risk acceptance 受预登记 catalog 约束 | `default` required negative/guard | deployment/runtime risk catalog |
| `CLAIM_PURGE_APPROVAL_SAFE` | purge/break-glass approval 结构化且可验证 | default denied required；positive only `repo-local-fixture-enabled` | `repo_purge` |
| `CLAIM_RESTORE_RECONCILIATION` | restore reconciliation mode 防止危险写入 | `default` required | `repo_lifecycle_retained`、`operation_recovery` |
| `CLAIM_RETAINED_LIFECYCLE_DEFAULT` | retained lifecycle 是默认 storage-state 正向能力 | `default` required | `repo_lifecycle_retained` |
| `CLAIM_TEMPLATE_QUOTA_BOUNDARY` | template 默认 denied，quota status 机器可读 | default denied required；positive only explicit fixture conformance | `repo_template` |
| `CLAIM_DEPLOYMENT_RISK_ENVELOPE` | deployment-only 风险只以 repo-local 模型/模拟/脱敏/升级证明 | `deployment-runtime-support` not required local GA positive | runtime support rows |
| `CLAIM_WORKFLOW_HARDENING_GUARD` | workflow hardening 只验证 repo-local 可检查事实 | `default` required guard | release gate workflow |
| `CLAIM_RELEASE_GATE_TRACEABLE` | 最终验收 bullet 可追溯到 manifest/generated report | `default` required | all claims |

Capability vocabulary v1 是一个收敛任务，不是假设当前代码已经一致。当前代码、manifest
或 verifier allowlist 可能仍出现 `storage`、`jvs`、`webdav_export`、`workload_mount`、
`repo_template`、`repo_purge`、`repo_lifecycle_retained` 等粒度；Package 1 必须把代码、
manifest verifier allowlist 和 evidence manifest 共同更新到同一 vocabulary。目标 row
可以包括 `namespace_binding`、`volume_preflight`、`admin_bootstrap`、
`caller_policy_readiness`、`repo_create`、`repo_projection`、`jvs_save_restore`、
`webdav_export`、`operation_recovery`、`path_redaction`、`repo_lifecycle_retained`、
`repo_purge`、`repo_template`、`workload_mount`。任何临时兼容映射都必须在 verifier
report 中显式标记为 migration compatibility，不能作为最终 claim vocabulary。

### 局部验收

- `bash scripts/verify-ga-release.sh` 能调用 manifest verifier seed。
- seed 报告明确标记 WebDAV/JVS/default user loop 仍需 positive evidence，不能误报最终 GA
  coverage 完成。
- seed mode 可以保留 `seed_gap_*_open` marker 作为 baseline gap；final mode 必须反转规则，
  required/final claim 一旦仍有 open marker 就 fail，并报告需要替代/关闭的 evidence ID。
- package 0 不修改产品能力，只建立后续包必须填充的证据 contract。

## 开发包 1: Capability & Operation Terminalization

### 要解决的问题

- API、worker、readyz、release evidence 可能各自判断 capability，导致 admission 与 execution 不一致。
- 新 mutation 可能进入 operation 队列，但 worker 因 gate 关闭或 handler 不存在而永远不处理。
- 历史 operation 在 capability 变化后缺少统一终态规则。

### 方案

- 实现或收敛一份 capability matrix contract，并让 API、worker、readyz、operator inspection、evidence manifest 共用。
- 将代码、manifest verifier allowlist 和 evidence manifest 收敛到 Package 0 定义的同一 capability
  vocabulary；旧的 `storage`、`jvs` 等粗粒度 ID 只能作为迁移兼容映射，不能作为最终 vocabulary。
- admission 按本文的固定顺序执行，确保 idempotent replay 优先于 capability denial。
- 每类 operation 定义 terminalization policy：`succeeded`、`failed`、`operator_intervention_required`。
- 每类 operation/phase 定义 side-effect boundary：side effect 未开始、已开始、未知三类边界必须能机器判断或进入 intervention。
- worker recovery dispatcher/classifier/terminalizer 覆盖所有已知 operation type；扫描历史 operation 时，不以当前 `ready/configured` gate 作为跳过理由。
- unsupported handler、runtime unavailable、capability now disabled、lease expired、fence uncertain、audit outbox blocked 都必须有稳定分类、错误码和审计事件。
- readyz 区分 default GA required capability 与 optional gated capability；optional gated 关闭不能让基础服务误报 not ready。
- 移除 `workload_mount` 对默认 fallback service-ready 集合的污染，或强制所有生产 readyz
  都从 capability matrix 注入 required 集合；optional disabled 不能使 service not ready。

### TDD/自动验收

- 先写 capability-off admission 测试：关闭 workload/template/purge 等 optional-gated 能力时，新 mutation 不创建永久 queued operation；默认 GA 的 pinned JVS 与 WebDAV gateway 不得被当作默认可选能力跳过。
- 先写 idempotency replay 测试：同 key/hash replay 返回既有 operation，不受当前 capability 状态影响。
- 先写 worker recovery 测试：历史 queued/running operation 在 capability 关闭或 handler 不支持时会终态化或进入 intervention。
- 先写 recovery discovery 测试：`ready/configured=false` 时，已知 operation type 仍会被 classifier/terminalizer 发现并处理。
- contract tests 覆盖 API、worker、readyz 暴露的 capability 状态一致。
- readyz P1 测试覆盖 `workload_mount` disabled 时 `service_ready` 仍按默认 required 集合判断，
  且生产 readyz 不使用包含 optional capability 的 fallback required 集合。
- operation state machine tests 覆盖 unsupported、runtime unavailable、crash/retry、audit replay、lease lost、fence uncertain。
- 精确 race/concurrency tests 覆盖同 repo save/restore/template/lifecycle 的串行化；不扩大到无关包。

### 交付物

- capability matrix contract/schema。
- stable capability denial error、API error code/schema/OpenAPI 映射与 audit event catalog。
- operation terminalization contract。
- per-operation phase side-effect boundary table，覆盖每类 operation/phase 的 side effect 未开始、已开始、未知边界，用于决定 `failed` vs `operator_intervention_required`。
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
- workload mount plan 领取必须检查 lease freshness；expired/stale binding 不能继续发普通 plan。default GA 只要求 disabled/denied/recovery；fixture positive 仅在显式 fixture-conformant 声明时 blocking。
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
- fixture-enabled 正向路径拆成五个子声明：plan fetch 返回 redacted mount plan；heartbeat 刷新 lease
  freshness；release 进入受控释放状态；revoke 终止访问并阻止后续 plan；terminal evidence 证明
  session/fence/audit 已终态化。

P2 不能单独宣称 workload stale binding 完成。P2 只证明默认 disabled/denied/recovery、
lease freshness、fixture access safety，以及 stale binding 会稳定进入 blocking/intervention
trace；真正的 stale/intervention closure 必须和 P3 operator inspection/repair 联合验收，
由 `CLAIM_OPERATOR_REPAIR_SAFE` 补齐发现、定位、allowlist repair、evidence 和 audit。

### TDD/自动验收

- WebDAV first-create-only credential 测试。
- idempotent replay 不返回 raw secret 测试。
- revoke/expiry deny 测试。
- gateway path policy 测试：`.jvs`、path traversal、Destination escape、control-root access 全部拒绝。
- WebDAV e2e 使用真实 Postgres ledger 和 repo-local gateway/runtime fixture，不能只靠 mock。
- workload expired lease 不返回普通 plan。
- stale/releasing binding 只能 blocking 或 teardown-only。
- stale binding 的 closure 不在 P2 单独关闭；P2 manifest 只能标记为 P3 依赖的
  intervention trace，缺 P3 repair evidence 时 verifier 不得把 stale path 判定完成。
- SecretRef redaction/RBAC/schema 测试。
- active/uncertain export 和 workload session 阻止 restore-run、template writer、archive/delete/purge 的危险推进。
- manifest 新增 `CLAIM_WORKLOAD_FIXTURE_READY` 的 plan fetch、heartbeat、release、revoke、
  terminal evidence 五个 subclaim；这些证据必须是 `evidence_profile=repo-local-fixture-enabled`、
  `fixture_enabled_mode=true`、`default_mode=false`，并由 `CLAIM_OPTIONAL_FIXTURE_CONFORMANT`
  聚合到显式 fixture conformance 声明；verifier negative case 覆盖缺任一子声明时失败。

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

repo-local 可验证 evidence fixture/object 不能只是“字段存在”。最少要有这些对象形态：

| Evidence object | 用途 | 必须可验证 |
| --- | --- | --- |
| terminal operation fixture | terminalize operation repair | operation before/after、side-effect boundary、executor/recovery trace、audit event。 |
| session terminal fixture | release fence、revoke session | WebDAV ledger revoke/expiry，或 workload heartbeat/release/revoke/terminal evidence。 |
| purge approval fixture | purge/break-glass positive path | 结构化 approval token/record 的 issuer/verifier、scope、expiry、hash/correlation、防重放。 |
| residual-risk acceptance fixture | bounded acceptance | 预登记 `risk_id`、scope、expiry/review point、blocking 类型、命名 safety predicate、audit。 |

residual-risk catalog 是一等 contract：

- 只有预登记 `risk_id` 可以被 acceptance；自由文本 risk 不可验收。
- 每个 `risk_id` 必须定义 scope、expiry/review point、evidence requirement、可解除/不可解除的
  blocking 类型。
- residual-risk acceptance 默认只记录和审计，不自动 unblock。
- 只有 catalog 中命名的 safety predicate 明确允许时，acceptance 才能解除指定 blocking；不得
  解除未列名的 writer、credential、mount、restore、purge 阻断。

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
- manifest 新增 operator repair、purge approval fixture、residual-risk catalog claim/evidence entries；
  verifier negative case 覆盖未预登记 `risk_id`、缺 fixture/object、acceptance 自动 unblock 时失败。

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
- restore archived/tombstoned 的产品决策已经固定：它们恢复访问性，默认 GA 必须证明无
  active/uncertain session/fence；无法证明时 fail-closed 或 `operator_intervention_required`。
- shared managed volume 的残余风险必须显式建模和验收，不能藏在普通 capability 文案里。

### 方案

Purge approval：

- break-glass 默认关闭。
- purge default profile 永远 disabled；required local gate 的 purge positive path 只能在
  `repo-local-fixture-enabled` profile 且结构化 approval evidence 可验证时成立。`deployment-runtime-support` 只声明运行态
  支持 envelope，不得进入 required local GA 证据集合。
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

Restore reconciliation mode 状态边界：

| Boundary | 行为 |
| --- | --- |
| 入口 | backup/restore 完成、metadata/storage generation 不一致、snapshot marker 缺失或 purge/tombstone marker 需要复核时进入。 |
| 允许只读操作 | operator inspection、caller redacted status、audit/recovery evidence 查询、只读 consistency check。 |
| 禁止写操作 | 新 WebDAV credential、mount plan、restore-run、save/template writer、archive/delete/purge、任何会改变 storage 或重新发访问权的动作。 |
| metadata active / storage missing | 禁止访问，进入 `operator_intervention_required`；不得静默创建 storage 或当作 active 成功。 |
| metadata purged / storage residual | 禁止访问和复活 repo，进入 intervention；只能走 purge invariant repair 或 residual-risk catalog 指定路径。 |
| 退出条件 | metadata、storage marker、session/fence/audit 状态一致，且所有 blocking predicate 清除或由命名 safety predicate 授权解除。 |
| operator repair 关系 | repair 只能补证、终态化或保持 blocking；不能把不一致状态直接改成 active。 |

Template/quota/lifecycle：

- template/clone 默认模式只证明 stable denied/fail-closed/recovery；若 fixture-enabled/optional capability 显式启用，正向路径只允许 same-namespace same-volume primitive。
- cross-namespace、cross-volume 默认稳定拒绝。
- 如果未来需要跨 namespace 发布，另行定义受控 admin import/publish；不混入本轮。
- quota schema/OpenAPI 暴露机器可读 enforcement status，例如 policy-only、not-enforced、runtime-enforced、effective_quota_bytes。
- lifecycle wording 统一为 storage-state：archive/delete/tombstone/purge 只表达存储可访问性、保留、恢复和清理状态。
- restore archived/tombstoned 的 session/fence blocking 必须写入 contract/API/worker/recovery/evidence：
  admission 无法证明无 active/uncertain access 时返回 stable error/audit；历史 operation 无法证明安全时进入 intervention。

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
- restore archived/tombstoned 的 active/uncertain session/fence blocking 有 contract/API/worker/recovery/evidence 测试。
- cross-namespace/cross-volume clone 稳定拒绝。
- quota enforcement status 进入 schema/OpenAPI/generated fixture。
- lifecycle wording 由 doc guard 或 contractcheck 防回退到业务 catalog 语义。
- shared-volume 测试覆盖 path traversal、symlink escape、cross-namespace mismatch、raw path/SecretRef redaction、backup restore residual data simulation、POSIX/CSI/subPath drift detection model fixture、dedicated-volume escalation、runbook/escalation guard、residual-risk acceptance audit；不要求真实 CSI/POSIX 部署作为 repo-local gate。
- manifest 新增 purge、restore reconciliation、template/quota/lifecycle、shared-volume residual-risk
  claim/evidence entries；verifier negative case 覆盖运行态支持 profile 被误放入本地必需证据集合、
  restore reconciliation 缺状态边界、purge approval 只有自由文本时失败。

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

## 开发包 5: Evidence Hardening & Coverage Report

### 要解决的问题

- 当前 `auto_verified` 颗粒度太粗，容易把 unit/text/contract baseline 误读为完整生产证据。
- 高风险项不能只有 doc guard。
- JVS provenance、真实 Postgres、WebDAV ledger e2e、generated-client、race/concurrency 等需要进入唯一 GA gate。
- 已有 `docs/release-evidence/ga-manifest.json` 是 seed/baseline，不能把 manifest 当作空白新建物并覆盖现实。
- 前四包应已经交付主要 claim/evidence；本包只做 hardening、coverage gap、generated report 和 gate wiring。

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
- 将 Package 0 的 verifier seed mode 切到 final mode：任何 required/final claim 仍引用
  `seed_gap_*_open` 或等价 open marker 时失败，除非该 marker 已被具体 evidence 替代并关闭。
- manifest schema 字段应沿用 Package 0 已前置的最终字段；本包只补 coverage，不重新发明字段：
  - `claim_id`: 稳定 GA 声明 ID。
  - `acceptance_id` 或 `subclaim_id`: 稳定子声明 ID，用于把最终验收 bullet 拆成可追踪、可聚合的子声明。
  - `risk_id`: 对应 risk register 或本计划风险 ID。
  - `fixture_id`: 使用的 repo-local fixture ID；无 fixture 时显式为空。
  - `capability_id`: 对应 capability matrix、API、worker、readyz 和 operator surface。
  - `evidence_type`: unit、contract、schema、openapi、generated-client、integration、e2e、provenance、race、doc-guard。
  - `evidence_profile`: `default`、`repo-local-fixture-enabled` 或 `deployment-runtime-support`。
  - `expected_runtime`: 证据命令预期耗时分级，例如 fast、integration、e2e、race。
  - `scope`: unit、package、service、repo-local e2e、doc guard 等覆盖范围。
  - `negative_or_positive`: negative、positive 或 both。
  - `default_mode`: 证据是否覆盖默认 GA 模式。
  - `fixture_enabled_mode`: 证据是否只在 repo-local fixture capability 启用时成立。
  - `pass_criteria`: verifier-checkable structured criteria；不能只是自由文本或“文字可检查”说明；高风险断言必须拆成 subclaim、acceptance、required selector 或 negative case。
  - `anchors`: 源码、schema、contract、runbook 或生成物锚点。
  - `command`: repo-local 可执行命令；不能要求人工 DSN、兄弟 repo 或真实部署状态。
- evidence type 至少支持：unit、contract、schema、openapi、generated-client、integration、e2e、provenance、race、doc-guard。
- 高风险项必须有非 doc-only evidence。
- Postgres integration gate 在 clean checkout 下必须能自启动临时 Postgres，或使用 repo-local 可复现 fixture；CI service 只是 CI 中的等价自动 provisioning，不能要求人工 DSN、预配置外部 DB 或部署侧状态。
- WebDAV GA evidence 必须使用真实 Postgres ledger 和 repo-local gateway/runtime fixture。
- JVS pinned binary provenance 和最小 smoke 自动验证；如果上游缺少某类 signature/bundle，manifest 必须记录可自动验证的替代证据，不能只写说明。
- product-neutral conformance/smoke 必须区分默认能力与 optional-gated 正向验证：默认模式验证 workload/template/purge 关闭时 stable denied/fail-closed/recovery；只有启用 repo-local fixture capability 后，才验证 mount plan、template/clone、purge 的正向路径；purge positive evidence 必须来自 `repo-local-fixture-enabled` profile、`default_mode=false` 且结构化 approval evidence 可验证。这不改变默认 GA 边界，也不依赖真实外部 orchestrator 或兄弟 repo。
- product-neutral conformance fixture 边界必须完全 repo-local：credential relay 用 fake trusted
  caller + fake connector fixture；mount plan consumption 用 fake orchestrator fixture；runtime
  connector 用 fake connector/runtime fixture。不得依赖兄弟 repo、真实 orchestrator、真实部署权限或外部业务 e2e。
- product-neutral happy/failure journeys 作为验收索引，覆盖默认 create/get/projection/list、save/history、restore-preview/run/discard、WebDAV export/gateway/revoke、operation/audit/recovery，以及 optional-gated denied 和 fixture-enabled positive paths。

Workflow hardening guard 是 repo-local DoD，不是 GitHub 环境审计。它只检查仓库内可证明事实，
锚点至少包括 `.github/workflows/ga-release.yml`、`internal/contractcheck/contractcheck_test.go`
和 `scripts/verify-ga-release.sh`：

| Guard | 必须检查 | 不得作为本地 gate 通过条件 |
| --- | --- | --- |
| 唯一脚本调用 | `.github/workflows/ga-release.yml` 的 workflow/release entrypoint 直接或间接调用 `bash scripts/verify-ga-release.sh`，并由 `internal/contractcheck/contractcheck_test.go` 防止维护另一套 GA 命令。 | 人工声称 CI 已跑过。 |
| 最小权限 | workflow YAML 声明最小 `permissions`，不默认扩大 token 权限；contractcheck 覆盖权限漂移。 | GitHub org/repo 实际权限截图或人工确认。 |
| release/tag trigger 与 artifact upload 配置声明 | 仓库内 workflow 声明 release/tag trigger 和 evidence artifact upload 配置；contractcheck 验证 trigger/upload key 存在且指向唯一 gate 产物。 | 真实 artifact 已存在、branch protection 已配置、GitHub environment rule 已设置。 |

Claim/evidence mapping 必须在 manifest 或相邻 generated report 中可读，并且只能派生自
Package 0 的唯一 taxonomy：

| claim_id | Claim | Required evidence shape | Manifest requirement |
| --- | --- | --- | --- |
| `CLAIM_PROFILE_BOUNDARY` | evidence profile 与默认/optional/deployment 边界一致 | profile schema + verifier negative cases | 只接受 `default`、`repo-local-fixture-enabled`、`deployment-runtime-support`。 |
| `CLAIM_ADMIN_BOOTSTRAP_READY` | 默认闭环前置 admin/bootstrap 已就绪 | volume preflight + namespace binding + caller role/policy + path resolver redaction | 每项至少一个 `default_mode=true` evidence；不得暴露 raw path。 |
| `CLAIM_DEFAULT_USER_LOOP` | 默认 GA trusted caller 闭环可用 | create/get/projection/list + save/history/restore + WebDAV + operation/audit/recovery repo-local evidence | 每个子能力至少一个 `default_mode=true` positive evidence。 |
| `CLAIM_DEFAULT_DENIAL_SAFE` | 默认负路径 fail-closed | authz/policy/lifecycle/session/fence/path denial tests | 稳定 error/audit；不得创建永久 queued。 |
| `CLAIM_CAPABILITY_MATRIX_CONSISTENT` | API、worker、readyz、operator、manifest 使用同一 matrix | contract/schema/OpenAPI + readyz/operator consistency tests | capability ID、surface type、required set 和 evidence manifest 不漂移。 |
| `CLAIM_OPERATION_TERMINALIZATION` | 历史 durable operation 可终态化/intervention | recovery dispatcher/classifier/terminalizer tests | read/status/preflight/redaction 不伪装成 durable operation。 |
| `CLAIM_DISCOVERY_SURFACES` | caller/orchestrator/operator/readyz discovery surface 分层 | caller status、orchestrator disabled/status、operator global、readyz contract tests | readyz 不替代 caller/orchestrator API contract。 |
| `CLAIM_WEBDAV_DEFAULT_ACCESS` | 默认 WebDAV access 链路安全可用 | first-create credential + gateway/revoke/ledger e2e | repo-local Postgres ledger evidence；raw secret/path 不回放。 |
| `CLAIM_SECRET_PATH_REDACTION` | SecretRef/raw path/host path/credential 不泄漏 | RBAC/redaction/path resolver negative tests | 普通 caller、connector、workload 只能见 redacted projection。 |
| `CLAIM_RETAINED_LIFECYCLE_DEFAULT` | retained repo lifecycle 属于默认 GA storage-state 正向能力 | archive/restore_archived/delete/tombstone/restore_tombstoned repo-local positive tests | 每个 action 至少一个 `default_mode=true` positive evidence；purge 不得归入该 claim。 |
| `CLAIM_OPTIONAL_DENIED_SAFE` | workload/template/purge 默认关闭时安全 | disabled/denied/recovery negative tests | `negative_or_positive=negative`、`default_mode=true`、不得创建永久 queued。 |
| `CLAIM_OPTIONAL_FIXTURE_CONFORMANT` | optional positive path 只在显式 fixture conformance 声明时 blocking | repo-local fixture positive tests | `evidence_profile=repo-local-fixture-enabled`、`fixture_enabled_mode=true`、`default_mode=false`；未声明 fixture-conformant 时不得阻断默认 GA。 |
| `CLAIM_WORKLOAD_FIXTURE_READY` | workload fixture 正向路径完整 | plan fetch、heartbeat、release、revoke、terminal evidence 五个 subclaim | 每个 subclaim 都是 repo-local fixture evidence，缺一不可。 |
| `CLAIM_OPERATOR_REPAIR_SAFE` | operator repair 有 allowlist、safety predicate、evidence、audit | contract + auth + audit/redaction/idempotency tests | 每个 repair action 有 claim/evidence pair。 |
| `CLAIM_PURGE_APPROVAL_SAFE` | purge approval 结构化且可验证 | approval fixture + replay/expiry/scope negative tests | default disabled；positive 只可 `fixture_enabled_mode=true`。 |
| `CLAIM_RESTORE_RECONCILIATION` | restore reconciliation mode 防止危险写入 | mode entry/exit + read-only/write-deny + inconsistency intervention tests | metadata/storage 不一致不得静默 active。 |
| `CLAIM_RESIDUAL_RISK_CATALOG` | residual-risk acceptance 受预登记 catalog 约束 | catalog fixture + named predicate + record-only default negative tests | 未预登记 `risk_id` 或自动 unblock 必须 fail。 |
| `CLAIM_TEMPLATE_QUOTA_BOUNDARY` | template/clone 默认 denied，quota status 机器可读 | default denied + same-namespace same-volume fixture + quota schema/OpenAPI tests | optional positive 不得 `default_mode=true`。 |
| `CLAIM_DEPLOYMENT_RISK_ENVELOPE` | deployment-only 风险有 repo-local检测/模拟/脱敏/升级证据 | model fixture + doc/runbook guard | 不允许要求真实 CSI/POSIX/subPath 部署 gate。 |
| `CLAIM_WORKFLOW_HARDENING_GUARD` | workflow hardening 只验证 repo-local 可检查事实 | 唯一脚本调用 + 最小权限 + artifact/tag trigger 配置声明 guard | branch protection、真实 artifact、GitHub environment 不能作为本地通过条件。 |
| `CLAIM_RELEASE_GATE_TRACEABLE` | 最终验收 bullet 可追溯 | manifest verifier + generated mapping | 每个最终验收 bullet 都引用 `claim_id`、`acceptance_id` 或 `subclaim_id`、evidence `id`。 |

### TDD/自动验收

- manifest schema validation。
- manifest verifier negative tests：缺少 required capability evidence、doc-only high-risk evidence、命令不存在、evidence type 不合法时失败。
- `scripts/verify-ga-release.sh` 覆盖 manifest verifier。
- schema/OpenAPI drift guard。
- repo-local generated-client fixture 编译。
- precise race/concurrency gate。
- Postgres migration/transaction/idempotency/lease/fence/audit outbox integration。
- product-neutral conformance/smoke 在默认模式覆盖 fake trusted caller -> fake connector credential relay、operation inspection、workload/template/purge stable denied/fail-closed；在 repo-local fixture capability 启用后覆盖 fake orchestrator mount plan consumption、template/clone、purge 正向路径，不引入业务项目名。
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

0. 先落 evidence manifest schema/verifier seed。
   这一步先固定 claim/subclaim/profile/risk/evidence 语言，避免后续包各自补证据。

1. 再落 capability matrix 与 operation terminalization contract。
   这一步决定 admission、worker recovery、readyz、operator 和 evidence 的共同语言。

2. 然后补 access session safety。
   WebDAV、workload、restore、template、lifecycle 都依赖 session/fence/lease 语义；先把 credential、ledger、lease freshness、SecretRef redaction 收紧。

3. 接着补 operator intervention。
   当 recovery 无法证明安全时，需要有受控的 inspection 和 repair 写路径，否则 `operator_intervention_required` 只会变成死状态。

4. 再收 irreversible lifecycle safety。
   purge、backup/restore、template clone、quota、lifecycle wording 都会碰到不可逆或调用方误解风险，必须在 capability 和 session 安全之后收口。

5. 最后做 evidence hardening 与 coverage report。
   每个开发包完成时都应同步补 evidence 条目；最后一步只做统一 verifier、gate 接入和缺口清零。

不存在 package-level GA。package 0-5 全部完成、全部证据进入唯一 gate，并且 `bash scripts/verify-ga-release.sh` 从干净 checkout 成功退出后，才能进入 GA 判定。

每一步都按同一方式推进：

1. 先改 contract/schema/OpenAPI/test fixture，让当前实现失败。
2. 再做最小产品中立实现。
3. 补 stable error、audit、redaction、idempotency、runbook/evidence。
4. 接入 `scripts/verify-ga-release.sh` 覆盖的 repo-local gate。

## 多轮 review 质量标准

多轮 review 是质量控制，不是 GA gate。review 不能替代自动化 gate，也不能把人工结论写成
required/final claim 的证据。每轮 review 都必须输出可复跑的 evidence command 和 manifest
trace；没有 command/trace 的意见只能作为待办，不能关闭 claim。

| Round | 角色与目的 | 必须输出 | 不能做什么 |
| --- | --- | --- | --- |
| Round 1 开发自审 | 开发者确认 package DoD、profile 标记、negative case 和 doc guard 没漏。 | 本包涉及的 evidence command、manifest `claim_id/subclaim_id/acceptance_id` trace、已知 seed gap。 | 用“我看过了”关闭 claim。 |
| Round 2 domain review | domain reviewer 复核产品边界、安全状态机、operator/recovery/lifecycle 决策是否一致。 | 复核过的 anchors、需要新增或修正的 evidence command、manifest trace。 | 扩大默认 GA 范围或引入业务项目 gate。 |
| Round 3 QA/release review | QA/release reviewer 复核 clean checkout gate、manifest coverage、workflow hardening guard 和 release note/doc-sync 口径。 | `bash scripts/verify-ga-release.sh` 运行证据、coverage/gap report、release note/doc-sync profile trace。 | 用 branch protection、真实 artifact 或 GitHub 环境状态替代 repo-local gate。 |
| Round 4 fix review | 修复后只复核被改动 claim、风险和证据，确认没有回归 profile 边界。 | 修复关联 command、manifest diff trace、remaining gap list。 | 把未重跑 gate 的修复判定为 GA ready。 |

review 通过只说明“质量复核已完成”。最终 GA 判定仍只看 clean checkout 下唯一脚本退出码，
以及 manifest verifier 对 required/final claim 的机器判断。

## Deployment readiness envelope

默认 GA 的 deployment readiness 只表达最小可运行配置和 readyz 语义，不把真实业务部署、
兄弟 repo、人工审批或主观 review 纳入 release gate。

最小可运行配置必须明确：

- service auth 与 trusted caller/operator/admin role 配置存在且可校验；optional orchestrator
  role/policy readiness 不属于默认 GA deployment readiness，只能在 `repo-local-fixture-enabled`
  或 `deployment-runtime-support` profile 中验证。
- PostgreSQL metadata store/migration 可用，或 repo-local integration fixture 可自动启动等价依赖。
- managed volume policy 和 path resolver root 配置存在，但 raw root path 不暴露给 caller。
- pinned JVS runner 配置、provenance/smoke evidence 可由 gate 验证。
- 内置 WebDAV gateway 作为默认 export policy boundary；不是 stock gateway 代替品。
- audit outbox HTTP JSON GA sink 或 repo-local sink fixture 配置可用，且 redaction guard 生效。
- optional-gated workload/template/purge 默认关闭；`repo-local-fixture-enabled` mode 必须显式打开并产生单独 evidence。
  默认 profile 只证明 optional orchestrator capability 的 disabled discovery/deny 安全。

readyz 必须表达：

| readyz dimension | Meaning | Default GA impact |
| --- | --- | --- |
| `service_ready` | 默认 GA 必需依赖可接收和安全处理请求 | false 时不能声明默认 GA ready。 |
| `default_capabilities_ready` | namespace/repo/JVS/WebDAV/lifecycle retained/recovery/audit 等默认能力 ready | 任一 required capability false，service-ready 必须反映。 |
| `optional_capabilities` | workload/template/purge 等 optional-gated 当前 disabled/denied/fixture-enabled 状态 | disabled 不使 service not ready；fixture-enabled positive path 必须单独显示。 |
| `recovery_ready` | classifier/terminalizer 可以发现历史 operation 并推进 failed/intervention | 不得因 optional capability disabled 从 recovery discovery 消失。 |
| `evidence_profile` | 当前进程/测试运行使用 `default`、`repo-local-fixture-enabled` 还是 `deployment-runtime-support` | 用于防止 positive fixture 被误读成默认开启。 |

## 最终验收命令

后续开发完成后，GA 只由干净 checkout 下这一条命令的退出码决定：

```bash
bash scripts/verify-ga-release.sh
```

任何人工 review、会议、owner sign-off、branch protection、真实 artifact、兄弟 repo e2e、
真实部署状态或 release note 文字，都不能替代这条命令。该命令必须自动证明：

- 默认 GA capability 都有 repo-local evidence，并可追溯到 manifest `claim_id`/evidence `id`。
- pinned JVS save/history/restore-preview/run/discard 和 WebDAV export/gateway/revoke 被自动证明为默认 GA 能力，不被 optional gate 跳过，并映射到 `CLAIM_DEFAULT_USER_LOOP`。
- retained repo lifecycle 的 archive、restore_archived、delete/tombstone、restore_tombstoned 有 repo-local positive evidence，并映射到 `CLAIM_RETAINED_LIFECYCLE_DEFAULT`；purge 不包含在 retained lifecycle 默认能力内。
- namespace-scoped repo projection/list 有分页、过滤和权限边界，不是 global search/aggregation/operator investigation 平台。
- optional gated capability 关闭时，新 mutation fail-closed，不创建永久 queued operation，并映射到 `CLAIM_OPTIONAL_DENIED_SAFE`。
- 默认 GA required/final blocking evidence 只要求 optional capability 的 disabled/denied/recovery 安全；optional 正向 fixture evidence 只在 release 明确声明某 optional capability fixture-conformant 时才 blocking。
- product-neutral conformance/smoke 在默认模式证明 workload/template/purge stable denied/fail-closed/recovery；启用 repo-local fixture capability 后才证明 mount plan/template/purge 正向路径，其中 template/clone 正向路径只允许 same-namespace same-volume primitive，purge positive evidence 必须来自 `repo-local-fixture-enabled` profile、`default_mode=false` 且结构化 approval evidence 可验证，并映射到 `CLAIM_OPTIONAL_FIXTURE_CONFORMANT`。
- idempotent replay 优先于 capability denial。
- 历史 operation 即使 capability 关闭，也会被 worker recovery 扫描并终态化或进入 `operator_intervention_required`；dispatcher/classifier/terminalizer 不因 `ready/configured=false` 从查询或分类范围消失。
- WebDAV credential first-create-only、revoke/expiry、gateway policy、redaction、ledger recovery 有真实 Postgres ledger 与 repo-local e2e 证据。
- workload plan 领取检查 lease freshness，expired/stale 只能 blocking 或 teardown-only。
- workload stale binding 不能只靠 P2 单独关闭；必须有 P3 operator inspection/repair evidence
  证明 stale/intervention queue 可发现、可定位、可按 allowlist 安全处理。
- operator inspection/repair 覆盖 correlated lookup、intervention queue、held fence/session、stale lease、audit lag、runtime recovery status，并有分页/过滤/脱敏、allowed transition、safety predicate、identity/reason/evidence/before-after/audit，映射到 `CLAIM_OPERATOR_REPAIR_SAFE`。
- residual-risk acceptance 不能自动绕过 active/uncertain writer、credential、mount、restore、purge 阻断；repair 后不重发 raw secret、不复活 purged repo、不把 uncertain session 当 terminal。
- purge approval evidence 结构化、可校验、防重放，包含 issuer/verifier、subject、audience，并与 audit 绑定。
- backup/restore 后 reconciliation mode 阻止危险新动作，purged repo 不复活，metadata/storage 不一致进入 intervention。
- `restore_archived` 和 `restore_tombstoned` 作为恢复访问性的 storage-state mutation，默认 GA 下受
  active/uncertain session/fence blocking；不能证明无 active/uncertain access 时 fail-closed 或进入
  `operator_intervention_required`，并同步映射 contract/API/worker/recovery/evidence。
- shared-volume residual risk 覆盖 path traversal、symlink escape、cross-namespace mismatch、raw path/SecretRef redaction、backup restore residual data simulation、POSIX/CSI/subPath drift detection model fixture、dedicated-volume escalation、runbook/escalation、residual-risk acceptance audit，并映射到 `CLAIM_DEPLOYMENT_RISK_ENVELOPE`。
- template/clone 默认模式只证明 stable denied/fail-closed/recovery；若 fixture-enabled/optional capability 显式启用，正向路径只允许 same-namespace same-volume primitive。
- quota enforcement status 机器可读。
- lifecycle vocabulary 保持 storage-state，不漂移成业务 catalog lifecycle。
- schema/OpenAPI/generated fixture 不漂移，product-neutral happy/failure journey index 能映射到 evidence manifest。
- doc-sync exit criterion 覆盖 PRD/Architecture/contracts/runbooks/README：清理 workload/template/purge
  默认正向、caller credential issuer、业务 catalog lifecycle、quota 硬 enforcement 等 wording 漂移。
- workflow hardening guard 只验证 repo-local DoD：唯一脚本调用、最小权限、artifact/tag trigger
  配置声明；branch protection、真实 artifact、GitHub environment 不能作为本地 gate 通过条件。
- JVS provenance/smoke、Postgres integration、WebDAV e2e、race/concurrency、doc guard 都由唯一 gate 覆盖；Postgres gate 在本地可自启动临时 Postgres 或使用 repo-local 可复现 fixture，不要求人工 DSN 或预配置外部 DB。
- generated claim/evidence report 证明每个最终验收 bullet 都可追溯到 manifest `claim_id`、`acceptance_id` 或 `subclaim_id`、evidence `id`，并映射到 `CLAIM_RELEASE_GATE_TRACEABLE`。

以下任一情况存在时，即使代码看起来可用，也不能判定 GA：

- 任一 required/final claim 仍对应 `seed_gap_*_open` 或等价 open gap。
- 高风险 claim 只有 doc-only evidence，没有非文档自动证据。
- required evidence 缺少 repo-local `command`，或 command 不被唯一 gate 覆盖。
- `bash scripts/verify-ga-release.sh` 没有在干净 checkout 运行成功。
- evidence 依赖人工审批、会议、兄弟 repo、真实部署状态、人工 DSN、branch protection、真实 artifact
  或 GitHub 环境配置。
- optional positive evidence 被标成 default GA，或 `deployment-runtime-support` 被放入 required
  local GA evidence。

只有这条命令从干净 checkout 成功退出，且上述不能判定条件全部不存在，才能认为下一阶段开发交付满足 GA 收敛要求。
