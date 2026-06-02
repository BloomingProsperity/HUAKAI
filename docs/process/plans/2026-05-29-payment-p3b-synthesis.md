# 支付 P3b 综合定稿 — Claude∥Codex 平行交叉 — 2026-05-29

> CLAUDE.md #10 平行计划法综合稿。Claude 稿 (`2026-05-29-payment-p3b-claude.md`) 与 Codex 稿 (`2026-05-29-payment-p3b-codex.md`) 各自独立成文后交叉。本稿记录 agree / gaps / Owner 决策 / 定稿设计 / 切片顺序 / 测试矩阵。
> 分支 work/quota-subsystem。**不碰新机净额账本**(billing_events.actual_cost_signed = 新机领地)。

## Clean-Room Guard (CLAUDE.md #11 / #12)
- **Lane**: specifier (两稿均已读参考源码出行为摘要)。
- **REFERENCE PROJECTS** (#16 三镜): sub2api (LGPL/AGPL, 仅行为摘要禁 vendoring)、CLIProxyAPI (MIT, 本域无订阅商业化 = no-equivalent)、new-api (行为摘要)。
- **本次新读 + 核实的源码** (Owner 决策证据, file:line 验证于 2026-05-29):
  - sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/subscription_service.go:169-251 (同组分配/续期入口: 同组已有订阅→未过期从原到期日累加天数、已过期从 now 算,返回"是续期"标志、非 no-op);:271 (仅延长到期时间的更新, 不改分组配置);:545-587 (正负调整时长)。
  - new-api@local-snapshot-2026-05-29:model/subscription.go:450-525 (从套餐建订阅快照的事务函数: 每次购买恒插入一条新订阅行、各自独立 caps, 带每用户购买次数上限字段计数限购);:523-629 (订阅订单完成入口: 锁单+provider 校验+幂等返回+同事务建订阅快照+标成功);:595 (充值流水 upsert 辅助)。
- 被引标识符未在 HUAKAI 代码/本散文 verbatim 复用。

## 1. 两稿一致 (AGREE — 直接采纳, 未打扰 Owner)
两边独立得出同一架构,且有 sub2api + new-api 双源行为支撑:
1. **F1 订单购订阅 = 扩展现有 `payment_orders` 加 `order_kind`('topup'|'subscription')**,不另建独立订单表。复用 P1/P2 已验证的订单状态机/provider/回调/CAS 幂等;Fulfill 末端按 kind **单次分流**:topup→入余额(现状) / subscription→激活订阅(新, 零余额零 billing_events)。
   - 依据: HUAKAI 已有完整订单引擎 (payment/service.go:58-171, store_postgres.go:213-373);两参考亦在 order/fulfillment 层分流余额 vs 订阅效果。
2. **F2 voucher 加 `grant_kind`('balance'|'subscription')** + nullable `subscription_plan_id`。余额券行为零变更;订阅券激活订阅且**不写 billing_events**;迁移把现存券默认 'balance'。
3. **F3 跨子系统原子激活**:在 `internal/subscription` 抽**事务内**激活入口 (传入已开的 pgx.Tx),由 payment/voucher 在各自 SERIALIZABLE 事务里调用 → 同一 DB 事务保证"扣了款必开通、绝不双开"。memory store 镜像同语义。
4. **订阅购买红线**:不写 payment_credits、不写 billing_events、不碰 internal/billing、不动净额余额。

## 2. Codex 补到 Claude 稿漏的 3 个真问题 (采纳)
- **(A) 订阅履约效果账本表** `subscription_fulfillment_effects`(类比 payment_credits,**唯一键 (tenant_id, payment_order_id)** / voucher 侧另一唯一键):存 user_id、plan_id、结果 user_subscription_id、本次延长天数 applied_validity_days、原到期日 prev_expires_at、新到期日 new_expires_at、reversal_state。
  - 为何需要: (i) 完成态重放幂等读;(ii) 续订延长需要 prev/new 到期日才能正确重放;(iii) 将来退款逆转需精确的"本次加了多久/原到期日"——只存 fulfilled_subscription_id 不够。
- **(B) 付费续订必须"延长"不能"空操作"**:HUAKAI P3a 的 `uq_user_subscriptions_active_group`(每组一条 active)若直接复用 AssignSubscription,第二次付费购买会被当幂等重放 → **用户付了钱啥也没拿到**(偷钱)。激活入口必须区分:同一订单/券重放(读 effect 账本返回原结果, 正确 no-op)vs 新订单同组活跃(必须 **UPDATE 延长 expires_at + 重装/延长 quota policy valid_until**)。sub2api 源码证实续期累加 (subscription_service.go:202)。
- **(C) 效果账本预留 reversal 列**(prev_expires_at / applied_validity_days / reversal_state):退款**逻辑**仍按 Owner 范围**延后 P5**,但这些列幂等重放本就要用,现在建好避免二次迁移。

## 3. Owner 决策 (AskUserQuestion 2026-05-29 两轮, #15 已带双源对照)
**问题**: 同组已有未过期订阅,再付费买同档位**不同套餐**(限额不同),如何处理? (时间叠加已定按 sub2api 累加,争议在"限额用哪个")
**第一轮**: Owner 否决"换成新套餐限额"(理由: 买便宜套餐会把已付更高档用户**降额**,违信任链)。
**第二轮定稿**: **自助购买只能往高、不能往低;降档只能管理员手动改。**

**最终规则**:
- **时间**: 到期时间从 `max(now, current_expires_at)` 累加新套餐 validity_days(sub2api 累加语义)。
- **自助购买(订单/兑换码)的限额闸 = caps 逐窗口支配 (only-up)**:
  - "往高"判定 = 新套餐**每个限额窗口(日/周/月)都 ≥ 当前生效值**(NULL/unlimited 视为最大 ∞)。
  - **满足(支配)** → 允许: 时间累加 + caps 换成新套餐(因处处 ≥ 旧, 等价只升不降, 零降额) + 更新 plan_id + 重装 quota_policies(关旧、按新 caps 装新, valid_until=新到期日) + 审计 subscription_renewed。
  - **不满足(任一窗口更低 = 往低)** → **拒绝**,不产生任何副作用,返回清晰错误"不支持自助降档,请联系管理员或等当前套餐到期"。
  - 注: 此判定按 **额度** 不按价格 —— Owner 否决换新的理由是"不让降额", caps 支配直接保证零降额。(若 Owner 改判为按价格档, 在此处替换比较函数即可。)
- **管理员手动分配(P3a admin AssignSubscription 路径)**: 关闭 only-up 闸 (`enforceUpgradeOnly=false`),可升可降(管理员override);走同一激活入口。
- **理由**: HUAKAI 信任链核心 (project_core_trust_chain_differentiator) — 自助购买永不降低已付额度;降档是管理员显式动作。
- **融合 delta**: sub2api 时间累加 (subscription_service.go:202) + new-api 多订阅并存用户享受额度并集 (model/subscription.go:783) → HUAKAI 压成"每组一条 + 时间累加 + only-up caps 支配闸 + 管理员override"。三维: 架构(单行模型 vs new-api 多行) + 算法(caps 支配判定 + 策略重装同事务原子) + 生态(自助/管理员双闸 + 审计区分)。

## 4. 定稿设计 (激活入口语义, 反映 Owner only-up 决策)
`internal/subscription` 暴露事务内入口 (示意,非最终签名):
- `ActivateOrRenewTx(ctx, tx, input)`,input 含 `enforceUpgradeOnly bool`(订单/兑换码自助=true;管理员手动=false):
  - 锁定 (tenant, user) 行 FOR UPDATE;
  - 查同 (tenant, user, granted_group) 的 active 订阅:
    - **无** → INSERT 新 active 订阅 + 装 caps 策略 + 升组 + 审计 subscription_created;
    - **有(同组)**:
      - 若 `enforceUpgradeOnly` 且新套餐 caps **未逐窗口支配**当前生效 caps(任一日/周/月窗口新值 < 当前,NULL=∞ 视为最大)→ **返回 ErrDowngradeNotAllowed,零副作用**(自助降档被拒);
      - 否则 → UPDATE expires_at = max(now, 现 expires_at) + new validity;**覆盖 caps 快照与 plan_id 为新套餐**;关旧 quota_policy_links 的策略、按新 caps 装新策略(valid_until=新到期日);审计 subscription_renewed(新事件类型, 加进 0073 后续迁移的 CHECK);
    - granted_group 不同(跨组)→ 沿用 P3a 升/降组机制;
  - 不 begin/commit(由 payment/voucher/admin 调用方事务持有)。
- **caps 支配判定** `capsDominate(new, cur) bool`: 对日/周/月三窗口,`new[w] >= cur[w]`(nil 表无限=最大;cur=nil 时仅 new=nil 才满足该窗口)。纯函数,judgement 与时间无关,可单测 mutation。
- payment Fulfill (subscription kind): CAS paid→in-progress → 查 effect 账本(在?返回原结果):否→调 ActivateOrRenewTx + INSERT effect 行 → 标 completed。crash-before-commit 无激活、重试重入;crash-after-commit 重放读 effect。并发回调靠行锁 + effect 唯一行 + 序列化重试收敛。
- voucher redeem (subscription grant): 现有 tenant/code/幂等键守卫 → 调 ActivateOrRenewTx + 标券已用,同事务,**不写 billing_events**。

## 5. 切片顺序 (小切片闭合; Claude reminder-first × Codex foundation-first 融合)
- **P3b-1 到期提醒邮件**(最小、纯我领地、非钱、独立于 money path): ExpiryWorker 加到期前分档(7d/3d/1d/当天)扫描 → 投递账本(唯一 (tenant, user_subscription_id, reminder_key) 防重发)→ 复用 internal/email + DLQ;租户 SMTP 配好才开。**先闭合**。
- **P3b-2 激活地基**(迁移 + 入口): order_kind/snapshot 列 + `subscription_fulfillment_effects` 账本 + voucher grant_kind/plan 列 + `ActivateOrRenewTx`(含 Owner 决策 A 续订语义)。
- **P3b-3 兑换码购订阅**: voucher redeem 调激活入口,余额券向后兼容。
- **P3b-4 订单购订阅**: payment create/fulfill 加 kind 分支,subscription 履约写 effect + 激活,零余额零 billing_events。
- 每片: 真 PG mutation 判别测试 + codex per-commit review ≤2 轮 + 闭合再开下一个。
- **延后**: 退款逻辑 (P5, 列已预留)、真钱 provider SDK (Owner-gated)、运行时强制 R-SUB-WIRE-1/2。

## 6. 测试矩阵 (mutation-discriminating 真 PG)
| 风险 | 测试 | mutation 该变红 |
|---|---|---|
| 订阅订单误入余额 | 订单购订阅完成 → 订阅 active 且 payment_credits 无新增、无 billing_events | Fulfill 漏 kind 分支→入余额→红 |
| 付费续订空操作偷钱 | 同组活跃再付费购买(往高)→ expires_at 真延长 + caps 升级 | 走 AssignSubscription 幂等→no-op→红 |
| 自助降档未拦截 (Owner only-up) | 活跃高档,自助买低档(任一窗口更低)→ ErrDowngradeNotAllowed + 零副作用(订单未完成/券未消耗) | 漏 capsDominate 闸→降额生效→红 |
| 往高续订 caps 升级 | 自助买更高档(处处≥)→ quota_policies = 新套餐 caps、旧策略 closed | 漏覆盖 caps→旧额度→红 |
| 管理员可降档 | enforceUpgradeOnly=false 时管理员分配低档 → 成功(override) | 误把闸应用到 admin→管理员改不动→红 |
| 扣款未开通 | 激活失败注入 → 整单回滚(订单不留 completed) | 拆两事务→留半→红 |
| 重复回调双开通 | 同订单重复 Confirm → effect 仅一行、订阅仅一条 | 去 effect 唯一键→双开→红 |
| 兑换券双用 | 同券重复兑换 → 订阅仅延一次 + 券计数不超 max | 去幂等→双用→红 |
| 订阅券误写 billing | 订阅券兑换 → 零 billing_events | 复用余额券路径→写 voucher_redeemed→红 |
| 提醒重发 | 多 tick → 同档提醒仅一次 | 漏 reminder_key 唯一→每 tick 重发→红 |
| 跨租户 | A 租户订单/券/提醒不触 B | 漏 tenant 谓词→串租户→红 |

## 7. 包与文件纪律 (CLAUDE.md #13)
- 落地区: `internal/payment`(加列+kind 分支+effect 账本, 非冻结)、`internal/subscription`(激活入口+续订+提醒 worker)、`internal/voucher`(grant_kind+订阅兑换)、`internal/email`(必要时小适配器)、`internal/subscriptionhttp`/`paymenthttp`(非冻结 handler)、`sql/migrations`(0073 之后)。
- 禁: 冻结包 gatewayhttp/gateway/proto 不加新文件;不碰 internal/billing / 净额余额 / LICENSE / 生产密钥。
- 迁移 = Owner-gated 高风险 + 与新机串行协调点(本批 additive 加列/新表, 不动 billing_events 约束)。

## Source files read (再列)
sub2api@91da8159:backend/internal/service/{subscription_service.go,payment_order.go,payment_fulfillment.go,payment_refund.go,redeem_service.go,subscription_expiry_service.go}; new-api@local-snapshot:model/{subscription.go,redemption.go},controller/{subscription.go,subscription_payment_epay.go,subscription_payment_stripe.go,redemption.go},service/{quota.go,user_notify.go}; CLIProxyAPI@21fad9db:{README.md,config.example.yaml,internal/api/server.go}(无订阅商业化). HUAKAI: internal/{payment,subscription,voucher,email}/*, sql/migrations/{0023,0025,0071,0073}.

Lane: specifier (synthesis)｜Agent: Claude Opus 4.8｜UTC: 2026-05-29
