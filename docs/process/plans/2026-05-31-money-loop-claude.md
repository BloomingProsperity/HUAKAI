# 钱闭环闭合计划 — Claude 稿(2026-05-31)

> #9 计划留痕 + #10 双稿交叉:本稿为 Claude(PM/架构师)独立稿;codex 独立稿见 `2026-05-31-money-loop-codex.md`(各自不看对方),综合后再执行。
> 来源:8-agent 完工评估 + 4-agent 清洁室三参考融合设计 workflow(sub2api@91da8159 + new-api@20d3e73,均 2026-05-20 活跃)。

## 1. 问题(已核真码)

- `voucher.Redeem`(`internal/voucher/store_postgres.go:206-305`)只写 `voucher_redemption` + `billing_events('voucher_redeemed')`,返回 `BalanceCents = SUM(voucher_redemption.amount_cents)`(:297,450-459),**从不碰 `user_balances`**。
- `balancehold.Reserve/Capture/Release`(`internal/balancehold/balancehold.go`)+ settler(`settler.go:247/306/681-688`)**只认 `user_balances`**;`ReserveBalanceHold`(`sql/queries/balance_holds.sql:3-11`)需要已存在的行且 `(balance-held)>=cost`。
- **生产代码从无 `INSERT INTO user_balances`**(只有 `*_integration_test.go` 种数据)→ 真实用户无行 → `balancehold.Reserve` 走 opt-in pass-through(`balancehold.go:44-61`)放行所有请求。
- **无任何** recharge / payment-order / payment-callback / wallet top-up 生产代码。

净效果:**兑券的钱对扣费不可见,enforcement 实际全员关闭** = 商业上线唯一硬阻断。

## 2. 三参考融合表(#15;CLIProxyAPI 无钱模型,每行记"无对应")

| 能力 | sub2api(基底) | new-api(融合) | HUAKAI delta | 维度 |
|---|---|---|---|---|
| 可花余额账本 | `user.balance` decimal(20,8) 在 user 行、可透支(user_repo.go:714-727) | 整数积分 + USD→积分 500000 因子(constants.go:62) | 取 sub2api USD decimal(拒积分因子,无舍入漂移),但存独立 `(tenant_id,user_id) user_balances` 表 + held/version 列(多租户隔离 + Tx1/Tx2 hold) | 架构 |
| **兑券→余额桥** | redeem 先 CAS 标用、再 credit 同 tx(redeem_service.go:274-405;redeem_code_repo.go:239-255 WHERE status=unused) | FOR UPDATE 码行 + status 守卫 → quota+=value 一 tx(model/redemption.go:115-156) | **核心修复**:HUAKAI 已有 CAS 半截(FOR UPDATE voucher + unique voucher_redemption),补同 tx 内 UPSERT credit `user_balances`;幂等重放不重复入账 | 算法+架构 |
| 充值订单 | CreateOrder + createOrderInTx(pending/日限、trade-no 重试、PENDING 默认)(service/payment_order.go:23-220) | topup schema + pending insert(model/topup.go:14-25;controller/topup.go:220-262) | 新 `internal/payment` 包;4 态 lifecycle;订单入账走**同一 credit 原语**(不另写 add) | 架构+算法 |
| 支付回调验签 | 验签→2xx-ack-unknown;provider/金额防篡改;toPaid 守卫 UPDATE WHERE status∈PENDING(payment_webhook_handler.go:71-145;payment_fulfillment.go:70-208) | 验签 + 每 trade 锁 + status==pending 守卫 + FOR UPDATE 入账(controller/topup.go:309-411;topup_stripe.go:147-290) | 融合双方防篡改;**DB-CAS 幂等(0 行=重放 no-op)+ serializable tx,无进程内/Redis 锁(多实例安全)** | 算法+架构 |
| 花时扣费 | 后扣可透支(usage_billing.go:16-42) | 预授权 hold + settle delta(billing_session.go:186-315) | **HUAKAI 已有且更强**:durable `balance_holds` 状态机(Reserve/Capture/Release)+ Tx1/Tx2,**不需改**,只缺资金来源 | 算法+架构 |
| 幂等/并发 | toPaid 守卫 UPDATE;redeem CAS;usage dedup UNIQUE | FOR UPDATE + status 守卫;原子 quota±;per-user 锁 | voucher 已有 UNIQUE idempotency_key;花费已 dedup(0044/0045);新增 recharge external_trade_no 唯一 + 回调守卫 UPDATE;**全 CAS,无分布式锁依赖** | 算法 |
| 审计 | 独立 payment_audit_log 追加表 + redeem 行兼作余额账(payment_audit_log.go:31-54) | RecordTopupLog + 消费日志 + 聚合计数(model/log.go:119-145) | `billing_events`(0023)已是追加事件流(`voucher_redeemed`);加 `balance_recharged` + `payment_audit_log`(订单 lifecycle/防篡改拒绝);**每笔贷/借都可从单一 billing_events 流重建** | 生态 |
| enforcement 模式 | always-on(balance<=0 拦下一请求) | always-on(preConsume 恒 enforce) | HUAKAI opt-in(Owner 2026-05-28 选 A)→ 分阶段翻 mandatory(每租户 flag,可逆,默认 OFF) | 生态 |

## 3. 6 切片(依赖/并行/风险)

```
MONEY-1 桥接(money,server-b,无schema)──┬─ MONEY-2 余额读真值(money,同文件→串行)
  [已派 server-b]                          └─ MONEY-6 翻 mandatory(Owner-gated 默认OFF,依赖1+2)
MONEY-3 充值订单(schema,local-codex,迁移0061)─┬─ MONEY-4 支付回调入账(money+schema,依赖1+3)
  [已派 local-codex]                            └─ MONEY-5 admin手动充值(money,依赖1+3)
```

- **MONEY-1**(已派 server-b):voucher.Redeem 同 serializable tx 内 UPSERT credit `user_balances` + 自动建行;幂等重放不重复入账。无迁移。判别性:无行用户兑$100 后 `balancehold.Reserve($100)` 必须成功建 held=100;撤销 UPSERT 则变红。
- **MONEY-2**(MONEY-1 后,server-b 同文件串行):返回余额改读 `user_balances.balance`(真实可花),非 SUM(voucher_redemption)。判别:兑$100→capture$30→读余额=7000c;还原 SUM 则报 10000c 变红。
- **MONEY-3**(已派 local-codex):新 `internal/payment` 包 + 迁移 0061 `recharge_order`;建单管线(状态机、external_trade_no 唯一、pending/日限);仅建单不接 provider。
- **MONEY-4**(依赖 1+3):支付回调验签 + 防篡改 + 幂等 PAID 守卫 UPDATE + 履约入账(复用 MONEY-1 的 credit 原语);迁移 0062 payment_audit_log + 0063 billing_events CHECK 扩展。**Owner 无法 live-test → 全靠 mock 网关 webhook 判别性测试**(验签错/重放幂等/金额不符/状态机)。
- **MONEY-5**(依赖 1+3):admin 手动充值/调额(负值 clamp 0)+ 统一审计;让闭环无 provider 也可用。
- **MONEY-6**(依赖 1+2,Owner-gated 默认 OFF):enforcement opt-in→mandatory 每租户 flag。

## 4. 迁移(下一空号 0061)
- MONEY-1/2:**无迁移**(user_balances 已存在,纯 UPSERT + 查询改)。
- 0061 recharge_orders(MONEY-3);0062 payment_audit_log + 0063 billing_events `balance_recharged` CHECK 扩展(0023 的 CHECK 含 voucher_redemption_id 配对,新分支须 voucher_redemption_id IS NULL + 加 nullable recharge_order_id;DROP+ADD 同一迁移、down 还原原文)(MONEY-4);0064 balance_enforcement_mode 默认 provisioned-only=行为保持(MONEY-6)。
- **PROD apply 全程门控**;本地 dev DB 仅供测试。

## 5. 清洁室 + 风险门
- 架构 HUAKAI 自研(#12);参考只为功能不缩水的 parity,**cite 进 commit message 不进代码注释**(codex 拦未引用)。禁抄符号:`UpdateBalance/DeductBalance/RedeemService/toPaid/confirmPayment/doBalance/out_trade_no(作Go符号)/PreConsumeTokenQuota/SettleBilling`;用 HUAKAI 词汇(Reserve/Capture/Release、balancehold、billing_events、voucher_redemption);列名用 `external_trade_no`/`provider_trade_no`。
- **单位陷阱**:HUAKAI 花费侧 USD numeric(20,8),voucher/billing_events 侧 amount_cents(bigint);credit 必须 cents→USD(amount_cents/100,:283 已算);拒 new-api 500000 积分因子。
- AGPL/LGPL(sub2api/new-api)只 paraphrase **禁 vendoring**(#12;copyleft 杀 SaaS,DR-002)。
- **所有 money/schema 切片** scope_files 命中 voucher/balance/payment → server 自动判 high-risk → 合并需 owner-token,**PM 审过后自批**(Risk-Based Confirmation Rule;billing ledger/quota/schema 均列高危)。每 commit codex review(#8)无未结 S0/S1;整切片闭合走 `/cross-review`(#7)。

## 6. 派工现状
- MONEY-1 → server-b、MONEY-3 → local-codex(已派,verify_rounds=3,文件不相交真并行)。
- MONEY-2 待 MONEY-1 落;MONEY-4/5 待 MONEY-3 落且 **codex 交叉核对后**再派;MONEY-6 Owner-gated 默认 OFF。

## 7. #10 codex 交叉综合(2026-05-31,两稿独立、互不看)

codex 独立稿真读 12 个 HUAKAI 文件 + 精确 file:line,与本稿**高度收敛**(强信号)。

**AGREE(两脑一致,直接采纳)**:① gap 诊断逐字一致(voucher 不写 user_balances、无行放行、无充值/回调、USD vs cents);② **MONEY-1=最高杠杆首切**(同一 tx UPSERT credit + cents/100 + 重放不重复);③ 桥接/回调机制一致;④ 新包隔离(冻结包合规);⑤ 扣费已有 hold 不重建;⑥ 审计走 billing_events + 不记原始 webhook body/签名;⑦ **两稿引用同一批 sub2api/new-api 源码行**(redeem_service.go CAS-then-credit、payment_webhook_handler.go/payment_fulfillment.go 验签+守卫转移、topup)——独立得出同源=机制可信。

**CONFLICT(1 处,真分歧)— enforcement 翻转时机**:codex 主张 MONEY-2 紧跟桥接立即 fail-closed(把"无行放行"当须尽快堵的洞);本稿放 MONEY-6、加每租户 flag、默认 OFF、Owner-gated。**裁决(PM)**:**采本稿的分阶段+flag+默认OFF**,但**吸收 codex 的 backfill**(先给全部用户 provision 零余额行,使翻转可安全执行)。理由:直接 fail-closed 会在行未铺满时**把所有未充值用户瞬间 402(大面积停服)**;且"开始拦截所有不付费用户"是**商业决策**,必须 Owner 显式按下,不能作技术默认。→ 我**建好开关+backfill**,翻转那一下留给 Owner(默认 OFF)。

**GAP codex 抓到(本稿采纳强化)**:① **handler 与 logic 分包**(回调/admin HTTP 放独立非冻结 `internal/paymenthttp`,不混进 logic 包)→ 更新 MONEY-4/5 scope;② **provider 插件边界 + enable-gate**(禁用 provider 不能入账)→ 并入 MONEY-4 结构;③ **spend/refund 一致性验证**(起$10→reserve$3→settle$2.25→$7.75/held$0)→ 新增 MONEY-7 验证切片;④ docs/release-gate 收尾 → roadmap 记。

**GAP 本稿抓到(codex 欠规格)**:codex 把"返回余额改读真值"并进 MONEY-1 未单列;**本稿 MONEY-2(BalanceCents 读 user_balances 非 SUM)保留**(花费后 SUM 不降=会误报)。

**净结论**:MONEY-1(server-b)+ MONEY-3(local-codex)**两稿共同确认、不动**(已在跑);MONEY-2 保留;MONEY-4 加 handler 分包 + provider enable-gate;MONEY-6 加 backfill、翻转留 Owner;新增 MONEY-7 一致性验证。包名沿用 `internal/payment`(MONEY-3 在飞,不改名)+ `internal/paymenthttp`(handler)。
