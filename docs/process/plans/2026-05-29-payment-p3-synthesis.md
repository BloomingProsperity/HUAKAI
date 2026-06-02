# 支付 P3「订阅」综合稿 (Claude ∥ Codex 交叉 + Owner 决策) — 2026-05-29

> CLAUDE.md #10 平行综合。Claude 稿 `payment-p3-claude.md` ∥ Codex 稿 `payment-p3-codex.md` 独立成文后交叉。分支 work/quota-subsystem。

## 0. Claude ∥ Codex 对齐 (强一致)
两稿独立成文,核心设计高度一致:
- 新建 `internal/subscription` + `internal/subscriptionhttp`(非冻结包),不碰 gatewayhttp/gateway/proto。
- 复用 P1/P2 支付单 + webhook 做购买入口;订阅激活观察已完成支付单。
- 配额复用 `internal/quota` 多层 policy,**不另建消费计数器**。
- 到期降级:仅当无更新的 active 升级订阅时才降回原组(new-api@20d3e73:model/subscription.go:822)。
- migration 0072 高风险 Owner-gated;lifecycle.go worker = 与新机串行协调点。
- mutation-discriminating 真 PG 测试(codex 给了 15 行矩阵,含重复授予/到期降级/周期重置/跨租户/并发激活)。
- 唯一分歧:codex 把 Owner「余额+配额」读成 C(两者),围绕余额授予 + credit-writer 抽取 + payment_credits 多源 migration 设计;Claude 把 A/B/C 列为待 Owner 决策。→ **Owner 已拍板 ↓**

## 1. Owner 决策 (AskUserQuestion 2026-05-29)
| 决策 | Owner 选择 | 影响 |
|---|---|---|
| D1 订阅授予什么 | **只给配额套餐**(B,非余额) | **覆盖**早先「余额+配额」。订阅 = 分层配额(internal/quota policy)+ 周期重置,**不充余额**。⇒ **无 billing_events/payment_credits 写、无 credit-writer 抽取、无 migration 碰 P1 钱表、无新机 billing 协调**。两稿的余额机制全部丢弃。 |
| D2 到账节奏 | **每周期自动续** | worker 周期重置配额窗口;到期不续费停。对照 new-api next_reset(model/subscription.go:933)+ sub2api 窗口重置(subscription_service.go:793)。 |
| D3 分组升级 | **首切片就连分组一起做** | P3a 含订阅升级用户组 + 到期降级。**但见 §2 发现:需先建用户→组绑定存储**。 |

## 2. 关键发现 (Owner 答 D3 时未知 — 需回确认)
**HUAKAI 当前无"用户→分组"绑定存储**:
- `users` 表(0007_l0_inbound_auth.up.sql:18)列 = id/tenant_id/email/display_name/status/时间戳,**无 group/tier 列**。
- 全仓**无 user_groups / membership / entitlement 表**。
- 唯一 `user_group` 出现处 = `routes.user_group_match`(0001_pool_routing.up.sql:227,'default'/'premium')—— 这是**路由规则匹配的字符串**,不是用户被分到哪组的存储。
- 结论:HUAKAI 有 premium 路由基础设施,但**没有把用户分到 premium 的机制**(当前所有用户实际等同 'default')。

⇒ Owner 选「首切片连分组」⇒ P3a 必须**额外建用户→组绑定**(给 users 加 `user_group` 列 或 新建 user_group_assignments 表)+ 订阅激活时 set、到期 restore。这是净新基础设施,触 `users` 核心表(schema = Owner-gated)。属 Claude 领地(auth/user)。
对照:两镜都内建用户组列(new-api User.group + upgrade_group/prev_user_group model/subscription.go:471/822;sub2api 绑订阅型 group)。HUAKAI 缺这一层,需补。

## 3. 待回确认 (group 路径) — 见 AskUserQuestion
- **路径 A**:P3a 含建最小 `user_group`(users 加列,默认 'default')+ 订阅 set/restore。首切片更大,但一步到位符合 Owner「连分组」。
- **路径 B**:P3a 只做订阅 plans+user_subscriptions+quota policy+周期重置+到期(状态),分组升降级 + 用户→组存储留 P3b。首切片小而闭合。

## 4. 重定范围后的 P3a (quota-only, 待 group 路径定)
- migration 0072:`subscription_plans`(tenant/名称/价格/周期/reset 周期/quota 模板/upgrade_group/enabled) + `user_subscriptions`(tenant/user/plan 快照/status/start/end/period 游标/upgrade_group/prev_group) [+ 若路径A: users.user_group 列] [+ subscription_policy_links 关联 quota policy] + subscription_audit_events。**不碰 payment_credits/billing_events**(quota-only)。
- internal/subscription:types/store/service(admin bind = 建 user_sub + 装 quota policy [+ 升组])/worker(周期重置 + 到期降级)。
- internal/subscriptionhttp:admin plan CRUD + bind/cancel + 查询。
- 复用 internal/quota 装 policy(scope=user,window=period,metric=cost/request);不另建计数器。
- 购买路径:P3a 先 admin-bind(无支付);P1/P2 支付购买激活 = P3b。
- wiring + lifecycle(worker,串行协调)+ routes。

## 5. fusion-upgrade delta (三维)
- 架构:订阅配额收敛到 internal/quota 统一引擎(两镜各自独立计数器);无第二配额引擎。
- 算法:周期重置开新窗口而非清旧计数(保审计历史);到期降级查更新 active 升级。
- 生态:订阅激活/重置/到期/升降组全进 subscription_audit_events(信任链可审计)。

## 6. Source files read
HUAKAI(自有):internal/payment/*、internal/quota/types.go、sql/migrations/{0001,0007,0071}。参考(行为级 #12):sub2api@91da8159(subscription_service.go、payment_config_plans.go)、new-api@20d3e73(model/subscription.go)、CLIProxyAPI@21fad9db(grep 确认无订阅=no equivalent)。被引标识符未在散文 verbatim 复用。

## 7. 拉最新核对 (Owner 2026-05-29「务必拉最新的更新」)
codeload tarball 拉最新(沙箱 git fetch 挂死),核对订阅模型:
- **sub2api @1d46be02**(今日 HEAD,base @91da8159 为 9 天前):核心订阅模型**未变**(subscription_service/payment_config_plans/user_subscription_repo 行数与 func 完全一致);唯一变化 = `subscription_expiry_service.go` 71→150 行,**新增订阅到期提醒邮件**(到期前 7/3/1 天,setting 开关 + NotificationEmailService)。→ **P3b/路标**(Feature Preservation,不丢)。
- **new-api @15880270**(今日 HEAD):核心模型未变;新增 `SubscriptionPlan.NormalizeDefaults` + `calcSubscriptionBalanceQuota` + `PurchaseSubscriptionWithBalance`(**用余额购买订阅**)。→ **P3b 融合点**:HUAKAI 有 P1/P2 真余额,可让用户用余额买订阅(new-api 刚补此能力,HUAKAI 站肩膀升级)。
- 结论:P3a(quota-only / admin-bind / 周期重置 / 分组)grounding 仍正确;两新功能 roadmap 到 P3b。后续 cite 用最新 SHA(sub2api@1d46be02 / new-api@15880270)。

## 8. P3a 地基已落 (本轮)
migration 0072(`subscription_plans` + `user_subscriptions` + `subscription_policy_links` + `subscription_audit_events` + `users.user_group` 列)已写 + 本地 PG up/down/up round-trip 验证通过。**不碰 payment_credits/billing_events**(quota-only)。⚠️ 触 `users` 核心表(加 user_group 列,additive 默认 'default')= Owner-gated schema,合并前与新机协调 users 表;随 P3a package+tests 一起 land。

Lane: specifier｜Agent: Claude Opus 4.8｜UTC: 2026-05-29
