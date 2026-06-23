# Feature-Tree Audit: payment-monetization

**Domain summary**: HUAKAI has a strong multi-layer monetization core (payment orders, voucher redemption, subscription + quota policies, immutable billing ledger, two-phase reserve/settle, and mismatch-refund DLQ) but is missing production payment-gateway integrations, auto-renewal, revenue analytics, multi-currency, and tax handling — all of which are present in sub2api / new-api and required by paying operators. (注:admin-initiated refunds 此前列为缺失,实已建并挂路由 `paymenthttp/refund.go:38` + `handler.go:203` `POST /{id}/refund`,见 I-2。)

**Audit date**: 2026-06-02 · Branch: `fix/hermes-phase-1-e33d940`

---

## Feature Table

| # | Feature | Status | Evidence (file:line or grep term tried) | Gap note |
|---|---------|--------|----------------------------------------|----------|
| **A. User Balance (Wallet)** |||||
| A-1 | User balance inquiry (derived from payment_credits SUM) | PRESENT | `backend/internal/payment/service.go` `GetBalance()` · `store_postgres.go` `UserBalanceCents()` | |
| A-2 | Pre-request balance hold (lease-based reservation) | PRESENT | `backend/internal/balancehold/` · migration `0060_user_balance_holds.up.sql` | |
| A-3 | Balance hold sweep / expiry (crash-safe release) | PRESENT | `backend/internal/billing/lease_sweep.go` | |
| A-4 | Balance enforcement at request time (strict / soft / disabled) | PRESENT | `backend/internal/billing/claim_gate.go` · migration `0064_balance_enforcement_mode.up.sql` · `billing/billing.go` `BalanceEnforcementMode` | |
| A-5 | Signup bonus / initial free credit for new users | MISSING | grep `free.*balance\|initial.*credit\|signup.*bonus\|welcome.*credit` → 0 hits | No mechanism to grant auto-credit at registration; acquisition funnel gap |
| A-6 | Admin balance credit / debit with audit trail | PRESENT | `backend/internal/payment/admin_credit.go` `AdminAdjustBalance()` · admin handler `adminhttp/balance_credit_handler.go` | |
| **B. Payment Orders & Topup** |||||
| B-1 | Create topup order (user self-service) | PRESENT | `backend/internal/payment/service.go:CreateOrder()` · `paymenthttp/handler.go:newCreateRechargeHandler()` | |
| B-2 | 7-state order machine (pending→paid→recharging→completed; expired/cancelled/failed) | PRESENT | `backend/internal/payment/types.go:21–80` `OrderStatus` enum | |
| B-3 | Order idempotency (out_trade_no uniqueness) | PRESENT | `backend/sql/migrations/0071_payment_p1.up.sql` unique index `(tenant_id, out_trade_no)` | |
| B-4 | Daily amount limit enforcement | PRESENT | `backend/internal/payment/order.go` `DailyAmountLimit` ($500 default) | |
| B-5 | Max pending orders per user limit | PRESENT | `backend/internal/payment/order.go` `MaxPendingPerUser` (3 default) | |
| B-6 | Order expiry (scheduler / worker) | PRESENT | `payment/types.go` `ExpiresAt` field; expiry enforced at read/confirm time | Order expiry is passive (no background sweeper found); sub2api uses active scheduler |
| B-7 | Order listing for users | PRESENT | `backend/internal/payment/service.go:ListOrders()` | |
| B-8 | Admin confirm paid (manual payment path) | PRESENT | `backend/internal/payment/service.go:AdminConfirmPaid()` | |
| B-9 | Two-phase fulfillment (recharging→completed, idempotent) | PRESENT | `backend/internal/payment/service.go:Fulfill()` · `store_postgres.go:BeginFulfill/CompleteFulfill()` | |
| B-10 | Order-backed subscription fulfillment | PRESENT | `backend/internal/subscription/order_fulfillment.go` · `order_subscription_integration_test.go` | |
| B-11 | OpenRecharge compatibility bridge (legacy path) | PRESENT | `backend/internal/payment/order.go:OpenRecharge()` | |
| **C. Payment Provider Integration** |||||
| C-1 | Manual admin-confirmation provider | PRESENT | `backend/internal/payment/provider.go:manualProvider` | |
| C-2 | HMAC webhook bridge provider | PRESENT | `backend/internal/payment/provider.go:hmacProvider` · `paymenthttp/webhook.go` | |
| C-3 | Test provider (local HMAC, signed callbacks) | PRESENT | `backend/internal/payment/provider.go:testProvider` · `SignTestCallback()` | Test-mode only; not suitable for production real money |
| C-4 | Automatic webhook callback path (P2a) | PRESENT | `backend/internal/payment/webhook.go:ConfirmPaidByCallback()` · constant-time HMAC-SHA256 verify | Provider is internal test/HMAC only; real third-party callbacks not wired |
| C-5 | Stripe integration | MISSING | grep `stripe` → 0 hits in backend/ | Biggest commercial gap for USD/global markets |
| C-6 | Alipay integration | MISSING | grep `alipay` → 0 hits | Required for CN market |
| C-7 | WeChat Pay integration | MISSING | grep `wechat_pay` → 0 hits | Required for CN market |
| C-8 | PayPal integration | MISSING | grep `paypal` → 0 hits | Required for global B2C |
| C-9 | Epay / xunhupay (sub2api-style aggregator bridge) | MISSING | grep `epay\|xunhupay\|paysign` → 0 hits | sub2api supports multiple CN aggregators |
| C-10 | QR code generation for payment | MISSING | No QR generation found | Alipay/WeChat Pay require this |
| C-11 | Payment status polling (async confirmation) | MISSING | No polling loop or status-check endpoint found | Required when webhooks unreliable |
| **D. Voucher / Redemption Codes** |||||
| D-1 | Voucher batch creation (bulk) | PRESENT | `backend/internal/voucher/types.go:BatchStatus` · migration `0023_voucher_system.up.sql:voucher_batch` | |
| D-2 | Single-use-per-user codes | PRESENT | `voucher_system.up.sql` `single_use_per_user` flag · unique index on `(tenant_id, voucher_id, user_id)` | |
| D-3 | Multi-use codes (max_redemptions) | PRESENT | `voucher/types.go:Voucher.MaxRedemptions` | |
| D-4 | Eligible-user targeting (per-user voucher) | PRESENT | `voucher_system.up.sql` `eligible_user_id` nullable column | |
| D-5 | Voucher expiry handling | PRESENT | `voucher/types.go:VoucherStatus` `active/expired/exhausted/revoked` | |
| D-6 | Burst rate limiting / anti-fraud | PRESENT | `backend/internal/voucher/anti_fraud.go` · `voucher_burst_block` table | |
| D-7 | Redemption idempotency | PRESENT | `backend/internal/voucher/idempotency.go` | |
| D-8 | Voucher audit trail | PRESENT | `backend/internal/voucher/audit.go` | |
| D-9 | Voucher-backed balance credit | PRESENT | `GrantKind.balance` → writes `billing_events` | |
| D-10 | Voucher-backed subscription activation | PRESENT | `backend/internal/subscription/voucher_fulfillment.go` · `voucher/redeem_subscription_integration_test.go` | |
| D-11 | Code hash (never raw code stored) + privacy redaction | PRESENT | `voucher/types.go:Voucher.CodeHash` (bytea) + `privacy.go` | |
| D-12 | Admin voucher revocation | PRESENT | `VoucherStatus.Revoked` · service layer | |
| D-13 | Public promo code / discount coupon (user-discoverable) | MISSING | No public-facing promo-code or discount concept beyond vouchers | sub2api / new-api have distinct promo-code flows with landing pages |
| **E. Subscription Management** |||||
| E-1 | Subscription plan catalog | PRESENT | migration `0073_subscription.up.sql:subscription_plans` | |
| E-2 | Subscription activation (admin / order / voucher sources) | PRESENT | `backend/internal/subscription/activation.go` | |
| E-3 | User-group upgrade on activation | PRESENT | `subscription/activation.go` → sets `users.user_group` | |
| E-4 | Subscription expiry worker (polls, marks expired, downgrades group) | PRESENT | `backend/internal/subscription/worker.go:ExpiryWorker` | |
| E-5 | Subscription-linked quota policies (daily/weekly/monthly caps) | PRESENT | `subscription_policy_links` table · `activation.go` creates quota policies | |
| E-6 | Subscription expiry reminders (email) | PRESENT | `backend/internal/subscription/reminder_mailer.go` · `reminder_worker.go` | |
| E-7 | Admin-forced downgrade | PRESENT | `activation.go:EnforceUpgradeOnly` flag; admin can override | |
| E-8 | Self-service plan upgrade | PARTIAL | `ErrDowngradeNotAllowed` enforced; upgrade path exists for admin, but no self-service upgrade endpoint found | Users cannot self-upgrade without admin; sub2api allows self-service plan purchase |
| E-9 | Subscription auto-renewal | MISSING | ExpiryWorker only marks expired; no renewal scheduler or recurring-payment trigger found | Critical for recurring revenue; sub2api / new-api both auto-renew |
| E-10 | Subscription grace period | MISSING | No grace_period field in schema or activation logic | |
| E-11 | Subscription pause / freeze | MISSING | No pause state in subscription state machine | |
| E-12 | Plan for-sale flag + storefronts | PRESENT | `subscription_plans.for_sale` + `enabled` columns in migration | |
| E-13 | Subscription cancellation | PRESENT | `store.go:CancelSubscription()` · `AuditGroupDowngraded` event | |
| **F. Quota System** |||||
| F-1 | Per-user quota policies | PRESENT | `backend/internal/quota/types.go:ScopeKind.User` | |
| F-2 | Per-API-key quota policies | PRESENT | `ScopeKind.APIKey` | |
| F-3 | Per-channel quota policies | PRESENT | `ScopeKind.Channel` | |
| F-4 | Per-pool-group quota policies | PRESENT | `ScopeKind.PoolGroup` | |
| F-5 | Per-provider-account quota policies | PRESENT | `ScopeKind.ProviderAccount` | |
| F-6 | Global quota policies | PRESENT | `ScopeKind.Global` | |
| F-7 | Multiple metrics (requests / tokens / cost_usd / concurrency) | PRESENT | `quota/types.go:Metric` enum | |
| F-8 | Window kinds (none / fixed / calendar_day / calendar_week / calendar_month) | PRESENT | `quota/types.go:WindowKind` · migration `0072_quota_calendar_month.up.sql` | |
| F-9 | Enforce / observe / manual_first / disabled modes | PRESENT | `quota/types.go:Mode` | |
| F-10 | Two-phase reserve + settle | PRESENT | `quota/service.go` + `quota/service_settle.go` | |
| F-11 | Quota reconciler (mismatch retry) | PRESENT | `backend/internal/quota/reconciler.go` | |
| F-12 | Quota window counters (reserved / settled / overage) | PRESENT | `quota_windows` table · `pg_store.go` | |
| F-13 | Per-key spending limit (via quota policy) | PRESENT | API key scope + cost_usd metric covers this | |
| F-14 | Concurrency slot tracking | PRESENT | `quota/types.go:ConcurrencySlot` · `service.go` concurrency dimension | |
| **G. Billing & Settlement** |||||
| G-1 | Immutable billing ledger (billing_events append-only) | PRESENT | migration `0027_ledger_append_only_trigger.up.sql` · `billing_events` table | |
| G-2 | Tx1 claim gate (row-lock + idempotency + balance check) | PRESENT | `backend/internal/billing/claim_gate.go` | |
| G-3 | Tx2 settler (usage record + audit event + quota settle + effects) | PRESENT | `backend/internal/billing/settler.go` | |
| G-4 | Settlement DLQ + recovery (post-delivery crash safety) | PRESENT | `backend/internal/settlementrecovery/` · `0053_post_delivery_settlement_dlq_kind.up.sql` | |
| G-5 | Reconciliation worker (billing mismatch detection) | PRESENT | `backend/internal/billing/reconciliation_worker.go` | |
| G-6 | Billing settings per tenant | PRESENT | `backend/internal/billing/settings_store.go` · migration `0046_billing_settings.up.sql` | |
| G-7 | Cache-hit pricing (discounted settlement) | PRESENT | migration `0043_usage_records_cache_hit_settlement.up.sql` · `billing/settler.go` | |
| G-8 | Idempotency replay records (Tx2 dedup) | PRESENT | `billing/replay_store.go` · migration `0044_idempotency_replay_records.up.sql` | |
| G-9 | Billing claim states (pending→committed/aborted) | PRESENT | `backend/internal/billing/state.go` | |
| G-10 | Trust-chain audit ledger (signed receipts) | PRESENT | migration `0013_trust_chain_audit_ledger.up.sql` · `audit/receipt_formatter.go` | |
| G-11 | Billing events for payment credit / voucher / recharge (4-branch mutual exclusion) | PRESENT | migration `0071_payment_p1.up.sql` expands billing_events CHECK constraint | |
| **H. Pricing Configuration** |||||
| H-1 | Per-model pricing data (input/output token prices) | PRESENT | `billing_pricing_versions` table · migration `0030_pricing_versions_public_scope.up.sql` | |
| H-2 | Historical pricing snapshots (immutable, versioned) | PRESENT | `backend/internal/billing/rate_table_source.go:RateTableSource` | |
| H-3 | Public pricing API (list versions, get by version/snapshot) | PRESENT | `billing/rate_table_source.go:GetRateTable()`, `ListRateTableSnapshots()` | |
| H-4 | Default pricing bootstrap (seed data) | PRESENT | migration `0068_default_pricing_bootstrap.up.sql` | |
| H-5 | Chat completions pricing calculation (token counting → cost_usd) | PRESENT | `backend/internal/gatewayhttp/chat_completions_pricing.go` | |
| H-6 | Multi-currency pricing (non-USD) | MISSING | `payment/types.go:175` `ErrUnsupportedCurrency` "P1 ledger is USD-only" · `payment/order_test.go:50` test name makes explicit | All pricing locked to USD; non-USD markets blocked |
| H-7 | Markup / margin configuration per tenant or channel | PRESENT | 按 pool_group 定价倍率:`backend/internal/pricingcatalog/ratio_resolver.go` `RatioResolver` (TTL 缓存 + 审计链) · admin 写端点 `pricingcataloghttp/pricing_ratio_handler.go:67` `MountPricingRatioRoutes` / `:73` `PUT /{pool_group_id}` / `:141` `UpsertRatio` 挂 `cmd/gateway/routes_pricing.go:16` · 注入所有 dispatch deps | per-pool-group ratio multiplier 已建并接计价 |
| H-8 | Admin API to update / publish new pricing version | MISSING | `RateTableSource` is read-only; no write endpoint found | Operators cannot update prices without DB surgery |
| H-9 | Per-provider cost tracking (upstream cost vs. user billing) | PARTIAL | Provider account tracked in billing_claims; no margin report | Schema has data; no report surface |
| **I. Refund & Dispute** |||||
| I-1 | Audit mismatch refund (negative reconciliation, DLQ-based) | PRESENT | `backend/internal/audit/refund_worker.go:RefundWorker` · `0032_audit_mismatch_refund_pending.up.sql` | |
| I-2 | Admin-initiated manual refund (credit back to balance) | PRESENT | `backend/internal/payment/service.go:236` `RefundOrder()` (幂等键必填 + 金额上限 CAS + 负向入账) · `paymenthttp/refund.go:38` `newAdminRefundHandler` 挂 `paymenthttp/handler.go:203` `POST /{id}/refund` | 专用 admin 退款端点已建并挂路由,与 admin_credit 调整区分;另有用户发起退款申请状态机 `paymenthttp/user_portal.go` |
| I-3 | Refund audit trail | PRESENT | `audit/refund_worker.go` appends to billing_events as negative reconciliation | |
| I-4 | Chargeback handling | MISSING | No chargeback/dispute flow | Required by real payment gateways |
| I-5 | Refund rate limits / anti-abuse | MISSING | No refund rate limiting found | |
| **J. Usage Transparency & Receipts** |||||
| J-1 | Per-request cost receipt (trust-chain verified) | PRESENT | `backend/internal/audit/receipt_formatter.go` · `receipt_storage.go` | |
| J-2 | User usage history API (paginated, cursor-based) | PRESENT | `backend/internal/meusagehttp/handler.go` → `GET /v1/users/me/usage` | |
| J-3 | Ledger verify hint in usage records (trust-chain path) | PRESENT | `meusagehttp/handler.go:verifyHint` struct | |
| J-4 | Usage export (CSV / PDF download) | MISSING | No export endpoint; grep `csv\|pdf\|download\|export` in billing/usage paths → 0 hits | Enterprise buyers require billing exports |
| J-5 | Monthly billing statement / invoice PDF | MISSING | No invoice generation found | Required for B2B billing |
| J-6 | Per-API-key usage breakdown | PARTIAL | `api_key_id` stored in billing_claims; `meusagehttp` handler does not filter by key | Schema supports it; query/endpoint missing |
| **K. Referral & Community** |||||
| K-1 | Invitation code generation (monthly quota, expiry, max-usage) | PRESENT | `backend/internal/community/invitation/service.go` · `types.go` | |
| K-2 | Referral tracking schema (referrals table, billing_event link) | PRESENT | migration `0034_community_invitation_referral.up.sql:referrals` + `referral_rewards` tables | |
| K-3 | Referral reward crediting service (actual credit on referral event) | PRESENT | `backend/internal/payment/signup_invitee_reward.go:169` `IssueInviteeReward()` / `:140` `IssueSignupBonus()` 真实写钱包 credit · `community/invitation/referral_reward_config.go` + `referral_qualification.go` + `referral_reward_store.go` · 注册路径调用 `cmd/gateway/wiring.go:925,929` | crediting 服务码已建并接注册路径(amounts 经 env,默认 0=OFF 但码在) |
| K-4 | Referral reward rate configuration | MISSING | No reward rate config found | |
| **L. Notifications** |||||
| L-1 | Subscription expiry reminder emails | PRESENT | `backend/internal/subscription/reminder_mailer.go` · `reminder_worker.go` · migration `0074_subscription_reminders.up.sql` | |
| L-2 | Low balance alerts (email / in-app) | PRESENT | `backend/internal/notify/notifier.go:93` `NotifyLowBalance()` + 可配 `BalanceThreshold` (`:112`) · `notify/types.go:23` `EventLowBalance="low_balance"` · 生产 settle 经 `cmd/gateway/wiring.go:943` `notify.NewSettler(...)` 包裹触发,多渠道 email/webhook/bark/gotify | 低余额告警子系统已建并接生产结算路径 |
| L-3 | Payment confirmation notification | MISSING | No payment success email flow found | |
| L-4 | Invoice / receipt email delivery | MISSING | No email delivery for receipts | |
| **M. Admin Revenue Operations** |||||
| M-1 | Admin order list + confirmation UI | PRESENT | `paymenthttp/handler.go:AdminConfirmPaid` · audit trail | |
| M-2 | Admin subscription assignment | PRESENT | subscription `source=admin` path | |
| M-3 | Admin billing settings management | PRESENT | `billing/settings_store.go` · admin handler | |
| M-4 | Revenue summary / admin dashboard (total revenue, MRR, active subs) | MISSING | No aggregation query or admin revenue endpoint found | Operators have no revenue visibility |
| M-5 | Per-model revenue breakdown | MISSING | Billing claims have model info; no aggregation endpoint | |
| M-6 | Provider cost vs. revenue margin report | MISSING | Data exists in schema; no report surface | |
| M-7 | Admin pricing version publish | MISSING | Rate table is read-only via API (see H-8) | |
| M-8 | Bulk user balance adjustment | MISSING | `AdminAdjustBalance` is single-user only | |
| **N. Tax & Compliance** |||||
| N-1 | VAT / GST / sales tax calculation | MISSING | grep `tax\|vat\|gst` → 0 hits | Required in EU/IN/AU for SaaS |
| N-2 | Tax invoice generation | MISSING | No tax invoice flow | |
| N-3 | Multi-jurisdiction compliance (regional pricing rules) | MISSING | No regional billing config | |
| **O. Free Tier & Trials** |||||
| O-1 | Free tier (default user group with implicit limits) | PARTIAL | `DefaultUserGroup = "default"` exists in subscription; but no automatic initial credit or quota policy for new users | Group exists; onboarding credit/policy missing |
| O-2 | Trial subscription period | MISSING | No trial_period_days or trial logic in subscription schema | |
| O-3 | Credit card pre-authorization (for metered billing) | MISSING | Not applicable to current topup model, but missing for future metered billing | |

---

## Top Missing Features, Ranked by Commercial Value

1. **Real payment gateway integration (Stripe / Alipay / WeChat Pay)** — C-5, C-6, C-7, C-8  
   No operator can accept live payments without a real gateway. Manual admin confirmation is a workaround, not a product. Estimated revenue impact: blocks all self-serve topup revenue.

2. **Subscription auto-renewal** — E-9  
   Subscriptions expire and the worker marks them expired, but there is no automatic re-charge or renewal trigger. Recurring revenue requires auto-renewal with payment-gateway integration. sub2api and new-api both implement this.

3. ~~**Admin-initiated manual refund endpoint** — I-2~~ 已实现(BUILT/WIRED)  
   专用 admin 退款 service + HTTP 端点已建并挂路由(`payment/service.go:236` `RefundOrder`,幂等键必填 + 负向入账;`paymenthttp/refund.go:38` `newAdminRefundHandler` 挂 `handler.go:203` `POST /{id}/refund`),客服无需直接改库即可退款。

4. **Revenue analytics / admin reporting** — M-4, M-5, M-6  
   Operators have zero revenue visibility (MRR, total topup, per-model revenue, provider margin). Sub2api provides a full stat dashboard. This is table-stakes for commercial operations.

5. **Multi-currency support** — H-6  
   Hard-coded `ErrUnsupportedCurrency` blocks every non-USD market (CN, EU, etc.). sub2api supports CNY + USD natively; new-api does too.

6. **Admin API to publish new pricing versions** — H-8  
   The pricing table and version history exist but are read-only via API. Operators cannot update pricing without direct DB writes, creating operational friction on every model price change.

7. ~~**Per-channel / per-tenant markup multipliers** — H-7~~ 已实现(BUILT/WIRED)  
   按 pool_group 的定价倍率已建并接计价:`pricingcatalog/ratio_resolver.go` `RatioResolver`(TTL + 审计链)+ admin 写端点 `pricingcataloghttp/pricing_ratio_handler.go:73` `PUT /{pool_group_id}` → `:141` `UpsertRatio`,挂 `routes_pricing.go:16`,注入所有 dispatch deps。

8. ~~**Low balance alert notifications** — L-2~~ 已实现(BUILT/WIRED)  
   低余额告警子系统已建并接生产 settle:`notify/notifier.go:93` `NotifyLowBalance` + 可配 `BalanceThreshold`(`:112`)+ `notify/types.go:23` `EventLowBalance="low_balance"`,经 `wiring.go:943` `notify.NewSettler(...)` 每次结算触发,多渠道 email/webhook/bark/gotify。

9. **Referral reward service implementation** — ~~K-3~~ 已实现(BUILT/WIRED), K-4  
   K-3 reward crediting 服务码已建:`payment/signup_invitee_reward.go:169` `IssueInviteeReward` / `:140` `IssueSignupBonus` 真实写钱包 credit + `community/invitation/referral_reward_config.go`/`referral_qualification.go`/`referral_reward_store.go`,注册路径调用 `wiring.go:925,929`(amounts 经 env,默认 0=OFF 但码在);K-4 reward rate 配置仍为 backlog。

10. **Usage export (CSV / PDF) and monthly invoices** — J-4, J-5  
    Enterprise and B2B buyers require downloadable billing statements for accounting and compliance. The usage data exists in the DB; it is just never serialized to a file format or emailed.

11. **Signup bonus / free initial credit** — A-5  
    No mechanism to grant automatic credit to new users at registration. This is the standard acquisition funnel entry point (e.g., sub2api grants configurable initial quota). Without it, new user activation requires admin intervention.

12. **Tax calculation and tax invoices** — N-1, N-2  
    Required by law for SaaS in EU (VAT), India (GST), Australia (GST), and others. Completely absent.
