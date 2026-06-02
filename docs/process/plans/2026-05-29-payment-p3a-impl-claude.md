# P3a 订阅子系统 实现计划 (refined) — Claude — 2026-05-29

> 承接 `2026-05-29-payment-p3-synthesis.md` (Owner 已锁 D1 quota-only / D2 每周期自动续 / D3 首切片含分组+存储)。
> 本文是执行前的 refined 实现计划 (CLAUDE.md #9 plan-before-execute, >200 LoC)。分支 work/quota-subsystem。
> codex 平行讨论 = 整合进 #8 强制 per-commit review (本切片 money-adjacent 但 quota-only, 属中难度 tier: Claude 起 plan + review 把关)。

## Clean-Room Guard (CLAUDE.md #11 / AGENTS.md)
- **Lane**: specifier (读参考源码 → 行为摘要)。本 artifact 由 specifier lane 单独成文。
- **REFERENCE PROJECTS IN SCOPE** (#16 默认三镜): sub2api (LGPL/AGPL, 仅行为摘要禁 vendoring)、CLIProxyAPI (MIT, 本域无等价 = no-equivalent)、new-api (行为摘要)。
- **硬禁止**: 不逐字复制上游函数名/结构字段名/注释; 不复制原始代码块; 不做逐行算法翻译; 行为用与上游代码顺序不同的句式转述。**file:line 引用允许**作证据, 但被引标识符本身不在散文 verbatim 复用 (本文已按此转述上游字段/函数名)。
- **Source files read**: sub2api@91da8159:backend/migrations/003_subscription.sql, 095_subscription_plans.sql; new-api@20d3e73:service/subscription_reset_task.go; CLIProxyAPI@21fad9db (grep 确认无订阅)。Lane: specifier｜Agent: Claude Opus 4.8｜UTC: 2026-05-29。

## 0. 本轮新解决的设计岔路 (synthesis 未定死, 写代码前必须定)

**问题**: migration 0072 早稿同时含「窗口化三档 cap (daily/weekly/monthly_cap_usd)」**和**「reset_period + 周期游标 (next_reset_at/current_period_start)」——这是**两套并存的重置模型**, 写事务存储前必须二选一/合一。

**配额窗口锚点**: 按日历边界重置 (每月1号), 还是按购买日锚定 (买在15号→每月15号)?

### 参考项目对照 (#15, source-read #12)
| 项目 | 重置模型 | 证据 |
|---|---|---|
| sub2api | 每订阅自带**三个独立滑动窗口**: 日/周/月各一个窗口起点时间戳列 + 各自一个 USD 已用量计数列; 三档 USD 花费上限挂在分组行上; 窗口锚定**订阅激活时刻**, 用量直接记在订阅行内 | sub2api@91da8159:backend/migrations/003_subscription.sql (订阅表三窗口起点 + 三用量列; 分组表三上限列) |
| new-api | master 节点 cron **每分钟**跑两个批处理: 一个扫到点订阅置失效, 一个扫到点订阅重置配额; worker 驱动周期重置, 锚定订阅自身的下次重置时刻 | new-api@20d3e73:service/subscription_reset_task.go (周期任务启动器, 批量 300) |
| CLIProxyAPI | 无订阅概念 (no equivalent) | CLIProxyAPI@21fad9db: grep 无 subscription |

### HUAKAI quota 引擎现状 (自有, 读源确认)
- `ResolvePolicies` 对同 scope+metric 的**所有** enforce 策略一并评估 → 任一超限即 deny ⇒ 三档 cap 可直接落成 3 条策略, 原生同时强制 (`backend/internal/quota/policy.go:53-78`, `service.go:251`)。
- **无匹配策略 → `DecisionAllow`** (allow-by-default) ⇒ quota 策略是**花费天花板**, 不是访问授予 (`backend/internal/quota/service.go:147-156`)。
- 窗口: `none`(从 ValidFrom 累计) / `fixed`(epoch 对齐) / `calendar_day` / `calendar_week` / `manual`; **无 calendar_month** (`backend/internal/quota/rate_window.go:13-40`, types.go:41-47)。

### 决策 (sub2api 三档模型 + HUAKAI 日历窗口 delta — 可逆假设)
- **保留**三档 cap (daily/weekly/monthly_cap_usd, nullable); 默认按 sub2api 支持三档 (单档=只填其一)。
- 每条非空 cap → 一条 quota 策略: `scope=user`(scope_id=user_id), `metric=cost_usd`, `window=calendar_day/week/month`, `limit_value=cap`, `mode=enforce`, `valid_from=starts_at`, `valid_until=expires_at`。
- 窗口**按日历边界**自动重置 ⇒ **不需要周期重置 worker** (引擎免费重置)。砍掉 `reset_period` / `next_reset_at` / `current_period_start` / `ListDueReset` / `ResetSubscription` / `idx_..._due_reset`。
- `valid_until=expires_at` ⇒ 订阅到期 cap 随之失效 (纵深防御, 不靠 worker 抢跑)。
- **唯一 worker = 到期 worker**: 标 expired + 关 (disable) quota 策略 + 降级 user_group (受 downgrade 守卫)。
- **delta vs sub2api**: sub2api 从激活时刻滑动, HUAKAI 对齐日历。代价: 首个不完整周期更短。收益: 可预测/透明 ("每日历月 $X")——契合 HUAKAI 透明差异化, 且引擎原生免 worker。**记为可逆假设**: 若 Owner/codex 要求按购买日锚定, 改为 worker close/reopen `WindowNone` 策略 (加回周期游标)。
- **PRIMARY entitlement = granted_group** (路由访问); caps 是 guardrail。到期降级 = 真正的"停服"。

### 三维 fusion delta
- 架构: 订阅配额收敛进统一 internal/quota 引擎 (两镜各自独立 in-row 计数器); 无第二配额引擎。
- 算法: 日历窗口自动重置 + valid_until 随订阅到期失效, 免周期 worker; 到期降级查"更新 active 升级"避免误降。
- 生态: 激活/到期/升降组全进 subscription_audit_events (信任链可审计)。

## 1. Schema 改动 (相对早稿 0072)
- **renumber**: `0072_quota_calendar_month` (quota 引擎补月窗口 CHECK) + `0073_subscription` (订阅, 由早稿 0072 改名)。
- 0072_quota_calendar_month: `ALTER TABLE quota_policies` 改 window_kind CHECK 加 `'calendar_month'` (DROP+ADD CONSTRAINT)。down 还原 (无 calendar_month 策略时安全)。
- 0073_subscription: subscription_plans / user_subscriptions / subscription_policy_links / subscription_audit_events + `users.user_group` 列。**删** reset_period (两表) / current_period_start / next_reset_at / idx_user_subscriptions_due_reset。

## 2. quota 引擎补 calendar_month (Commit A, quota 模块)
- types.go: `WindowCalendarMonth WindowKind = "calendar_month"`。
- rate_window.go: ComputeWindow 加 case — start=当月1号00:00 UTC, end=start.AddDate(0,1,0)。
- rate_window_test.go: 判别测试 (月初/月末/跨年边界; mutation: 用 AddDate(0,0,30) 假实现 → 2月该红)。
- migration 0072_quota_calendar_month。

## 3. internal/subscription 包 (Commit B)
- types.go / store.go: 删 reset 机制 (见 §0)。
- store_postgres.go: 事务核心 (SERIALIZABLE + 40001/40P01 retry, 仿 voucher/payment):
  - **AssignSubscription** (幂等): 同 (tenant,user,granted_group) 已有 active → 返回现有 + audit `idempotent_replay` (Idempotent=true)。否则单事务: INSERT user_subscription (plan 快照) + 每非空 cap 调 quota store 插策略 (raw SQL into quota_policies) + INSERT subscription_policy_links + UPDATE users.user_group (capture prev_user_group) + audit (created/activated/group_upgraded)。靠 `uq_user_subscriptions_active_group` partial unique 兜并发。
  - **ExpireSubscription** (幂等, 已 expired→no-op): 标 expired + close policy_links + disable quota_policies + 降级守卫 (仅当无更新 active 升级订阅占用同组才还原 prev_user_group) + audit (expired/group_downgraded)。
  - **CancelSubscription**: 同 expire 但 status=cancelled + cancelled_at + audit cancelled。
  - **ListDueExpiry**: active 且 expires_at<=now, limit, tenant 内。
  - Plan CRUD: CreatePlan/GetPlan/ListPlans/DisablePlan。
- store_memory.go: 同语义内存实现 (单测用)。
- service.go: 校验 (validity_days>0, caps>=0, plan enabled) + 编排 + 错误映射。
- worker.go: 到期 worker (tick, 扫 ListDueExpiry, 逐条 ExpireSubscription; CompareAndSwap 防重入, 仿 new-api)。

## 4. internal/subscriptionhttp (Commit B 或单独)
- admin: plan CRUD + assign/cancel + 查询; user: 查自己的订阅。不进冻结 gatewayhttp。

## 5. wiring / lifecycle / routes (串行协调点)
- wiring.go 构造 service; lifecycle.go 启停到期 worker; routes.go 挂载。编辑前查新机未并发改。

## 6. 测试矩阵 (mutation-discriminating, 真 PG)
| # | 风险 | 测试 | mutation 自检 |
|---|---|---|---|
| 1 | 双权益 | 同 (user,group) 重复 assign → 仅一条 active + 仅一组策略 | 去 partial unique / 去幂等检查 → 双 active → 红 |
| 2 | 越权访问 | assign → users.user_group=granted_group + prev 记原值 | 漏 UPDATE users → 组未变 → 红 |
| 3 | 误降级 | 用户有"更新的 active 升级订阅", 旧订阅到期 → **不**还原 prev (守卫) | 去守卫 → 误降 → 红 |
| 4 | 配额未生效 | assign 后 quota_policies 出现对应 cost_usd/calendar 策略 (用 quota service reserve 真验 deny) | 漏插策略 → 不 deny → 红 |
| 5 | 到期未停 | expire 后 policy_links closed + quota_policies disabled | 漏 disable → 仍 enforce → 红 |
| 6 | 跨租户 | tenant A assign 不动 tenant B 的 users/policies | 漏 tenant 谓词 → 串租户 → 红 |
| 7 | 并发授予 | 两并发 assign 同 (user,group) → 一成一幂等, 不双权益 | 非事务/无 unique → 双 → 红 |
| 8 | 月窗口 | calendar_month ComputeWindow 边界 (见 §2) | AddDate(0,0,30) → 2月红 |

## 6b. 运行时强制尚未接线 (codex review S2 → 强制路标, 非缺陷)
P3a 是**权益数据 + admin 生命周期层**, 不含请求热路径强制。两条接线明确推迟为独立切片 (触冻结包 + 新机领地):
- **R-SUB-WIRE-1 分组→路由**: `users.user_group` 已写, 但模型解析 (`internal/registry` ResolveModel → 池绑定) 尚未读 user_group / routes.user_group_match。⇒ group-only 订阅当前**不改变上游路由**。接线触请求热路径 (冻结 gatewayhttp + 需把 user_group 透传进请求上下文), 列独立切片。
- **R-SUB-WIRE-2 配额闸→热路径**: 订阅装的 `quota_policies` 已就位, 但请求路径走 `billing.DefaultClaimGate.Reserve`, 尚未调 `internal/quota` Reserve/ResolvePolicies (quota 子系统一直是控制面、未接热路径, 见 project_two_data_planes)。⇒ cap 当前**不在线强制**。接线触新机 billing 领地 ClaimGate, 串行协调后做。
- **诚实声明**: 在 R-SUB-WIRE-1/2 落地前, 分配订阅会正确落库 (订阅行/分组列/quota 策略/审计) 且 admin/user API 可见, 但**用户侧不会观察到路由升级或花费封顶**。commit body 必须显式说明, 不得当作"已强制"。Feature Preservation = 安全等价(Mandatory Roadmap), 非功能缩水。

## 6c. 已知限制 (codex R2 → 记票, 当前安全)
- **每用户每 granted_group(含空串)限一条 active 订阅** (`uq_user_subscriptions_active_group`)。⇒ 同一用户的两个"纯 cap 无分组"套餐, 第二个 assign 会被当幂等返回第一个 (不重复装策略), API 返回 `idempotent=true` 可见信号。**当前行为保守安全**(不超发权益、不双账)。支持同用户多个独立 cap-only 套餐需把幂等键/unique 扩到含 plan 维度 = schema 重设计 → P3b。多数套餐会绑分组, 该限制实际影响小。
- **关闭订阅后 `resolveGroupFromActiveTx` fallback = default**: 前提是 **P3a 当前没有任何"订阅之外写 `users.user_group`"的入口** (migration 默认 'default', 唯一写者是订阅 store)。故用户订阅前的组恒为 'default', fallback=default 对所有可达状态正确。`prev_user_group` 列为**未来**引入 operator 手动设组入口而保留: 届时需区分"基线组(operator 设)"与"订阅授予组", 关闭时若无剩余 active 订阅且 prev 非订阅驱动则还原 prev, 否则回 default。当前无此入口, 不实现, 避免链式升级 (default→basic→premium) 误还原到已到期的 basic。

## 7. 落地纪律
- 每 commit 前 `codex exec review --uncommitted -c model_reasoning_effort=xhigh --enable fast_mode < /dev/null` (read-only), ≤2 轮, S0/S1 必修阻塞 / S2/S3 记票。
- 验证: 重建 scratch DB → `migrate up` → `HUAKAI_DATABASE_URL=<socket dsn> go test -tags=integration_pg ./internal/subscription/... -count=1` + `go build ./...`。
- 触 `users` 核心表 (加 user_group 列) = 合并前与新机协调; push 仅 work/quota-subsystem。
- 收尾 #per-slice ref recompare: 对照 sub2api/new-api 订阅同模块查缺补漏 + 三维升级点。

## 8. Source files read
HUAKAI 自有: internal/quota/{types.go,rate_window.go,policy.go,service.go}, sql/migrations/{0070,0072}。
参考 (行为级): sub2api@91da8159:backend/migrations/003_subscription.sql,095_subscription_plans.sql; new-api@20d3e73:service/subscription_reset_task.go; CLIProxyAPI@21fad9db (grep no-equivalent)。被引标识符未在散文 verbatim 复用。

Lane: specifier｜Agent: Claude Opus 4.8｜UTC: 2026-05-29
