# 2026-05-21 Owner 主链分析评估 — Codex 独立稿

| 字段 | 内容 |
|---|---|
| 性质 | CLAUDE.md #10 平行评估；Codex 独立判断 |
| Owner 指令 | 独立评估 Owner 提供的「请求主链微步骤 + 功能项」差异分析，并对齐方向 1 / Phase 执行计划 |
| 约束执行 | 只读代码/文档/本地参考源码；只写本文档；未读 Claude 对本材料的评估稿 |
| 基线提醒 | Owner 材料以 `880b443` 为旧基线；当前已包含 PR1 `c4d85f7` 和 PR2 后续提交 |

## 1. 评估判断

总体判断：这份分析质量高，方向基本正确。它抓住了「账号转 API」最容易漏的真实主链：身份入口、绑定、候选路由、槽位、sticky、凭证版本、协议损失、attempt 轨迹、失败计费、健康、Ops trace。它与方向 1 的核心判断一致：Go `gatewayhttp` 必须成为账号/凭证/路由/计费/重试的大脑，Rust sidecar 只承担传输增强；方向 1 明确把 Go 管线作为核心，并列出洞②⑥重试/failover/跨池、洞④热刷新、洞③Anthropic buffered、洞①cloaking、洞⑤调用方限流、洞⑦bcrypt 等 Phase 化补洞项（`docs/process/plans/2026-05-21-direction-1.md:32-97`）。

事实准确性：对 sub2api 的主链判断大体准确。sub2api 源码显示 API key 解析带 user/group 关联（`Wei-Shaw/sub2api@91da8159:backend/internal/repository/api_key_repo.go:106-111`），账号选择把 group、session sticky、排除列表、slot 获取、等待计划串到同一调度入口（`Wei-Shaw/sub2api@91da8159:backend/internal/service/gateway_service.go:1407-1516`），failover 状态持有失败账号集、同账号重试计数、切换次数（`Wei-Shaw/sub2api@91da8159:backend/internal/handler/failover_loop.go:43-125`）。这些支撑 Owner 对“池路由、sticky、槽位、attempt/failover 可解释”的概括。

对 CLIProxyAPI 的判断也基本准确，但要收窄措辞。CLIProxyAPI 确有 `request-retry`、`max-retry-credentials`、`disable-cooling`、`round-robin/fill-first` 和 session-affinity 配置（`router-for-me/CLIProxyAPI@21fad9db:config.example.yaml:85-123`），构建器按配置选择 fill-first 或 round-robin，并可包 session affinity（`router-for-me/CLIProxyAPI@21fad9db:sdk/cliproxy/builder.go:211-240`）。它的 session 来源不是“6 个”那么窄，源码列出 8 类优先级：Claude metadata session、`X-Session-ID`、`Session_id`、`X-Amp-Thread-Id`、`X-Client-Request-Id`、普通 `metadata.user_id`、`conversation_id`、消息 hash（`router-for-me/CLIProxyAPI@21fad9db:sdk/cliproxy/auth/selector.go:470-483,572-655`）。它也有 provider executor / credential injection 的集中接口（`router-for-me/CLIProxyAPI@21fad9db:sdk/cliproxy/auth/conductor.go:30-45`）。

需要修正的点：

1. 「CLIProxyAPI refresh 强化」不能推成“热路径刷新参考标准”。现有 gap 分析已经纠正：CLIProxyAPI 请求时直接用存储密钥，未证明热路径令牌刷新是参考项目普遍标准（`docs/process/plans/2026-05-21-account-to-api-gap-analysis.md:73-84`）。HUAKAI 洞④仍是真缺口，但它是 HUAKAI 自身升级项，不应包装成“所有参考项目都有”。
2. 「sub2api 失败/回退也体现在 usage 账务链」要谨慎。源码能支撑 idempotency、failover、slot/wait 和 ops 解释链（例如 `idempotency_repo.go:21-50`、`failover_loop.go:43-125`），但不能等同于 HUAKAI 的 claim/usage/billing ledger 四元索引。接入 HUAKAI 时应该按“参考其 attempt 可解释性”，不要照搬为账务 schema 结论。
3. Owner 材料基线偏旧。PR1 后当前 `DefaultRouter` 已能从 registry pool candidates 生成多 attempt plan、带 `Reason`、`AttemptBudget` 和 per-attempt upstream model（`backend/internal/router/default_router.go:68-97`；`backend/internal/router/route_plan.go:60-117`）。PR2 后错误 taxonomy 已有 `TransportErrorClass`、`AttemptRetryDecision`、401 auth 子预算、热刷新意图（`backend/internal/gateway/attempt_error.go:15-53,143-166`）。因此“路由策略/失败 reason 完全没有”已不准确；更准确是“planner/taxonomy 在做，handler loop、billing 原子性、真正重试执行还没落地”。
4. 当前 HUAKAI 并没有真实落地 `request_attempts` 表。实际迁移/查询的 attempt 脊柱是 `billing_ledger_claims.attempt_seq`、`usage_records.attempt_seq`、`billing_events`、`pool_slot_acquisitions`；`rg` 只在旧计划/规格中发现 `request_attempts`，未在 `backend/sql/migrations` 找到建表。旧 `docs/specs/client-identity.md` 还写了 `request_attempts` 已存在前置（`docs/specs/client-identity.md:41-45,97-99,167-173`），这与现状冲突，是需要清理的文档漂移。

## 2. 24 项对齐表

状态定义：`已做` = 当前代码/规格已能支撑主要行为；`在做` = 已进入方向 1 当前 PR 链或已有提交但未完整打开；`已规划` = 方向 1 / Phase 已覆盖但尚未执行；`真新增` = 当前方向 1 / Phase 框架未明确覆盖，需要补入。

### 一、请求主链 12 微步骤

| # | Owner 项 | 对齐状态 | 方向 1 / Phase 依据 | Codex 判断 |
|---|---|---|---|---|
| 1 | 用户身份/租户入口 + session affinity 来源矩阵 | 真新增 | 身份入口已做：API key resolver 返回 tenant/api key/user（`backend/internal/auth/api_key_resolver.go:138-142`），router request context 也带三者（`backend/internal/router/route_plan.go:6-10`）。但统一 session 来源优先级/拒绝行为不在方向 1 Phase 1 正文。 | 身份闭环不是空白；真正新增的是 CLIProxyAPI 风格 session signal matrix + acceptance。建议并入 Phase 1 PR5 前的回归或 Phase 3 控制面，不先上 schema。 |
| 2 | API Key 与账户绑定解析 + 绑定变更可追踪 | 在做 | registry 已有 binding metadata、snapshot version、pool candidates（`backend/internal/registry/registry.go:55-78`；`backend/internal/registry/postgres_registry.go:118-180`）；PR1 已把 metadata 映射进 router（`backend/internal/gatewayhttp/chat_completions_dispatch.go:85-123`）。 | 绑定到路由已有主干；“绑定版本/失效原因/切换事件可读链路”还没完整。属于 Phase 1/3 的 trace 增量，不是方向外。 |
| 3 | 路由策略选择 + `strategy_id` + reason-code | 在做 | PR1 planner 生成 attempt reason：`primary` / `cross_pool_fallback` / `same_pool_account_failover`（`backend/internal/router/default_router.go:73-88`）。Phase 1 指定 PR1 为多候选 planner（`docs/process/plans/2026-05-21-phase1-design-synthesis.md:68-84`）。 | reason 已开始落地；`strategy_id`、灰度、实验语义仍缺。补入 Phase 1 PR5 配置/审计，不宜阻塞 PR3/PR4。 |
| 4 | 并发槽位/排队四态 | 已规划 | 现有 slot acquire 写 acquisition token 和 attempt_seq（`backend/internal/pool/dispatcher/slot_manager.go:58-119`）；sub2api wait plan 已作为 gap 参考（`docs/process/plans/2026-05-21-account-to-api-gap-analysis.md:66-72`）。 | 槽位能力已在主链内；`queued/limited/assigned/timed_out` 统一 telemetry/state machine 是新增子项。建议 Phase 1 先做指标/trace 字段，持久化四态另报 Owner。 |
| 5 | Sticky 会话及中止条件 | 已规划 | Phase 1 明确“流开始后禁 failover/交付前才重试”（`docs/process/plans/2026-05-21-direction-1.md:47-53`），Phase 1 synthesis 也要求 delivery tracker（`docs/process/plans/2026-05-21-phase1-design-synthesis.md:53-64`）。 | first token 边界已覆盖；工具调用前后、上下文断层、跨模型 sticky 规则未完整。并入 Phase 1 acceptance + Phase 3 identity/sticky 策略。 |
| 6 | 凭证 lease 与版本 | 已规划 | credential store 已有 `CredentialVersion`、状态、CAS 刷新（`backend/internal/credentialstore/postgres_store.go:39-76,470-518,572-621`）；slot acquisition 有 lease token（`backend/internal/pool/dispatcher/slot_manager.go:97-107`）。方向 1 Phase 2 规划 hot refresh（`docs/process/plans/2026-05-21-direction-1.md:56-63`）。 | token_version 有基础；`credential_lease_id` 作为 attempt 注入证据还未成型。建议随 Phase 2 hot refresh 和 Phase 1 PR4 usage 写入一起设计。 |
| 7 | CredentialInjector 解耦 | 已规划 | 方向 1 要在 dispatch 前完成凭据解析/热刷新，再交给 Rust 或 Go transport（`docs/process/plans/2026-05-21-direction-1.md:133-158`）。CLIProxyAPI 的 executor/HttpRequest 接口证明集中注入边界合理（`router-for-me/CLIProxyAPI@21fad9db:sdk/cliproxy/auth/conductor.go:30-45`）。 | HUAKAI 思路同向，但当前 handler 仍耦合 account selection、claim、dispatch。建议 Phase 2 或 Phase 5 抽接口测试，不插进 PR3 初版。 |
| 8 | 协议适配器与 `protocol_loss` | 已做 | HCSF 要求 lossy projection 必须产出 ProtocolLossEntry（`backend/internal/proto/protocol_loss.go:16-24,73-97`）；usage insert 已有 `protocol_loss` 字段（`backend/sql/queries/billing_settle.sql:32-59`）。 | 后端 schema/规则已覆盖。缺的是 Ops/UI 聚合，不是主链协议层缺失。 |
| 9 | 重试/失败切换 attempt 轨迹 | 在做 | Phase 1 目标就是洞②⑥，PR1/PR2 已落 planner 和 taxonomy；handler 当前仍只激活第一 attempt（`backend/internal/gatewayhttp/chat_completions_dispatch.go:60-80`），`AttemptSeq: 1` 仍硬编码（`backend/internal/gatewayhttp/chat_completions_dispatch.go:197-211`）。 | Owner 判断正确，但需更新为“在做”。PR3-PR5 才是实际执行、账务原子性和开关打开。 |
| 10 | 失败计入用量与账务链 | 在做 | Phase 1 Owner D1 已拍板：失败 attempt 留作废记录（`docs/process/plans/2026-05-21-phase1-design-synthesis.md:8-13`）；billing queries 支持 abort/re-reserve/attempt_seq（`backend/sql/queries/billing_claims.sql:50-72`），usage/billing abort path 写零费用记录（`backend/sql/queries/billing_settle.sql:114-120`）。 | 方向内核心项。当前还没完成 PR4，所以不能宣称已做。 |
| 11 | 通道健康、cooldown、恢复坡度 | 已做 | channel health spec 已定义 active/degraded/cooling_down/ramping/disabled/manual_paused、1%-10%-50%-100% ramp、policy version、attempt outcome 输入（`docs/specs/channel-health-auto-disable.md:49-54,60-72,74-107,109-150`）。gap 分析也把 Go channelhealth 评为无大缺口（`docs/process/plans/2026-05-21-account-to-api-gap-analysis.md:33-35`）。 | 不是方向 1 的首要洞；后续需要把 PR2 taxonomy 信号接得更稳，但设计方向已覆盖。 |
| 12 | 日志与审计 Ops Trace 单条视图 | 已规划 | usage_records 已带 claim/account/attempt/routing_reason/protocol_loss（`backend/sql/queries/billing_settle.sql:32-59`），observability 查询能列 usage/claims/audit（`backend/sql/queries/observability.sql`，已定向读取）。方向 1 Phase 1 PR4/PR5 产生数据后才可串视图。 | Owner 说这是核心短板，我同意。现有是数据碎片，不是“用户可读 + 运维可排查”的单条 trace。补入 Phase 3，不能只做前端 mock。 |

### 二、按功能维度 12 项

| # | Owner 项 | 对齐状态 | 方向 1 / Phase 依据 | Codex 判断 |
|---|---|---|---|---|
| 1 | 账户资产模型容量/状态/租约/路由标签 | 已规划 | provider account、slot、credential version、channel health 已有分散基础；Phase 1/2/3 分别触及账号选择、凭证、健康。 | 不是空白，但还没有“账户资产变更全可追溯”的统一模型。建议作为 Phase 3 Ops trace/API 聚合，不在 Phase 1 改表。 |
| 2 | API key 到路由静态绑定 pending/apply/active/disabled | 真新增 | 当前 registry binding 是 snapshot read path；方向 1 Phase 1 只消费 binding，不定义 binding 变更状态机。 | 这是控制面新增能力，且可能动 schema/回滚语义。建议 Phase 3，需 Owner 确认。 |
| 3 | 账户组/池策略 enum：compat_priority_lru/round_robin/fill_first/risk_pareto | 在做 | PR1 已有保守顺序和 reason；CLIProxyAPI round-robin/fill-first 有实证（`router-for-me/CLIProxyAPI@21fad9db:sdk/cliproxy/builder.go:224-240`；`selector.go:26-36,359-368`）。 | enum/灰度还没。建议 Phase 1 PR5 先落 `strategy_id` + `fill_first` safe equivalent；复杂 `risk_pareto` 后置。 |
| 4 | 多租户隔离 edge path 错误泄漏 | 已规划 | auth resolver 校验 tenant/user/key active（`backend/internal/auth/api_key_resolver.go:120-144`）；credential resolve 双侧 tenant 约束（`backend/internal/credentialstore/postgres_store.go:470-499`）；slot acquire 拒跨租户（`backend/internal/pool/dispatcher/slot_manager.go:65-71`）。方向 1 安全边界也列明凭据/UDS/审计约束（`docs/process/plans/2026-05-21-direction-1.md:23-30`）。 | 数据路径有防线；raw error 泄漏属于 error body/redaction 问题，应并入 Phase 3/7 安全硬化和 contract tests。 |
| 5 | 凭证存储接口抽象，先 PG 不多实现 | 已做 | `credentialstore.Store` 已承担 PG store、metadata、version、audit、refresh 成功/失败保存（`backend/internal/credentialstore/postgres_store.go:39-85,470-621`）。 | Owner 建议与当前实现方向一致：先接口/审计，不急多 backend。 |
| 6 | 登录/凭证导入 create/callback/cancel/finalize 生命周期 | 已做 | OAuth start/callback 有 session/status 流程（`backend/internal/credentialacq/oauth.go:44-130`），Finalizer 有 begin/validate/create/finalized/audit（`backend/internal/credentialacq/finalizer.go:45-80`）。 | 后端生命周期已有；需要补的是统一 provider onboarding UI/API 体验，可进 Phase 2/3。 |
| 7 | 刷新风暴控制 3-scope + singleflight + jitter | 已规划 | 方向 1 洞④规划 `RefreshingCredentialVault`，复用 refresh lock/storm controller（`docs/process/plans/2026-05-21-direction-1.md:56-63`）；当前 credential refresh 有 version/CAS 基础。 | 热路径刷新还没落。建议 Phase 2；不要把它塞进 Phase 1 PR4 的 billing 高风险段。 |
| 8 | 路由状态码/error class 版本化 + provider override 回归 | 在做 | PR2 已建 attempt retry decision/taxonomy（`backend/internal/gateway/attempt_error.go:37-70,143-210`）。 | taxonomy 在做；“版本化 class map + provider override”未覆盖。建议 Phase 1 PR5 或 Phase 5 sidecar contract 前补。 |
| 9 | 会话来源矩阵与模型/供应商兼容映射 | 真新增 | 当前 handler 只用 prompt hash 做 session hash（`backend/internal/gatewayhttp/chat_completions_dispatch.go:197-211`）；旧 client-identity spec 有类似方向但依赖不存在的 `request_attempts`（`docs/specs/client-identity.md:41-45,97-99`）。 | 这是本评估里最应该新增的 acceptance 项之一。先无 schema：统一提取器 + HMAC/hash + 测试。 |
| 10 | OpenAPI 与实现一致的契约红线测试 | 真新增 | 方向 1 Phase 0 是契约/spec，但未明确“每个新/改 endpoint 必跑 OpenAPI drift 红线测试”。 | 这是低风险高收益新增项。建议作为 Phase 0/每 PR CI 规则，不等后续阶段。 |
| 11 | 前端配置管理 6 面板：账号/绑定/池/凭证/健康/attempt trace | 真新增 | 方向 1 当前文档重心是 Go/Rust/backend phases；Ops trace 数据要到 PR4/PR5 后才完整。 | 不能先做 mock 拼合。建议 Phase 3：先读真实 usage/claim/audit/health API，再做六面板。 |
| 12 | 安全与脱敏：日志分级、白名单、错误体积限制 | 已规划 | channel health 规格明确不存 raw credential/prompt/upstream body（`docs/specs/channel-health-auto-disable.md:101-107`）；credential finalizer 对错误做 redaction（`backend/internal/credentialacq/finalizer.go:102-110`）；方向 1 UDS/凭据边界有安全约束（`docs/process/plans/2026-05-21-direction-1.md:23-30,158-172`）。 | 已在方向内，但需要变成 gateway 错误响应和日志的统一 contract tests。 |

## 3. 真新增项与建议补入 Phase

1. **Session source matrix + sticky regression**：覆盖 Owner 一-1、二-9。补进 Phase 1 PR5 前置测试或 PR5 同步项；先做无 schema 的 `SessionIdentityExtractor`，输出 signal class、confidence、hash、reject reason，使用 HMAC/摘要，不持久化原始 user/session/header 值。若后续要持久化，再进 Phase 3 并单独报 schema 风险。
2. **绑定变更状态机 pending/apply/active/disabled + rollback**：覆盖二-2。补进 Phase 3 控制面，不进 Phase 1。它触及绑定原子性、回滚、下游可追踪，可能需要 schema 和 admin API，按项目规则属于高风险确认点。
3. **OpenAPI contract redline tests**：覆盖二-10。补进 Phase 0/工程纪律，作为每个 endpoint PR 的测试门槛。它是低风险测试/CI，不需要等功能阶段。
4. **Ops trace 单条视图 + 六面板真实数据**：覆盖一-12、二-11。补进 Phase 3，依赖 Phase 1 PR4/PR5 先产生真实 attempt/usage/claim/health/audit 数据。禁止先用 mock UI 代替主链证据。
5. **显式 slot 四态 telemetry**：覆盖一-4 的细差异。补进 Phase 1 PR5 或 Phase 3，先以内存/metrics/audit 字段表达 queued/limited/assigned/timed_out；若要建持久状态表，另报 Owner。
6. **`strategy_id` 灰度与版本化 error/provider override**：覆盖一-3、二-3、二-8。补进 Phase 1 PR5：先记录 `strategy_id`、policy version、reason-code；provider override 表或 DB 配置后置到 Phase 5/Phase 3，避免 PR1/PR2 范围膨胀。
7. **日志分级、脱敏白名单、错误体积限制 contract tests**：覆盖二-12。补进 Phase 3/7 安全硬化，并给 gateway error writer、audit writer、transport sidecar fallback 各加测试。

不建议列为“真新增立刻做”的项：`request_attempts` 表。它在旧 accapi 计划里出现过，但当前方向 1 Phase 1 synthesis 已明确“不改表结构，复用 `attempt_seq`”（`docs/process/plans/2026-05-21-phase1-design-synthesis.md:19-24,55-66`）。是否恢复成 append-only attempt table，应在 Phase 1 跑通后用实际 trace 查询缺口来决策。

## 4. Owner 3 周排班重排建议

第 1 周建议改成 Phase 1 PR3/PR4 的真实顺序：先 handler attempt loop skeleton（retry 关闭、行为不变），再 PR4 billing/claim 原子性，最后 PR5 打开 retry/failover。`request_attempt 表`不要放第 1 周；route failure reason 属 PR2/PR3/PR5；sticky 来源矩阵可以作为 PR5 acceptance，无 schema。

第 2 周拆分：route strategy enum / fill-first / cooldown 配置可进入 Phase 1 PR5 后半；credential refresh、disable-cooling、max-retry-credentials 属 Phase 2 洞④；Ops trace 页面必须等 PR4/PR5 有真实 usage/claim/audit 数据后进 Phase 3。

第 3 周里，provider onboarding 生命周期可并入 Phase 2/3；管理端安全边界、日志分级、多 tenant 回归属于 Phase 3/7。邀请/返佣 trust chain 不应抢 Phase 1 P0 主链资源，除非 Owner 单独把社区/账务增长链列为同等优先级。

## 5. `request_attempt` 建表判断

独立判断：**Phase 1 当前不应新建 `request_attempts` 表，应先复用现有 claim/usage/billing spine，把 `attempt_seq` 跑通。**

理由：

1. 当前 Phase 1 权威设计已经把“不改表结构”作为裁决，且 `attempt_seq` 已存在（`docs/process/plans/2026-05-21-phase1-design-synthesis.md:19-24`）。PR4 明确修 `ReReserveAbortedClaim`、清 stale account/token、失败 attempt 写零费用作废记录、最终只一次正费用成交（`docs/process/plans/2026-05-21-phase1-design-synthesis.md:55-66`）。
2. 现有 SQL 已有足够的最小 spine：claim lookup/insert 返回 `attempt_seq`（`backend/sql/queries/billing_claims.sql:10-17,28-40`），re-reserve 会递增 `attempt_seq`（`backend/sql/queries/billing_claims.sql:57-72`），settle/usage 写 `provider_account_id`、`acquisition_token`、`attempt_seq`、`routing_reason`、`protocol_loss`（`backend/sql/queries/billing_settle.sql:28-59`），abort path 也写零费用 usage/billing evidence（`backend/sql/queries/billing_settle.sql:114-120`）。
3. 新表会把 Phase 1 的高风险集中点从“handler + billing 原子性”扩大到 schema、sqlc、migration、rollback、ops 查询、旧 spec 清理。项目规则把 DB schema、billing ledger、quota enforcement 都列为高风险；在 PR4 之前加表会增加失败面。
4. `request_attempts` 作为 append-only forensic table 的价值是真实的，但它应该由证据缺口驱动：当 Phase 1 跑通后，如果 claim/usage/billing 无法回答“这次为什么切了几次、每次从哪个 account 到哪个 account、哪次已交付、哪次计费”，再在 Phase 3 设计 append-only `attempt_events` / `request_attempts`。那时字段、索引和 UI 查询会更清楚。

所以我的建议是：Phase 1 不建表；PR4/PR5 必须保证每个失败 attempt 至少落到 usage/billing/audit 可解释记录；Phase 3 再决定是否加 append-only attempt table。旧 `docs/specs/client-identity.md` 对 `request_attempts` 的前置描述应标为 stale，不应作为建表理由。

## 6. 风险与盲点

1. **把旧基线缺口当当前缺口**：PR1/PR2 已把多候选 planner 和 taxonomy 推进到“在做”。后续排期要避免重复规划已提交的内容。
2. **过早 schema 化**：`request_attempts`、binding lifecycle、slot 四态、identity_signal_config 都可能合理，但一起塞进 Phase 1 会放大 DB/billing/quota 风险。
3. **参考项目论断过宽**：CLIProxyAPI 能证明策略/session/retry/cooldown，不证明热路径刷新；sub2api 能证明 failover/slot/ops，不等价证明 HUAKAI 账务四元索引。
4. **前端先行会造假**：Ops trace 页面如果早于 PR4/PR5 数据，只能 mock。必须先有真实 `request -> claim -> attempt_seq -> usage/billing/audit/health` 数据。
5. **streaming 边界最容易出事故**：first token 后、工具调用后、partial delivery 后都不能简单换号；否则会双扣费、重复副作用或把半截响应伪装成成功。
6. **多租户错误体泄漏不是单点修复**：需要 error writer、audit writer、provider error parser、front-end display 一起收敛；只在某个 handler 里 redact 不够。
7. **旧文档漂移会误导执行**：`docs/specs/client-identity.md` 和 5 月初 accapi 计划多处假定 `request_attempts` 已存在；当前迁移没有。后续必须在 Phase 3 前清理或重开决策。

## 7. 结论

Owner 这份分析应被采纳为“方向 1 的主链补强清单”，但不能原样变成 3 周施工单。正确路径是：先完成 Phase 1 PR3-PR5，让 retry/failover/attempt_seq/usage/billing 真跑通；同时把 session source matrix、contract tests、trace UI、binding lifecycle 标为新增项，分别放入 Phase 1 acceptance、Phase 0 CI、Phase 3 控制面，而不是在第 1 周直接建 `request_attempts` 表。

功能缩水判断：不建议删除任何 Owner 提出的功能；对高风险项改为 Safe Equivalent / Phase 后置。clean-room 风险：本文只做行为级评估和 file:line 证据引用，未复制参考项目代码、结构或算法实现。安全风险：主要在 schema/账务/多租户错误泄漏/credential trace，已在上文标出需要 Owner 确认的位置。

---

Agent: Codex (GPT-5.5 Codex)

读过的文件/来源：

- `docs/process/plans/2026-05-21-direction-1.md`
- `docs/process/plans/2026-05-21-account-to-api-gap-analysis.md`
- `docs/process/plans/2026-05-21-phase1-design-synthesis.md`
- `backend/internal/router/route_plan.go`
- `backend/internal/router/default_router.go`
- `backend/internal/gateway/attempt_error.go`
- `backend/internal/gatewayhttp/chat_completions_dispatch.go`
- `backend/internal/gatewayhttp/chat_completions_attempt.go`
- `backend/internal/registry/registry.go`
- `backend/internal/registry/postgres_registry.go`
- `backend/internal/auth/api_key_resolver.go`
- `backend/internal/credentialstore/postgres_store.go`
- `backend/internal/credentialacq/oauth.go`
- `backend/internal/credentialacq/finalizer.go`
- `backend/internal/pool/dispatcher/slot_manager.go`
- `backend/internal/proto/protocol_loss.go`
- `backend/sql/queries/billing_claims.sql`
- `backend/sql/queries/billing_settle.sql`
- `backend/sql/queries/observability.sql`
- `backend/sql/migrations/0002_observability_billing.up.sql`
- `docs/specs/channel-health-auto-disable.md`
- `docs/specs/client-identity.md`
- 本地参考源码：`/home/codex/refs/sub2api` at `91da8159`，定向读取 `backend/internal/repository/api_key_repo.go`、`backend/internal/repository/idempotency_repo.go`、`backend/internal/service/gateway_service.go`、`backend/internal/handler/failover_loop.go`
- 本地参考源码：`/home/codex/refs/CLIProxyAPI` at `21fad9dbb447a2ab70d51d0ac3e3d032525a6054`，定向读取 `config.example.yaml`、`internal/config/config.go`、`sdk/cliproxy/builder.go`、`sdk/cliproxy/auth/selector.go`、`sdk/cliproxy/auth/conductor.go`
- `rg` 检索范围：`backend/sql`、`docs/schema`、`docs/specs`、`docs/process/plans`，仅用于确认 `request_attempts` 是否已在现行迁移/查询中落地；未读取 Claude 对本材料的评估稿。

UTC timestamp: 2026-05-21T09:26:59Z
