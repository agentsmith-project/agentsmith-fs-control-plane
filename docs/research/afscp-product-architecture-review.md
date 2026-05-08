# AFSCP 产品与架构独立审查报告

Status: product-neutral research report.

本报告不是 GA gate，不是实施计划，也不是对既有计划或代码的修改建议单。它只记录本轮只读审查发现的主要问题、证据和建议方向。运行态的 `operator_intervention_required`、`operator_admin`、调用方审批引用、清理审批引用和审计化 operator 动作，属于系统安全控制和运行控制，不是 GA release gate 的人工审批条件；现有 GA gate 仍以仓库内自动化证据为边界。

## Executive Summary

当前方向强调“独立发布、产品中立、直达 GA”，这是合理的；但首发 GA 闭环的功能面仍然过宽，已经把 volume、namespace、repo lifecycle、JVS、templates、WebDAV、workload mount、audit、recovery、operator inspection 等能力同时纳入。问题不在于“必须回到复杂多阶段路线”，而在于需要收窄直接 GA 的首发闭环，把直接 GA 目标从全量平台改成可证明的最小闭环；至少应把 workload mount、template、purge 等高阶或高风险路径置于 capability-gated 之后。

技术实现已经有不少 repo-local gate、契约和测试，但部分 `auto_verified` 证据强度不足以覆盖文档中的完整声明。特别是 API 能排队 JVS-backed mutation，而 worker gate 关闭或不支持时，操作可能长期停留而不是在 API 层 fail-closed 或被 worker 终态化。operator 可观测性、真实端到端可用性、运行态修复路径、共享卷多租户残余风险和发布验证强度也需要进一步补证。

## 审查范围

- 产品理念：产品中立边界、独立 GA、调用方/连接器/编排器协作模型。
- 功能设计：repo lifecycle、template、WebDAV export、workload mount、operator inspection、quota、happy/failure journeys。
- 技术架构：API intake、operation recovery、worker capability gates、JVS runner、fence/session drain、PostgreSQL store、schema/OpenAPI/release gate。
- 安全与运维：凭据边界、operator intervention、purge approval reference、backup/restore、一致性边界、runbook、threat model。

## 主要结论

1. 直接 GA 可以保留，但首发闭环应更小、更可证明；否则当前 GA 范围像“全量平台首发”，风险面超过自动化证据的承载能力。
2. 独立 release 与真实用户价值需要分开判断：AFSCP 可以不依赖兄弟项目发布，但调用方、client connector、orchestrator 链路仍决定是否真正可用。
3. operator inspection 的需求与 GA 排除 list/search/aggregation API 存在张力；如果不增加只读 inspection surface，就必须明确 deployment tooling contract。
4. 多处契约与实现已经接近安全闭环，但 API admission、worker capability、stale mount lease、terminal evidence、restore lifecycle drain、operator repair 写路径仍有错位。
5. 当前 release gate 是有效基础，但证据主要覆盖 unit/text/contract baseline；对真实 Postgres、迁移、事务、WebDAV+Postgres、JVS binary provenance、race/concurrency、generated-client 等声明覆盖不足。

## Findings

### High Research Risk

#### Finding 1: 直接 GA 范围过宽，首发闭环难以证明

- Severity: High
- 证据: `docs/GA_PRE_DEV_READINESS.md:26-40` 将 volume、namespace、repo lifecycle、JVS save/restore、templates、WebDAV、workload mount、audit/recovery/operator inspection 全部列入 GA；`docs/PRODUCT_REQUIREMENTS.md:76-100` 的 GA Admission Criteria 覆盖 25 项；`docs/GA_PRE_DEV_READINESS.md:122-139` 又要求 schema/OpenAPI、WebDAV、mount、lifecycle、operation recovery、audit、runbook 等全部有证据。
- 问题说明: 当前首发 GA 不是单一可证明闭环，而是多个复杂能力同时达标。风险不是“不能直达 GA”，而是直接 GA 的对象太大，导致每个能力都需要强证据，否则任何薄弱点都会拖累整体可信度。
- 影响: GA 声明容易超过实际验证范围；高风险路径如 workload mount、template clone、purge 会把安全、编排器、JVS、存储删除等风险一次性拉入首发。
- 建议方向: 保持直接 GA 目标，但收窄直接 GA 的首发闭环，优先证明 namespace + repo create + save/history/restore-preview/discard 或 WebDAV 只读/读写中的最小闭环；workload mount、template、purge 至少 capability-gated，并要求运行环境显式打开后才进入可用面。

#### Finding 2: API admission 与 worker capability gate 可能不一致

- Severity: High
- 证据: `internal/api/operation_intake.go:146-209` 根据 route operation ID 创建 queued operation，没有看到与 worker capability gate 的直接对齐；`internal/workerapp/run_once.go:281-595` 只有对应 worker gate enabled 时才注册 repo create/lifecycle/purge/savepoint/template/restore executor；`internal/workerapp/run_once.go:782-879` 只有 enabled 的能力才列入 recovery，因此 worker gate 关闭时对应 operation 不进入 recovery list；`internal/operationexec/operation.go:126-134` 不支持的 handler 返回 unsupported support，但该路径只适用于已被 worker 扫描且没有 handler 的 operation。
- 问题说明: mutating API 可以接收并排队 JVS-backed mutation，但 worker 侧可能因为 gate 关闭而不扫描对应 operation，或在已扫描但无 handler 时返回 unsupported。当前证据显示这两类路径都没有与 API admission 共享同一能力判断。
- 影响: 调用方拿到 operation ID 后可能长期等待；operator 看到 queued/running 操作但没有清晰 terminalization 路径；重启恢复也可能因为能力开关变化导致行为不稳定。
- 建议方向: API admission 与 worker capability 必须共享同一能力矩阵；不能执行的操作应在 API 层返回 `CAPABILITY_DENIED`，或由 worker 将 unsupported queued operation 明确终态化为 failed/operator_intervention，并带稳定 error code 与 runbook reference。

#### Finding 3: Workload mount 安全闭环仍有运行态缺口

- Severity: High
- 证据: `docs/WORKLOAD_MOUNTS.md:126-140` 要求缺少 orchestrator contract 时返回稳定 capability error；`internal/api/workload_mount_handler.go:253-280` 获取 orchestrator mount plan 时只读 binding 后直接调用 plan reader；`internal/store/postgres/workload_mount_bindings.go:316-354` 的 plan SQL 检查 status、namespace/repo/volume/fence，但没有检查 `lease_expires_at > now`；`internal/workloadmount/reconcile.go:55-62` stale lease 只扫描并保持 blocking，不推进状态；`internal/store/postgres/workload_mount_bindings.go:107` 用 volume id 推导 `secret_ref`。
- 问题说明: mount plan retrieval 没有显式 lease expiry gate，过期但非终态的 binding 仍可能被拿到 plan；stale reconciliation 只是观察并保持 blocked，不能形成 terminal evidence；SecretRef 由 volume id 规则推导，缺少显式部署映射/轮换边界。
- 影响: 过期绑定、异常编排器或 secret 命名变化可能造成 plan 误发、无法回收或排障困难；restore/lifecycle 被 stale lease 长期卡住时，operator 缺少可证明的自动推进路径。
- 建议方向: plan retrieval 需要检查 lease freshness 或显式允许 teardown-only plan；stale reconciliation 应有清晰的 operator/tooling 接口来记录 terminal evidence；SecretRef 应来自部署配置或 volume capability record，而不是硬编码推导。

#### Finding 4: 发布 gate 存在，但证据强度不足以覆盖部分声明

- Severity: High
- 证据: `scripts/verify-ga-release.sh:12-16` 先跑 diff/shell/contractcheck，再跑 baseline；`scripts/verify-ga-baseline.sh:12-17` 主要是 `go test ./...` 和 contract verifier；`docs/READINESS_EVIDENCE.md:62-84` 多个 gate 标记 `auto_verified`；`docs/JVS_SMOKE_EVIDENCE_2026-05-05-v0.4.8.md:21-27` 记录 checksum OK，但 cosign 未本地验证；`docs/JVS_SMOKE_EVIDENCE_2026-05-05-v0.4.8.md:112-115` 也说明这只关闭 JVS runner gate，不等于真实存储 mutation GA。
- 问题说明: gate 是有效基础，且已有 workflow 运行 release script；但当前自动化更像 unit/text/contract baseline。缺少真实 Postgres migration/transaction integration、WebDAV+Postgres e2e、JVS binary cosign/smoke 自动化、race/concurrency gate、OpenAPI/JSON Schema/generated-client validation、机器可追溯 evidence manifest，以及 branch protection 可证明性、artifact/evidence manifest、tag/release trigger、permissions hardening 等 workflow hardening 证据。
- 影响: `auto_verified` 容易被理解为“生产 GA 级别已完全覆盖”，但部分声明仍只由文档或局部测试支撑。这里不是否定既有工作，而是证据颗粒度与声明强度不匹配。
- 建议方向: 将 evidence ledger 的 `auto_verified` 拆成更细的 evidence manifest，标注 unit/contract/text/integration/e2e/provenance/race/generated-client 等证据类型；关键路径增加真实数据库、迁移、并发和端到端 smoke。

### High

#### Finding 5: 独立 GA 与真实可用性被混在一起

- Severity: High
- 证据: `docs/GA_PRE_DEV_READINESS.md:20-24` 明确 GA 不依赖 first/reference consumer；`docs/PRODUCT_REQUIREMENTS.md:11-18` 定义了 admin、calling service、orchestrator、client connector、workload、operator 等参与者；`docs/PRODUCT_REQUIREMENTS.md:35-36` 要求 controlled exports 与 workload mount；`docs/EXPORT_WEBDAV.md:11-22` 的目标流是 Client -> Calling Product -> AFSCP -> WebDAV runtime。
- 问题说明: AFSCP 可以独立 release，但真实用户价值必须经过调用方授权、client connector 凭据转交、orchestrator plan 消费等链路。当前文档正确排除了兄弟项目作为 gate，但还缺少产品中立的 reference conformance/smoke 来证明链路可用。
- 影响: GA 可发布但不可用的风险会上升；问题可能被误归因到调用方或编排器，而不是接口契约缺口。
- 建议方向: 增加 product-neutral reference conformance/smoke，覆盖 export credential relay、orchestrator plan consumption、operation inspection、denied cases。该 smoke 不应成为兄弟项目 gate，而是本仓库的中立兼容性证据。

#### Finding 6: Operator observability 需求与 GA API 面不匹配

- Severity: High
- 证据: `docs/PRODUCT_REQUIREMENTS.md:17` 要求 operators inspect operations/logs/audit/health/recovery/intervention；`docs/PRODUCT_REQUIREMENTS.md:97` 要求 inspect operations、audit events、repo projections、volume/namespace health、stale leases、held fences、intervention records；但 `docs/GA_PRE_DEV_READINESS.md:38-56` 将稳定 API inspection surface 收敛到 operation by ID，并排除 list/search/aggregation；`docs/OPERATIONS_AND_AUDIT.md:111-135` 又要求 operator 能通过 runbook/tooling 查 correlated resource、intervention、stale leases、fences、audit lag。
- 问题说明: repo projection/list 能力已经存在，缺口不应概括为“完全缺少 inspection”。更准确的问题集中在 operation correlated lookup、intervention queue、held fence/session view、audit delivery lag 等 operator 日常定位入口；只保留 operation by ID 仍难支撑“先发现问题，再定位 operation ID”的运维路径。
- 影响: operator 仍可能依赖读库 SQL、日志和部署侧工具来补齐相关查询，导致不同部署可观测性不一致；事故中恢复速度、审计一致性和权限边界都可能下降。
- 建议方向: 在既有 repo projection/list 基础上补充只读 operator inspection surface，或在 GA 契约中明确 deployment tooling contract，包括 operation correlated lookup、intervention queue、held fence/session view、audit delivery lag、权限、分页/过滤、审计和脱敏要求。

#### Finding 7: Operator repair/intervention 写路径未契约化

- Severity: High
- 证据: `docs/OPERATIONS_AND_AUDIT.md:133-135` 要求能 release fence、mark terminal、revoke session、rotate Secret 或接受残余风险的动作必须有 operator role 和 reason；`docs/runbooks/ga-runbooks.md:24-31` 要求无法证明状态时进入 intervention 并保留 fence；`docs/runbooks/ga-runbooks.md:419-434` 只描述 crash 后重取 lease、恢复 fence、发 audit；`docs/API_CONTRACT_DRAFT.md:786-791` 仅说明 operator/admin 可 global inspection and repair，但没有 repair API 契约。
- 问题说明: 文档承认 operator repair/intervention 是安全控制，但缺少具体写路径契约：谁能写、写什么状态、需要哪些 proof、如何审计、如何防止误 release fence。
- 影响: 真事故中 operator 可能只能通过读库和临时 SQL 修复，形成审计缺口和一致性风险；也会让 `operator_intervention_required` 从安全状态变成长期卡死状态。
- 建议方向: 契约化最小 operator repair/intervention 写路径，至少包含 reason、evidence reference、affected IDs、before/after state、audit event，并限制为可证明的 fence release/session terminalization/operation terminalization。

#### Finding 8: Purge break-glass approval reference 仍偏弱

- Severity: High
- 证据: `docs/PRODUCT_REQUIREMENTS.md:45-46` 要求 purge 有 explicit request、retention check、operation record、audit event，以及 caller confirmation/approval reference/reason；`docs/API_CONTRACT_DRAFT.md:571-575` 要求 approval reference 和 reason，retention override 使用 approved break-glass policy；`docs/runbooks/ga-runbooks.md:296-315` 只要求验证 approval reference/reason/policy。
- 问题说明: 当前 approval reference 更像字符串或外部引用，没有说明校验方式、不可抵赖性、授权主体、过期时间、是否可重放、与 audit 的绑定关系。
- 影响: 对不可逆 purge 来说，弱引用可能无法证明删除合法性；也可能让 break-glass 变成普通字段，而不是强安全控制。
- 建议方向: 把 purge approval reference 定义为受控 evidence object 或可校验引用，包含审批主体、策略版本、scope、过期时间、reason、hash/correlation，并由 audit 绑定。

#### Finding 9: Backup/restore 缺少 control-plane 与 storage-plane 一致性边界

- Severity: High
- 证据: `docs/OPERATIONAL_READINESS.md:55-73` 的 GA backup scope 主要覆盖 PostgreSQL metadata、session、audit、config，并要求恢复后不重发 credential、不复活 purged repo；`docs/contracts/repo-lifecycle-v1.md:200-202` 要求无法证明 tombstoned/restored/purged 时进入 intervention。
- 问题说明: 备份范围清楚列了 control-plane metadata，但对 storage-plane payload/control root 的时间点、一致性、恢复顺序、purged storage 不得复活的技术边界还不够具体。
- 影响: 恢复 PostgreSQL 与恢复底层存储如果时间点不一致，可能出现 control-plane 认为 purged、storage-plane 仍存在，或 control-plane 认为 active、storage-plane 缺失的状态。
- 建议方向: 定义 control-plane/storage-plane restore consistency contract，包括 snapshot timestamp、purge tombstone marker、storage generation、reconciliation runbook 和禁止自动 reissue credential 的检查。

### Medium

#### Finding 10: WebDAV credential issuer wording 不一致

- Severity: Medium
- 证据: `docs/PRODUCT_REQUIREMENTS.md:15` 写 client connectors consume calling-product issued WebDAV export credentials；`docs/EXPORT_WEBDAV.md:17-22` 写 calling product asks AFSCP create export，AFSCP returns short-lived credentials，calling product returns one-time credential view；`docs/API_CONTRACT_DRAFT.md:326-332` 写 AFSCP stores session and returns one-time credential only on first create。
- 问题说明: “调用方签发 credential”和“AFSCP 返回/签发 credential”两个说法容易混淆责任边界。安全上更准确的是：AFSCP 向 trusted caller 签发短期 WebDAV access credential，caller relay 给 client connector。
- 影响: 凭据所有权、审计责任、泄露响应和 reissue 语义可能被调用方误解。
- 建议方向: 统一措辞为“AFSCP 向 trusted caller 签发，caller relay 给 client connector；client connector 不直接调用 AFSCP，caller 不自行生成 WebDAV 密码”。

#### Finding 11: Template 能力范围与常见心智不匹配

- Severity: Medium
- 证据: `docs/PRODUCT_REQUIREMENTS.md:32-34` 定义 namespace-scoped immutable templates，并默认拒绝 cross-namespace clone；`docs/GA_PRE_DEV_READINESS.md:86-88` 冻结 namespace-scoped、cross-namespace reject、cross-volume reject；`docs/API_CONTRACT_DRAFT.md:697-721` 要求新建 template 时 fresh save point，同 namespace clone，volume mismatch reject。
- 问题说明: “template”通常让用户预期可复用、可发布、可跨空间传播；当前能力实际上更像同 namespace/same volume 的 repo clone primitive。
- 影响: 调用方可能把它设计成模板市场或跨业务模板，结果被 namespace/volume 限制打断；产品语义与技术边界不一致。
- 建议方向: 二选一：要么降级命名/定位为 repo clone primitive；要么定义受控 admin import/publish 机制，把跨 namespace/volume 的模板发布作为明确受控能力。

#### Finding 12: Lifecycle vocabulary 仍带产品工作流倾向

- Severity: Medium
- 证据: `docs/PRODUCT_REQUIREMENTS.md:27-29` 使用 archive/delete/tombstone/purge 并称 common product semantics；`docs/API_CONTRACT_DRAFT.md:262-270` 说明这些是 product-familiar storage semantics；`docs/contracts/repo-lifecycle-v1.md:23-29` 又提供 product-facing mapping；同时 `docs/PRODUCT_REQUIREMENTS.md:69` 把 product-specific lifecycle vocabulary inside AFSCP 列为 non-goal。
- 问题说明: archive/delete/tombstone/purge 是必要 storage-state，但文档有时用“product semantics”表述，容易让调用方把产品 catalog lifecycle 直接映射到存储 lifecycle。
- 影响: 调用方可能误以为 AFSCP 管理产品删除、恢复、展示状态，导致业务工作流与 storage state 耦合。
- 建议方向: 强调 storage-state vocabulary/mapping：AFSCP 只表达存储可访问性、保留、恢复、清理状态；产品 catalog、显示名、用户删除 UX 仍由调用方拥有。

#### Finding 13: Quota 字段容易误导

- Severity: Medium
- 证据: `docs/PRODUCT_REQUIREMENTS.md:47` 说明 `quota_bytes_default` 只是 policy record/enforcement hook；`docs/API_CONTRACT_DRAFT.md:205-228` schema 示例中仍把 `quota_bytes_default` 放在 binding 主字段；`api/schemas/afscp-internal-v1.schema.json:482-485` 通过 description 说明不是硬限制。
- 问题说明: 字段名 `quota_bytes_default` 很容易被 caller 理解为已经 enforced 的容量限制；现有说明依赖文字，不依赖机器可读状态。
- 影响: 调用方可能向用户承诺硬 quota，但实际 deployment 未启用 directory quota enforcement。
- 建议方向: 增加 machine-readable `quota_enforcement_status`、`effective_quota_bytes`、`enforced=false` 等信号，或将字段命名更保守为 `quota_policy_bytes_default`。

#### Finding 14: 概念模型暴露过多，调用方心智负担偏高

- Severity: Medium
- 证据: `docs/PRODUCT_REQUIREMENTS.md:20-47` 同时暴露 volume、namespace、binding、repo lifecycle、JVS、template、export、mount、fence、quota 等概念；`docs/API_CONTRACT_DRAFT.md:162-490` 展开 Volume、NamespaceVolumeBinding、Repo、RestorePlan、RepoTemplate、ExportSession、OrchestratorMountPlan；`docs/OPERATIONS_AND_AUDIT.md:7-39` OperationRecord 字段也暴露大量内部关联 ID。
- 问题说明: 对外部调用方而言，核心心智可以收敛为 namespace、repo、savepoint、access_session、operation。mount plan、fence、drain、restore plan internals 更适合 operator/orchestrator 或内部实现。
- 影响: 调用方会学习并依赖内部概念，后续演进难度上升；也增加误用 mount/fence/drain 的风险。
- 建议方向: 定义分层心智：caller API 只暴露最小 storage primitive；orchestrator 看到 mount plan；operator 看到 fence/drain/intervention；内部实现保留 JVS/control-root 细节。

#### Finding 15: 缺少产品中立 happy paths 与 failure journeys

- Severity: Medium
- 证据: 文档有目标流和契约，如 `docs/EXPORT_WEBDAV.md:11-22`、`docs/API_CONTRACT_DRAFT.md:816-839`，也有 runbook 故障处理如 `docs/runbooks/ga-runbooks.md:127-174`、`docs/runbooks/ga-runbooks.md:262-315`；但 `rg` 未发现专门的 product-neutral happy path/failure journey 文档，现有内容分散在契约与 runbook。
- 问题说明: 契约和 runbook 描述“单点行为”，但缺少端到端 journey：创建 namespace/repo、保存、导出、撤销、恢复、template clone、mount、失败重试、权限拒绝、stale session 等如何串起来。
- 影响: 调用方、QA、operator 很难形成共同验收语境；也不利于发现跨接口状态机漏洞。
- 建议方向: 增加 product-neutral happy paths/failure journeys，不作为产品 UI 计划，只作为 API/运行态验收故事和 smoke test 索引。

#### Finding 16: Restore archived/tombstoned 的 session drain 实现比契约更严格

- Severity: Medium
- 证据: `docs/contracts/repo-lifecycle-v1.md:96-105` 只要求 archive/delete/purge 等等待所有 session terminal；`docs/contracts/operation-state-machine-v1.md:279-282` 也只写 archive/delete/purge 需要 confirmed terminal non-accessing；但 `internal/repoexec/lifecycle_executor.go:328-335` 对 archive、restore_archived、delete、restore_tombstoned 都返回 requires session drain；`internal/store/postgres/repo_lifecycle_operations.go:169-177` success commit SQL 对所有 lifecycle operation 共同使用 `no_sessions` CTE。
- 问题说明: 实现对 restore archived/tombstoned 也要求 no sessions，可能比契约更严格。恢复一个 archived/tombstoned repo 理论上应已经不可普通访问，额外 no_sessions 可能来自安全保守，但契约没有明说。
- 影响: 恢复操作可能被历史 session 残留卡死，且调用方无法从契约预期该行为。
- 建议方向: 对齐契约与实现：如果 restore 也必须 no_sessions，写入 contract 和 stable error；如果不是，调整实现只对 archive/delete/purge 执行 drain。

### Low

#### Finding 17: 文档一致性存在旧 wording 和实现状态冲突

- Severity: Low
- 证据: `docs/PRODUCT_REQUIREMENTS.md:49-53` 仍写 GA 由 generated-client compatibility evidence 等关闭，措辞与当前单一 repo-local gate 容易混读；`cmd/README.md:5-11` 仍说 API 不实现 WebDAV、mount、save/restore、template handlers yet，但代码和文档已有相应 handler 与 gateway；`docs/GA_RELEASE_GATES.md:11-21` 已把 gate 明确为 repo-local command。
- 问题说明: 个别文档仍保留旧 evidence wording 或早期实现状态描述，容易让 reviewer 误判当前能力或 gate 边界。
- 影响: 主 agent 验收、release note、operator handoff 时可能出现口径冲突。
- 建议方向: 后续单独清理文档口径：PRODUCT_REQUIREMENTS 的 evidence wording 与 GA_RELEASE_GATES 对齐；cmd README 与当前实现状态对齐。本报告不修改这些文件。

#### Finding 18: 共享卷多租户 residual risk threat model 不足

- Severity: Low
- 证据: `docs/security/threat-model.md:3-13` 列出了 credentials、managed volume root、repo payload、JVS metadata、operation/audit、mount plan 等资产；`docs/security/threat-model.md:16-92` 覆盖 credential exposure、confused deputy、path escape、JVS metadata tampering、template leak、operation loss、restore racing writers；`docs/GA_PRE_DEV_READINESS.md:80-82` 冻结 shared managed volume 和 separated control/payload roots。
- 问题说明: threat model 覆盖了主要安全边界，但对 shared-volume 多租户 residual risk 还不够明确，例如同一 managed volume 内 namespace 隔离失败、volume-level admin 误配置、backup restore 跨 namespace 残留、底层 POSIX 权限漂移。
- 影响: 安全评审可能认为 shared-volume 默认策略缺少残余风险说明和补偿控制。
- 建议方向: 增加 shared-volume residual risk section，列出隔离假设、失败模式、检测指标、补偿控制和何时升级到 dedicated volume。

## 建议优先级

这些不是实施阶段或 GA gate，只是研究报告建议关注顺序。

第一优先关注:
- 收窄直接 GA 的首发闭环，明确哪些能力默认 capability-gated。
- API admission 与 worker capability 共享矩阵；unsupported operation 必须 fail-closed 或终态化。
- 修补 workload mount plan lease/stale/SecretRef 契约缺口。
- 将 release evidence 拆成机器可追溯 manifest，补关键 integration/e2e/provenance/race 证据。

第二优先关注:
- 增加 product-neutral reference conformance/smoke，但不作为兄弟项目 gate。
- 明确 operator inspection/tooling contract，并契约化最小 operator repair/intervention 写路径。
- 强化 purge approval reference 和 backup/restore 一致性边界。
- 对齐 restore lifecycle drain 契约与实现。

第三优先关注:
- 统一 WebDAV credential issuer wording。
- 将 template 定位为 clone primitive，或定义受控 admin import/publish。
- 为 quota 增加 effective/enforced 信号或更保守命名。
- 增加 product-neutral happy paths/failure journeys。
- 清理旧文档 wording 和 cmd README 状态描述。

## 需要进一步验证的问题

1. 当前 OpenAPI/JSON Schema 是否已有 generated-client 的真实生成与编译验证，还是只做 schema/route parity。
2. `verify-ga-release.sh` 在 CI workflow 中是否强制运行，是否有分支保护、artifact 保存和 evidence manifest。
3. 是否存在真实 Postgres migration + transaction integration 测试，覆盖 crash/retry/idempotency/fence release。
4. WebDAV gateway 是否有连接真实 Postgres 的 e2e smoke，覆盖 credential begin/end/reconcile，而不仅是 handler/store 单测。
5. JVS binary 是否有 cosign/bundle 自动验证，而不仅是本地 checksum 记录。
6. Workload mount orchestrator 是否已有中立 conformance fixture，证明 Secret RBAC、payload-only mount、heartbeat/release/revoke、terminal evidence。
7. Operator intervention 写路径当前是否预期由部署侧工具实现；如果是，工具契约和审计格式在哪里定义。
8. Shared-volume 隔离在实际部署中依赖哪些底层权限、bucket policy、CSI 配置和备份策略。
