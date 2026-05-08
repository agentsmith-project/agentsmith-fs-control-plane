# AFSCP GA 收敛工作计划

Status: background convergence map; not the active development execution handoff.

本文档根据 `docs/research/afscp-product-architecture-review.md` 和产品、架构、安全运维、QA/release 四个审查视角整理。目标是把下一轮工作收敛到一个清晰结果：AFSCP 作为产品中立、可独立运行、可独立 gate、可独立 release 的共享文件系统控制面，达到可交给开发团队直接实现和验证的 GA 质量。

当前唯一开发执行 handoff 是 `docs/GA_NEXT_PHASE_DEVELOPMENT_HANDOFF_PLAN.md`。本文档只保留为 research-to-workstream 背景映射和审查语境，不是并行执行入口，不覆盖 handoff 中的 P0-P5、claim/evidence、selector/final gate 或 next-slice 口径。

本文档不是路线图，不定义复杂 rollout，也不把任何外部业务系统作为验收条件。所有 GA 通过条件必须由本仓库自己的代码、契约、schema、OpenAPI、测试、脚本和证据完成；具体开发切片、语义依赖和验收 owner 以 `docs/GA_NEXT_PHASE_DEVELOPMENT_HANDOFF_PLAN.md` 为准。

## 产品定位

AFSCP 的职责是提供稳定的 storage control plane primitive：

- 管理 namespace、managed volume binding、repo、savepoint、restore、受控导出、workload access、lifecycle、operation、audit 和 recovery。
- 隐藏底层 JuiceFS/JVS/control-root 细节，只把必要的产品中立能力暴露给 trusted caller、orchestrator 和 operator。
- 在能力不可用、运行态不安全、审批证据不足或一致性无法证明时 fail-closed，并返回稳定错误、审计证据和可恢复状态。
- 通过本仓库唯一 GA gate 证明自身质量，不依赖兄弟项目、不依赖人工审批作为 release gate。

## 收敛目标

下一轮开发不扩大功能面，重点解决研究报告中的主要问题：

- 直接 GA 闭环过宽，部分能力声明强于证据。
- API admission 与 worker capability gate 可能不一致。
- operation 存在进入长期 queued/running 的风险。
- workload mount 的 lease、stale binding、teardown-only plan、SecretRef 边界不够完整。
- operator inspection 与 repair/intervention 写路径不够契约化。
- purge break-glass approval evidence 太弱。
- backup/restore 缺少 control-plane 与 storage-plane 一致性边界。
- WebDAV credential issuer、template、quota、lifecycle wording 和调用方心智需要收口。
- release evidence 需要从文档声明升级为可追溯、可自动判断、可复现的证据链。

完成后的目标状态：

- 默认 GA 闭环可证明：namespace/volume binding、repo create/get、save/history/restore-preview/restore-run/discard、WebDAV export/revoke、operation/audit/recovery。
- 高风险能力可以保留在同一 GA 产品中，但必须 capability-gated：workload mount、template/clone、purge/break-glass、需要特定 runtime 的 mutation 只能在 capability ready 时接受请求。
- 不满足 capability、lease、approval、fence、session drain 或 consistency 条件时，API 稳定拒绝，或 worker 将历史 operation 明确终态化。
- operator 可以发现、定位、阻断、修复和审计最小必要问题，不依赖临时 SQL 作为唯一手段。
- 每条 GA 声明都有 repo-local 自动化证据，并被 `scripts/verify-ga-release.sh` 覆盖。

## Research finding 到 workstream 映射

这张表只帮助开发团队从 `docs/research/afscp-product-architecture-review.md` 快速定位工作域；它不是阶段路线，也不改变本文档的 GA 收敛方向。

| 研究报告主题 | 对应工作域 | 开发交接重点 |
| --- | --- | --- |
| GA scope 过宽、能力声明强于证据 | Workstream A、Workstream I | 收敛默认 GA 闭环；高风险能力按 capability-gated 语义接单并自动证明 |
| Capability/admission 与 worker gate 不一致 | Workstream A、Workstream B | API admission、worker recovery、readyz、release evidence 共用同一 capability matrix |
| Operation 长期 queued/running 或 unsupported handler | Workstream B | 每类 mutation 都有稳定终态、audit evidence 和 recovery/idempotency 测试 |
| Workload mount lease、stale binding、SecretRef 边界 | Workstream C、Workstream D | lease freshness、teardown-only plan、stale intervention、operator-only runtime config 与 redaction/RBAC |
| Release evidence 强度不足、`auto_verified` 颗粒度过粗 | Workstream I | evidence manifest、repo-local 子 gate、Postgres/WebDAV/JVS/generated-client/race 证据接入唯一 GA gate |
| 独立 GA 与真实可用性之间缺少中立验证 | Workstream H、Workstream I | repo-local product-neutral conformance/smoke 覆盖 credential relay、mount plan consumption、inspection 和 denied cases |
| Operator inspection/repair/intervention 契约不足 | Workstream D | 最小 inspection surface、受控 repair 写路径、reason/evidence/before-after/audit/redaction |
| Purge break-glass approval reference 太弱 | Workstream E | 结构化 approval evidence、scope/expiry/policy/replay protection、purge session drain 与审计绑定 |
| Backup/restore 控制面与存储面一致性边界不足 | Workstream F | restore consistency contract、reconciliation mode、tombstone/purge marker、audit replay idempotency |
| WebDAV credential issuer 责任边界不一致 | Workstream G | AFSCP 签发短期 credential 给 trusted caller；caller relay；raw secret first-create-only |
| Template 心智与 clone primitive 不一致 | Workstream H | GA 语义收口为 namespace-scoped repo clone；cross-namespace/cross-volume 稳定拒绝 |
| Quota 字段容易被误解为硬 enforcement | Workstream H | 暴露机器可读 quota enforcement status，并进入 schema/OpenAPI/client fixture |
| Lifecycle/restore archived/tombstoned session drain 契约错位 | Workstream F、Workstream H | 对 restore 是否需要 `no_sessions` 做一等契约决策，并补 API/worker/recovery 测试 |
| Shared-volume 多租户 residual risk 未充分威胁建模 | Cross-cutting: Shared-volume residual risk | 隔离假设、失败模式、补偿控制、dedicated-volume escalation 与 residual-risk acceptance 审计 |
| stale wording、cmd README、文档一致性清理 | Workstream H、Workstream I、Developer Handoff 清单 | doc guard、contractcheck、schema/OpenAPI drift guard 与 stale wording cleanup |

## 非目标

- 不做调用方产品授权、用户 UI、业务 catalog、任务、项目、workspace lifecycle 或业务工作流。
- 不把任何外部业务系统的集成结果作为 AFSCP GA gate。
- 不依赖人工审批、人工 review 或会议结论作为 release gate；运行态 approval/intervention 只属于安全控制。
- 不把普通 caller、client connector 或 workload 暴露到 raw JuiceFS root、metadata URL、SecretRef 或底层 credential。
- 不做通用运维搜索平台；只补齐 operator 必要 inspection 与受控 repair。
- 不做 namespace delete。
- 不把 namespace-scoped clone 扩展成跨 namespace template marketplace，除非未来另行定义受控发布能力。
- 不用 worker 扫不到、后台沉默或长期 pending 来表达能力不可用。

## 架构收敛方案

以下 Workstream A-I 仅为背景分类和 research finding 映射；实际开发切片、语义依赖、claim/evidence owner 与 next-slice 选择以 `docs/GA_NEXT_PHASE_DEVELOPMENT_HANDOFF_PLAN.md` 的 P0-P5 为准。

以下 Workstream A-I 是并行问题域，不是 rollout 阶段，也不要求分阶段交付；开发团队应围绕同一个目标一次性收敛到唯一 GA gate。

### Workstream A: GA 闭环与 capability matrix

要解决的问题：

- 当前 GA 功能面覆盖 repo lifecycle、JVS、WebDAV、workload mount、template、purge、audit、recovery 等多个高风险路径，证据强度不均衡。
- API、worker、readiness、release evidence 对能力可用性的判断可能不一致。

方案：

- 定义一份产品中立 capability matrix，作为 API admission、worker executor/recovery、readyz、operator inspection 和 release evidence 的共同依据。
- capability matrix 必须区分 `required_for_service_ready`、`required_for_default_ga`、`optional_gated`、`namespace_policy` 和 `volume_runtime_capability`。
- `required_for_service_ready` 只表达服务是否能对默认必选能力接单；`optional_gated` 能力关闭时不能把整个服务误判为 not ready。
- `required_for_default_ga` 表达默认 GA 闭环必须自动证明的能力；`optional_gated` 表达高风险能力可以在同一产品中存在，但只有 namespace policy 与 volume/runtime capability 同时允许且 ready 时才接受新 mutation。
- `namespace_policy` 表达某 namespace 是否允许使用某能力；`volume_runtime_capability` 表达 operator/orchestrator 配置的 runtime 是否真的可执行该能力。
- API、worker、readyz、operator inspection 和 release evidence 必须使用同一 matrix 的同一字段语义，不能把 service ready、default GA、optional gated 或 runtime config 混成一个布尔值。
- capability 至少表达能力是否 supported、configured、ready，以及不可用时的稳定 error code、audit 事件、runbook reference。
- API intake 在创建新的 mutating operation 前检查 capability；不能执行的新请求 fail-closed。
- API capability admission 不能破坏 idempotent replay：相同 idempotency key/hash 的 replay 必须先返回既有 operation；capability denial 只阻止新的 mutation admission。
- 对历史已经入队但当前 capability 不可用的 operation，worker 必须产生明确终态或 `operator_intervention_required`，不能无限等待。
- 高风险能力不是从产品中删除，而是在证据不足或 runtime 未 ready 时以稳定拒绝语义存在。

TDD 与自动化验收：

- 先写失败测试：关闭 JVS/WebDAV/workload/template/purge capability 时，对应 mutation 不会创建永久 queued operation。
- 先写 idempotent replay 测试：同 key/hash replay 返回既有 operation，不因当前 capability 状态改变而变成 denied。
- 先写 worker recovery 测试：历史 queued operation 在 capability 关闭、handler 不支持或 runtime 不满足时会终态化或进入 intervention。
- 先写 readyz/contract 测试：API、worker、readyz 返回同一 capability 状态。
- readyz 测试必须证明 optional-gated 能力关闭不会污染 `required_for_service_ready`。
- denied case 必须有稳定 error envelope；如果没有 operation record，也必须有 audit denied evidence。

交付结果：

- capability contract。
- stable capability error/audit catalog。
- API admission 与 worker recovery 的一致性测试。
- release evidence manifest 中的 capability 证据映射。

### Workstream B: Operation terminalization 与 recovery

要解决的问题：

- operation 可能在 queued/running 中长期停留，调用方和 operator 无法判断是等待、失败还是需要人工运行态干预。
- unsupported handler、runtime 缺失、fence 不安全、lease 过期等路径缺少统一 terminal evidence。

方案：

- 每类 mutation 都必须定义 terminalization 规则：`succeeded`、`failed` 或 `operator_intervention_required`。
- `operator_intervention_required` 是安全运行状态，不是 release 审批；进入该状态时必须保留必要 fence/session blocking，并提供 runbook/evidence reference。
- recovery loop 必须识别 unsupported、capability denied、lease expired、fence uncertain、JVS/runtime unavailable、audit outbox blocked 等状态。
- 所有 terminalization 都必须 idempotent，重复执行不能产生新的外部语义。

TDD 与自动化验收：

- operation state machine contract tests 覆盖 unsupported、capability denied、runtime unavailable、crash/retry、audit outbox replay。
- worker recovery tests 证明重启后 operation 不会丢失、重复执行或长期悬挂。
- concurrent mutation tests 覆盖同一 repo 的 save/restore/template/lifecycle 串行化。
- 精确 race gate 覆盖关键并发包，不默认扩大到无关包。

交付结果：

- 更新后的 operation state machine contract。
- worker recovery 单测与小范围 race 测试。
- terminal evidence 与 audit event schema。

### Workstream C: Workload access 安全闭环

要解决的问题：

- mount plan retrieval 对 lease freshness、teardown-only plan、stale binding 的语义不够完整。
- stale reconciliation 只能保持 blocked，缺少 operator 可见的推进路径。
- SecretRef 由规则推导，部署配置、轮换和权限边界不够明确。

方案：

- mount plan 领取必须检查 lease freshness。
- 过期、stale 或不安全 binding 不能重新授权 workload access；需要 teardown 时只能返回 teardown-only plan。
- stale binding 必须进入 operator 可见 inspection/intervention 流，并保留 fence/session blocking，直到有安全证据。
- SecretRef 应来自 orchestrator/operator-only runtime config，不是 caller-visible `Volume.capabilities`。
- runtime config 必须有 schema、redaction 和 RBAC gate，普通 caller 不得看到 raw SecretRef。
- workload access 仍是产品中立基础能力，不绑定具体编排平台业务逻辑。

TDD 与自动化验收：

- expired lease 不返回普通 issuance plan。
- releasing binding 只能获得 teardown-only plan。
- stale binding 进入 operator inspection/intervention，并阻止 restore/lifecycle 的危险推进。
- runtime config schema/redaction/RBAC、SecretRef redaction、payload-only mount、heartbeat/release/revoke 都有 contract/e2e fixture。

交付结果：

- workload binding contract 更新。
- mount plan freshness 与 teardown-only 测试。
- operator stale binding inspection 证据。

### Workstream D: Operator inspection 与 repair/intervention

要解决的问题：

- operator 可观测面不足以支持先发现问题再定位 operation/resource。
- repair 写路径没有产品中立契约，容易退化为临时 SQL 和审计缺口。

方案：

- 定义最小 operator inspection surface：correlated operation lookup、intervention queue、held fence/session view、audit outbox lag、stale mount lease、runtime request recovery status。
- 定义最小 operator API/CLI/tooling contract，不要求一次性做成完整 OpenAPI 管理平台。
- 定义最小 repair/intervention 写路径，只允许受控动作：terminalize operation、release writer/lifecycle fence、revoke/terminalize session、记录 residual-risk acceptance。
- 每个 repair 必须包含 operator identity、reason、evidence reference、affected IDs、before/after state、correlation ID 和 audit event IDs。
- 无法证明安全状态时默认保持 blocking，不允许强行 release。

TDD 与自动化验收：

- 只有 operator role 可以执行 repair。
- namespace-scoped caller 不能查看或修复 global/operator records。
- 缺少 reason/evidence 的 repair 请求失败。
- release fence 前必须证明 operation/session/runtime/audit 状态安全。
- repair、residual-risk acceptance、session revoke、terminalize 都产生审计事件并脱敏。

交付结果：

- operator inspection/repair contract。
- repair authorization、audit、redaction、idempotency 测试。
- operator runbook 与 API contract 对齐。

### Workstream E: Purge approval evidence

要解决的问题：

- break-glass purge approval reference 过于自由，无法稳定证明审批主体、scope、有效期、策略版本和不可重放性。
- 不可逆删除需要比普通字符串更强的运行态证据。

方案：

- 普通 retention purge 继续按 policy。
- break-glass purge 默认关闭；未配置可验证 approval capability 时 fail-closed。
- approval evidence 必须结构化，至少表达 approver/subject、policy name/version、approved scope、repo/action、reason、expires_at、correlation/hash、request binding 或 replay protection。
- audit 绑定 approval 摘要或 hash，不记录敏感审批材料。
- purge 前必须确认没有 active 或 uncertain export/workload access session。

TDD 与自动化验收：

- 缺少、过期、scope 不匹配、policy 不匹配、replay 的 approval evidence 全部拒绝。
- 成功 purge 后只保留最小 control-plane audit/idempotency record。
- purged repo 不可 restore、不可 export、不可 mount、不可 template/clone。
- purge denied/succeeded 都有审计证据。

交付结果：

- purge approval evidence contract。
- purge lifecycle 与 audit schema 更新。
- purge approval negative tests 与 idempotency tests。

### Workstream F: Backup/restore 一致性

要解决的问题：

- 当前备份口径主要覆盖 control-plane metadata，对 storage-plane payload/control-root 的时间点、一致性、恢复顺序和 purged storage residual 边界不够明确。

方案：

- 定义 restore consistency contract，覆盖 control-plane snapshot timestamp、storage generation 或等价 marker、tombstone/purge marker、restore reconciliation mode。
- 恢复后进入 reconciliation mode，在一致性检查完成前禁止新 credential、mount plan、restore-run 和 purge。
- 不自动重发 WebDAV credential。
- 不复活 purged repo。
- metadata 与 storage 不一致时进入 operator intervention，而不是静默修复。
- audit outbox replay 必须 idempotent。
- restore archived/tombstoned 与 active session drain 的契约必须做一等决策：要么 contract 明确 restore 也需要 `no_sessions` 并定义 stable error/audit；要么实现放松到现有 contract，只在 archive/delete/purge drain。

TDD 与自动化验收：

- metadata active 但 storage 缺失，进入 intervention。
- metadata purged 但 storage residual 存在，进入 intervention 并禁止访问。
- tombstoned repo 在 retention 内按 contract 可恢复。
- restore archived/tombstoned 的 session drain 行为必须有 contract test、API test 和 worker/recovery test。
- purged repo 不能通过备份恢复复活。
- operation store 恢复后不会二次返回 WebDAV password。
- audit replay 多次执行不产生重复外部语义。

交付结果：

- restore consistency contract。
- repo lifecycle/session drain decision record。
- backup/restore simulation 或 integration fixture。
- recovery mode gate 与 operator runbook 更新。

### Cross-cutting: Shared-volume residual risk

要解决的问题：

- shared managed volume 的 namespace 隔离假设、失败模式和补偿控制需要明确，避免把 volume-level 风险隐藏在普通 capability 文案里。

方案：

- 明确同一 managed volume 内 namespace 隔离假设：caller 不能提供 raw path，namespace path/subdir 由 AFSCP 生成并校验。
- 明确失败模式：path traversal、symlink escape、cross-namespace resource mismatch、volume-level admin 误配置、backup/restore residual data、POSIX/CSI/subPath 权限漂移。
- 明确补偿控制：payload-only access、gateway/path policy、SecretRef redaction、runtime config RBAC、audit correlation、operator inspection。
- 明确何时必须升级为 dedicated volume：隔离证据不足、合规要求需要物理/volume 级隔离、shared volume residual risk 无法被 operator 接受时。
- residual-risk acceptance 只能由具备对应权限的 operator/admin 记录，必须包含 reason、evidence reference、scope、expiry 或 review point，并产生审计事件。

TDD 与自动化验收：

- path traversal、symlink escape、`.jvs` 访问、cross-namespace resource mismatch 全部 fail-closed。
- 普通 caller 永远拿不到 raw root path、metadata URL、SecretRef 或 JuiceFS credential。
- residual-risk acceptance 缺少 reason/evidence/scope 时失败，成功时写入审计并脱敏。

交付结果：

- shared-volume residual risk threat model。
- dedicated-volume escalation rule。
- residual-risk acceptance contract 与 audit tests。

### Workstream G: WebDAV export credential 与 gateway 证据

要解决的问题：

- 文档中 credential issuer 责任边界不一致。
- WebDAV 关键安全路径需要真实 ledger/e2e 证据支撑。

方案：

- 统一表述：AFSCP 向 trusted caller 签发短期 WebDAV credential，caller relay 给 client connector；client connector 不直接调用 AFSCP，caller 不自行生成 WebDAV password。
- credential 只在首次 create response 返回；idempotent replay 不返回 raw secret。
- GET export 只返回 redacted session。
- AFSCP 只存 verifier，不存 raw password。
- gateway 是 policy boundary，必须拒绝 `.jvs`、control root、raw path、path traversal、Destination escape。
- revoke/expiry 后 future request 必须失败，runtime request ledger 不存 password、host path 或 sensitive path material。

TDD 与自动化验收：

- first-create-only credential test。
- replay/no-reissue test。
- revoke/expiry deny test。
- read-only method policy test。
- `.jvs`、path traversal、Destination escape deny test。
- gateway crash 后 stale runtime request recovery test。
- WebDAV e2e 必须使用真实 Postgres ledger 和 repo-local gateway/runtime fixture，不只测 handler。
- mock 可以辅助单测，但不能关闭 WebDAV GA evidence。

交付结果：

- WebDAV contract wording 更新。
- credential redaction/schema guard。
- WebDAV e2e gate 证据。

### Workstream H: Template/clone、quota 与调用方心智

要解决的问题：

- 当前 template 更像 namespace-scoped same-volume clone primitive，与“可发布模板”心智不一致。
- quota 字段容易被误解为已经硬 enforcement。
- lifecycle vocabulary 容易被误读成业务 workflow。
- 概念模型对普通 caller 暴露过多。

方案：

- 将 GA 语义收口为 namespace-scoped repo clone primitive；cross-namespace、cross-volume 默认稳定拒绝。
- 如果未来要做跨 namespace 发布，必须另行定义受控 admin import/publish 能力，不混入当前 GA 收敛。
- 增加机器可读 quota enforcement status，例如表达 effective quota、policy-only、not-enforced 或 runtime-enforced。
- lifecycle vocabulary 统一为 storage-state：archive/delete/tombstone/purge 只表达存储访问性、保留、恢复、清理，不表达业务 catalog 状态。
- 分层表达概念：caller 关注 namespace/repo/savepoint/access session/operation；orchestrator 关注 mount plan；operator 关注 fence/drain/intervention；JVS/control-root 是内部实现细节。

TDD 与自动化验收：

- cross-namespace/cross-volume clone 默认拒绝并返回稳定 error。
- quota enforcement status 出现在 schema/OpenAPI/client fixture 中，调用方不能只从文字判断 enforcement。
- lifecycle state 与业务状态 wording 通过 doc guard 或 contractcheck 防回退。
- product-neutral happy/failure journeys 覆盖创建、保存、导出、撤销、恢复、拒绝、stale session、intervention。
- repo-local product-neutral conformance/smoke 覆盖 credential relay、orchestrator mount plan consumption、operation inspection 和 denied cases；不得引入任何业务项目名，也不作为兄弟项目 gate。

交付结果：

- template/clone contract 更新。
- quota schema/OpenAPI 更新。
- lifecycle wording 与 journey 文档。
- schema/client fixture 编译证据。

### Workstream I: Evidence manifest 与 GA gate

要解决的问题：

- 目前 `auto_verified` 颗粒度不足，容易把 unit/text/contract baseline 误读为完整生产证据。
- JVS binary provenance、真实数据库事务、WebDAV e2e、generated-client、race/concurrency 等证据需要进入 repo-local gate。

方案：

- 保留唯一 authoritative gate：

```bash
bash scripts/verify-ga-release.sh
```

- 新增机器可读 evidence manifest，映射 GA 声明、风险、能力、证据类型、命令和覆盖项。
- `verify-ga-release.sh` 直接或间接调用所有 required evidence command。
- 高风险 gate 不能只有 doc/text evidence。
- workflow 必须运行唯一 authoritative script，并使用最小权限；release/tag 证据应保存 manifest artifact。

建议 evidence 类型：

- `unit`
- `contract`
- `schema`
- `openapi`
- `generated-client`
- `integration`
- `e2e`
- `provenance`
- `race`
- `doc-guard`

建议 repo-local gate 覆盖：

- capability matrix contract tests。
- operation terminalization/recovery tests。
- Postgres migration/transaction/idempotency/lease/fence/audit outbox integration tests。
- WebDAV gateway + ledger e2e。
- JVS pinned public release binary + checksum + release-provided signature/bundle verification，以及 save/history/restore-preview/restore-run/discard/clone/doctor smoke。
- 如果 JVS 上游缺少某类 provenance 证明，evidence manifest 必须记录可自动验证的替代证据；不能用文字声明代替。
- repo-local generated-client 最小 fixture 编译，不扩展成多语言兼容矩阵。
- product-neutral conformance/smoke。
- Postgres integration gate 自动启动临时 Postgres 或使用 CI service，不能要求人工提供 DSN。
- precise race/concurrency tests。
- evidence manifest verifier。
- markdown/contract wording guard。

交付结果：

- evidence manifest。
- gate verifier。
- 更新后的 release gate 文档。
- CI workflow hardening 配置检查。
- workflow hardening 的 repo-local 边界是检查 workflow YAML 是否声明调用唯一脚本、最小权限、artifact/tag trigger 配置；branch protection、实际 artifact 是否存在、GitHub 环境设置不能作为本地 gate 通过条件。

## TDD 工作方式

每个 workstream 都按同一顺序推进：

1. 先更新目标 contract/schema/OpenAPI 或测试 fixture，让当前实现失败。
2. 再实现最小产品中立行为。
3. 然后补充 audit、redaction、idempotency、error envelope 和 runbook evidence。
4. 最后把对应证据接入 `scripts/verify-ga-release.sh`。

TDD 约束：

- 不用文档声明替代失败测试。
- 不用 mock-only 测试证明事务、迁移、ledger、recovery 或 runtime safety。
- WebDAV GA evidence 不能用 mock-only 替代真实 Postgres ledger 与 repo-local gateway/runtime fixture。
- Postgres integration gate 必须由脚本自动启动临时 Postgres 或使用 CI service，不能要求人工 DSN。
- generated-client gate 只要求 repo-local 最小 fixture 编译，不扩展成多语言矩阵。
- 不用人工审批关闭 release gate。
- 不把外部业务系统的 e2e 结果作为本仓库 GA 证据。
- 不为了追求覆盖率扩大到大量无关测试；gate 应精确覆盖高风险路径。

## Developer Handoff 清单

开发团队接手时应按以下大类更新，不需要等待额外产品澄清。

Contract 更新：

- capability matrix contract。
- operation state machine 与 terminalization contract。
- workload mount binding/mount plan contract。
- operator inspection/repair/intervention contract。
- purge approval evidence contract。
- restore consistency contract。
- WebDAV export credential contract。
- template/clone contract。
- repo lifecycle/session drain contract。

Schema/OpenAPI 更新：

- capability status、stable error code、audit event shape。
- operator inspection 与 repair request/response。
- purge approval evidence object。
- restore consistency/reconciliation state。
- quota enforcement status。
- redacted credential/session shapes。
- generated-client fixture 可编译的 request/response examples。

Docs 更新：

- GA scope/acceptance 口径。
- product-neutral happy/failure journeys。
- operator runbook 与 repair safety rules。
- backup/restore consistency runbook。
- WebDAV credential boundary wording。
- shared-volume residual risk threat model。
- evidence manifest 与 release gate 说明。
- stale or outdated wording cleanup。

Tests 更新：

- capability denied/admission tests。
- worker unsupported terminalization tests。
- operation recovery/idempotency tests。
- Postgres migration/transaction/lease/fence/audit outbox tests。
- WebDAV e2e tests。
- workload lease freshness/teardown-only/stale binding tests。
- operator authorization/repair/audit/redaction tests。
- purge approval negative/positive/idempotency tests。
- backup/restore consistency tests。
- generated-client compile tests。
- precise race/concurrency tests。
- doc/schema/OpenAPI drift guards。

Gate 更新：

- `scripts/verify-ga-release.sh` 保持唯一入口。
- 新增或接入精确子 gate，但所有 required gate 必须被唯一入口覆盖。
- 增加 evidence manifest verifier。
- 增加 JVS provenance/smoke automation，使用公开发布的 pinned binary、checksum、release-provided signature/bundle verification；缺失项必须由 manifest 记录自动可验证替代证据。
- 增加 workflow YAML 检查：唯一脚本、最小权限、artifact/tag trigger 配置声明。
- 不把 branch protection、实际 artifact 存在或 GitHub 环境设置作为本地 gate 通过条件。

## 最终验收口径

从干净 checkout 运行：

```bash
bash scripts/verify-ga-release.sh
```

必须自动证明：

- 所有 GA-required capability 都有 repo-local evidence。
- capability 不满足时 API/worker fail-closed 或终态化。
- operation 不会因 unsupported、runtime unavailable、lease expired、fence uncertain 长期悬挂。
- WebDAV credential、revoke、ledger、path policy、redaction 有真实 Postgres ledger 与 repo-local gateway/runtime e2e 证据。
- workload access 不会在 lease/stale/secret 不安全时重新授权。
- purge approval evidence、backup/restore consistency、operator repair/intervention 都有自动化验证。
- schema/OpenAPI/generated-client 不漂移。
- JVS binary provenance 与最小 smoke 由本仓库脚本验证。
- 关键并发路径有精确 race/concurrency 证据。
- release gate 不依赖兄弟项目、不依赖人工审批、不依赖主观 review。

当以上条件满足时，AFSCP 可以被认为具备进入实际 GA 实现收口和 release 判定的工程基础。
