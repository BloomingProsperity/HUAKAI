Using clean-room specifier lane only. I verified the local gap before planning.

**Gap Verification**
- Voucher redemption writes `voucher_redemption`, updates voucher counters, then writes a `billing_events` row, but does not credit `user_balances`: `backend/internal/voucher/store_postgres.go:247`, `backend/internal/voucher/store_postgres.go:273`, `backend/internal/voucher/store_postgres.go:285`.
- The displayed voucher “balance” is derived from redeemed voucher cents, not spendable wallet state: `backend/internal/voucher/store_postgres.go:450`.
- Spend enforcement is tied to `user_balances`: reserve updates only existing rows, and the no-row branch allows the request: `backend/sql/queries/balance_holds.sql:4`, `backend/internal/balancehold/balancehold.go:44`, `backend/internal/balancehold/balancehold.go:58`.
- Capture/debit also depends on an existing hold/user balance path: `backend/internal/balancehold/balancehold.go:82`, `backend/internal/balancehold/balancehold.go:103`.
- `user_balances` exists as USD `numeric(20,8)`: `backend/sql/migrations/0060_user_balance_holds.up.sql:1`.
- Voucher/billing cents exist as `amount_cents bigint`: `backend/sql/migrations/0023_voucher_system.up.sql:9`, `backend/sql/migrations/0023_voucher_system.up.sql:72`.
- I found no production recharge order, payment callback, or webhook route code. Current user routing mounts voucher redeem only: `backend/cmd/gateway/routes.go:92`; existing user voucher handler only mounts `/redeem`: `backend/internal/gatewayhttp/voucher_handler.go:84`.

**Architecture**
- New package: `backend/internal/money`.
  Owns wallet credit/debit primitives, cents-to-USD conversion, idempotent credit application, recharge order lifecycle, payment callback fulfillment, and audit writing.
- Optional new HTTP package: `backend/internal/moneyhttp`.
  Keeps new handlers out of frozen `internal/gatewayhttp`; `backend/cmd/gateway/routes.go` only mounts routes.
- Existing mutable projection: `user_balances`.
  It becomes the authoritative spendable balance projection for enforcement.
- Existing append-only money ledger: `billing_events`.
  Extend allowed event types for recharge/adjustment and link recharge order IDs.
- New table `recharge_orders`.
  Tracks tenant, user, provider, external order key, amount cents, USD credit amount, status, idempotency key, expiry, timestamps.
- New table `payment_webhook_events` or `money_audit_events`.
  Tracks provider callback idempotency, verification outcome, redacted metadata, order transition attempts, and operator repair evidence.
- Enforcement policy.
  Backfill/provision zero `user_balances` rows and change missing-row reserve behavior from allow to fail-closed. Temporary compatibility should be explicit and Owner-gated, not silent default.

**Sliced Plan**
| Slice | Scope files | Discriminating test | Risk | Lane | Depends |
|---|---|---|---|---|---|
| MONEY-1 voucher balance bridge | `backend/internal/money/*`, edit `backend/internal/voucher/store_postgres.go`, tests | Redeem $10 for a user with no balance row, then `balancehold.Reserve($10)` succeeds; replay does not double-credit. Removing the UPSERT or cents/100 conversion makes it fail. | money | Codex small patch | 0060 |
| MONEY-2 fail-closed balance enforcement | edit `backend/internal/balancehold/balancehold.go`, `backend/sql/queries/balance_holds.sql`, migration `0061_*`, generated db | User with no/zero row cannot reserve nonzero cost. Restoring current no-row allow branch makes it fail. | money/schema/quota | Owner-gated backend | MONEY-1 |
| MONEY-3 recharge order foundation | new `backend/internal/money`, migration `0062_recharge_orders`, SQL queries, tests | Create order for 1234 cents stores `12.34000000` USD, pending status, and idempotency returns same order. Dropping unique/idempotency creates duplicate rows and fails. | money/schema | Owner-gated backend | MONEY-1 |
| MONEY-4 mock payment callback | `backend/internal/money`, `backend/internal/moneyhttp`, edit `backend/cmd/gateway/routes.go`, migration `0063_webhook_audit`, tests | Valid signed mock webhook completes order and credits once; replay returns success with no second credit; bad signature/amount mismatch does not credit. | payment/schema/security | Owner-gated backend | MONEY-3 |
| MONEY-5 spend/refund consistency | `backend/internal/billing/settler.go`, `backend/internal/money`, tests | Start at $10, reserve $3, settle $2.25, final balance $7.75 and held $0. Capturing wrong amount or using voucher-sum balance fails. | money | backend | MONEY-1/2 |
| MONEY-6 user/admin visibility + audit | `backend/internal/moneyhttp`, route wiring, docs/tests | Balance/order/audit endpoint shows recharge and spend without raw webhook body/signature. Logging raw payload or omitting callback audit fails. | security/money | frontend/backend split | MONEY-4 |
| MONEY-7 provider plugin boundary | `backend/internal/money/providers/*`, config examples, mock tests | Disabled provider callback cannot credit; enabled mock provider can. Removing provider enable gate fails. | payment/security | Owner-gated | MONEY-4 |
| MONEY-8 docs/release gate | `docs/*` parity, risk, acceptance tests | Matrix names every money-loop capability as implemented/flagged/roadmap. Dropping callback or enforcement row is test-reviewed as missing. | none | reviewer | all |

**Reference Fusion**
| Capability | Clean-room mechanism evidence | HUAKAI delta | Dimension |
|---|---|---|---|
| Wallet/balance ledger | Sub2API keeps user balance fields and updates balance during usage billing: `Wei-Shaw/sub2api@91da8159:backend/ent/schema/user.go:49`, `Wei-Shaw/sub2api@91da8159:backend/internal/repository/usage_billing_repo.go:176`. New API models quota and billing funding sessions: `QuantumNous/new-api@20d3e73:model/user.go:40`, `QuantumNous/new-api@20d3e73:service/funding_source.go:36`. | HUAKAI uses USD `user_balances` projection plus `billing_events`, not quota integers. | architecture |
| Voucher to balance | Sub2API’s redemption transaction marks code used and credits balance in the same DB transaction: `Wei-Shaw/sub2api@91da8159:backend/internal/service/redeem_service.go:317`, `Wei-Shaw/sub2api@91da8159:backend/internal/service/redeem_service.go:337`. | HUAKAI keeps existing voucher tables, adds same-tx `user_balances` credit with cents-to-USD conversion. | algorithm |
| Recharge order | Sub2API creates pending payment orders with amount/provider/status and unique external order key: `Wei-Shaw/sub2api@91da8159:backend/internal/service/payment_order.go:147`, `Wei-Shaw/sub2api@91da8159:backend/migrations/092_payment_orders.sql:1`, `Wei-Shaw/sub2api@91da8159:backend/migrations/120_enforce_payment_orders_out_trade_no_unique_notx.sql:1`. New API records top-up trade/status data: `QuantumNous/new-api@20d3e73:model/topup.go:14`. | HUAKAI adds `recharge_orders` with cents + numeric USD, idempotency key, and provider-neutral mock first. | architecture/ecosystem |
| Payment callback verify/idempotency | Sub2API caps/reads callback body, resolves provider, verifies notification, validates amount/provider, and uses guarded status transition: `Wei-Shaw/sub2api@91da8159:backend/internal/handler/payment_webhook_handler.go:70`, `Wei-Shaw/sub2api@91da8159:backend/internal/service/payment_webhook_provider.go:32`, `Wei-Shaw/sub2api@91da8159:backend/internal/service/payment_fulfillment.go:70`, `Wei-Shaw/sub2api@91da8159:backend/internal/service/payment_fulfillment.go:142`. New API validates raw provider event and fulfills locked orders: `QuantumNous/new-api@20d3e73:controller/topup_stripe.go:155`, `QuantumNous/new-api@20d3e73:controller/topup_stripe.go:258`. | HUAKAI uses DB constraints/CAS plus webhook-event idempotency; no raw body/signature logging. | algorithm/ecosystem |
| Spend-time deduction | Sub2API deduplicates usage billing and updates balance/quota transactionally: `Wei-Shaw/sub2api@91da8159:backend/internal/repository/usage_billing_repo.go:22`, `Wei-Shaw/sub2api@91da8159:backend/internal/repository/usage_billing_repo.go:108`. New API pre-consumes and settles deltas: `QuantumNous/new-api@20d3e73:service/pre_consume_quota.go:31`, `QuantumNous/new-api@20d3e73:service/billing.go:32`. | HUAKAI keeps reserve/capture/release holds, but removes no-row free-spend behavior. | algorithm |
| Audit | Sub2API has payment audit records: `Wei-Shaw/sub2api@91da8159:backend/migrations/093_payment_audit_logs.sql:1`. New API logs top-up/consume/refund categories: `QuantumNous/new-api@20d3e73:model/log.go:44`, `QuantumNous/new-api@20d3e73:model/log.go:119`. | HUAKAI uses `billing_events` for ledger plus redacted payment audit events. | ecosystem |
| Enforcement policy | New API checks quota before pre-consume: `QuantumNous/new-api@20d3e73:service/pre_consume_quota.go:31`, `QuantumNous/new-api@20d3e73:service/billing_session.go:349`. | HUAKAI must move from opt-in row presence to mandatory fail-closed enforcement. | architecture |

**Owner-Gated Slices**
MONEY-2, MONEY-3, MONEY-4, MONEY-7 are high-risk because they touch schema, payment callback behavior, balance enforcement, or provider/security boundaries. MONEY-1 is smaller but still money-path logic, so it should get explicit Owner approval before execution.

**First Slice**
MONEY-1 is the highest-leverage first slice. It needs no new schema, makes already-issued vouchers spendable, proves the cents-to-USD bridge against the existing reserve/capture path, and creates the reusable credit primitive that payment callbacks will need. It does not fully close enforcement by itself; MONEY-2 must follow to stop no-row free spend.

Source files read: HUAKAI `backend/internal/voucher/store_postgres.go`, `backend/internal/voucher/service.go`, `backend/sql/migrations/0023_voucher_system.up.sql`, `backend/sql/migrations/0060_user_balance_holds.up.sql`, `backend/internal/balancehold/balancehold.go`, `backend/sql/queries/balance_holds.sql`, `backend/internal/billing/claim_gate.go`, `backend/internal/billing/settler.go`, `backend/internal/billing/billing.go`, `backend/cmd/gateway/routes.go`, `backend/cmd/gateway/wiring.go`, `backend/internal/gatewayhttp/voucher_handler.go`; references listed in table citations. Lane: specifier. Agent: Codex GPT-5. UTC date: 2026-05-31.

中文总结：我独立核实了 MONEY-LOOP 缺口：voucher 只写兑换和账单事件，不写可消费余额；消费扣款只认 `user_balances`，但无余额行会放行；生产代码里没有充值订单或支付回调。建议先做 MONEY-1，把 voucher 金额同事务桥接到 `user_balances`，再做强制余额行/失败关闭、充值订单、mock 支付回调验签与幂等、审计和真实 provider 插件化。没有建议功能缩水；clean-room 只抽取机制并给出源码引用；主要安全/资金风险集中在 schema、支付回调、余额强制扣款和真实 provider 接入，均需要 Owner 确认。
