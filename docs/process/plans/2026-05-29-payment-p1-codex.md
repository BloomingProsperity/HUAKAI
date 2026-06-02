# 2026-05-29 HUAKAI 支付子系统切片 P1 Codex 独立实施计划

> 本文件是 Codex 独立计划稿。未读取 `docs/process/plans/2026-05-29-payment-*-claude.md`。本轮只写计划，不写实现代码，不 commit，不 push。

## 0. 计划信封

| 项 | 内容 |
| --- | --- |
| Owner directive | “独立起草 HUAKAI 支付子系统切片 P1 的实施计划……只写计划文档……不写实现代码” |
| Scope | P1 只做内部支付机器：订单表、订单状态机、可插拔 provider 接口、管理员手动确认、测试 provider、履约入账、billing_events 入账 seam、幂等、审计、真 PG 判别测试。 |
| Out of scope | Stripe / 支付宝 / 微信 / epay 等真实支付 SDK 与真实商户密钥；订阅授予余额；改动 `internal/billing`、`internal/db/billing`、冻结包新增文件；生产部署。 |
| Success criteria | P1 能在无外部依赖下完成“建单 -> 管理员确认已支付 -> 履约入账 -> 用户余额按 SUM 派生可查”；重复确认、并发履约、断点续跑、跨租户隔离均由真 PG 测试守住。 |
| Time estimate | 计划执行期约 1.5-2.5 个工程日：migration + store/service 0.8d，HTTP/wiring 0.4d，PG 判别测试 0.6d，审计/隐私/文档/修补 0.4d。 |
| Blast radius | 钱路径：双账、丢账、越权确认、跨租户串单、错误余额、审计缺口、billing_events 约束误伤已有 claim/voucher 路径。 |
| Failure modes | 见 §9。核心缓解是唯一约束、CAS rows-affected、SERIALIZABLE 事务、同事务入账、复用 voucher 派生余额模式、真实 PG 并发测试。 |
| Decision points | 见 §12。真正需要 Owner 拍的是 0071 共享约束串行协调、admin 路由别名、是否引入独立 payment audit 表。 |
| Pre-execution checklist | 1. 确认 `backend/sql/migrations/0070_*` 已是本分支最新 migration。2. 与另一台机器串行锁定 0071 对 `billing_events` CHECK/互斥约束的改动。3. 确认不得在冻结包新增文件。4. 准备 `HUAKAI_DATABASE_URL` 跑真 PG。5. Stage 后按项目规则跑 Codex review。 |

## 1. 设计原则

P1 的支付子系统走 `voucher` 同款入账 seam：业务包自己在 PostgreSQL 事务里写一条 `billing_events(event_type='payment_credited')`，余额不落独立 `user_balance` 表，而是由 `billing_events.actual_cost_signed` 派生 SUM。这样不 import `internal/billing` Go 代码，也不碰 `internal/db/billing`，但仍与现有钱路径落在同一审计表。

P1 的真实支付能力不删除，只做安全等价闭环：真实 SDK 变成后续 Owner-gated provider 插件；P1 先提供 manual provider 和 test provider，把订单、履约、入账、幂等和审计机器做实。订阅留 P3，未来订阅授予余额时复用 `internal/quota` 配额策略。

状态机采用：

```text
PENDING -> PAID -> RECHARGING -> COMPLETED
   |         |          |
   v         v          v
EXPIRED   FAILED      FAILED
   |
   v
CANCELLED
```

`RECHARGING` 是显式断点：确认已支付之后，如果履约进程在入账前后崩溃，重试可以从 `RECHARGING` 继续，不能把钱卡在“已支付但未入账”的不可恢复状态。

## 2. 包结构与文件边界

不得在以下冻结包新增文件：`backend/internal/gatewayhttp`、`backend/internal/gateway`、`backend/internal/proto`。P1 新功能落新包，只有共享 wiring 文件串行改动。

| 路径 | 职责 | 备注 |
| --- | --- | --- |
| `backend/internal/payment/types.go` | 订单、状态、金额、错误、输入输出结构 | P1 领域类型集中定义；不引入 provider SDK 类型。 |
| `backend/internal/payment/store.go` | Store interface + 内部 record 结构 | 模仿 voucher 的 store/service 分层，但字段按 HUAKAI 自己命名。 |
| `backend/internal/payment/store_postgres.go` | PostgreSQL 权威实现 | 原生 pgx；SERIALIZABLE 事务；行锁；CAS；写 `billing_events`。 |
| `backend/internal/payment/store_memory.go` | 内存实现 | 只给 service/handler 单测和本地开发；不得作为钱路径验收依据。 |
| `backend/internal/payment/service.go` | `CreateOrder` / `AdminConfirmPaid` / `Fulfill` / 查询余额 | 封装状态机、provider seam、审计事件、错误归一。 |
| `backend/internal/payment/provider.go` | Provider interface + manual/test provider | P1 零外部依赖；真实 provider 后续插件化加入。 |
| `backend/internal/payment/idempotency.go` | 外部订单号生成、规范化、冲突分类 | `out_trade_no` 是 Owner 已定术语；生成值必须 tenant-aware 且难碰撞。 |
| `backend/internal/payment/audit.go` | 支付审计事件、sink、原因枚举 | 避免把 provider 原始 payload 或密钥写入审计。 |
| `backend/internal/payment/privacy.go` | provider/admin payload 红线与脱敏 | 明确不落真实商户密钥、完整 webhook body、个人敏感支付材料。 |
| `backend/internal/payment/service_test.go` | 快速单测 | 守状态机分支、错误映射、audit 调用。 |
| `backend/internal/payment/store_postgres_integration_test.go` | 真 PG 钱路径测试 | build tag `integration_pg`；必须判别 mutation。 |
| `backend/internal/payment/test_helpers_test.go` | PG fixture/cleanup | 复用 quota/voucher 风格，按 tenant 清理。 |
| `backend/internal/paymenthttp/handler.go` | HTTP handler | 新包，不给 `gatewayhttp` 增文件。 |
| `backend/internal/paymenthttp/handler_test.go` | handler 单测 | admin/session auth、JSON error、tenant 隔离。 |
| `backend/cmd/gateway/wiring.go` | 注入 payment service | 共享串行改动点；只加字段和初始化。 |
| `backend/cmd/gateway/routes.go` | 挂载 payment 路由 | 共享串行改动点；避免与另一机器同时改。 |
| `backend/sql/migrations/0071_payment_p1.up.sql` | P1 schema up | 与另一台机器串行协调 `billing_events` 约束。 |
| `backend/sql/migrations/0071_payment_p1.down.sql` | P1 schema down | 测试/空数据可回滚；有钱数据时不静默破坏。 |

## 3. Migration 0071 设计

### 3.1 `payment_orders`

字段语义：

| 字段 | 语义 |
| --- | --- |
| `id` | 内部订单主键。 |
| `tenant_id` | 所属租户；所有查询、FK、唯一约束都带 tenant。 |
| `user_id` | 被充值用户；必须属于同 tenant。 |
| `out_trade_no` | P1 幂等外部订单号；Owner 已指定该概念。建议 `(tenant_id, out_trade_no)` 唯一，生成值携带随机段，provider-facing 值实际全局难碰撞。 |
| `provider_key` | `manual` 或 `test`；真实 provider 后续 Owner-gated。 |
| `provider_order_ref` | provider 返回的安全引用；P1 manual 可为空，test provider 可填 deterministic ref。 |
| `credit_amount` | 最终授予余额，`numeric(20,8)`，必须 `> 0`。 |
| `pay_amount` | 应付金额，`numeric(20,8)`，P1 可等于 `credit_amount`；未来可支持折扣/汇率。 |
| `currency_code` | 三位币种代码；P1 默认 `USD` 或 Owner 指定配置值。 |
| `status` | `pending` / `paid` / `recharging` / `completed` / `expired` / `cancelled` / `failed`。 |
| `created_by_admin_id` | admin 建单操作者；用户自助建单若后续开放再扩展。 |
| `confirmed_by_admin_id` | 手动确认支付的 admin。 |
| `confirm_reason` | 确认原因；不能存敏感支付凭证。 |
| `audit_request_id` | 贯穿 order / credit / billing event 的请求审计 ID。 |
| `expires_at` | pending 过期时间；P1 可由 service 设置默认 TTL。 |
| `paid_at` / `recharging_at` / `completed_at` / `failed_at` | 状态时间戳，用于断点恢复和排障。 |
| `failure_code` / `failure_message` | 红线脱敏后的失败分类和短消息。 |
| `created_at` / `updated_at` | 审计时间。 |

约束与索引：

- `CHECK credit_amount > 0`，`CHECK pay_amount >= 0`。
- `CHECK status IN (...)`。
- `UNIQUE (tenant_id, out_trade_no)`，守住重复建单和跨租户 fixture。
- `(tenant_id, user_id)` FK 到 users 维度。
- 常用索引：`(tenant_id, status, updated_at)`、`(tenant_id, user_id, created_at DESC)`、`(tenant_id, provider_key, provider_order_ref)`。

### 3.2 `payment_credits`

`payment_credits` 是“已入账事实”，一张订单最多一条 credit。它不是余额表，余额仍从 `billing_events` SUM 派生。

字段语义：

| 字段 | 语义 |
| --- | --- |
| `id` | credit 主键。 |
| `tenant_id` | 租户隔离键。 |
| `payment_order_id` | 关联订单。 |
| `user_id` | 被入账用户，冗余保存便于查询和 FK 检查。 |
| `amount` | 入账金额，`numeric(20,8)`，必须 `> 0`。 |
| `currency_code` | 与订单一致。 |
| `reason_class` | `manual_confirmed` 或 `test_provider_paid`。 |
| `billing_event_id` | 写入 `billing_events` 后回填，用于人工审计跳转。 |
| `created_at` | 入账时间。 |

约束与索引：

- `UNIQUE (tenant_id, payment_order_id)`，订单重复履约不会双账。
- `UNIQUE (tenant_id, billing_event_id)` where not null，billing event 不被多个 credit 复用。
- 复合 FK：`(tenant_id, payment_order_id)` -> `payment_orders`。
- 可选复合唯一：`UNIQUE (tenant_id, id)`，方便 `billing_events` 用 tenant-scoped FK。

### 3.3 支付审计表

推荐新增 `payment_audit_events`，而不是扩展已有 admin audit 的 CHECK：支付是钱路径，应该有领域审计；同时避免把 admin 审计表变成第二个共享串行冲突点。

字段：`tenant_id`、`payment_order_id`、`event_type`、`actor_kind`、`actor_id`、`reason_class`、`request_id`、`redacted_payload`、`occurred_at`。

P1 事件：`order_created`、`paid_confirmed`、`fulfillment_started`、`credited`、`fulfillment_failed`、`idempotent_replay`。表用 append-only 触发器或最小化 UPDATE 权限策略，风格对齐现有 money-path append-only 约束。

### 3.4 `billing_events` 扩展

Up migration：

- 添加 nullable `payment_credit_id`。
- 添加 `(tenant_id, payment_credit_id)` 到 `payment_credits` 的 FK。
- 增加 `payment_credited` 到 `event_type` CHECK。
- 将现有 claim/voucher 互斥约束扩展为三类互斥：
  - usage/claim 类事件：`claim_id` 非空，`voucher_redemption_id` 和 `payment_credit_id` 为空。
  - voucher 入账：`voucher_redemption_id` 非空，`claim_id` 和 `payment_credit_id` 为空。
  - payment 入账：`payment_credit_id` 非空，`claim_id` 和 `voucher_redemption_id` 为空。

Down migration：

- 先检测是否存在 P1 payment 数据；如果有 `payment_credited` 或 `payment_credits`，down 必须 fail fast，避免静默删除钱数据。这属于生产数据回滚 Owner-gated。
- 空数据/测试环境下，按相反顺序 drop FK/index/列/表，并恢复 P1 前的 `billing_events` CHECK/互斥约束。
- 如果 Owner 要求生产可带数据回滚，则必须另起高风险迁移计划，定义数据归档和审计保留，不在 P1 自动处理。

## 4. 状态机与幂等设计

### 4.1 建单

`CreateOrder` 只创建 `pending` 订单，不入账。相同 `(tenant_id, out_trade_no)` 的请求：

- 如果关键业务字段一致，返回已有订单并记录 `idempotent_replay`。
- 如果金额、用户、provider、币种不一致，返回 conflict，不修改旧订单。

`out_trade_no` 默认由 service 生成；仅 admin/test 可以显式传入，用于测试和外部系统对账。显式传入必须经过长度、字符集、tenant 维度唯一校验。

### 4.2 手动确认支付

`AdminConfirmPaid` 做两件事：

1. 以 CAS 方式把 `pending -> paid`，只允许 rows-affected = 1 进入下一步。
2. 调用 `Fulfill` 执行履约。

幂等规则：

- 已是 `paid` / `recharging` / `completed`：不重复确认，转为 `Fulfill` 或返回已完成结果。
- `expired` / `cancelled`：默认拒绝；是否允许 admin rescue 是后续 Owner 决策，不进 P1。
- `failed`：允许 `Fulfill` 断点重试，但必须写审计原因，不能重新建一条 credit。

### 4.3 履约与 `RECHARGING` 断点

`Fulfill` 分两段：

第一段短事务：

- 锁定订单。
- 若 `completed`，返回已完成。
- 若 `recharging`，跳过 CAS，进入第二段恢复。
- 若 `paid` 或可重试 `failed`，CAS 到 `recharging`，rows-affected 必须为 1。
- 其他状态返回业务错误。

第二段 SERIALIZABLE 事务：

- 锁定订单并确认 tenant/user/status。
- 插入 `payment_credits`；唯一约束保证同一订单最多一条。
- 插入 `billing_events(event_type='payment_credited')`。
- 回填 `payment_credits.billing_event_id`。
- 更新订单到 `completed`。
- 写 `payment_audit_events(credited)`。

如果崩溃发生在第一段之后、第二段之前，订单停在 `recharging`，下一次 `Fulfill` 继续。若崩溃发生在第二段中，SERIALIZABLE 事务整体提交或回滚，不会出现 credit/event/completed 三者部分成功。

### 4.4 幂等三重锁

1. `UNIQUE (tenant_id, out_trade_no)`：重复外部订单号不能产生第二张订单。
2. CAS rows-affected：状态转换只能由一个执行者获得。
3. 同事务入账 + `UNIQUE (tenant_id, payment_order_id)`：即使重复进入 `Fulfill`，也只能有一条 credit 和一条 `payment_credited` billing event。

## 5. Service API 计划

P1 service 面向 HTTP、测试 provider 和后续 provider 插件，不暴露 SQL 细节。

```go
func (s *Service) CreateOrder(ctx context.Context, in CreateOrderInput) (CreateOrderResult, error)
func (s *Service) AdminConfirmPaid(ctx context.Context, in AdminConfirmPaidInput) (FulfillResult, error)
func (s *Service) Fulfill(ctx context.Context, in FulfillInput) (FulfillResult, error)
func (s *Service) GetOrder(ctx context.Context, in GetOrderInput) (Order, error)
func (s *Service) GetBalance(ctx context.Context, in BalanceInput) (Balance, error)
```

关键输入字段：

- `CreateOrderInput`: `TenantID`、`UserID`、`ActorAdminID`、`ProviderKey`、`OutTradeNo`、`CreditAmount`、`PayAmount`、`CurrencyCode`、`AuditRequestID`、`IdempotencyKey`、`ExpiresAt`、`Reason`。
- `AdminConfirmPaidInput`: `TenantID`、`OrderID` 或 `OutTradeNo`、`ActorAdminID`、`AuditRequestID`、`ConfirmReason`。
- `FulfillInput`: `TenantID`、`OrderID` 或 `OutTradeNo`、`ActorKind`、`ActorID`、`AuditRequestID`、`RetryReason`。
- `BalanceInput`: `TenantID`、`UserID`。

`billing_events` 写入字段必须精确：

| 字段 | 值 |
| --- | --- |
| `tenant_id` | 订单 tenant。 |
| `event_type` | `payment_credited`。 |
| `actual_cost` | credit amount，`numeric(20,8)`。 |
| `actual_cost_signed` | credit amount，正数。 |
| `stream_state` | 已完成态；沿用现有 billing event 非流式完成语义。 |
| `delivered_token_count` | `0`。 |
| `fingerprint` | P1 自生成稳定 fingerprint，包含 tenant/order/credit/request 语义，不能包含密钥或原始 provider payload。 |
| `audit_request_id` | service 输入或请求上下文生成值。 |
| `payment_credit_id` | 刚插入的 credit id。 |
| `claim_id` | `NULL`。 |
| `voucher_redemption_id` | `NULL`。 |

余额查询：`SUM(actual_cost_signed)`，过滤 `tenant_id`、`event_type IN ('voucher_redeemed','payment_credited')` 或复用现有“余额类正向事件”查询封装；P1 不建 `user_balance` 表。

## 6. Provider seam

Provider interface 只表达行为，不耦合真实 SDK：

- `CreatePaymentIntent(ctx, order)`：manual provider 返回无需跳转的内部确认对象；test provider 返回 deterministic test ref。
- `VerifyPaid(ctx, proof)`：P1 manual 由 admin auth 证明，test provider 由测试 token/fixture 证明。
- `Name()` / `Capabilities()`：用于 audit 和后续真实 provider 注册。

P1 provider 实例：

- `manual`: 生产可用但只能 admin 手动确认；不接触真实商户密钥。
- `test`: 仅测试/本地配置启用；任何生产配置默认关闭。

真实 Stripe/支付宝/微信/epay provider 后续切片加入，必须单独 Owner gate，原因是引入真实密钥、webhook 验签、退款/撤销语义和 SDK license/供应链风险。

## 7. HTTP handler 与路由

新包：`backend/internal/paymenthttp`。handler 不放进 `gatewayhttp`，避免冻结包新增文件。

Admin endpoints：

| Method | Path | 行为 |
| --- | --- | --- |
| `POST` | `/admin/v1/payments/orders` | admin 建单；返回 order、status、金额、provider ref、audit id。 |
| `POST` | `/admin/v1/payments/orders/{id}/confirm-paid` | admin 手动确认并触发履约；幂等返回当前 fulfillment 结果。 |
| `POST` | `/admin/v1/payments/orders/{id}/fulfill` | admin/operator 断点重试；P1 可只对 `recharging` / `failed` 开放。 |
| `GET` | `/admin/v1/payments/orders/{id}` | admin 查单和审计摘要。 |

User endpoints：

| Method | Path | 行为 |
| --- | --- | --- |
| `GET` | `/v1/users/me/payments/orders/{id}` | 当前用户查自己的订单；必须 tenant/user 双过滤。 |
| `GET` | `/v1/users/me/payments/orders` | 当前用户订单列表；按创建时间倒序分页。 |
| `GET` | `/v1/users/me/payments/balance` | 当前用户余额；由 billing_events SUM 派生。 |

Wiring：

- `backend/cmd/gateway/wiring.go`: 新增 payment store/service/provider registry 初始化；如果无 PG pool，则仅允许测试/内存模式按现有项目约定处理。
- `backend/cmd/gateway/routes.go`: 挂载 `paymenthttp.MountAdminRoutes` 和 `paymenthttp.MountUserRoutes`。这两个文件是共享串行点，实施时需与另一台机器避开同一 commit 冲突。
- Admin 鉴权复用现有 `adminAuth`；user 鉴权复用 session/context tenant 解析，不新增 auth core。

## 8. 真 PG 判别测试计划

所有钱路径验收测试使用真实 PostgreSQL，build tag `integration_pg`，环境变量 `HUAKAI_DATABASE_URL`。每个测试必须说明守的缺陷，并给出能让 mutation 变红的 fixture。

### 8.1 重复外部订单号不双账

测试名：`TestPaymentPostgres_DuplicateOutTradeNoDoesNotDoubleCredit`

Fixture：

- tenant: `pay_p1_tenant_dupe_a`
- user: `pay_p1_user_dupe_a`
- `out_trade_no`: `pay-p1-dupe-001`
- amount: `12.34000000`
- request: `req-pay-dupe-001`

步骤：

1. 用相同 `(tenant, out_trade_no)` 建单两次，第二次字段完全一致。
2. 对同一订单调用 `AdminConfirmPaid` 两次。
3. 断言只有一张订单、一条 `payment_credits`、一条 `billing_events(payment_credited)`。
4. 断言余额 SUM 恰好 `12.34000000`。

守的缺陷：重复建单或重复确认导致双账。

Mutation 会红：移除唯一约束、把幂等 replay 改成新建订单、或履约时不查已有 credit，都会让 event count 或 balance 断言失败。

### 8.2 并发履约只有一个 CAS 成功

测试名：`TestPaymentPostgres_ConcurrentFulfillOnlyOneCASSucceeds`

Fixture：

- tenant: `pay_p1_tenant_cas_a`
- user: `pay_p1_user_cas_a`
- `out_trade_no`: `pay-p1-cas-001`
- amount: `7.89000000`
- goroutines: `32`，注释和实际 N 必须一致。

步骤：

1. 建一个已进入 `paid` 的订单。
2. 32 个 goroutine 用 barrier 同时调用 `Fulfill`。
3. 断言只有一个调用报告获得履约写入，其余返回 idempotent completed/replay。
4. 断言订单最终 `completed`，credit/event count 均为 1，余额为 `7.89000000`。

守的缺陷：并发 worker 同时从 `paid` 入账。

Mutation 会红：删除 CAS 条件、忽略 rows-affected、或把 credit 唯一约束拿掉，会出现多条 credit/event 或非幂等错误。

### 8.3 入账 event 字段与派生余额精确

测试名：`TestPaymentPostgres_CreditBillingEventAndDerivedBalanceMatch`

Fixture：

- tenant: `pay_p1_tenant_credit_a`
- user: `pay_p1_user_credit_a`
- `out_trade_no`: `pay-p1-credit-001`
- amount: `25.50000000`
- request: `req-pay-credit-001`

步骤：

1. 建单、确认、履约。
2. 读取 `payment_credits` 和对应 `billing_events`。
3. 断言 `event_type='payment_credited'`、`actual_cost=25.50000000`、`actual_cost_signed=25.50000000`、`payment_credit_id` 匹配。
4. 断言 `claim_id IS NULL` 且 `voucher_redemption_id IS NULL`。
5. 断言余额 SUM 为 `25.50000000`。

守的缺陷：credit 写了但 billing event 缺失、金额方向错误、互斥字段错误、余额查询漏 payment 事件。

Mutation 会红：跳过 `billing_events` insert、把 `actual_cost_signed` 写负数/零、或余额查询只算 voucher，都会失败。

### 8.4 跨租户隔离

测试名：`TestPaymentPostgres_TenantIsolationForSameOutTradeNo`

Fixture：

- tenant A: `pay_p1_tenant_iso_a`
- user A: `pay_p1_user_iso_a`
- amount A: `3.21000000`
- tenant B: `pay_p1_tenant_iso_b`
- user B: `pay_p1_user_iso_b`
- amount B: `4.56000000`
- same `out_trade_no`: `pay-p1-shared-tenant-ref`

步骤：

1. A/B 两个租户分别用同一个 `out_trade_no` 建单。
2. 只确认并履约 tenant A 的订单。
3. 断言 A 余额 `3.21000000`，B 余额 `0.00000000`。
4. 断言 B 用户查 A 订单返回 404/403，不返回订单详情。
5. 断言 B 无 `payment_credited` event。

守的缺陷：按 `out_trade_no` 或 order id 查询时漏 tenant predicate，导致串租户或错账。

Mutation 会红：任何查询/更新删掉 tenant 条件，都会让 B 读到 A 订单、B 余额变化，或确认错订单。

### 8.5 断点续跑

测试名：`TestPaymentPostgres_RechargingRetryCompletesOnce`

Fixture：

- tenant: `pay_p1_tenant_retry_a`
- user: `pay_p1_user_retry_a`
- `out_trade_no`: `pay-p1-retry-001`
- amount: `9.99000000`

步骤：

1. 建单并确认到 `paid`。
2. 用 test hook 或 store helper 只执行第一段，把订单推进 `recharging`，不写 credit。
3. 再调用公开 `Fulfill`。
4. 断言订单 `completed`，credit/event 各一条，余额 `9.99000000`。

守的缺陷：进程崩溃后订单永久卡在中间态，或重试双账。

Mutation 会红：如果 `Fulfill` 不接受 `recharging` 继续，测试会卡失败；如果继续逻辑重复入账，count/balance 会失败。

## 9. Blast Radius 与缓解

| 风险 | 后果 | P1 缓解 |
| --- | --- | --- |
| 双账 | 用户余额被多加，财务不可追 | 唯一外部订单号 + CAS + credit 唯一约束 + 同事务 billing event。 |
| 丢账 | 用户已支付但余额未加 | `paid -> recharging -> completed` 断点续跑；admin retry endpoint；审计事件。 |
| 越权确认 | 非 admin 或错误 tenant 给订单入账 | handler 复用 admin auth；store 所有 update/select 带 tenant；PG 测试覆盖跨租户。 |
| 串租户 | A 的订单/余额影响 B | 复合唯一/FK、tenant-scoped 查询、跨租户同 `out_trade_no` 测试。 |
| billing_events 约束破坏旧路径 | claim/voucher 写入失败 | 0071 只扩展互斥，不改旧事件语义；迁移前后跑 voucher/billing 相关测试。 |
| provider 隐私泄露 | 密钥或支付凭证入库 | P1 不接真实 SDK；privacy allowlist；只存 redacted payload 和 provider ref。 |
| audit 不足 | Owner 无法追责手动入账 | `payment_audit_events` + `audit_request_id` 串起 order/credit/billing_event。 |
| 迁移回滚误删钱数据 | 历史账丢失 | down 对非空 payment 数据 fail fast，生产 rollback 另走 Owner-gated 数据计划。 |

## 10. Fusion / Upgrade / Delta 参考对照

以下只使用参考项目的行为级证据，不复制源码、函数名、结构体字段或注释。引用用于证明 P1 为什么需要状态机、CAS、唯一外部订单号、行锁和手动确认闭环。

| 维度 | Fusion：P1 吸收 | Upgrade：HUAKAI 更强 | Delta / 非 P1 | 参考证据 |
| --- | --- | --- | --- | --- |
| 订单生命周期 | 采用 pending/paid/recharging/completed 主干和 expired/cancelled/failed 旁路。 | 明确 `RECHARGING` 为可恢复断点，并把 credit、billing event、completed 放同一 SERIALIZABLE 事务。 | 自动查询真实 provider 支付结果留后续真实 provider 切片。 | Sub2API 在创建时落 pending 状态和外部订单引用，后续用 paid/recharging/completed 推进：`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_order.go:147`、`:164`、`:188`；履约前切入中间态并最终完成：`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_fulfillment.go:221`、`:235`、`:302`、`:304`。 |
| 幂等外部订单号 | P1 建 `(tenant_id, out_trade_no)` 唯一，重复输入按 replay/conflict 分类。 | tenant-scoped 唯一 + generated value 难碰撞，测试用同号跨租户 fixture 验证不会串租户。 | 真实 provider 的全局订单号格式由后续 provider 插件适配。 | Sub2API 对外部订单引用做唯一约束和候选冲突检查：`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_order.go:222`、`:235`，迁移中也强化唯一索引：`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/migrations/120_enforce_payment_orders_out_trade_no_unique_notx.sql:6`。 |
| 状态 CAS | P1 所有状态推进检查 rows-affected，失败即 replay 或 conflict。 | 再加 credit 唯一约束和同事务 `billing_events`，把“状态正确但账重复”也挡住。 | 分布式队列/worker 池不是 P1 必需。 | Sub2API 支付通知和履约都用条件更新的 affected-row 结果决定是否继续：`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_fulfillment.go:142`、`:146`、`:156`。 |
| 手动确认 | P1 admin 手动确认可直接驱动 paid + fulfill。 | 手动确认必须写 payment audit、绑定 admin actor、原因和 request id。 | 手动救援 expired/cancelled 订单不进 P1，避免扩大钱路径。 | New-API 的 admin 完成路径会锁定订单、检查状态并在同一事务更新订单与用户额度：`QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/topup.go:317`、`:333`、`:336`、`:341`、`:345`、`:371`；controller 侧只把 admin 请求转给模型层：`QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/topup.go:493`、`:501`。 |
| 行锁与并发 | P1 在 PG store 用 row lock + SERIALIZABLE 守并发履约。 | 真 PG 32 goroutine 测试验证，不用内存 mock 证明钱路径。 | 不做跨数据库兼容；HUAKAI 设计约束是 PostgreSQL。 | New-API 多条支付路径会在事务中锁定订单并检查状态后再更新余额：`QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/topup.go:80`、`:90`、`:98`、`:120`、`:141`、`:142`。 |
| 进程内锁 | P1 不依赖进程内锁作为正确性基础。 | 并发正确性落在 DB 约束和事务；内存锁最多作为优化。 | 如果后续加 worker，可再评估本地去抖。 | New-API 在 controller 层还有每订单内存锁来降低重复处理窗口：`QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/topup.go:267`、`:278`、`:292`。P1 只把它作为防抖启发，不作为正确性来源。 |
| 订阅 | P1 不实现订阅；保留 provider/service seam。 | P3 订阅余额授予将复用 `internal/quota` 策略，而不是临时写余额表。 | 订阅是后续切片。 | New-API 订阅完成路径也锁定支付记录、检查状态并在事务内更新订阅/充值记录：`QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/subscription.go:520`、`:524`、`:526`、`:532`、`:546`。 |

## 11. 具体执行顺序

1. 写 P1 migration 测试和 store 集成测试 skeleton，先明确 mutation 判别断言。
2. 新增 `0071_payment_p1.up.sql` / `.down.sql`，先只跑 migration up/down 和基础约束测试。
3. 新增 `backend/internal/payment` 类型、store interface、memory store，用 service 单测驱动状态机。
4. 实现 PostgreSQL store：建单、确认、履约、余额查询、审计写入。
5. 实现 provider seam：manual/test provider；test provider 只在测试配置启用。
6. 实现 `backend/internal/paymenthttp` handler 和单测。
7. 串行修改 `wiring.go` / `routes.go` 挂载 payment service。
8. 跑验证：普通 unit tests、payment package tests、真 PG integration tests、voucher/billing 相关回归。
9. Stage intended diff 后运行 `codex exec review --uncommitted --full-auto --sandbox read-only`；S0/S1 必修，S2/S3 记录。

建议验证命令：

```bash
go test ./backend/internal/payment ./backend/internal/paymenthttp
HUAKAI_DATABASE_URL="$HUAKAI_DATABASE_URL" go test -tags integration_pg ./backend/internal/payment
go test ./backend/internal/voucher ./backend/internal/billing
```

如果 repo 当前 Go module 路径要求从 `backend/` 目录执行，实施者按现有 CI 命令调整，但测试覆盖目标不变。

## 12. 需要 Owner 拍板的开放决策

1. **0071 共享约束串行协调**：`billing_events` 的 `event_type` CHECK 和 claim/voucher/payment 互斥约束是共享钱路径，必须和另一台机器串行。建议由 Owner 指定哪台机器最终落 0071 迁移。
2. **admin 路由别名**：P1 是否只挂 `/admin/v1/payments/...`，还是同时兼容 `/v1/admin/payments/...`。建议 P1 只挂现有 admin 前缀，避免路由面膨胀。
3. **独立 `payment_audit_events` 表**：我建议 P1 加领域审计表。若 Owner 希望复用现有 admin audit 表，需要确认是否接受再扩展一个共享审计 CHECK/约束点。

不需要 P1 重新拍板的点：真实支付 SDK 延后、订阅延后、voucher-style billing seam、零 import `internal/billing`、handler 新包 `internal/paymenthttp`，这些已在 Owner 信封中确定。

## 13. 功能、clean-room、安全结论

- 功能没有缩水：真实 SDK 没进 P1 是 Owner-gated 后续切片，不是删除支付能力；P1 交付的是可测试、可审计、可恢复的内部支付闭环。
- Clean-room 风险可控：参考项目只作为行为证据；P1 schema、包结构、service API、测试 fixture 均按 HUAKAI 现有 voucher/billing 约束重新设计。
- 安全风险主要在钱路径和 admin 权限：P1 用 admin auth、tenant predicate、DB 约束、SERIALIZABLE 事务、audit_request_id、红线脱敏和真 PG 并发测试控制。
- 与新机协调点只有两个：`0071` 共享 migration，以及 `routes.go` / `wiring.go` 串行挂载。

## 14. Owner 中文摘要

P1 计划把支付先做成内部可闭环的钱路径机器：订单状态机、manual/test provider、管理员确认、断点履约、`payment_credited` 入账、审计和真 PG 判别测试都在范围内；真实支付渠道和订阅不删功能，只按 Owner 信封后移。计划不碰 `internal/billing`、`internal/db/billing`，不在冻结包新增文件，clean-room 只吸收参考项目的行为级证据。最高风险是 `billing_events` 共享约束和双账/丢账，所以 0071 migration 要串行，核心验收必须用真 PG 并发和重复入账测试。

Source files read:

- HUAKAI internal:
  - `docs/RULES.md`
  - `docs/01_PROJECT_BRIEF.md`
  - `docs/03_FEATURE_PARITY_MATRIX.md`
  - `docs/10_RISK_REGISTER.md`
  - `docs/11_ACCEPTANCE_TEST_MATRIX.md`
  - `docs/12_AGENT_WORKFLOW.md`
  - `backend/internal/voucher/store.go`
  - `backend/internal/voucher/store_postgres.go`
  - `backend/internal/voucher/store_memory.go`
  - `backend/internal/voucher/service.go`
  - `backend/internal/voucher/types.go`
  - `backend/internal/voucher/audit.go`
  - `backend/sql/migrations/0002_observability_billing.up.sql`
  - `backend/sql/migrations/0017_stream_state.up.sql`
  - `backend/sql/migrations/0023_voucher_system.up.sql`
  - `backend/sql/migrations/0023_voucher_system.down.sql`
  - `backend/sql/migrations/0029_billing_events_audit_request_id.up.sql`
  - `backend/sql/migrations/0039_money_path_append_only_triggers.up.sql`
  - `backend/cmd/gateway/routes.go`
  - `backend/cmd/gateway/wiring.go`
  - `backend/internal/gatewayhttp/voucher_handler.go`
  - `backend/internal/gatewayhttp/voucher_handler_test.go`
  - `backend/internal/quota/pg_store_integration_test.go`
  - `backend/internal/billing/settler_integration_test.go`
- Reference source, behavior-level only:
  - `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_order.go`
  - `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_fulfillment.go`
  - `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_order_lifecycle.go`
  - `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/migrations/092_payment_orders.sql`
  - `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/migrations/102_add_out_trade_no_to_payment_orders.sql`
  - `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/migrations/120_enforce_payment_orders_out_trade_no_unique_notx.sql`
  - `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/topup.go`
  - `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/topup.go`
  - `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/topup_stripe.go`
  - `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/subscription.go`

Lane: specifier

Agent: Codex GPT-5

UTC timestamp: 2026-05-29T03:16:05Z
