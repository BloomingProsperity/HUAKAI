# Billing & Quota Feature Tree — HUAKAI Audit

**Domain summary:** HUAKAI has a sophisticated billing core (Tx1 balance-hold + Tx2 atomic settlement, trust-receipt chain, voucher, subscription, payment orders) that is partially production-wired; the quota-policy enforcement engine (`internal/quota`) is now WIRED into the live request path — `internal/quotaenforce` (which imports `internal/quota`, `quotaenforce/settler.go:15`) is imported by `gatewayhttp/chat_completions_dispatch.go:33` and calls `QuotaReserver.Reserve` inside `reserveQuota` (`chat_completions_dispatch.go:522`), denying with `insufficient_quota` (`:539-546`); embeddings/images/rerank/completions/audio handlers and `cmd/gateway/wiring.go` import it too. Per-window/concurrency/token policies therefore take real runtime effect.

Audit date: 2026-06-02  
Auditor: Claude Sonnet 4.6 (read-only grep sweep, no code modified)  
Scope: `backend/` Go codebase; `exploratory/rust-core-gateway/` excluded (Rust sidecar is TLS/transport only, no billing logic)

---

## Feature Table

| # | Feature | Status | Evidence (file:line or grep) | Gap Note |
|---|---------|--------|------------------------------|----------|
| 1 | **Prepaid balance top-up (user self-service)** | PARTIAL | `internal/paymenthttp/routes.go:83` `MountUserRoutes`, `internal/payment/types.go:OrderStatus` | User recharge API exists (`POST /v1/users/me/recharges`). Only manual/test/HMAC providers implemented; no real payment gateway (Alipay/WeChat/Stripe). |
| 2 | **Payment order lifecycle (pending→paid→recharging→completed)** | PRESENT | `internal/payment/types.go:37-60` `OrderStatus`, `internal/payment/order.go` | Full state machine with idempotency (out_trade_no), expiry TTL, recharging recovery breakpoint. |
| 3 | **Admin manual balance credit/debit** | PRESENT | `internal/payment/admin_credit.go:45` `AdminAdjustBalance`, `internal/adminhttp/balance_credit_handler.go:32` | `/admin/v1/balances/adjustments` endpoint wired in routes. Both credit and debit (negative amount) supported. |
| 4 | **Payment webhook / auto-callback** | PARTIAL | `internal/paymenthttp/webhook.go:31` `MountPaymentWebhookRoutes`, `internal/payment/provider.go:36` `CallbackVerifier` | `/v1/payments/webhooks/{provider}` endpoint live. Only `test` provider implements `CallbackVerifier`; `manual` provider returns `ErrProviderNoCallback`. Real-money channel (Alipay/WeChat/Stripe) not implemented. |
| 5 | **Balance hold / escrow at request time (Tx1)** | PRESENT | `internal/balancehold/balancehold.go:46` `Reserve`, `internal/billing/claim_gate.go:118` calls balancehold | Holds predicted cost from user balance inside Serializable Tx before upstream call. `ErrInsufficientBalance` → 402 to client. Two enforcement modes: mandatory / opt-in. |
| 6 | **Balance enforcement mode (mandatory / opt-in)** | PRESENT | `internal/billing/settings_policy.go:14-30` `BalanceEnforcementMode`, `internal/billing/claim_gate.go:244` | Admin-configurable per-tenant; stored in `billing_settings` table; default mandatory. |
| 7 | **Atomic billing settlement (Tx2)** | PRESENT | `internal/billing/settler.go:75` `DefaultSettler.Settle`, `internal/billing/billing.go:30-55` `SettleRequest` | Atomic: usage_records INSERT + billing_events INSERT + balance_hold capture + claim status flip + pool slot release. |
| 8 | **Settlement recovery / crash resilience** | PRESENT | `internal/settlementrecovery/`, `internal/billing/reconciliation_worker.go:1` | DLQ-backed recovery for inflight claims that lost Tx2 (crash or timeout). Pending reconciliation worker scans for orphans. |
| 9 | **Per-model pricing with versioned rate tables** | PRESENT | `internal/billing/rate_table_source.go:28` `RateTableSource`, migration `0068_default_pricing_bootstrap.up.sql` | `billing_pricing_versions` table; input/output/cache-read/cache-creation micro-USD pricing; `model_multiplier` per model; public read API `/v1/pricing/rate-table`. |
| 10 | **Cache-tier pricing (5m / 1h cache creation, cache read)** | PRESENT | `internal/gatewayhttp/chat_completions_pricing.go:28-45` `completionRateVector` | Distinct rate slots: `CacheCreation`, `CacheCreation5m`, `CacheCreation1h`, `CacheRead`. Aligned with Anthropic official pricing tiers. |
| 11 | **Pricing admin read API** | PRESENT | `cmd/gateway/routes.go` `/v1/pricing/rate-table`, `/v1/pricing/snapshots` | Public endpoints to fetch current and historical rate tables. |
| 12 | **Pricing admin write API (publish new rates)** | MISSING | Grep: no `/admin/v1/pricing` write handler in `routes.go` or any `adminhttp` file | Rate table is only updated via migration (`0068`). No HTTP endpoint for admin to publish new pricing versions at runtime. |
| 13 | **Usage record tracking (per-request ledger)** | PRESENT | `internal/db/billing/observability.sql.go` `ListUsageRecords`, `cmd/gateway/routes.go:58` `/v1/me/usage` | `usage_records` table written atomically in Tx2. User-facing paginated endpoint `/v1/me/usage` with cursor pagination. |
| 14 | **Admin usage / billing claims / audit event query** | PRESENT | `cmd/gateway/routes.go:510-513` `/admin/v1/usage`, `/admin/v1/billing/claims`, `/admin/v1/audit-events` | Full admin query surface with filter (tenantID, model, API key, time range). |
| 15 | **Audit / trust receipt chain** | PRESENT | `internal/trustreceipt/`, `internal/auditledger/`, `internal/gatewayhttp/cost_receipt_handler.go` | Per-request cryptographic cost receipt; Merkle-tree audit ledger; user-verifiable via `/v1/receipts/{id}/verify`. HUAKAI-original differentiator. |
| 16 | **Billing cost mismatch detection + auto-refund** | PRESENT | `internal/audit/mismatch_detector.go`, `internal/audit/refund_worker.go:340` | Async crosscheck of receipt vs re-computed cost; enqueues DLQ refund if delta exceeds threshold. Refund written as negative billing_event. |
| 17 | **Internal billing refund (negative credit)** | PRESENT | `internal/billing/settler.go:535` `DefaultSettler.Refund`, `internal/billing/billing.go` `RefundRequest` | Idempotent negative reconciliation event on a settled claim. Used by mismatch auto-refund path. |
| 18 | **User-initiated refund / full order refund** | PRESENT | `internal/paymenthttp/refund.go:38` `newAdminRefundHandler` → `payment.Service.RefundOrder` (`payment/service.go:236`); route `paymenthttp/handler.go:203` `r.Post("/{id}/refund", ...)`; user-side `paymenthttp/user_portal.go` `RefundRequestStatus` state machine | Admin full-order refund (idempotency-key required, CAS, negative ledger entry) is wired; users file refund requests (pending→approve/reject) that an operator settles via `RefundOrder`. |
| 19 | **DLQ with admin replay** | PRESENT | `internal/dlq/`, `cmd/gateway/routes.go` `/admin/v1/dlq/{handler}/replay` | DLQ for settlement, billing persist, and settlement recovery; admin replay endpoints wired. |
| 20 | **Voucher / redemption-code system** | PRESENT | `internal/voucher/types.go:1` full package; `internal/gatewayhttp` voucher admin routes wired | Batch creation, single-use-per-user flag, eligible user restriction, expiry, revocation, anti-fraud audit. Two grant kinds: balance credit and subscription activation. |
| 21 | **Subscription plans (catalog)** | PRESENT | `internal/subscription/types.go:70` `Plan` struct; `internal/subscriptionhttp/handler.go` CRUD | Plan CRUD with daily/weekly/monthly USD caps, granted user group, validity days, for-sale flag, sort order. |
| 22 | **Subscription lifecycle (assign / cancel / expire)** | PRESENT | `internal/subscription/service.go:122` `CancelSubscription`; `internal/subscription/worker.go` `ExpiryWorker` | Admin assign, user cancel (via HTTP), expiry worker (1-min scan), group downgrade on expiry. |
| 23 | **Subscription auto-renewal (payment-triggered)** | MISSING | Grep: no auto_renew or recurring charge path in subscription or payment packages | Subscriptions expire and must be manually re-purchased. No automatic charge of stored payment method on renewal. |
| 24 | **Subscription expiry reminders (email)** | PRESENT | `internal/subscription/reminder_worker.go`, `internal/subscription/reminder_mailer.go` | Periodic worker scans near-expiry subscriptions and queues reminder emails at configurable intervals. |
| 25 | **Subscription enforcement (route gate)** | PRESENT | `internal/subscriptionenforce/gate.go:1` `GroupPolicyGate`; wired in pool selector | Pool-group whitelist gate per `user_group`; fail-open on DB error (entitlement vs money gate distinction). |
| 26 | **Subscription purchase via order** | PRESENT | `internal/payment/types.go:80` `OrderKindSubscription`; `internal/subscription/activation.go` `ActivateOrRenewTx` | Subscription order links `subscription_plan_id`; fulfillment atomically activates/renews subscription in same Tx as payment credit. |
| 27 | **Subscription purchase via voucher** | PRESENT | `internal/voucher/types.go:18` `GrantKindSubscription`; `internal/subscription/voucher_fulfillment.go` | Voucher with `grant_kind='subscription'` activates subscription without billing_events write. |
| 28 | **Quota policy schema (multi-scope, multi-metric, multi-window)** | PRESENT | `internal/quota/types.go:12-55` all scope/metric/window kinds; migration `0070_quota_subsystem.up.sql` | Scopes: global/user/api_key/channel/pool_group/provider_account. Metrics: requests/tokens_estimated/cost_usd/concurrency. Windows: fixed/calendar_day/calendar_week/calendar_month/manual. Modes: enforce/observe/manual_first/disabled. |
| 29 | **Quota reservation service (window counter + concurrency slot)** | PRESENT | `internal/quota/service.go:62` `Service.Reserve`; integration tests at `service_integration_test.go` | Full policy-matching, window counter atomic increment, concurrency slot acquisition, fail-closed on store error, idempotency replay, audit event write. |
| 30 | **Quota settlement service** | PRESENT | `internal/quota/service_settle.go`; `internal/quota/reconciler.go` | Settle moves reserved→settled in quota_windows; overage tracked; reconciliation job backfill for crash recovery. |
| 31 | **Quota enforcement wired into gateway hot path** | PRESENT | `internal/gatewayhttp/chat_completions_dispatch.go:33` imports `quotaenforce` (which imports `internal/quota`, `quotaenforce/settler.go:15`); `reserveQuota` calls `QuotaReserver.Reserve` at `chat_completions_dispatch.go:522`, denies with `insufficient_quota` at `:539-546` | Quota reservation runs in the live dispatch path: input-token estimate feeds the token-per-window metric, deny aborts the claim and returns `insufficient_quota`. embeddings/images/rerank/completions/audio handlers wire `quotaenforce` too. Window/concurrency/token/request limits take real runtime effect. |
| 32 | **Quota admin CRUD API (create/update/delete policies)** | MISSING | Grep: no `/admin/v1/quota-policies` route in `routes.go`; no quota admin handler file | Only subscription activation writes quota_policies (indirectly via `subscription/store_postgres.go`). No HTTP endpoint for operators to create/modify/delete quota policies directly. |
| 33 | **Per-API-key quota enforcement** | MISSING | `ScopeAPIKey` defined in `quota/types.go:22`; no gateway code checks it | Schema supports it; no dispatch-path enforcement and no admin API to set per-key quotas. |
| 34 | **Per-channel / pool-group quota enforcement** | MISSING | `ScopeChannel`, `ScopePoolGroup` defined; no router gate for it | Same as per-API-key: schema exists, not wired. Pool-level quota in `provider_accounts` table (cap_quota_daily/weekly/total via migration `0001`) is read in SQL but never checked in pool dispatcher logic. |
| 35 | **Provider-account egress quota caps** | PARTIAL | `internal/db/billing/pool_accounts.sql.go:122-129` `CapQuotaDaily/Weekly/Total` fields read; no pool dispatcher code checks them | Fields exist in schema and are fetched by SQL queries; `internal/pool/dispatcher/` and `internal/pool/router/` contain no code that reads or enforces these caps. |
| 36 | **Concurrency limiting (per-user / per-key)** | PRESENT | `internal/quota/types.go:37` `MetricConcurrency`; reserved via `chat_completions_dispatch.go:522` `QuotaReserver.Reserve` | Concurrency slot model is implemented in `quota.Service` and now reached on the live dispatch path through `quotaenforce` (see #31). |
| 37 | **Token-level quota enforcement (pre-request estimate)** | PRESENT | `internal/tokencheck/estimator.go` `HeuristicEstimator`; `MetricTokensEstimated` in quota types; input-token estimate fed to Reserve at `chat_completions_dispatch.go:522` (`ReservedTokens` field) | The dispatch path now passes the input-token estimate into `QuotaReserver.Reserve` as `ReservedTokens`, exercising the token-per-window metric (see #31). |
| 38 | **Multi-tenant billing isolation** | PRESENT | `internal/billing/fk_regression_integration_test.go:55` cross-tenant FK test; `billing/claim_gate.go:42` tenant-scope guard | `tenant_id` on every table; composite FKs prevent cross-tenant claim/usage binding; cross-tenant abort rejected by scoped WHERE clause. |
| 39 | **Idempotency (payment, claim, voucher)** | PRESENT | `internal/payment/types.go` `out_trade_no` unique; `internal/billing/billing.go` `ComputeIdempotencyFingerprint`; `internal/voucher` per-user dedup | Three-layer idempotency: payment orders (out_trade_no), billing claims (logical_request_id + payload hash), voucher (per-user redemption). |
| 40 | **Balance query API (user)** | PRESENT | `internal/payment/service.go` `GetBalance`; `internal/paymenthttp/handler.go` user routes | Users can query their current balance via `/v1/users/me/payments/balance` (inferred from UserDeps.Service interface). |
| 41 | **Balance query API (admin)** | PRESENT | `internal/payment/service.go` `GetBalance` used by `AdminDeps`; `paymenthttp/handler.go` admin routes | Admin can query any user's balance. |
| 42 | **Payment order list / audit log** | PRESENT | `internal/paymenthttp/handler.go` `ListOrders`, `ListAuditEvents` | Admin and user can list orders and payment audit events. |
| 43 | **Subscription audit log** | PRESENT | `internal/subscription/types.go:128-140` `AuditEvent` with event types; `subscriptionhttp/handler.go` | Subscription audit events for created/renewed/expired/cancelled/group-upgraded/downgraded. |
| 44 | **Spend / low-balance alert / threshold notification** | PRESENT | `internal/notify/notifier.go:93` `NotifyLowBalance` + `:112` `BalanceThreshold`; `notify/types.go:23` `EventLowBalance = "low_balance"`; wired in `cmd/gateway/wiring.go:943` `notify.NewSettler(settler, notifier, ...)` | Low-balance alert subsystem: configurable `BalanceThreshold` (default 5.00, `types.go:29`), `low_balance` event over email/webhook/bark/gotify, triggered from the production settle path via the `notify.NewSettler` wrapper. |
| 45 | **Free tier / trial credits (new-user bonus)** | MISSING | Grep: no `free_tier`, `FreeTier`, `trial`, `registration_bonus` in any non-test Go file | No automatic credit grant on registration. Users start with zero balance. |
| 46 | **Postpaid billing / credit limits** | MISSING | `internal/billing/billing.go` ErrInsufficientBalance drives hard prepaid gate; no overdraft concept | All billing is prepaid. No postpaid mode, no monthly invoiced credit limit for trusted customers. |
| 47 | **Invoice generation / export** | MISSING | Grep: no `invoice`, `Invoice`, PDF, CSV billing export in any file | No invoice creation, download, or export endpoint. |
| 48 | **Revenue / usage analytics dashboard API** | MISSING | Admin usage query exists but returns raw records; no aggregated revenue time-series endpoint | No `/admin/v1/analytics/revenue` or equivalent. Operators must aggregate raw `usage_records` themselves. |
| 49 | **Pricing admin write (runtime rate publish)** | MISSING | Grep: no POST/PUT `/admin/v1/pricing` endpoint in routes or adminhttp | Operators must deploy a DB migration to update model prices. No runtime pricing management API. |
| 50 | **Real payment gateway integrations** | MISSING | `internal/payment/provider.go:21` comment lists Stripe/Alipay/WeChat/epay as "Owner-gated future"; only manual/test/HMAC exist | `ProviderManual`, `ProviderTest`, `ProviderHMAC` are the only live providers. All real-money channels are absent. |
| 51 | **Multi-currency support** | PARTIAL | `internal/payment/types.go` `ErrUnsupportedCurrency = "P1 ledger is USD-only"`; `currency_code` field exists | Schema accepts any currency_code string, but service rejects non-USD with `ErrUnsupportedCurrency`. No FX conversion. |
| 52 | **User-visible quota status API** | MISSING | `/v1/me/usage` exists (usage records) but no `/v1/me/quota` showing remaining quota, window reset time, or policy limits | Users cannot see their quota limits or consumption from the API. |
| 53 | **Manual quota reset (admin override)** | MISSING | `WindowManual` kind defined in `quota/types.go:47`; no admin endpoint to trigger manual window reset | The manual window kind is defined but there is no HTTP endpoint to trigger it, and the quota service is not wired anyway (see #31). |
| 54 | **Subscription downgrade-guard (self-service anti-downgrade)** | PRESENT | `internal/subscription/types.go` `ErrDowngradeNotAllowed`; `activation.go` `EnforceUpgradeOnly` flag | Self-service purchases cannot go to a lower-cap plan for same group; admin override allowed. |
| 55 | **Subscription plan visibility (public catalog)** | PRESENT | `subscriptionhttp/handler.go` `ListPlans`; `GET /v1/users/me/subscriptions` user routes | Users can list available plans (for_sale=true) and their own active subscriptions. |
| 56 | **Quota reconciliation worker (crash recovery)** | PRESENT | `internal/quota/reconciler.go`; `internal/billing/reconciliation_worker.go` | Two complementary reconcilers: billing-side PendingReconciliationWorker for stream orphans; quota-side ReconciliationJob table for quota settle failures. |
| 57 | **Provider-account in-flight tracking (concurrency)** | PRESENT | `internal/db/billing/pool_slot_acquisitions.sql.go`; settler releases slot in Tx2 | `pool_slot_acquisitions` table tracks active upstream requests per account; released atomically in Tx2 or by sweep worker. |
| 58 | **Subscription fulfillment effect ledger (idempotency + reversal foundation)** | PRESENT | `internal/subscription/types.go:140` `FulfillmentEffect`; `internal/subscription/activation.go` | Per-source (order/voucher/admin) idempotent activation record; `reversal_state` field pre-wires refund reversal (P5 roadmap). |
| 59 | **Billing settings audit trail** | PRESENT | `internal/gatewayhttp/admin_billing_settings_audit_tx.go` | Settings changes (enforcement mode, interrupt policy) written atomically with admin audit log entry. |
| 60 | **Per-request cost breakdown (cache split)** | PRESENT | `internal/gatewayhttp/chat_completions_pricing.go:60` `actualCompletionCost` returns `completionCostBreakdown` with `CacheCreationCost`/`CacheReadCost` | Cache-creation and cache-read costs tracked separately from base cost; included in billing_events payload. |

---

## Top Missing Features, Ranked by Commercial Value

1. ~~**Quota enforcement wired to gateway hot path** (#31)~~ — *DONE (no longer missing).*  
   The `internal/quota` subsystem is now reached on the live request path via `internal/quotaenforce`: `gatewayhttp/chat_completions_dispatch.go:33` imports it and `reserveQuota` calls `QuotaReserver.Reserve` (`chat_completions_dispatch.go:522`), denying with `insufficient_quota` (`:539-546`); embeddings/images/rerank/completions/audio handlers and `cmd/gateway/wiring.go` wire it too. Window/concurrency/token/request limits take real runtime effect.

2. **Real payment gateway integrations** (#50) — *Revenue-blocking.*  
   Only manual-admin and test/HMAC providers exist. Without Alipay/WeChat Pay/Stripe, end-users cannot self-service top up. All recharges require admin intervention, making the platform non-autonomous commercially.

3. **Pricing admin write API** (#49) — *Ops friction.*  
   Model prices can only change via DB migration. Operators cannot update pricing at runtime (e.g., responding to upstream cost changes). Competitors (new-api, sub2api) have UI-driven pricing admin.

4. ~~**Spend / low-balance alerts** (#44)~~ — *DONE (no longer missing).*  
   Low-balance alerts are built and wired: `notify.NotifyLowBalance` (`internal/notify/notifier.go:93`) with a configurable `BalanceThreshold` (`:112`) emits the `low_balance` event (`notify/types.go:23`) over email/webhook/bark/gotify, triggered from the production settle path via `notify.NewSettler` (`cmd/gateway/wiring.go:943`).

5. **Quota admin CRUD API** (#32) — *Ops self-service.*  
   Operators cannot create or manage quota policies via API. Only subscription activation indirectly writes quota_policies. No direct per-user, per-key, or global cap management without raw DB access.

6. **User-visible quota status API** (#52) — *Transparency / trust.*  
   HUAKAI's core differentiator is transparency. Users cannot see their own quota limits, current consumption, or window reset time from any API endpoint. This directly contradicts the F-TRUST principle.

7. **Invoice generation / export** (#47) — *Enterprise / compliance.*  
   Enterprise customers and accountants require invoice PDFs or CSV exports for accounting. Absence blocks enterprise sales.

8. **Free tier / trial credits** (#45) — *Top-of-funnel conversion.*  
   No automatic credit on registration. New users must contact admin for any initial balance, creating friction. sub2api and new-api both grant initial credits to convert registrations.

9. **Subscription auto-renewal with payment** (#23) — *Recurring revenue.*  
   Subscriptions expire silently; re-purchase requires user action. No stored-payment-method auto-charge on renewal. Churns paying users unnecessarily.

10. **Provider-account egress quota cap enforcement** (#35) — *Upstream cost control.*  
    `cap_quota_daily/weekly/total` fields exist in `provider_accounts` table but the pool dispatcher never reads them. Accounts can exceed their intended upstream cost caps, creating uncontrolled egress spend.

11. **Multi-currency support** (#51) — *International markets.*  
    Hard-coded USD-only blocks CNY/EUR/GBP markets. Currency code field exists in schema but service rejects all non-USD amounts.

12. ~~**User-initiated refund** (#18)~~ — *DONE (no longer missing).*  
    Admin full-order refund is wired (`paymenthttp/refund.go:38` `newAdminRefundHandler` → `payment/service.go:236` `RefundOrder`, route `paymenthttp/handler.go:203`), and users can file refund requests (`paymenthttp/user_portal.go` `RefundRequestStatus`, pending→approve/reject) that an operator settles via `RefundOrder`.
