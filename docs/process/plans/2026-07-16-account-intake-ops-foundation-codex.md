# 2026-07-16 账号接入与运营联动基础修复（Codex）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “那你全部修复啊。你看下c这些文件是不是有人在动”；此前已要求所有修复作为 PR 提交，未经 Owner 同意不得并入主线。 |
| Scope | 在独立分支 `fix/account-intake-ops-20260716-codex` 完成第一批后端基础修复：统一账号接入 dry-run 合同、账号运营聚合合同、现有 bulk-by-tag 逐账号原子执行与完整结果，并闭环 review 发现的批内去重顺序和生产 selector 软冷却豁免接线。Owner 后续明确授权本批继续完成上游个人身份和稳定凭据材料指纹的持久化合同。同步判别测试、OpenAPI 和最少必要文档。 |
| Out of scope | 本批不修改 selector/PASR、Gemini 出站、官方定价、R4 harness、前端页面、资金账、配额、鉴权核心、真实凭据或生产部署；不直接启用 Claude Cookie、Setup Token、Agent Identity、CRS 或秘密迁移包。上述能力保留为后续独立 PR，不算取消。 |
| Success criteria | 账号接入核心可在零写库、零网络请求条件下得到稳定的 create/update/skip/conflict/fail 预检结果，且结果不包含秘密；在租户 grant 未落地前不挂 HTTP 入口。凭据创建时持久化个人 subject 及其来源，创建/轮换/刷新时持久化租户域隔离的有效凭据材料单向指纹；只有 provider token 交换来源可按 subject 自动匹配，导入、手工和存量来源不明的 subject 必须显式冲突或确认。不保存凭据明文、不建立会自动合并账号的唯一约束。运维可从一个账号合同看到真实阻断来源、状态来源、恢复时间和允许动作；批量操作中单账号 update 与 audit 同成同败，一个账号失败不阻断后续账号；迁移往返、目标测试、相关包测试、代码预算、`git diff --check` 和只读 Codex review 通过；提交 Draft PR，不合并。 |
| Time estimate | 本批约 5-8 小时；后续高风险能力按独立 PR 推进。 |
| Blast radius | 影响管理端账号接入与账号运营 API、credential store 写入/轮换/刷新元数据、三列加性 schema 及对应测试；不改变数据面转发、selector、计费或鉴权。 |
| Failure modes | 预检误把多份凭据绑定到同一账号；响应泄漏 token/cookie/private key；运营聚合自创状态而与 selector 真相漂移；bulk 只做到部分 update 却没有审计；为了整批原子持有长事务；与其他工作树正在修改的 wiring/OpenAPI 文件冲突。 |
| Mitigations | 预检保持纯函数和窄 inventory 接口；秘密仅产不可逆摘要；所有状态标注权威来源及 selector 是否消费；单账号事务、整批继续执行；判别测试覆盖中间失败与后续继续；避免修改正在被 selector、Gemini、定价、R4 和前端审计工作树占用的文件，必要合同先放独立增量文档。 |
| Decision points | Owner 已确认：租户 grant 未落地前，账号接入 dry-run 只保留纯核心和测试，不开放 HTTP；不能让部署者代任意租户处理，也不能默认授权全部租户管理员。Owner 已于本批明确授权 Codex 按源码和既有设计决定个人身份及 access token 指纹持久化，不再等待二次确认。全项目后续统一执行“三项同时命中才审批”：成熟项目存在实质分歧、没有经源码核实的成熟先例或 Safe Equivalent、选错会造成高危，三项缺一均继续实施；三项同时成立时先做其他可执行项，最后集中请 Owner 决策。PR 合并、生产部署、真实秘密、`LICENSE` 与破坏性操作仍走独立硬门。 |
| Parallel-plan status | Owner 明确要求 Codex 独立工作并与其他目标分开。本计划继承已有 Codex 独立方案，不读取或修改其他 agent 的计划与未提交工作。 |

## 当前冲突矩阵

| 正在活动的工作树 | 已观察到的活动范围 | 本批处理 |
| --- | --- | --- |
| `HUAKAI-wt-selector-chain` | `cmd/gateway/wiring.go`、selector、PASR、rate precheck、queue wait | 不修改这些文件；运营聚合只读取现有权威合同 |
| `HUAKAI-wt-gemstream` / `HUAKAI-wt-r4integ` | Gemini passthrough、R4 harness、部分集成文件 | 不修改 Gemini 和 R4 文件 |
| `HUAKAI-wt-pricing` | 官方模型定价 migration 与 pricing 代码 | 不修改 schema 与定价 |
| `HUAKAI-frontend-truth-audit-20260716-codex` | OpenAPI、openapicheck、前端真相审计 | 该工作已停止；本批仅同步自身新增 operations 路径与 bulk 响应，不改前端页面或其它合同 |
| `HUAKAI-wt-loader` | 真实账号加载器 | 不读取或修改其未提交内容 |

## Pre-execution checklist

1. 以 `origin/integration/r4-test@6083b22d` 创建独立干净工作树。
2. 确认本工作树无其他目标的未提交改动。
3. 完整读取账号 bulk handler/store、凭据接入 parser/finalizer、账号详情、channel health、credential、auth/model cooldown 的生产源码与测试。
4. 在编辑实现前列出准确文件范围，避开冲突矩阵中的活动文件。
5. 所有 Go 注释与测试注释使用中文，生产代码注释不出现借鉴项目名。
6. 不使用 `/tmp`；测试临时目录统一使用 `/home/ubuntu/.codex-tmp/account-intake-ops`。
7. 不新增 runtime dependency，不修改鉴权、资金或真实秘密；schema 仅允许本计划列明的三个 nullable 元数据列和非唯一部分索引。
8. 运行目标单测、相关包测试、代码预算和 `git diff --check`。
9. 只暂存本批差异，运行只读 Codex review；S0/S1 修复后最多再审一轮。
10. 中文提交并推送 Draft PR；未经 Owner 同意不合并。

## 执行中边界修正

`cmd/gateway` 的 OpenAPI 一致性测试发现 operations 路由已注册但合同缺失，形成 `impl_only=1`。因此本批必须同步 OpenAPI 路径、响应 schema、bulk 新增字段和 1000 条上限错误，并增加 method/schema 判别测试；否则实现不能通过启动接线门，不能把该缺口推迟为后续集成。

首轮只读 review 另发现两处产品正确性问题：失败凭据提前占用去重键，以及 `disable_cooling` 运营语义与生产候选查询不一致。前者调整为仅可执行项登记去重；后者只在真实生产 `pool_group` 查询中豁免 `throttled/cooldown`，不豁免 `revoked`，并同步 operations 信号。旧 channel 查询没有携带该开关到后续 gate，本批不制造假接线。

## 执行顺序

1. 源码核实并确定现有接口可复用边界。
2. 实现统一账号接入 dry-run 领域合同与 parser 适配。
3. 实现账号运营聚合领域合同及只读管理端入口。
4. 修复 bulk-by-tag 的逐账号原子性和完整逐项结果。
5. 补正常、失败、恢复、秘密不回显及租户边界判别测试。
6. 运行检查、只读 review、提交并创建 Draft PR。

## Owner 授权后的持久化方案

1. 在 `account_credentials` 增加 nullable `external_subject_id`，与具体 credential/auth mode 同生命周期；作用域始终是 `(tenant_id, vendor)`，不做跨租户或全局唯一。
2. 增加 nullable `external_identity_source`，记录 subject/account 元数据来自 provider token 交换、导入还是手工录入。存量不回填、不猜测；空来源按不可信处理。
3. 增加 nullable `credential_material_fingerprint`。指纹使用固定域标签和 `tenant_id/vendor/auth_mode` 参与的 SHA-256，按运行时凭据类型抽取 token、API key、云组合密钥或服务账号启动材料；忽略标签等无关元数据，避免身份漂移。
4. 不使用当前 AES 主密钥做长期 HMAC 身份键：加密密钥轮换后必须仍能命中旧指纹。实际运行或启动凭据应具备高熵，租户域隔离的单向 SHA-256 与项目现有 refresh 指纹策略一致。
5. 两个索引均为非唯一部分索引。数据库只负责快速查找，重复行必须返回多个候选，由 `AccountIntakePlan` 产生显式冲突，禁止数据库约束或 `LIMIT 1` 自动替系统决定账号归属。
6. 迁移不解密、不回填存量密文，避免离线批量接触秘密。新创建立即写入；轮换/刷新逐步补齐凭据材料指纹；来源不明的旧 subject 不得自动更新，缺少 subject 的旧账号仅允许可信 token 交换结果走 `legacy_identity_upgrade` 人工确认路径。
7. 管理元数据可显示 subject，但 identity source 与 access token 指纹不进入普通 admin credential JSON、日志或审计正文，只供租户范围内的账号接入 inventory 查询使用。

## 账号身份冲突成熟项目对照增补

| 项目 | 内容 |
| --- | --- |
| Owner directive | “如果库里已经有两条同一上游 ID，它还会随便取第一条更新。我要把这两种都收紧为显式冲突，要求人工消歧，避免自动写错账号。参考成熟项目！” |
| Lane | `specifier`，只读 `sub2api`、`new-api`、`CLIProxyAPI` 当前默认分支源码；本会话不承担同一对照产物的 reviewer lane。 |
| Scope | 核实账号导入/创建时使用什么身份键，重复身份是拒绝、覆盖、更新、合并还是允许并存，批量导入怎样报告单项冲突，以及上游是否依赖数据库唯一约束。 |
| Success criteria | 每个成熟项目的结论都带当前可达 commit SHA 与源码行号；明确区分观察、推断和 open question；HUAKAI 只采用独立设计的行为合同和判别测试。 |
| Blast radius | 对照本身只读；若证据表明当前 `identity_ambiguous` 或邮箱冲突策略不合适，仅调整 intake 纯计划和测试，不开放 HTTP、不写库。 |
| Failure modes | 把成熟项目的内部命名、schema 或算法顺序带回本仓库；只看搜索结果不读上下文；把“没找到”误写成“明确不支持”；把单账号更新策略误套到批量导入。 |
| Mitigations | 三镜像均更新 HEAD；逐段阅读入口、服务层、持久化调用和测试；输出只写行为；未观察到的能力列入 open question。 |

### Clean-room lane guard

- PRIOR LANES ON THIS ARTIFACT: none
- REFERENCE PROJECTS IN SCOPE: CLIProxyAPI + sub2api + new-api
- 禁止复制函数名、结构字段名、注释、schema、测试和逐行算法。
- 证据仅以 `<repo>@<sha>:<file>:<line>` 锚点记录，正文使用 HUAKAI 自身词汇。

## 后续批次，不静默缩水

1. Claude Setup Token 一等接入与统一 gate。
2. Claude Cookie 一次性换码 Safe Equivalent。
3. Codex 账号级批量执行与 Agent Identity Experimental。
4. CRS 受控连接器与安全账号迁移包。
5. 主动探测、`last_request_observed_at`、跨协议统一链路。
6. 三身份单层租户 grant 持久化与授权操作面。

## 本批实施结果

1. OAuth、CLI/JSON/CSV 导入和手工管理入口均可把 `external_subject_id` 与身份来源传到 credential store；导入 JWT 无论能否解析均固定为 `import_payload`，手工入口固定为 `manual`。
2. `account_credentials` 增加 nullable `external_subject_id`、`external_identity_source` 与 `credential_material_fingerprint`；创建、人工轮换写入身份来源和稳定指纹，自动刷新保持已有身份与指纹不变，仅为空时补齐，审计失败时数据库变更同步回滚。
3. 新增租户范围内的无秘密 identity inventory。预检按真实 `provider_account_id` 判断账号歧义：同账号多 `auth_mode` 选择目标模式，不同账号重复身份显式冲突，没有目标模式时显式 `identity_mode_conflict`。
4. 只有 provider token 交换产生的身份来源可按 subject 自动选中已有账号；导入、手工和存量来源不明的 subject 均不能触发自动轮换，并返回可操作的冲突或确认信号。
5. `Build` 必须携带正数 `TenantID`，缺失时 fail-closed，避免租户域 token 指纹静默退化为空。
6. 个人 subject 不进入 `RedactedContext`、日志或内部指纹 API；普通 admin credential 合同只显示 subject，不显示内部来源与 `credential_material_fingerprint`。
7. `intake` 匹配职责拆到 `match.go`，未修改代码预算基线。
8. 项目全局规则已同步为三项同时命中门：成熟项目有实质分歧、无可验证成熟先例或 Safe Equivalent、选错会造成高危，三项同时成立才留到最后审批；PR 合并、生产部署、真实秘密、`LICENSE` 与破坏性操作保持独立硬门。
9. 第二轮只读 review 的 S1 已闭环：API key、AWS 长期凭据、服务账号启动材料和 refresh-only 启动凭据均能生成稳定指纹；普通轮换未提交身份字段时不覆盖已有身份来源。
10. 同批不同凭据声明相同但未经验证的 subject 时返回 `unverified_identity_collision`，不再静默跳过或创建多个疑似同身份账号。
11. 最终独立 review 的两个 S1 已闭环：`revoked`、`operator_attention` 和刷新进行中的凭据不能由普通导入隐式复活或并发覆盖；OAuth、服务账号和 metadata 模式优先长期启动材料，自动刷新不再覆盖已建立的稳定指纹。
12. 最终窄范围复审确认无阻断项：真实 PostgreSQL 测试分别覆盖旧指纹为 SQL NULL 和空字符串时的补齐；普通 OAuth `client_email` 不会被误当成稳定 metadata 身份；从 inventory 构建预检时，`revoked` 凭据保持显式冲突，不会退化成 create/update。

## 全局颗粒度审计口径

Observed regions: 7 / Inferences: 1 / Open questions: 0

1. 账号导入不能只判定“有批量入口”。需要逐项核对：输入形态、参数范围、过期判断、身份键、批内重复、已有账号更新开关、凭据字段合并、续期材料保留、单项失败、汇总结果、缓存失效和本批后续项是否看到刚创建/更新的账号。最新源码观察到成熟账号池实现确实把这些行为拆开处理，并在 access-only 更新时保留已有续期材料。`Wei-Shaw/sub2api@bc2244c83fd8e92769d89ca01eb980513a720486:backend/internal/handler/admin/account_codex_import.go:117`、`Wei-Shaw/sub2api@bc2244c83fd8e92769d89ca01eb980513a720486:backend/internal/handler/admin/account_codex_import.go:159`、`Wei-Shaw/sub2api@bc2244c83fd8e92769d89ca01eb980513a720486:backend/internal/handler/admin/account_codex_import.go:255`、`Wei-Shaw/sub2api@bc2244c83fd8e92769d89ca01eb980513a720486:backend/internal/handler/admin/account_codex_import.go:490`、`Wei-Shaw/sub2api@bc2244c83fd8e92769d89ca01eb980513a720486:backend/internal/handler/admin/account_codex_import.go:929`、`Wei-Shaw/sub2api@bc2244c83fd8e92769d89ca01eb980513a720486:backend/internal/handler/admin/account_codex_import.go:1053`
2. 凭据文件批量导入不能只看“支持多文件”。需要核对：单文件与多文件响应区别、部分成功语义、非法文件是否覆盖存量、文件名边界、内容先校验后持久化、落盘权限及运行时注册是否联动。最新源码观察到成熟代理实现会返回逐文件失败，非法 JSON 不覆盖旧文件，并在校验后以收紧权限落盘再更新运行时记录。`router-for-me/CLIProxyAPI@106270bea6f18ba2f2cc8b0b5887987f2874eed8:internal/api/handlers/management/auth_files.go:735`、`router-for-me/CLIProxyAPI@106270bea6f18ba2f2cc8b0b5887987f2874eed8:internal/api/handlers/management/auth_files.go:897`、`router-for-me/CLIProxyAPI@106270bea6f18ba2f2cc8b0b5887987f2874eed8:internal/api/handlers/management/auth_files.go:922`、`router-for-me/CLIProxyAPI@106270bea6f18ba2f2cc8b0b5887987f2874eed8:internal/api/handlers/management/auth_files.go:946`
3. 多凭据池不能只判定“能轮询 key”。需要核对：每个 key 的状态、禁用原因和时间、并发选择锁、随机/轮询策略、无可用 key 的显式错误、单 key 故障是否只隔离该 key、全失效是否上卷账号状态、运营分页/过滤/统计、敏感写权限和审计。最新源码观察到成熟渠道实现覆盖了这些小闭环。`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:model/channel.go:175`、`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:model/channel.go:199`、`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:model/channel.go:641`、`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:model/channel.go:706`、`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:controller/channel.go:1404`
4. 对 HUAKAI 的合理推断：后续 GW-WIRE 全局排查应把每个大功能拆成“入口 → 校验 → 身份/规范化 → 选择 → 状态写入 → 缓存/调度传播 → 失败分类 → 重试/回退 → 审计 → 运营恢复”，并为每个节点核对生产调用者和判别测试；只有类型、表或 helper 存在不算接线完成。

## 已完成验证

- `go test ./... -count=1`
- `go vet ./...`
- `go test -race ./internal/credentialstore ./internal/credentialacq/... ./internal/gatewayhttp -count=1`
- `go test -race ./internal/credentialacq/intake -count=1`
- `go test ./internal/codebudget -count=1`
- PostgreSQL 15 隔离库完整 migration `up → down -all → up`，最终版本 `0188`
- PostgreSQL 15 隔离库真实验证 credential 创建、轮换、刷新身份/指纹持久化与审计事务回滚
- `go test -tags=integration_pg -race -count=1 -timeout 10m ./internal/credentialstore`
- 最终独立只读 review：`APPROVE，无阻断项`
- `git diff --check`

Source files read: backend/internal/handler/admin/account_codex_import.go; backend/internal/service/admin_account.go; backend/internal/repository/account_repo.go; internal/api/handlers/management/auth_files.go; internal/api/handlers/management/auth_files_batch_test.go; model/channel.go; controller/channel.go
Lane: specifier
Agent: Codex GPT-5
UTC timestamp: 2026-07-16T16:02:00Z
