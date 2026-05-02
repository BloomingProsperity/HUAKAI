# New API billing, routing, and payment deep dive

Date: 2026-05-02
Reference repo: `.omc/reference-src/new-api`
Snapshot: `main`, commit `dac55f0fdeb1`, tag `v1.0.0-rc.2`
Status: clean
Tracked files: 1907

## Scope

This pass focuses on the parts where New API is materially beyond one-api: guarded request body handling, billing sessions, tiered billing expressions, subscription/wallet funding, channel affinity, auto-group routing, and payment/top-up recovery.

Read files:

- `middleware/gzip.go`
- `common/gin.go`
- `common/body_storage.go`
- `common/disk_cache.go`
- `middleware/body_cleanup.go`
- `service/billing_session.go`
- `service/billing.go`
- `service/pre_consume_quota.go`
- `service/tiered_settle.go`
- `pkg/billingexpr/types.go`
- `pkg/billingexpr/settle.go`
- `pkg/billingexpr/run.go`
- `service/channel_select.go`
- `service/channel_affinity.go`
- `setting/operation_setting/channel_affinity_setting.go`
- `controller/channel_affinity_cache.go`
- `service/channel.go`
- `controller/topup.go`
- `controller/topup_stripe.go`
- `controller/payment_webhook_availability.go`
- `model/topup.go`
- `model/subscription.go`

## Request body, decompression, and reuse

Source-confirmed behavior:

- New API has explicit request decompression middleware for `gzip` and `br`, and wraps decompressed and uncompressed bodies with `http.MaxBytesReader`. Evidence: `.omc/reference-src/new-api/middleware/gzip.go:25`, `.omc/reference-src/new-api/middleware/gzip.go:31`, `.omc/reference-src/new-api/middleware/gzip.go:42`, `.omc/reference-src/new-api/middleware/gzip.go:59`, `.omc/reference-src/new-api/middleware/gzip.go:69`.
- The max request body size is env-configured as `MAX_REQUEST_BODY_MB`, documented as post-decompression protection against zip bombs. Evidence: `.omc/reference-src/new-api/common/init.go:134`, `.omc/reference-src/new-api/common/init.go:136`.
- Reusable request bodies are stored behind a `BodyStorage` interface with read/seek/close/bytes/size/isDisk. Evidence: `.omc/reference-src/new-api/common/body_storage.go:13`.
- Large bodies can spill to disk with exclusive temp files, cleanup on close, and old-file cleanup. Evidence: `.omc/reference-src/new-api/common/body_storage.go:97`, `.omc/reference-src/new-api/common/body_storage.go:181`, `.omc/reference-src/new-api/common/disk_cache.go:36`, `.omc/reference-src/new-api/common/disk_cache.go:100`.
- `GetRequestBody` enforces max body size, caches the storage object in Gin context, and closes the original request body. Evidence: `.omc/reference-src/new-api/common/gin.go:36`, `.omc/reference-src/new-api/common/gin.go:59`, `.omc/reference-src/new-api/common/gin.go:68`, `.omc/reference-src/new-api/common/gin.go:78`.
- Request cleanup middleware closes body storage and cleans file sources after request completion. Evidence: `.omc/reference-src/new-api/middleware/body_cleanup.go:11`, `.omc/reference-src/new-api/middleware/body_cleanup.go:16`.

HUAKAI delta:

- `F-REQ-BODY-001` should use New API, not one-api, as the behavior target: gzip/br decode, decompressed max bytes, reusable body storage, and request-end cleanup.
- Suggested feature IDs:
  - `F-REQ-BODY-001`: max-size guarded decompression for gzip/br/plain bodies.
  - `F-REQ-BODY-002`: reusable request body store with memory/disk threshold and cleanup.
  - `F-REQ-BODY-003`: panic/error paths must return stable errors for too-large bodies without logging full body content.
- Recommended level: L1. This protects the gateway before public traffic.

## Billing session and funding source model

Source-confirmed behavior:

- Billing is wrapped into a per-request `BillingSession` with funding source, actual pre-consumed quota, token consumed quota, extra reserved quota, trusted bypass flag, and idempotency flags. Evidence: `.omc/reference-src/new-api/service/billing_session.go:23`.
- Settlement is guarded by a mutex and `settled`/`fundingSettled` flags. It adjusts the funding source first, then token quota, and records subscription post-delta. Evidence: `.omc/reference-src/new-api/service/billing_session.go:31`, `.omc/reference-src/new-api/service/billing_session.go:42`, `.omc/reference-src/new-api/service/billing_session.go:49`, `.omc/reference-src/new-api/service/billing_session.go:63`.
- Refund returns pre-consumed funding and token quota asynchronously, but refuses once settled/refunded/funding-settled. Evidence: `.omc/reference-src/new-api/service/billing_session.go:71`, `.omc/reference-src/new-api/service/billing_session.go:94`, `.omc/reference-src/new-api/service/billing_session.go:117`.
- Pre-consume has trust-quota bypass for wallet users, but subscription funding cannot use the trust bypass because subscription records must remain consistent. Evidence: `.omc/reference-src/new-api/service/billing_session.go:166`, `.omc/reference-src/new-api/service/billing_session.go:178`, `.omc/reference-src/new-api/service/billing_session.go:187`, `.omc/reference-src/new-api/service/billing_session.go:260`, `.omc/reference-src/new-api/service/billing_session.go:279`.
- Additional reservation can roll back funding if token reservation fails. Evidence: `.omc/reference-src/new-api/service/billing_session.go:147`, `.omc/reference-src/new-api/service/billing_session.go:150`, `.omc/reference-src/new-api/service/billing_session.go:235`.
- Billing preference supports `subscription_only`, `wallet_only`, `wallet_first`, and `subscription_first`, with fallback logic when the preferred source lacks quota. Evidence: `.omc/reference-src/new-api/service/billing_session.go:315`, `.omc/reference-src/new-api/service/billing_session.go:371`, `.omc/reference-src/new-api/service/billing_session.go:376`, `.omc/reference-src/new-api/service/billing_session.go:385`.
- The relay stores the BillingSession on `relayInfo` for later settle/refund. Evidence: `.omc/reference-src/new-api/service/billing.go:17`, `.omc/reference-src/new-api/service/billing.go:29`.
- The older wallet-only pre-consume path remains as compatibility fallback. Evidence: `.omc/reference-src/new-api/service/pre_consume_quota.go:33`, `.omc/reference-src/new-api/service/pre_consume_quota.go:64`, `.omc/reference-src/new-api/service/billing.go:67`.

HUAKAI delta:

- HUAKAI should model "billing session" explicitly, not as scattered quota functions.
- Suggested feature IDs:
  - `F-BILL-SESSION-001`: per-request billing session with idempotent pre-consume, settle, and refund.
  - `F-BILL-SOURCE-001`: wallet/subscription funding source abstraction.
  - `F-BILL-PREF-001`: user billing preference: wallet only, subscription only, wallet first, subscription first.
  - `F-BILL-TRUST-001`: trust quota bypass with explicit ban for subscription funding.
- Recommended level: L2. Basic wallet pre-consume is L1, but subscription-aware billing must be L2 before subscriptions become paid production.

## Tiered billing expressions

Source-confirmed behavior:

- Billing snapshot freezes expression string/hash, group ratio, estimated token/quota/tier, quota-per-unit, and expression version at pre-consume time. Evidence: `.omc/reference-src/new-api/pkg/billingexpr/types.go:36`.
- Actual settlement computes against the frozen snapshot, converts price output to quota, applies group ratio, and records whether actual tier crossed the estimated tier. Evidence: `.omc/reference-src/new-api/pkg/billingexpr/settle.go:19`, `.omc/reference-src/new-api/pkg/billingexpr/settle.go:25`, `.omc/reference-src/new-api/pkg/billingexpr/settle.go:27`.
- Expression runtime exposes token variables, `tier()`, math helpers, request header lookup, request body JSON param lookup, and time helpers. Evidence: `.omc/reference-src/new-api/pkg/billingexpr/run.go:51`, `.omc/reference-src/new-api/pkg/billingexpr/run.go:55`, `.omc/reference-src/new-api/pkg/billingexpr/run.go:71`, `.omc/reference-src/new-api/pkg/billingexpr/run.go:74`, `.omc/reference-src/new-api/pkg/billingexpr/run.go:91`.
- Token parameter construction normalizes cache/image/audio subcategories so separately priced variables are excluded from base prompt/completion tokens. It distinguishes GPT-style and Claude-style usage semantics. Evidence: `.omc/reference-src/new-api/service/tiered_settle.go:12`, `.omc/reference-src/new-api/service/tiered_settle.go:21`, `.omc/reference-src/new-api/service/tiered_settle.go:38`, `.omc/reference-src/new-api/service/tiered_settle.go:46`, `.omc/reference-src/new-api/service/tiered_settle.go:77`.
- If tiered expression settlement fails, it falls back to final pre-consumed quota or estimated quota after group. Evidence: `.omc/reference-src/new-api/service/tiered_settle.go:91`, `.omc/reference-src/new-api/service/tiered_settle.go:106`, `.omc/reference-src/new-api/service/tiered_settle.go:107`.

HUAKAI delta:

- If HUAKAI wants commercial pricing flexibility, copy neither formula nor expression syntax, but do adopt the concept of "frozen billing snapshot".
- Suggested feature IDs:
  - `F-BILL-SNAPSHOT-001`: freeze pricing inputs at pre-consume time.
  - `F-BILL-TIER-001`: tiered billing engine with bounded expression surface or equivalent rule DSL.
  - `F-BILL-TOKEN-NORM-001`: normalize provider usage fields for cache/image/audio/text dimensions.
  - `F-BILL-SETTLE-FALLBACK-001`: settlement fallback when dynamic billing evaluation fails.
- Recommended level: L3 unless dynamic pricing is a launch requirement. `F-BILL-SNAPSHOT-001` should be L2 because it prevents price drift between request start and settlement.

## Routing, auto groups, and channel affinity

Source-confirmed behavior:

- Channel selection accepts a retry parameter and supports `auto` token group selection. For auto groups with cross-group retry, it tracks current group index and retry start index in request context. Evidence: `.omc/reference-src/new-api/service/channel_select.go:14`, `.omc/reference-src/new-api/service/channel_select.go:48`, `.omc/reference-src/new-api/service/channel_select.go:82`, `.omc/reference-src/new-api/service/channel_select.go:91`.
- Auto group retry exhausts priorities in a group before moving to the next group. Evidence: `.omc/reference-src/new-api/service/channel_select.go:99`, `.omc/reference-src/new-api/service/channel_select.go:111`, `.omc/reference-src/new-api/service/channel_select.go:115`, `.omc/reference-src/new-api/service/channel_select.go:126`.
- Satisfy checks can verify whether a channel is enabled for group/model from memory cache or DB, including normalized model names. Evidence: `.omc/reference-src/new-api/model/channel_satisfy.go:8`, `.omc/reference-src/new-api/model/channel_satisfy.go:23`, `.omc/reference-src/new-api/model/channel_satisfy.go:45`.
- Channel affinity rules can extract affinity keys from context, request headers/body via gjson, model/path/user-agent regex, and store preferred channel IDs in Redis or hot memory cache. Evidence: `.omc/reference-src/new-api/service/channel_affinity.go:81`, `.omc/reference-src/new-api/service/channel_affinity.go:289`, `.omc/reference-src/new-api/service/channel_affinity.go:545`, `.omc/reference-src/new-api/service/channel_affinity.go:559`, `.omc/reference-src/new-api/service/channel_affinity.go:607`.
- Affinity can apply parameter override templates, skip retry on failure, record admin-visible reason/key fingerprint, and record successful channel choice with TTL. Evidence: `.omc/reference-src/new-api/service/channel_affinity.go:527`, `.omc/reference-src/new-api/service/channel_affinity.go:591`, `.omc/reference-src/new-api/service/channel_affinity.go:621`, `.omc/reference-src/new-api/service/channel_affinity.go:639`, `.omc/reference-src/new-api/service/channel_affinity.go:676`.
- Default affinity settings include Codex CLI and Claude CLI trace behavior and pass-through header templates. Evidence: `.omc/reference-src/new-api/setting/operation_setting/channel_affinity_setting.go:76`, `.omc/reference-src/new-api/setting/operation_setting/channel_affinity_setting.go:82`, `.omc/reference-src/new-api/setting/operation_setting/channel_affinity_setting.go:97`.
- Operators can inspect and clear channel affinity caches, plus inspect usage-cache stats by rule/group/key fingerprint. Evidence: `.omc/reference-src/new-api/controller/channel_affinity_cache.go:11`, `.omc/reference-src/new-api/controller/channel_affinity_cache.go:20`, `.omc/reference-src/new-api/controller/channel_affinity_cache.go:62`.
- Auto-disable respects per-channel auto-ban, normalized channel errors, skip-retry errors, configured status-code rules, and keyword matching. Evidence: `.omc/reference-src/new-api/service/channel.go:18`, `.omc/reference-src/new-api/service/channel.go:44`, `.omc/reference-src/new-api/service/channel.go:51`, `.omc/reference-src/new-api/service/channel.go:54`, `.omc/reference-src/new-api/service/channel.go:57`.

HUAKAI delta:

- New API's channel affinity is a serious operator feature. HUAKAI should not compress it into generic "sticky session".
- Suggested feature IDs:
  - `F-ROUTE-AUTOGROUP-001`: auto-group fallback with per-group priority exhaustion.
  - `F-ROUTE-AFFINITY-001`: configurable request affinity by model/path/header/body/context.
  - `F-ROUTE-AFFINITY-002`: affinity cache inspection, clear-all, clear-by-rule, and usage/cache-hit stats.
  - `F-ROUTE-OVERRIDE-001`: safe per-route/channel param override templates with audit/admin display.
- Recommended level: L2 for auto-group fallback; L3 for full configurable affinity unless HUAKAI's first customers require Codex/Claude CLI stability.

## Top-up, webhook, and subscription orders

Source-confirmed behavior:

- Top-up info is assembled from enabled payment providers: Epay, Stripe, Creem, Waffo, Waffo Pancake, amount options, discounts, min top-up, and display type. Evidence: `.omc/reference-src/new-api/controller/topup.go:25`, `.omc/reference-src/new-api/controller/topup.go:93`.
- Epay top-up validates min amount, payment method, group ratio, creates trade number, creates a pending top-up order, and returns provider purchase params. Evidence: `.omc/reference-src/new-api/controller/topup.go:180`, `.omc/reference-src/new-api/controller/topup.go:187`, `.omc/reference-src/new-api/controller/topup.go:204`, `.omc/reference-src/new-api/controller/topup.go:209`, `.omc/reference-src/new-api/controller/topup.go:239`.
- Webhook handling uses an in-process per-trade lock, verifies provider callback, acknowledges provider before fulfillment, checks local order existence/provider/status, updates status, increments user quota, and records top-up log. Evidence: `.omc/reference-src/new-api/controller/topup.go:259`, `.omc/reference-src/new-api/controller/topup.go:299`, `.omc/reference-src/new-api/controller/topup.go:342`, `.omc/reference-src/new-api/controller/topup.go:362`, `.omc/reference-src/new-api/controller/topup.go:365`, `.omc/reference-src/new-api/controller/topup.go:390`.
- Stripe top-up validates amount bounds and redirect URLs, creates a checkout session and pending order, verifies webhook signature, handles completed/expired/async success/async failed events, and funnels successful payment through shared fulfillment. Evidence: `.omc/reference-src/new-api/controller/topup_stripe.go:65`, `.omc/reference-src/new-api/controller/topup_stripe.go:79`, `.omc/reference-src/new-api/controller/topup_stripe.go:96`, `.omc/reference-src/new-api/controller/topup_stripe.go:148`, `.omc/reference-src/new-api/controller/topup_stripe.go:177`, `.omc/reference-src/new-api/controller/topup_stripe.go:260`.
- Stripe webhook logs raw body and signature. This is a production-risk negative example, especially if provider payloads contain personal or billing metadata. Evidence: `.omc/reference-src/new-api/controller/topup_stripe.go:156`, `.omc/reference-src/new-api/controller/topup_stripe.go:163`.
- Pending top-up status updates are row-locked and can enforce expected payment provider. Evidence: `.omc/reference-src/new-api/model/topup.go:80`, `.omc/reference-src/new-api/model/topup.go:90`, `.omc/reference-src/new-api/model/topup.go:95`, `.omc/reference-src/new-api/model/topup.go:98`.
- Admin can manually complete pending top-up orders with row lock, idempotent success return, provider-aware quota calculation, DB quota update, and log outside the transaction. Evidence: `.omc/reference-src/new-api/controller/topup.go:482`, `.omc/reference-src/new-api/model/topup.go:313`, `.omc/reference-src/new-api/model/topup.go:328`, `.omc/reference-src/new-api/model/topup.go:335`, `.omc/reference-src/new-api/model/topup.go:366`.
- User top-up history is limited to a 30-day query window; admin top-up list is not time-limited; search uses a hard count limit to reduce DoS risk. Evidence: `.omc/reference-src/new-api/model/topup.go:160`, `.omc/reference-src/new-api/model/topup.go:166`, `.omc/reference-src/new-api/model/topup.go:202`, `.omc/reference-src/new-api/model/topup.go:231`.
- Subscription plans have cached plan/info lookup, duration units, reset periods, max purchase per user, group upgrade, order expiration, and admin binding. Evidence: `.omc/reference-src/new-api/model/subscription.go:17`, `.omc/reference-src/new-api/model/subscription.go:26`, `.omc/reference-src/new-api/model/subscription.go:85`, `.omc/reference-src/new-api/controller/subscription_payment_stripe.go:64`, `.omc/reference-src/new-api/model/subscription.go:619`, `.omc/reference-src/new-api/model/subscription.go:644`.

HUAKAI delta:

- New API is less complete than Sub2API for refund rollback, but stronger than a simple recharge table.
- Suggested feature IDs:
  - `F-PAY-TOPUP-001`: provider-enabled payment method discovery with min amount, amount options, discount, and display type.
  - `F-PAY-TOPUP-002`: pending/success/failed/expired top-up order state machine with provider-pinned updates.
  - `F-PAY-TOPUP-003`: webhook idempotency lock and manual admin completion.
  - `F-PAY-TOPUP-004`: safe webhook logging: signature hash, event id/type, trade no, no raw body.
  - `F-SUB-PLAN-001`: subscription plan duration, quota reset period, max purchase limit, and group upgrade.
- Recommended level: top-up order states and webhook idempotency are L2. Subscription plans are L2 if subscriptions are in the first paid launch, otherwise L3.

## Immediate backlog insertions

1. `F-REQ-BODY-001`: guarded request decompression.
   - Level: L1
   - Acceptance direction: gzip/br/plain requests are capped after decompression; too-large requests produce stable 413-style behavior; body storage is cleaned after request.
2. `F-BILL-SNAPSHOT-001`: frozen billing snapshot.
   - Level: L2
   - Acceptance direction: changing model price after request start cannot change settlement for that request.
3. `F-BILL-SESSION-001`: unified billing session.
   - Level: L2
   - Acceptance direction: pre-consume, settle, and refund are idempotent under retries and streaming failures.
4. `F-ROUTE-AFFINITY-001`: request affinity routing.
   - Level: L3
   - Acceptance direction: same CLI trace key routes to the same account/channel until TTL; operators can clear and inspect cache state.
5. `F-PAY-TOPUP-004`: webhook redaction.
   - Level: L1
   - Acceptance direction: webhook verification failures and success logs never include full raw payload or full signature.

## Open questions

- Need to read New API's actual relay controller to see exactly where `RecordChannelAffinity`, `SettleBilling`, and `ReturnPreConsumedQuota` are invoked in all success/error/streaming paths.
- Need a provider-specific clean-room pass for Stripe, Creem, Waffo, and Epay before copying behavior into HUAKAI specs.
- Need to compare subscription quota reset task and active subscription pre-consume transaction details against HUAKAI's intended ledger model.
- Need frontend pass for channel affinity rule editor, payment method editor, and subscription plan editor.
