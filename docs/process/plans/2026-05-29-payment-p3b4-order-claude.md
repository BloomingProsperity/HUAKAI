# P3b-4 订单购订阅 — Claude 实现计划 — 2026-05-29

> 切片 P3b-4,分支 work/quota-subsystem。地基: migration 0075 已给 payment_orders 加 `order_kind`('topup'|'subscription')
> + `subscription_plan_id`(订阅单必填, CHECK + 复合 FK)。subscription 侧已有 `ActivateOrRenewTx` + 效果账本
> (payment_order_id 部分唯一索引幂等锚) + P3b-3 的 `FulfillVoucherTx` 同形封装范式。本切片把"订单完成履约"按 order_kind 分流。
>
> Claude 单方稿(#10 高 ceremony 已在 P3b synthesis 对全族做过); 无新产品级决策; per-commit review 因 codex 用量上限(2026-05-30 23:15 重置)由 opus 独立 code-reviewer 替补(Owner 2026-05-29 指示"继续 P3b-4 同样 opus 替补 review 后落")。

## Clean-Room Guard (#11 / #12)
- Lane: specifier。三镜: sub2api(LGPL/AGPL 仅行为摘要)、new-api(行为摘要)、CLIProxyAPI(无订阅商业化=无等价)。
- 新读核实 (file:line, 2026-05-29):
  - sub2api@91da8159:backend/internal/service/payment_fulfillment.go:215-216 (履约按 OrderType 分流, 订阅类走专用履约);:316-365 (订阅履约: 读 order 上 group_id+days, 幂等查审计日志 SUBSCRIPTION_SUCCESS 标记, 调订阅分配/续期入口, 标完成)。
  - new-api@local-snapshot:model/subscription.go:523-629 (订阅订单完成入口: 锁单+provider 校验+幂等返回+同事务建订阅快照+标成功)。
- 被引标识符不在 HUAKAI 代码/本散文 verbatim 复用。

## 1. Scope
**做**:
1. payment Order 读 `order_kind` + `subscription_plan_id`(orderSelectColumns const + scanOrder + Order 结构)。
2. CreateOrder 接受 `OrderKind` + `SubscriptionPlanID`(service 校验: subscription 必带 plan_id, 缺省 topup);insertOrderTx 写两列。
3. subscription 包新增 `FulfillOrderTx`(同形 FulfillVoucherTx, 幂等键=payment_order_id, SourceKind=order)。
4. payment `completeFulfillOnce` 按 order_kind 分流:
   - `topup`(默认) → 现状: 写 payment_credits + billing_events 'payment_credited' + completed。**字节不变**。
   - `subscription` → 同事务调 `FulfillOrderTx`(激活/续期 + 写 effect)+ completed; **零 payment_credits / 零 billing_events / 零余额**。
   - 已完成幂等重放路径同样分流: topup 读 credit; subscription 读 effect(经 FulfillOrderTx 幂等回放)。
5. FulfillResult 加 `Subscription *SubscriptionGrant`(nil=topup 单);审计 payload 订阅单记订阅维度。
6. 真 PG 集成测试 + mutation。

**不做(记路标)**: 订阅单的 paymenthttp 下单/确认 API endpoint(handler 层, 另切片);webhook(P2)的订阅分流(本切片聚焦 manual/admin confirm→fulfill 链路, webhook 复用同 completeFulfillOnce 分支天然覆盖);负向/退款(P5)。

## 2. 落地文件(职责 + 冻结校验)
- `internal/subscription/order_fulfillment.go`(**新**, 非冻结):`FulfillOrderTx` + `FulfillOrderInput`。与 voucher_fulfillment.go 并列(同属"外部购买→订阅履约"职责)。复用既有 fulfillResultFromEffect / getFulfillmentEffectByOrderTx / insertFulfillmentEffectTx。
- `internal/payment/store_postgres.go`(改): orderSelectColumns +2 列; scanOrder +2 字段; insertOrderTx INSERT +2 列; completeFulfillOnce 两路径按 order_kind 分支; import subscription。
- `internal/payment/types.go`(改): Order +OrderKind/+SubscriptionPlanID; OrderKind 常量; CreateOrderInput +2; FulfillResult +Subscription; SubscriptionGrant 类型(payment-local DTO, 从 subscription.FulfillResult 映射)。
- `internal/payment/store.go`(改): createOrderRecord +OrderKind/+SubscriptionPlanID。
- `internal/payment/service.go`(改): CreateOrder 校验 + 透传; Fulfill 审计分支(订阅维度)。
- `internal/payment/store_memory.go`(改): 订阅单 CompleteFulfill 返回 sentinel(真路径 PG-only, 同 P3b-3 D3)。
- 测试: `internal/payment/order_subscription_integration_test.go`(新, integration_pg)。
- **无 migration**(0075 已含); **无冻结包改动**(payment/subscription 非冻结)。

## 3. 参考项目对照(#15)
| 维度 | sub2api | new-api | HUAKAI delta + 维度 |
|---|---|---|---|
| 订单授订阅 | 履约按 OrderType 分流调 AssignOrExtend (`payment_fulfillment.go:215,361`) | 订阅订单完成入口同事务建快照 (`model/subscription.go:523-629`) | 采分流模型; **delta(架构)**: 订单引用 plan_id 取 caps/validity 快照 vs sub2api 码/单上扁平 group+days |
| 履约幂等 | 审计日志查 SUBSCRIPTION_SUCCESS 字符串标记 (`:356`) | 锁单+completed 状态检查 | **delta(架构)**: effect 账本 payment_order_id 部分唯一索引 + completed 态读 effect 回放, 不靠日志字符串匹配 |
| 入账副作用 | 订阅单不动余额 | 余额单才 upsert | 采"按 kind 零交叉"; **delta(生态)**: 订阅单零 billing_events + effect 账本可审计/可退款重放 |
| 原子性 | 多步(分配+标完成分离) | 同事务建快照 | **delta(架构)**: 激活+effect+completed 全在 completeFulfillOnce 的 SERIALIZABLE 同事务, 崩溃可重入 |

## 4. 测试矩阵(mutation-discriminating, 真 PG)
| 风险 | 测试 | mutation → 应红 |
|---|---|---|
| 订阅单误入余额(掺水反向) | 订阅单 fulfill → 订阅 active + cap 策略; payment_credits 与 billing_events 计数零增; 用户支付余额不变 | completeFulfillOnce 漏 order_kind 分支走 credit 路径 → 写 payment_credited → 计数+1 → 红 |
| 完成态重放零双开 | 同订阅单二次 fulfill → 订阅只延一次 + effect 仅一行 + 单仍 completed | 重放路径对订阅单读 credit(不存在)报错 / 漏 effect 幂等 → 双激活 → 到期跳 → 红 |
| 自助降档闸 | 已持高档, 低档订阅单 fulfill → ErrDowngradeNotAllowed + 事务回滚(单不进 completed, 留 recharging 可人工) | FulfillOrderTx EnforceUpgradeOnly=false → 降档生效 → 红 |
| 同事务原子 | FulfillOrderTx 内失败 → 激活+effect+completed 全回滚(单仍 recharging) | 把激活挪到 completed 之后 → 单 completed 但订阅没开 → 红 |
| 跨租户 | 租户 A 订阅单不触 B | 查询漏 tenant 谓词 → 红 |
| topup 零回归 | topup 单 fulfill → 仍写 payment_credits + billing_events 'payment_credited' + 余额增 | 分支误把 topup 导向订阅 → 无 credit → 红 |

## 5. 实现决策(我定, 非产品级)
- **D1**: `FulfillOrderTx` 与 `FulfillVoucherTx` 同形但独立函数(差异: 幂等键 order vs voucher、SourceKind、effect 源指针)。共 ~20 行 activate+effect+result 范式有重复; 暂不抽共享核(2 个稳定调用方后再抽更安全), 记 S3 备 review。
- **D2**: payment store import subscription(单向无环; subscription 不 import payment, 已核 voucher 时同理)。
- **D3**: memory store 订阅单 = sentinel(`ErrSubscriptionOrderRequiresPG` 或复用 ErrInvalidInput),真路径 PG-only(同 P3b-3)。
- **D4**: 订阅单 amount_cents = 套餐价(>0, 正常单价),但**不入任何余额**(订阅分支不写 payment_credits, 而 payment 余额 = payment_credits SUM, 故订阅单天然不进余额 — 无需像 voucher 那样额外加 grant_kind 过滤, 因为 payment 余额只数 credits 不数 orders)。这是 payment 与 voucher 的结构差异(voucher 余额数 redemption.amount 需过滤; payment 余额数 credit 不数 order, 订阅单不产 credit 即自动排除)。

## 6. Success Criteria
- `go build ./...` 过(payment→subscription 无环)。
- `HUAKAI_DATABASE_URL=<socket> go test -tags=integration_pg ./internal/payment/... ./internal/subscription/... -count=1` 全绿。
- `go test ./...` exit 0。
- §4 每条 mutation 证红恢复绿。topup 路径零回归(现存 payment 测试全绿)。
- opus 独立 review 无未结 S0/S1; codex cross-model retro 待 5/30。

## 7. Blast Radius / 风险
- 半径: payment 包(Order 列 + Fulfill 分支 + CreateOrder)+ subscription 包(新 order_fulfillment.go)。**不碰** topup 入账写法(字节不变)、不碰冻结包、不碰新机 money 路径。
- 风险: (a) topup 回归(中, 现存 payment 测试守 + topup-零回归测试); (b) orderSelectColumns 加列与 scanOrder 错位(中, const 单点 + build/scan 立即暴露); (c) 订阅单误入余额(高, §4 头条判别测试守); (d) 完成态重放路径对订阅单读 credit 崩(中, 重放分支测试守)。
- 时间估: 实现 ~2-3h; 测试+mutation ~1-2h; opus review + 修 + commit ~1h。
