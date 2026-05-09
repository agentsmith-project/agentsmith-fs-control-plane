# AFSCP 开发者指南

本文面向接手 AFSCP 编码、测试和维护的工程团队。它不是完整设计说明，而是日常开发时应遵守的工作手册。
修改 API、operation、store、worker、audit、contracts 和 release evidence 之前，先守住控制面边界。

## 开发原则

- 独立控制面：AFSCP 是独立发布、独立部署、独立通过 gate 的共享文件系统控制面，不依赖任何参考消费者或 sibling project 才能宣布就绪。
- 产品无关边界：AFSCP 只管理 volume、namespace、repo、save point、template、export、workload mount、operation、audit 等存储控制面概念。业务对象名、业务流程和产品目录逻辑留在调用方。
- TDD：新增或修改边界行为时，先补 focused tests。尤其是 admission、idempotency、operation lease、audit、redaction、path resolver、session/fence、store migration 和 worker recovery。
- Contract-first：API、schema、OpenAPI、docs/contracts、错误码、operation 类型和 evidence 要一起演进。不要先落实现再让合同追着补。
- Repo-local machine gate：GA 接受标准只看本仓库机器证据。权威入口是 `bash scripts/verify-ga-release.sh`，不是人工批准、会议、外部仓库状态或生成客户端评审。
- 默认安全和失败关闭：能力未启用、配置不完整、会话状态不确定、ledger 不一致、JVS 校验不完整、secret/redaction 可疑时，默认拒绝并审计，不做“尽量继续”。
- 最小权限：普通 product caller、client、workload 不能看到 JuiceFS/JVS 原始路径、root credential、Secret ref 或 `.jvs` 控制目录。只有 orchestrator 角色能取 orchestrator-only mount plan。
- 不绕过边界：不要绕过 operation、audit、fence、session 边界。worker 写进度和终态必须走 lease-fenced primitive；有 session/fence 语义的存储变更必须通过已审查的 admission 和 store boundary。

## 架构和代码布局

入口命令：

- `cmd/afscp-api`：内部 API 服务入口。
- `cmd/afscp-worker`：显式启用的 worker/recovery/outbox/reconcile 入口。
- `cmd/afscp-export-gateway`：AFSCP 控制的 WebDAV policy gateway。
- `cmd/afscp-contract-verify`：合同、OpenAPI、schema、DTO 漂移检查。
- `cmd/afscp-evidence-verify`：release evidence manifest/selector verifier，由 release gate 脚本调用。

核心包：

- `internal/api`、`internal/apiapp`：route metadata、auth gate、handler shell、DTO、error envelope、operation intake。
- `internal/auth`、`internal/namespaceauth`、`internal/capability`：service principal、caller role、namespace policy、capability/admission gate。
- `internal/store/postgres`：PostgreSQL adapter、migration contract、operation/idempotency/audit/session/fence/resource 持久化边界。
- `internal/resources`：volume、namespace、binding、repo、lifecycle 等纯模型和校验。
- `internal/operations`、`internal/operationexec`、`internal/operationinspect`：operation 类型、状态机、lease、intake、inspection 和执行分类。
- `internal/audit`、`internal/auditdelivery`：audit event、redaction、outbox、HTTP JSON delivery。
- `internal/pathresolver`：结构化 ID 到 payload/control 路径解析，遍历、`.jvs`、symlink 逃逸防线。
- `internal/jvsrunner`、`internal/repoexec`：固定 JVS 版本、checksum/pin、`init`、`doctor --strict`、save/restore/template/lifecycle 执行。
- `internal/exportaccess`、`internal/exportgateway`、`internal/exportreconcile`：WebDAV export session、gateway admission、runtime request ledger、terminal reconcile。
- `internal/workloadmount`、`internal/mountbindingexec`：workload mount binding、orchestrator-only plan、heartbeat/release/revoke/stale scan。
- `internal/fences`、`internal/sessionstate`、`internal/repoaccess`：writer/lifecycle fence、session drain、repo access admission。
- `internal/contractcheck`、`internal/releaseevidence`：文档/合同/evidence 的 repo-local guard。

外部契约和证据位置：

- API 合同和 schema：`docs/API_CONTRACT_DRAFT.md`、`api/openapi/internal-v1.openapi.yaml`、`api/schemas/afscp-internal-v1.schema.json`。
- focused contracts：`docs/contracts/`。
- 用户、集成方和操作者入口：`docs/USER_GUIDE.md`。
- release evidence：`docs/READINESS_EVIDENCE.md`、`docs/release-evidence/ga-manifest.json`、`docs/release-evidence/ga-release-selector.json`。
- runbooks：`docs/runbooks/`。

## 数据与控制流

### Mutating API 主路径

1. API admission 校验 service auth、caller role、namespace header/path/body 一致性、capability、repo lifecycle、session/fence 状态和请求 schema。
2. Handler 通过 operation intake 创建或复用 operation，并执行 idempotency hash 检查。重复请求返回已有 operation；冲突请求拒绝。
3. Store boundary 在同一受控路径内写 operation、idempotency 和需要的 audit/outbox 记录。失败时不能留下半承诺的可见结果。
4. Worker 通过 operation lease 获取可执行记录。lease 过期、取消、恢复和终态写入必须使用 DB 边界和 lease-fenced update。
5. Executor 调用 JVS、repoexec、storage、session/fence 或 resource store。JVS 调用必须使用 pinned runner、checksum/版本校验和结构化输出处理。
6. Worker 以 terminal operation 和 audit 结束。成功、失败、取消、operator intervention 都必须是可恢复、可审计、可检查的终态。

### WebDAV runtime ledger

WebDAV 请求不是每次都创建 operation。`afscp-export-gateway` 在 DB runtime request ledger 中记录
begin、heartbeat、end，并同步维护 active counts。新请求只在 session 仍 active 且未过期时被 DB
边界接纳；revoke/expiry 期间的 TOCTOU 由 ledger 关闭。terminal reconcile 会先恢复 stale
non-terminal request，再把零 active 的 session 推到 revoked/expired；计数漂移必须失败关闭。

### Workload mount 和 orchestrator 边界

AFSCP 负责 workload mount binding 和 orchestrator-only mount plan。外部 orchestrator 才创建
Secret/PV/PVC/Pod 或等价挂载，并回报 heartbeat、release、revoke/confirmed-unmounted 状态。
普通 caller、client、workload 不应取得 Secret ref 或原始 JuiceFS 参数。缺少 orchestrator 合同、
能力未启用、session 不确定时，AFSCP 返回稳定 capability/admission 错误，而不是发放不完整 mount。

### Restore/template gates

Restore-run 不是普通文件 IO。它必须获取 writer-session fence，并与 read-write export/workload admission 共享 repo-row serialization。已有 active 或 uncertain RW session 默认阻断。restore writer 不得直接跳过 session drain、fence release、JVS doctor 或 operation lease。

Template create 会从 source repo 生成新的 source save point，因此对 source repo 执行 writer-fence、dirty-source 和 session 检查，并使用 operation lease、JVS doctor 和 audit 维持恢复语义。

Template clone 是 template read gate 加 target repo exclusive create。它校验 template 可读、namespace/volume/policy 允许，并通过 target repo 创建侧的独占 admission 防止目标资源冲突；source repo 只作为 template lineage 被引用。

## 功能开发方式

### 新增或修改 API

- 先确认这是 AFSCP 通用存储能力，不是调用方业务 API。
- 更新 narrative contract、OpenAPI、JSON schema、route metadata、DTO 和稳定错误码。
- 在 `internal/api` 增加 handler 或 shell wiring，保持标准 envelope、namespace mismatch、role denial、capability denial 和 denied audit。
- Read-only API 不应创建 operation；它返回 redacted projection，并使用 namespace/resource boundary。
- Mutating API 必须走 operation intake、idempotency、audit 和 store boundary，不直接调用 executor。

### 新增或修改 operation

- 在 `internal/operations` 定义 operation type、phase、state/result/error 语义，并保持 route operation ID 映射。
- 定义 idempotency canonical request、resource identity、input_summary、external_resource_ids 和 redacted output。
- 设计 worker lease 行为：可重试、不可重试、恢复、取消、operator intervention 何时发生。
- 终态提交必须同时满足 operation lease、audit outbox、fence/session 释放或保留规则。

### Store migration 和 PostgreSQL adapter

- migration 要表达 durable contract，而不是只让当前测试通过。
- 对新表/列写 focused migration tests 或 adapter tests，覆盖唯一约束、状态转换、lease/fence/session 原子性和 redaction 字段。
- 保持 DB-only 边界：同一个逻辑承诺要么一起写入，要么一起失败。
- 不用裸 `UpdateOperation` 写 worker-owned progress/terminal 状态；使用 lease-fenced update/commit primitive。

### Worker executor

- executor 从 leased operation 开始，从 terminal operation 结束。
- JVS 路径只通过 `pathresolver` 和 `jvsrunner`/`repoexec`，不要拼接或暴露 host/JuiceFS/JVS 控制路径。
- 每个外部效果要有恢复语义：重跑是否安全，失败后能否识别已完成、需补偿或需 operator intervention。
- 对 destructive 行为，例如 purge，必须保留 retention、approval reference、session drain、audit 和 runbook 证据。

### Capability、admission 和默认关闭

- 新能力默认 disabled，disabled admission 要有稳定错误和 denied audit。
- capability gate 应覆盖 API admission、worker recovery、runtime gateway 或 reconcile 的所有入口。
- 不要用环境变量“暗中开启”绕过合同。启用能力时要更新 selector/evidence 规则和测试。

### Audit 和 redaction

- Denied、accepted、terminal、operator intervention、capability denied、policy denied 都应可审计。
- audit details 只放结构化、可 redaction 的信息；不要放 secret、完整路径、`.jvs` 路径、WebDAV password、Secret value。
- WebDAV secret-bearing response 只允许首次 create 成功返回；重放和 get 返回 redacted session。
- 新增 audit 类型或字段时，补 redaction 测试和 manifest/contract guard。

### Docs、contracts 和 evidence

- 行为变更通常需要同步 `docs/contracts/`、`docs/API_CONTRACT_DRAFT.md`、`docs/READINESS_EVIDENCE.md`、runbook 或 ADR。
- `docs/release-evidence/ga-manifest.json` 只登记 repo-local、可执行或可机器检查的证据。
- `docs/release-evidence/ga-release-selector.json` 决定 final candidate 和 optional capabilities。不要把 optional fixture
  当作 default GA 证据。
- `go run ./cmd/afscp-evidence-verify -mode final -check-only ...` 会被 CLI 拒绝，不能用于 final acceptance。
  结构性预检可运行 `go run ./cmd/afscp-evidence-verify -mode seed -check-only -manifest docs/release-evidence/ga-manifest.json`；
  final acceptance 只来自 release script 的无 check-only 路径。

## 测试和 Gate

日常开发建议从 focused tests 开始：

```bash
go test -count=1 ./internal/api -run 'TestName'
go test -count=1 ./internal/store/postgres -run 'TestName'
go test -count=1 ./internal/operations ./internal/audit ./internal/pathresolver
```

扩大验证时运行：

```bash
go test -count=1 ./...
go run ./cmd/afscp-contract-verify \
  -openapi api/openapi/internal-v1.openapi.yaml \
  -schema api/schemas/afscp-internal-v1.schema.json \
  -api-contract docs/contracts/afscp-internal-api-v1.md \
  -api-draft docs/API_CONTRACT_DRAFT.md
bash scripts/verify-ga-release.sh
```

发布接受规则：

- `bash scripts/verify-ga-release.sh` 是唯一权威 selector-driven final gate。
- selector 为 `release_intent=final_candidate` 时，脚本必须带 selector 进入 final mode；exit code `0`
  才表示选中 repo-local GA release evidence 通过。
- `go run ./cmd/afscp-evidence-verify -mode final -check-only ...` 会被 CLI 拒绝，不能用于 final acceptance。
  结构性预检可运行 `go run ./cmd/afscp-evidence-verify -mode seed -check-only -manifest docs/release-evidence/ga-manifest.json`；
  final acceptance 只来自 `bash scripts/verify-ga-release.sh` 的无 check-only 路径。
- Manual approval、security/owner approval、generated-client approval、sibling project 状态、runbook 会议都不能替代 repo-local machine gate。

本次文档类改动如果只要求 whitespace 自检，运行：

```bash
git diff --check
```

## Tutorial：三个常见开发任务

### 1. 新增只读 API 查询

适用场景：返回 AFSCP 已有 metadata 的 redacted projection，例如按 namespace 查询某类状态。

1. 先在合同中定义路径、角色、namespace 规则、响应 schema 和错误码。确认它不是业务 catalog 查询。
2. 更新 OpenAPI/schema/route metadata。route 标记为 non-mutating，不映射 operation type。
3. 在 `internal/api` 写 handler：通过 AuthGate，校验 namespace header/query/path 一致性，调用只读
   store interface，返回 redacted DTO。
4. 补 tests：role denial、namespace mismatch、invalid ID、not found、redaction、route metadata 不映射 operation。
5. 如合同或 readiness evidence 受影响，更新 docs/contracts 或 manifest anchor，再跑 focused tests 和相关 contract verifier。

### 2. 新增 mutating operation

适用场景：发起持久存储变更，例如创建、恢复、撤销、终态推进或需要 worker 执行的动作。

1. 先写 contract：请求、operation envelope、resource identity、idempotency、admission、audit、recovery、失败关闭语义。
2. 更新 schema/OpenAPI/route metadata，并在 `internal/operations` 增加 operation type/phase 映射。
3. Handler 只做 admission 和 operation intake，不直接执行外部效果。accepted response 返回 operation envelope。
4. 在 store/postgres 增加原子边界：operation、idempotency、必要资源状态、audit outbox、session/fence 记录要么一起承诺，要么一起失败。
5. Worker 通过 lease 获取 operation，调用 executor，使用 lease-fenced terminal commit。涉及 JVS、
   restore、template、lifecycle、mount/export 的路径必须经过对应 fence/session/ledger。
6. 补 tests：disabled capability、idempotency conflict、lease lost、retry/recovery、terminal audit、redaction、session/fence denial 和 runbook/evidence guard。

### 3. 修改 release evidence 或 manifest

适用场景：新增证据项、关闭 seed gap、调整 optional capability、或把测试纳入 GA gate。

1. 先判断证据 profile：default、repo-local fixture-enabled、deployment-runtime-support 不要混用。
2. 在 `docs/release-evidence/ga-manifest.json` 增加或修改 item：`id`、`capability_id`、`evidence_type`、`required`、`command`、`anchors` 和 pass criteria 要能被仓库当前文件证明。
3. 需要 final selector 时，同步 `docs/release-evidence/ga-release-selector.json`。只有 selector 声明的 optional capability 才成为 final-blocking optional positive。
4. 更新 `docs/READINESS_EVIDENCE.md`、`docs/GA_RELEASE_GATES.md` 或对应 contract anchor，保证人读 ledger 和机器 manifest 一致。
5. 用 focused releaseevidence/contractcheck tests 验证结构；最终 acceptance 仍只来自 `bash scripts/verify-ga-release.sh`，不是 verifier 的 `-check-only`。

## 开发禁忌

- 不要引入业务应用名称、业务对象、业务审批流或产品目录逻辑。
- 不要把 sibling projects、consumer adoption、manual approval、security/owner signoff、生成客户端评审设为 AFSCP gate。
- 不要向普通 caller、client、workload、日志、stderr、audit 泄露 JuiceFS metadata URL、bucket credential、access/secret key、Secret value、Secret ref、host path、repo control root 或 `.jvs` path。
- 不要手写拼接 repo/template/export/workload 路径；使用 structured IDs、namespace context 和 `pathresolver`。
- 不要绕过 JVS pin/checksum、`doctor --strict` 或 runner clean-CWD 约束。
- 不要绕过 operation lease、lease-fenced worker update、audit outbox、writer/session/lifecycle fences。
- 不要把 stock `juicefs webdav` 当作 GA policy boundary；WebDAV 必须经过 AFSCP gateway。
- 不要在 session 状态不确定、ledger drift、active RW session 未处理时继续 restore/template/lifecycle/purge。
- 不要让 break-glass direct mount、未审查 dirty restore option、普通 single-writer lock 或 version merge 偷偷进入 GA 行为。

## 相关文档

- [README](../README.md)
- [USER_GUIDE](USER_GUIDE.md)
- [DEVELOPER_HANDOFF](DEVELOPER_HANDOFF.md)
- [ARCHITECTURE](ARCHITECTURE.md)
- [API_CONTRACT_DRAFT](API_CONTRACT_DRAFT.md)
- [contracts](contracts/README.md)
- [GA_RELEASE_GATES](GA_RELEASE_GATES.md)
- [READINESS_EVIDENCE](READINESS_EVIDENCE.md)
- [runbooks](runbooks/README.md)
- [OPERATIONS_AND_AUDIT](OPERATIONS_AND_AUDIT.md)
- [EXPORT_WEBDAV](EXPORT_WEBDAV.md)
- [WORKLOAD_MOUNTS](WORKLOAD_MOUNTS.md)
