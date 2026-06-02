# 支付子系统 Slice P1 实施计划 — Claude 独立稿 (2026-05-29)

> CLAUDE.md #10 平行计划法 Claude 一侧，独立成文未参考 codex 稿。分支 work/quota-subsystem。实现由 Claude 直接写代码。

## 0. 信封（Owner 已拍）
- 真钱延后：P1 只建内部机器 + 可插拔 provider 接口 + 管理员手动确认入账 + 测试 provider；真实渠道 SDK 留 Owner-gated 切片。
- 入账 seam 复刻 voucher：SERIALIZABLE 事务内 INSERT 一条 `billing_events(event_type='payment_credited')`，零 import internal/billing。余额=派生 SUM。
- 领地：不碰 internal/billing、db/billing；handler 放新包 internal/paymenthttp（gatewayhttp 冻结禁新文件）；routes.go/wiring.go 串行；migration 0071 的 billing_events 约束扩展是与新机串行协调点（Owner-gated）。

## 1. voucher 入账精确范式（已核码，P1 镜像）
`internal/voucher/store_postgres.go:184-283` Redeem：BeginTx(Serializable) → 幂等键预查重放 → INSERT 业务行 RETURNING id → INSERT billing_events RETURNING id → 回填外键 → 派生 SUM 余额 → Commit。
billing_events 写法（payment 镜像，仅换 event_type + 外键列）：
```sql
INSERT INTO billing_events (tenant_id, event_type, actual_cost, actual_cost_signed,
  stream_state, delivered_token_count, fingerprint, payment_credit_id)
VALUES ($1,'payment_credited',$2,$2,2,0,$3,$4) RETURNING id
```

## 2. migration 0071（payment_subsystem）
新表：
- `payment_orders`(tenant_id bigint, id bigserial, user_id bigint, out_trade_no text, amount_cents bigint, currency_code text, status text CHECK in(pending/paid/recharging/completed/expired/cancelled/failed), provider_kind text, provider_snapshot jsonb, request_fingerprint text, created_at, expires_at, paid_at, completed_at; UNIQUE(tenant_id, out_trade_no); PK(id) 或 (tenant_id,id) 复合；FK tenant_id→tenants)。
- `payment_credits`(tenant_id, id bigserial, order_id bigint, user_id bigint, amount_cents bigint, billing_event_id bigint, created_at; UNIQUE(tenant_id, order_id) 保证一单一入账; FK order_id→payment_orders)。
billing_events 扩展（与 0023 同模式，串行协调点）：
- `ADD COLUMN IF NOT EXISTS payment_credit_id bigint`。
- 重建 `billing_events_event_type_check` 加 `'payment_credited'`（含原 4 值）。
- 重建 `billing_events_claim_or_voucher_check` → 三分支互斥：claim 类(claim_id NOT NULL, voucher NULL, payment NULL) / voucher_redeemed(claim NULL, voucher NOT NULL, payment NULL) / payment_credited(claim NULL, voucher NULL, payment_credit_id NOT NULL)。
- FK `payment_credit_id (tenant_id,payment_credit_id)→payment_credits(tenant_id,id)`。
down：对称 DROP（约束回退到 0023 版四分支 + 删列删表）。**Owner 确认 + 与新机协调 billing_events 约束时序后执行。**

## 3. 包结构（仿 voucher）
```
internal/payment/
  types.go          # Order, OrderStatus 枚举, CreditRecord, Provider 接口, CreateOrderInput/Result 等, 错误哨兵
  store.go          # Store 接口 + 内部 record
  store_postgres.go # CreateOrder / TransitionStatusCAS / Fulfill(写 billing_events) / GetOrder / ListByUser / UserBalance
  store_memory.go   # 测试用
  service.go        # 外观: 规范化/校验/状态机编排/调 audit
  provider.go       # Provider 接口 + manualProvider(测试/管理员确认, 真钱渠道留后续)
  idempotency.go    # out_trade_no 生成/校验 + 幂等键
  audit.go          # AuditSink + 事件常量 (payment_order_created / payment_paid / payment_credited / payment_failed)
  privacy.go        # 复用 internal/privacy 脱敏
  *_test.go
internal/paymenthttp/handler.go  # MountPaymentAdminRoutes + MountPaymentUserRoutes
```

## 4. 订单状态机 + 幂等（采 sub2api CAS 模型）
状态：`pending → paid → recharging → completed`，旁路 `expired/cancelled/failed`。
幂等三重：
1. `out_trade_no` 唯一索引：重复建单 → unique violation → 返回已存在订单（重放）。
2. 履约 CAS：`UPDATE payment_orders SET status='recharging' WHERE tenant_id=$1 AND id=$2 AND status IN ('paid','failed')` → rows-affected=0 表示已被并发履约或状态不符 → 不重复入账。
3. 入账与状态终态(`completed`)同 SERIALIZABLE 事务；`payment_credits` 的 `UNIQUE(tenant_id, order_id)` 兜底防双入账。
RECHARGING 中间态支持断点续跑（崩溃后重入只会 CAS 命中一次）。

## 5. service API
- `CreateOrder(ctx, CreateOrderInput{TenantID,UserID,AmountCents,Currency,OutTradeNo?,Provider="manual"}) (Order, error)`：幂等 out_trade_no（缺省生成）；写 pending 订单 + audit。
- `AdminConfirmPaid(ctx, tenant, orderID) (Order, error)`：manual provider 模拟"支付成功"，CAS pending→paid + audit。
- `Fulfill(ctx, tenant, orderID) (FulfillResult{Order, CreditID, BillingEventID, BalanceCents}, error)`：CAS paid→recharging → INSERT payment_credits → INSERT billing_events(payment_credited) → 回填 → CAS recharging→completed → audit。失败回滚整事务。
- `UserBalance(ctx, tenant, userID)`：派生 SUM（voucher_redemption + payment_credits）。

## 6. handler（internal/paymenthttp，routes.go 串行挂载）
- admin：`POST /v1/admin/payments/orders`（建单）、`POST /v1/admin/payments/orders/{id}/confirm`（确认+履约）、`GET /v1/admin/payments/orders`（列单）。
- user：`GET /v1/users/me/payments`（自己的订单+余额）。
- 挂载点 routes.go 仿 voucher 段（admin r.Route("/v1/admin/payments") + user r.Route("/v1/users/me/payments")）。wiring.go 构造 `payment.NewService(payment.NewPostgresStore(pgPool))`。

## 7. mutation-discriminating 真 PG 测试（#14）
| 测试 | 守的缺陷 | 判别 fixture | mutation 变红 |
|---|---|---|---|
| T1 DuplicateOutTradeNoNoDoubleCredit | 重复建单/履约双账 | 同 out_trade_no 建单两次→履约→断言 payment_credits 仅 1 行、billing_events payment_credited 仅 1 条、余额只增一次 | 去 unique/CAS → 双账 → 红 |
| T2 ConcurrentFulfillOnlyOneCASWins | 并发履约超发 | 同一 paid 订单并发 Fulfill ×N → 断言只 1 次成功入账、余额增一次 | 非 CAS（先查后写）→ 多入账 → 红 |
| T3 CreditWritesBillingEventAndBalance | 漏写事件/余额不增 | 履约后断言 billing_events 恰一条 payment_credited(actual_cost=金额) 且 UserBalance SUM 精确增 | 漏写 billing_events → 余额不变 → 红 |
| T4 TenantIsolation | 串租户 | tenant A 履约不影响 tenant B 余额；同 user_id 数值跨租户隔离 | 漏 tenant 谓词 → 串租户 → 红 |
| T5 FulfillRequiresPaidState | 状态机越权 | 对 pending（未确认）订单直接 Fulfill → 拒（CAS rows=0），无入账 | CAS 条件放宽 → 误入账 → 红 |
| T6 IdempotentReplayReturnsSameOrder | 重放语义 | 同 out_trade_no 第二次建单返回同一订单 id，不新建 | 不处理 unique → 新建第二单 → 红 |
真 PG（scratch DB migrate→0071），不 mock；钱风险（双账/串租户/越权）用真注入。自证：T1 同测对比一次 vs 重复的余额必须相同。

## 8. blast radius / 协调
- 钱：双账/丢账 → SERIALIZABLE + out_trade_no unique + payment_credits unique(order) + CAS 三重；真 PG 测试覆盖。
- 串租户 → 所有 query 带 tenant_id + T4。
- 与新机硬协调点：billing_events 约束扩展（migration）→ Owner 确认 + 串行落，避免两机 DROP+ADD 互相覆盖。
- routes.go/wiring.go 串行编辑。

## 9. fusion-upgrade delta（三维）
- 架构：入账走 append-only `billing_events`（可审计事件流）+ 派生余额，而非参考项目的独立可变余额表；payment_credits 与 voucher_redemption 并列为 credit 来源，统一进信任链审计。
- 算法：sub2api 的 RECHARGING-CAS 幂等（无应用层锁）+ 三重幂等；HUAKAI 升级 = 入账与终态同 SERIALIZABLE 事务 + payment_credits unique(order) 兜底。
- 生态：admin/user 端点 + 审计事件，为后续 provider/webhook(P2)、订阅(P3)、签到返佣(P4)、退款(P5) 打底；真钱接入 Owner-gated 隔离。

## 10. Source files read（SPECIFIER lane，HUAKAI 自有 + 参考行为级）
HUAKAI: `internal/voucher/store_postgres.go:184-283`、`sql/migrations/0002_observability_billing.up.sql`(billing_events)、`0023_voucher_system.up.sql:120-160`(约束扩展)、`0039_money_path_append_only_triggers.up.sql`、`internal/quota/*`、`cmd/gateway/routes.go`(voucher 挂载段)。
参考（行为级，见缺口对照 #15）：sub2api payment_order 状态机 + RECHARGING-CAS（~/refs/sub2api .../payment_fulfillment.go:142）、new-api topup（~/refs/new-api/model/topup.go）。

Lane: specifier｜Agent: Claude Opus 4.8｜UTC: 2026-05-29
