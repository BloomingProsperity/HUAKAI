# 支付 P3b 实现计划 — Claude 独立稿 — 2026-05-29

> CLAUDE.md #10 平行计划法 Claude 一侧, 独立成文未参考 codex 稿。分支 work/quota-subsystem。
> Owner 2026-05-29 决策: P3b = 充钱直接买套餐(选项A) + 兑换码购订阅 +「他们有的我们必须得有」(Feature Preservation); 到期提醒邮件归我照做。**不碰新机净额账本**(billing_events signed = 新机领地)。

## Clean-Room Guard (CLAUDE.md #11 / #12 / AGENTS.md)
- **Lane**: specifier (读参考源码 → 行为摘要)。
- **REFERENCE PROJECTS IN SCOPE** (#16 默认三镜): sub2api (LGPL/AGPL, 仅行为摘要禁 vendoring)、CLIProxyAPI (MIT, 本域无订阅 = no-equivalent)、new-api (行为摘要)。
- **硬禁止**: 不逐字复制上游函数名/结构字段名/注释; 不复制原始代码块; 不逐行翻译; 行为用不同句式转述。**file:line 引用允许**作证据, 被引标识符不在散文 verbatim 复用。
- **Source files read**: new-api@(refs-latest HEAD):model/subscription.go (独立订阅订单结构 / 订单完成入口 / 建订阅快照事务函数)、controller/subscription.go (余额购买开关)、service/subscription_reset_task.go; sub2api@91da8159:backend/internal/service/subscription_expiry_service.go、backend/internal/repository/redeem_code_repo.go:276 (兑换码 type 含 subscription)、backend/migrations/003_subscription.sql; CLIProxyAPI@21fad9db (grep 无订阅)。HUAKAI 自有: internal/{payment,voucher,subscription,email}/*、sql/migrations/{0071,0073}。Lane: specifier｜Agent: Claude Opus 4.8｜UTC: 2026-05-29。

## 0. 范围 (Owner 确认)
1. **F-SUB-ORDER 支付订单购买订阅**: 用户走支付渠道付款(或 admin 手动确认)买订阅, 付款成功即激活。复用 P1/P2 payment order + P2a 回调。**不入余额**(与充值订单区分: 充值→记 payment_credit/balance; 订阅订单→激活订阅, 不记余额)。
2. **F-SUB-VOUCHER 兑换码购买订阅**: 兑换"订阅券"→ 激活订阅(不入余额)。复用 voucher 兑换 + 幂等。
3. **F-SUB-REMINDER 到期提醒邮件**: 到期前分档提醒, 复用 internal/email, setting 开关, 防重发。

## 1. 参考项目对照 (#15, source-read #12)
| 维度 | new-api | sub2api | CLIProxyAPI | HUAKAI 现状 / delta |
|---|---|---|---|---|
| 订单购订阅 | 独立订阅订单结构(plan_id/trade_no/status/provider), 付款回调→订单完成入口幂等(FOR UPDATE + 跨网关回调防护)建用户订阅, source=order (new-api model/subscription.go:203,511,438) | 订阅经支付订单/兑换购买(003_subscription.sql) | 无订阅 | HUAKAI 有 payment_orders(P1)+ 回调(P2a), 但 Fulfill 固定入余额; 需区分订阅订单 |
| 兑换码 | 兑换码系统(redemption) | 兑换码 **type 含 "subscription"** (redeem_code_repo.go:276) | 无 | HUAKAI voucher 现仅余额券(amount_cents → billing_events voucher_redeemed); 需加授予类型 |
| 到期提醒 | 定时任务跑到期/重置(subscription_reset_task.go) | 订阅到期服务 ticker: 批量置失效 + 发到期提醒, setting 开关 + 通知邮件服务 (subscription_expiry_service.go) | 无 | HUAKAI 有 ExpiryWorker(P3a)+ 完整 email 子系统(smtp/dlq/factory/settings); 加提醒发送 |

## 2. 设计岔路 (需 codex 交叉 + 可能 surface Owner)
- **F1 订单购订阅怎么落**:
  - **α 独立 subscription_orders 表**(仿 new-api): 自带状态机, 付款成功 → 激活, 零入余额。清晰隔离, 但重复一套订单状态机/回调接线。
  - **β 扩展 payment_orders + order_kind('topup'|'subscription') + subscription_plan_id**: 复用现有订单状态机/provider/webhook/CAS 幂等; Fulfill 按 kind 分支(topup→入余额[现状] / subscription→激活订阅[新], 互斥不双做)。改动集中在我领地的 payment 包。
  - **Claude 倾向 β**: 复用 P1/P2 已验证的订单状态机 + 回调 + CAS 幂等(避免重复造状态机), 仅在 Fulfill 末端按 kind 选"入余额"或"激活订阅"二选一。delta vs new-api(独立表): HUAKAI 统一订单引擎 + kind 分支, 少一套并行状态机。需 Owner/codex 确认(动 payment_orders schema = 我领地内 additive 加列, 非新机)。
- **F2 voucher 授予类型**: 给 vouchers 加 `grant_kind('balance'|'subscription')` + `subscription_plan_id`(nullable)。redeem 时 balance→现状(billing_events voucher_redeemed); subscription→激活订阅(不入余额)。对照 sub2api redeem type=subscription。
- **F3 跨子系统激活原子性(难点核心)**: 付款完成/兑换 与 订阅激活 必须**单事务原子 + 幂等**, 不能"扣了款/用了券但没开通"或"开通两次"。
  - subscription 包暴露事务内激活入口 `ActivateFromOrder(tx, orderID, planID, userID)` / `ActivateFromVoucher(tx, voucherID, planID, userID)`(复用 P3a AssignSubscription 的装策略+升组+审计逻辑, 抽出 tx 版), 由 payment/voucher 在自己的完成事务里调用。**同一 DB 事务**保证原子。
  - 幂等: 订单/兑换的幂等键 + 订阅 active 唯一索引双保险(P3a 已有 uq_user_subscriptions_active_group); 重复回调/重复兑换 → 返回已激活, 不双开。
- **F4 到期提醒**: ExpiryWorker 加"到期前 N 天扫描未提醒的 active 订阅 → 发提醒 → 标记已提醒(防重发)"。需 schema: user_subscriptions 加 `reminder_sent_at`(或独立 reminded 表)记录已发档位。setting 开关复用 email settings_store。

## 3. 难点清单 (一次写全)
- 订阅订单**不得入余额**(与 topup 互斥): Fulfill 分支必须二选一, 测试要守"订阅订单完成后 payment_credits 无新增 + 订阅 active"。
- 跨子系统激活**同事务**: payment 完成事务里调 subscription 事务级激活(传 tx), 不能两个独立事务(中间崩溃 = 扣款未开通)。
- 幂等三保险: 订单 out_trade_no 唯一 + CAS 状态 + 订阅 active 唯一索引; 重复回调/兑换不双开通、不双扣。
- 兑换"订阅券"原子: voucher redeem 事务里激活订阅 + 标记 voucher 已用, 单事务。
- 到期提醒防重发: 已发档位持久化, worker 重启/多 tick 不重发; 发送失败进 email DLQ(已有)。
- 退款/取消订阅订单: 订阅订单完成后退款 = 复杂(撤销订阅 + 退款), P3b 暂不做退款, 列路标(沿用 P5 退款切片)。
- 跨租户/越权: 订单/券的 tenant + plan tenant 一致校验。

## 4. 切片顺序 (小切片闭合)
- **P3b-1 到期提醒邮件**(最小、纯我领地、非钱): ExpiryWorker 加提醒 + schema reminder 标记 + email 接线 + setting。先闭合。
- **P3b-2 兑换码购订阅**(中): voucher 加 grant_kind + subscription_plan_id; redeem 激活订阅(抽 subscription tx 级激活入口)。
- **P3b-3 支付订单购订阅**(中, 动 payment_orders): order_kind + subscription_plan_id; Fulfill 分支激活。
- 每片: 真 PG mutation 判别测试 + codex per-commit review ≤2 轮 + 闭合再开下一个。

## 5. 三维 fusion delta
- 架构: 统一订单引擎 + kind 分支(vs new-api 独立订阅订单结构); voucher 单表多授予类型(vs 余额/订阅分表)。
- 算法: 跨子系统激活走同事务 tx 传递 + 订阅 active 唯一索引幂等(vs 应用层锁)。
- 生态: 订阅购买/兑换/提醒全进 subscription_audit_events + 复用 email DLQ(失败可补发)。

## 6. 测试矩阵 (mutation-discriminating 真 PG)
| 风险 | 测试 | mutation |
|---|---|---|
| 订阅订单误入余额 | 订单购订阅完成 → 订阅 active 且 payment_credits 无新增 | Fulfill 漏 kind 分支 → 入余额 → 红 |
| 扣款未开通 | 付款完成事务原子: 注入激活失败 → 整单回滚(订单不留 completed) | 拆成两事务 → 中间崩溃留半 → 红 |
| 重复回调双开通 | 同订单重复 ConfirmPaidByCallback → 订阅仅一条 active | 去幂等 → 双开 → 红 |
| 兑换券双用 | 同券重复兑换 → 订阅仅一条 + 券 redeemed_count 不超 max | 去幂等 → 双用 → 红 |
| 提醒重发 | 多 tick → 同档提醒仅发一次 | 漏 reminded 标记 → 每 tick 重发 → 红 |
| 跨租户 | A 租户订单/券不激活 B 订阅 | 漏 tenant 谓词 → 串租户 → 红 |

## 7. Source files read (再列)
new-api@refs-latest:model/subscription.go,controller/subscription.go,service/subscription_reset_task.go; sub2api@91da8159:internal/service/subscription_expiry_service.go,internal/repository/redeem_code_repo.go,migrations/003_subscription.sql; CLIProxyAPI@21fad9db(无订阅)。HUAKAI: internal/{payment,voucher,subscription,email}, sql/migrations/{0071,0073}。被引标识符未在代码/散文 verbatim 复用。

Lane: specifier｜Agent: Claude Opus 4.8｜UTC: 2026-05-29
