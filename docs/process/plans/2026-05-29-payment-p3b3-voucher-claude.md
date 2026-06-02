# P3b-3 兑换码购订阅 — Claude 实现计划 — 2026-05-29

> 切片 P3b-3,分支 work/quota-subsystem。地基由 P3b-2 (commit 08c1630) 铺好:migration 0075 已给 voucher 加
> `grant_kind`/`subscription_plan_id`;subscription 侧已有 `ActivateOrRenewTx` + 效果账本 `subscription_fulfillment_effects`
> (含 `voucher_redemption_id` 部分唯一索引幂等锚) + `EffectSourceVoucher`/`SourceVoucher`。本切片只剩"voucher 兑换按
> grant_kind 分支调激活入口"这一段接线。
>
> **此计划是 Claude 单方稿。** #10 平行交叉的高难度 ceremony 已在 P3b synthesis (2026-05-29-payment-p3b-synthesis.md)
> 对整个 P3b 族做过 (Claude∥codex 平行 + Owner 两轮决策)。本切片无新的产品级 material decision(见 §5),
> 故按 ceremony-tiered 模型走"Claude 起 plan + Owner 决策";per-commit codex review (#8) 仍在实现落地时强制。

## Clean-Room Guard (#11 / #12)
- Lane: specifier(已读参考源码出行为摘要)。
- 三镜 (#16): sub2api (LGPL/AGPL, 仅行为摘要禁 vendoring)、new-api (行为摘要)、CLIProxyAPI (MIT, 本域无订阅商业化 = no-equivalent)。
- 本次新读 + 核实 (file:line, 2026-05-29):
  - sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/redeem_service.go:307-308 (订阅类兑换码必须带 group_id, 否则 BadRequest);:358-378 (兑换事务 txCtx 内按类型分流, 订阅类调订阅分配/续期入口、传 validity_days + group_id, validity=0 默认 30);:539-561 (负天数=缩短/取消订阅, 本切片不实现)。
  - new-api@local-snapshot-2026-05-29:model/redemption.go (兑换码仅余额/额度类, grep subscription 零命中 → 订阅只走付费订单, 兑换码无订阅授予)。
- 被引标识符不在 HUAKAI 代码/本散文 verbatim 复用。

## 1. Scope(做什么 / 不做什么)
**做**:
1. voucher 锁行查询补取 `grant_kind` + `subscription_plan_id` 两列(现有 `getVoucherByCodeHashForUpdate` / `getVoucherByID` + scanVoucher)。
2. subscription 包导出一站式事务入口 `FulfillVoucherTx(ctx, tx, in) (FulfillResult, error)`:幂等读 effect(按 voucher_redemption_id 命中即返回原结果)→ 否则 `ActivateOrRenewTx(EnforceUpgradeOnly=true, SourceKind=EffectSourceVoucher)` → 写 effect 账本 → 返回。封装"激活+账本"为一个事务单元,调用方(voucher / 后续 P3b-4 payment)不手拼 effect 字段。
3. voucher PG `Redeem` 在同一 SERIALIZABLE 事务内按 `grant_kind` 分流:
   - `'balance'`(默认,现存券)→ 现状路径,写 `billing_events 'voucher_redeemed'` 入余额,行为零变更。
   - `'subscription'` → 调 `subscription.FulfillVoucherTx`,**不写 billing_events、不动余额**;RedeemResult 带订阅结果摘要(plan_id / 到期日 / created|renewed)。
4. voucher Service.Redeem 审计:订阅券发 `voucher_redeemed` 审计事件时 payload 改记订阅维度(plan_id / result_kind / 到期日)而非 balance_cents;不泄原始 code(复用 sanitizeAuditPayload)。
5. 真 PG 集成测试 + mutation 自检。

**不做(本切片外,记路标)**:
- 负天数兑换码(sub2api reduceOrCancelSubscription)→ 路标,P5/退款族再议。
- 订阅券的 admin 创建 UI/API(voucher 创建时指定 grant_kind=subscription + plan_id)→ P3b-3b 或随 paymenthttp/subscriptionhttp 切片;本切片聚焦兑换链路,创建侧用测试直插或最小 store 入参。
- memory store 的订阅券完整镜像 → 见 §5 决策,本切片 memory store 对 subscription 券返回明确 sentinel(真路径用 integration_pg 覆盖)。

## 2. 落地文件(职责 + 冻结校验)
- `internal/subscription/voucher_fulfillment.go`(**新文件**,非冻结包):`FulfillVoucherTx` + `FulfillVoucherInput`/`FulfillResult`。职责=订阅履约编排(幂等读+激活+账本),与 activation.go(纯激活/续期)分文件,符合 #13 按职责分。
- `internal/subscription/store_postgres.go`(改):`insertFulfillmentEffectTx` / `getFulfillmentEffectByVoucherTx` 已存在(P3b-2),`FulfillVoucherTx` 复用,无需新增 store 方法。
- `internal/voucher/store_postgres.go`(改):`Redeem` 加 grant_kind 分支;`getVoucherByCodeHashForUpdate`/`getVoucherByID`/`scanVoucher` 补两列。import subscription(单向, 无环, 与 P3b-4 payment 调激活入口同向)。
- `internal/voucher/store_memory.go`(改):scan/branch 镜像;subscription 券走 §5 sentinel。
- `internal/voucher/types.go`(改):Voucher 加 `GrantKind string` + `SubscriptionPlanID *int64`;RedeemResult 加 `Subscription *SubscriptionGrant`(nil=余额券)。
- `internal/voucher/service.go`(改):Redeem 审计 payload 按券类型分流。
- 测试:`internal/voucher/redeem_subscription_integration_test.go`(新, integration_pg) + `internal/subscription/voucher_fulfillment_integration_test.go`(新, integration_pg)。
- **无 migration**(0075 已含 voucher 两列)。**无冻结包改动**(voucher/subscription 均非冻结)。

## 3. 参考项目对照(#15)
| 维度 | sub2api | new-api | HUAKAI delta + 维度 |
|---|---|---|---|
| 兑换码授订阅 | 码 Type=subscription, 同 tx 调 AssignOrExtend (`redeem_service.go:358-378`) | 兑换码无订阅类 (`model/redemption.go` 无 subscription) → 仅付费订单授订阅 | 采 sub2api 同事务模型;**delta(架构)**: 券引用 plan_id → caps/validity 从 plan 快照, 非码上扁平字段 |
| 订阅券限额来源 | 码带 validity_days + group_id, 无 caps 概念 (`:369`) | 无 | **delta(架构+算法)**: HUAKAI 券→plan→日/周/月三档 caps + only-up 闸, sub2api 仅分组权限无额度闸 |
| 入账副作用 | 订阅类不动 balance, 余额类才 UpdateBalance (`:344` vs `:358`) | 余额类 upsert 流水 | 采"按类型零交叉";**delta(生态)**: HUAKAI 订阅券零 billing_events + 走 effect 账本(可审计/可退款重放), sub2api 直接改 user 行无独立账本 |
| 幂等 | 兑换码一次性(used 标志)+ tx | code used 标志 | **delta(架构)**: effect 账本 voucher_redemption_id 部分唯一索引作幂等锚, 完成态可重放读;券本身 single_use/max_redemptions 仍管兑换次数 |
| 负向调整 | 负天数缩短/取消 (`:539-561`) | 无 | 本切片不做, 记路标(退款族) |

## 4. 测试矩阵(mutation-discriminating, 真 PG)
| 风险 | 测试 | mutation(注入缺陷 → 应变红) |
|---|---|---|
| 订阅券误入余额(偷钱反向/掺水) | 订阅券兑换 → user_subscriptions active + 日历 cap 策略装好;billing_events 与 payment_credits 计数零增 | Redeem 漏 grant_kind 分支走余额路径 → 写 voucher_redeemed → billing_events 计数+1 → 红 |
| 券双兑只开一次 | 同券同幂等键二次兑换 → 订阅只延一次 + effect 仅一行 + voucher redeemed_count 不超 max | 去幂等读 / 去 effect 唯一索引 → 双开/双延 → 到期日断言变红 |
| 自助降档闸沿用 | 订阅券买低档(已持高档未过期)→ 兑换整体回滚 + 券未被消耗(redeemed_count 不变)+ 无订阅副作用 | FulfillVoucherTx 传 EnforceUpgradeOnly=false → 降档生效 → cap 断言变红 |
| 同事务原子 | FulfillVoucherTx 内部模拟失败 → 券消耗 + 激活 + 账本全回滚(redeemed_count 不变) | 把订阅分支挪到 commit 后 → 券消耗但订阅没开 → 红 |
| 跨租户 | 租户 A 订阅券不触租户 B | 激活/账本查询漏 tenant 谓词 → B 受影响 → 红 |
| 续期累加 | 订阅券对已持同组活跃续期 → 到期 max(now,旧到期)+plan.validity | base 误用 now → 累加断言变红(P3b-2 已覆盖, 此处复验券路径) |

## 5. 实现决策(我定, 非产品级, 记录备 #8 review 审视)
- **D1 effect 账本封装边界 = 一站式 wrapper(FulfillVoucherTx)**,不让调用方手拼 FulfillmentEffect。理由:effect 账本是 subscription 内部不变量(幂等锚+退款重放数据),调用方手拼易错漏字段;wrapper 把"幂等读→激活→写账本"封为一个原子单元,P3b-4 payment 复用同形 `FulfillOrderTx`。不改任何产品行为(零碰钱表/only-up/原子性/幂等都不变),仅 API 形状 → ceremony-tiered 下属 coding-execution choice。备选(调用方手拼)更灵活但易错+券订单两处重复,弃。
- **D2 voucher PG store 直接 import subscription**(非接口注入)。理由:激活必须在 store 开的 tx 内(原子红线),tx 不上浮到 service;与 P3b-4 payment 同向;无 import 环(subscription 不 import voucher,已核)。落地先 `go build` 验环;若意外成环,退到 store 上注入 `fulfillVoucherFn` 函数字段(wiring 期绑定)。
- **D3 memory store 订阅券 = 明确 sentinel(ErrSubscriptionVoucherRequiresPG 或复用 ErrInvalidInput + 文档)**,不在内存里镜像订阅表+quota 策略。理由:订阅激活依赖真 subscription/quota_policies 表,内存镜像成本高且测不到真风险(per risk-based testing:真风险活在真 PG);真路径用 integration_pg 覆盖。synthesis line 19"memory 镜像同语义"对订阅券降级为 sentinel,**此偏离需 Owner/codex 知会**(列入 §6 surface)。

## 6. Surface 给 Owner 的点
- 进度:P3b-2 已 land(08c1630)+push;Round 1 codex review 仅 1 条 P2(过期旧订阅参与 only-up 闸)已实修+判别测试;Round 2 因 codex 用量上限中断(2026-05-30 23:15 重置)。
- 阻塞:codex 用量上限同时卡 P3b-3 的 per-commit review(#8 强制 commit 前置);故 P3b-3 可实现但**重置前无法 commit**。
- 决策点(需 Owner 知会, 非高风险):D3 memory store 订阅券降级为 sentinel(偏离 synthesis 的"内存镜像同语义");D1/D2 为内部 API 形状, 默认按本计划, codex #8 review 时复核。
- 路标:订阅券创建侧 admin API(grant_kind=subscription + plan_id)+ 负天数兑换(退款族)本切片外。

## 7. Success Criteria
- `go build ./...` 过(含 voucher→subscription 无环验证)。
- `HUAKAI_DATABASE_URL=<socket> go test -tags=integration_pg ./internal/voucher/... ./internal/subscription/... -count=1` 全绿。
- `go test ./...` exit 0。
- §4 每条 mutation 证红后恢复绿。
- 余额券路径回归零变更(现存 voucher 测试全绿)。
- codex #8 Round 1 review 无未结 S0/S1。

## 7b. 实现期发现的 pre-existing voucher-PG 缺陷 (本切片 voucher 首次有 PG 集成测试才暴露)
voucher 子系统 (P1) 此前**只有 memory store 单测, 零 PG 集成测试**, 故以下 scanVoucher/CreateVoucher 的真 PG 行为从未被执行过:
- **F-PRE-1 (已在本切片修)**: `scanVoucher` 把 `revoked_reason` 扫进 `*string`, 真 NULL (未撤销券) 报 `cannot scan NULL into *string`。任何从 PG 读未撤销券都会炸 (含 getVoucherByCodeHashForUpdate → 阻塞我的订阅券路径)。修: 改扫 `sql.NullString`。这是 voucher 读路径的真 bug, 顺修。
- **F-PRE-2 (本切片外, 待独立切片 + Owner 知会)**: `CreateVoucher` 单券 INSERT 在真 PG 报 `inconsistent types deduced for parameter $8 (SQLSTATE 42P08)` (valid_until 同时用于列值与 `CASE WHEN $8 <= $13`, 叠加 nil 指针参数 → pgx 类型推断失败)。**我的 diff 未触 VALUES/CASE, 确属 pre-existing**。影响面待定: 可能单券创建路径在生产即不可用 (批量走 insertVoucherTx 另一段)。本切片用 direct-insert 隔离, 不修 (scope)。建议: 独立"voucher PG 加固"切片 = 显式 `::timestamptz` 转型 + 补 voucher 创建/撤销/列表的 PG 集成测试。

## 8. Blast Radius / 风险
- 半径:voucher 包(兑换链路)+ subscription 包(新 wrapper 文件)。**不碰** billing_events 写法(余额券分支字节不变)、不碰新机 money 路径、不碰冻结包。
- 风险:(a) voucher→subscription import 成环(低,已核;有退路 D2 备选);(b) 余额券回归(中,靠现存 voucher 测试全绿守);(c) 审计 payload 改动泄 code(低,复用 sanitize + 测试断言无 raw code);(d) 订阅券误入余额(高,§4 头条判别测试守)。
- 时间估:实现 ~2-3h;测试 + mutation ~1-2h;codex 重置后 review+修+commit ~1h。
