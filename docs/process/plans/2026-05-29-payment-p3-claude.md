# 支付子系统 Slice P3「订阅」实施计划 — Claude 独立稿 (2026-05-29)

> CLAUDE.md #10 平行计划 Claude 一侧,独立成文未参考 codex 稿。#16 三镜调研已做。分支 work/quota-subsystem,实现由 Claude 写代码。

## 0. 三镜 shape inventory (开写前调研, #16)

| 维度 | sub2api@91da8159 | new-api@20d3e73 | CLIProxyAPI@21fad9db |
|---|---|---|---|
| 计划模型 | 订阅型 group + 窗口 USD 上限 (`payment_config_plans.go:123` CreatePlan: price/validity_days/validity_unit/for_sale) | `SubscriptionPlan` (`model/subscription.go:145`: price/duration/total_amount 配额量/upgrade_group 分组升级/quota_reset_period) | **无等价** (纯 relay, 无订阅/计划模块) |
| 用户订阅实例 | `UserSubscription` (`subscription_service.go:357`: starts/expires/status + 日/周/月 window_start+usage_usd) | `UserSubscription` (`model/subscription.go:234`: amount_total/amount_used 计数器 + start/end + last/next_reset_time + upgrade_group/prev_user_group) | 无等价 |
| 授予什么 | **窗口化花费上限**(日/周/月 USD cap, 用进废退不累积) | **配额量计数器**(amount_total, PreConsume 扣减)+ **用户分组升级** | 无等价 |
| 周期重置 | lazy(`NeedsDailyReset`)+ 维护队列异步 (`subscription_service.go:907`) | next_reset_time 到点 amount_used=0 (`model/subscription.go:933` + ResetDueSubscriptions worker `:1100`) | 无等价 |
| 到期降级 | status=expired → 中间件拒 (`:1098` ValidateSubscription) | ExpireDueSubscriptions worker (`:823`) 标 expired + 降级回 prev_user_group(除非另有 active 升级订阅) | 无等价 |
| 购买路径 | 支付单 → 履约 → 订阅;admin 分配 | SubscriptionOrder(trade_no unique)→ webhook CompleteSubscriptionOrder(`:511` FOR UPDATE 幂等);AdminBindSubscription(`:645`) | 无等价 |
| 幂等 | assign exists→幂等返回/冲突检测 | trade_no unique + status guard + PreConsumeRecord.request_id unique | 无等价 |

**关键发现**:两镜**都不"充真余额"**——sub2api 给窗口花费上限,new-api 给配额量计数器+分组升级。HUAKAI Owner 决策「授予余额(billing_events 入账)」是**两镜都没有的升级点**(真余额账本 + 信任链可审计)。这正是 fusion-upgrade。

## 1. 核心 Owner 决策 (D1: 订阅授予什么) — 必须先定才能开工

Owner 原话「订阅 = 余额 + 配额策略」。落成 3 个具体模型 (带三镜对照):

- **A 余额充值型**:订阅周期性充一笔**真余额**(走 P1/P2 billing_events payment_credited 入账 seam),用户像充值余额一样消耗,不累积/累积按策略。
  - 参考对照:无单一参考这么做;最接近 new-api `amount_total` 配额量但 HUAKAI 用真余额账本 (new-api@20d3e73:model/subscription.go:239 amount_total/used 是计数器非余额账本);sub2api 无余额授予 (windowed caps `subscription_service.go:842`)。**HUAKAI 升级=订阅→真账本入账**。
  - 优点:复用 P1/P2 单一可信入账点,余额透明可审计(契合信任链 [[project_core_trust_chain_differentiator]])。缺点:缺"套餐分层限速"语义。
- **B 配额策略型**:订阅给一个分层配额策略(Pro 档 $X/天 或 N/月),用 **internal/quota** 引擎按窗口限流 + 周期重置(用进废退),不充余额。
  - 参考对照:sub2api 窗口化日/周/月 USD 上限 (sub2api@91da8159:subscription_service.go:842 CheckUsageLimits) + new-api quota_reset_period (new-api@20d3e73:model/subscription.go:933 maybeReset)。**主流做法**。
  - 优点:真"套餐"语义,贴近两镜。缺点:不走余额账本(钱事实弱),与 P1/P2 入账核心解耦。
- **C 两者叠加 (fusion)** ← 最贴 Owner「余额 + 配额策略」原话:订阅既充余额(billing_events)又附配额策略(internal/quota 限速/上限)。余额=钱底,配额=分层管控。到期降级。
  - 参考对照:无单一项目这么做(new-api 给 quota+分组升级 model/subscription.go:438 CreateUserSubscriptionFromPlanTx; sub2api 给 windowed caps);**HUAKAI 三维融合**(架构:双层 = 余额账本 + 配额策略;算法:两层取严;生态:可审计)。
  - 优点:最强最灵活。缺点:最复杂——须讲清**余额与配额上限如何交互**(配额是"限速"还是"硬上限"?余额耗尽 vs 配额耗尽谁先拦?),实现最大。
  - **若选 C 还需 D1b 子决策**:配额层语义 = (i) 限速(rate/窗口 cap,不扣余额);(ii) 硬上限(封顶月消费);(iii) 余额来源标记(订阅余额优先于充值余额消耗)。

**D2: 用户分组升级/降级**:两镜都"购买升级 group、到期降级回原 group"(new-api upgrade_group/prev_user_group)。HUAKAI 是否要分组维度?需查 HUAKAI user/group 模型(internal/quota 已有多层 scope,可能复用)。建议 P3a 先不做分组升级,订阅只授予余额/配额,分组升级留 P3b。

**D3: 切片粒度**:订阅大,小切片闭合 → P3a(plan + user_subscription 表 + 授予 skeleton + 幂等)先闭合;P3b(周期重置 + 消费集成 + 到期 worker + 分组降级)后续。

## 2. 范围 (P3a 首个闭合切片, 假设待 Owner D1 定)
- **In**:`subscription_plans` + `user_subscriptions` 表(migration 0072);plan CRUD(admin);授予路径(购买/admin 分配 → 创建 user_subscription + 按 D1 授予余额/配额);幂等(plan 购买单 unique + 授予幂等);到期状态 + 基础查询。
- **Out (→P3b)**:周期重置 worker、消费扣减集成到 gateway 计费、到期降级 worker(lifecycle.go)、分组升级。
- **不碰**:冻结 gatewayhttp/gateway/proto;internal/billing 及新机 money 文件。handler 进 internal/paymenthttp 或新 subscriptionhttp 包(非冻结)。

## 3. 设计 (待 D1 收敛后细化)
- 授予复用:若 D1=A/C,授予余额复用 P1 `Fulfill` 入账核心(订阅授予 = 一条 payment_credited,reason_class=subscription_grant);若 D1=B/C 的配额层,复用 internal/quota policy 写入。**单一可信入账点不重复钱逻辑**。
- 包结构:internal/payment 现 10 文件,加订阅可能超预算(~20)→ 评估新建 **internal/subscription** 包(非冻结)。migration 0072 新表(高风险 Owner-gated)。
- 购买入账与 P2a 关系:订阅购买可走 P2a 同款回调(provider 回调 → 识别 plan → 授予),或 admin 直接分配。

## 4. mutation-discriminating 真 PG 测试 (按真实风险)
| 测试 | 守的缺陷 | 判别 fixture |
|---|---|---|
| 重复授予幂等 | 同购买单授予两次 → 双倍余额/配额 | 同 trade_no 履约两次 → 余额/配额只增一次 |
| 到期不再授予 | 过期订阅仍授予 | 过期订阅消费/重置 → 拒 |
| 跨租户隔离 | 串租户授予 | tenant A 订阅不影响 B 余额/配额 |
| 周期重置精确(P3b) | 重置丢额度/早重置 | next_reset 到点才清零,未到不动 |
| 配额耗尽拦截(B/C) | 超额放行 | 配额用尽请求被拒, 真注入触发 |

## 5. fusion-upgrade delta (三维)
- **架构**:订阅授予收敛到 P1/P2 同一 billing_events 入账 seam(若含余额)+ internal/quota 多层 policy(若含配额);两镜各自独立余额表/配额计数器 → HUAKAI 单一可信账本 + 统一配额引擎。
- **算法**:周期重置 + 到期降级 + 多层配额取严(对照 sub2api 窗口 + new-api next_reset)。
- **生态**:订阅授予/重置/到期全进 payment_audit_events + billing_events,信任链可审计(两镜审计弱)。

## 6. Source files read
HUAKAI(自有):internal/payment/*, internal/quota/*。参考(行为级 #12):sub2api@91da8159(subscription_service.go、payment_config_plans.go、user_subscription_repo.go)、new-api@20d3e73(model/subscription.go)、CLIProxyAPI@21fad9db(grep 确认无订阅模块=no equivalent)。被引标识符未在散文 verbatim 复用。

Lane: specifier｜Agent: Claude Opus 4.8｜UTC: 2026-05-29
