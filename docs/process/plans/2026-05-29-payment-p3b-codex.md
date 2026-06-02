=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: specifier
  - specifier = read source files, produce behavior-only summary
  - reviewer  = verify behavior summary WITHOUT re-reading source
  - On the same artifact, lane MUST be a different agent session than
    any prior lane (no same agent doing both lanes on same file).

PRIOR LANES ON THIS ARTIFACT: none

REFERENCE PROJECTS IN SCOPE: CLIProxyAPI + sub2api + new-api
  - sub2api: LGPL/AGPL reference behavior only; no vendoring, no copying.
  - CLIProxyAPI: MIT reference; this payment/subscription commerce domain has no observed equivalent.
  - new-api: reference behavior summary only; no copying.

HARD PROHIBITIONS:
  - NEVER copy function names verbatim
  - NEVER copy struct field names verbatim
  - NEVER copy comments verbatim
  - NEVER do line-by-line algorithmic translation; behaviors must be
    expressed in different sentence structure than upstream code ordering
  - NEVER paste raw upstream code blocks (even small snippets)
  - When upstream uses a distinctive identifier, rename in summary

CITATION POLICY (reconciled 2026-05-10 with CLAUDE.md #12):
  - file:line citations are ALLOWED in prose as evidence anchors -
    `<repo>@<sha>:<file>:<line>` style satisfies #12 per-claim citation
  - the cited identifier itself must NOT appear verbatim in the prose
    surrounding the citation; reference it by paraphrased role only
  - "Source files read" tail block remains required (see below)

ESCALATION: if behavior cannot honestly be summarized without violating
the prohibitions, return a no-op "cannot summarize within clean-room"
rather than violating. Owner prefers a partial gap to a clean-room breach.

=== END CLEAN-ROOM LANE GUARD ===

# 2026-05-29 Payment P3b Codex Independent Plan

| Field | Content |
| --- | --- |
| Owner directive | "P3b = (1)充支付渠道直接买订阅(非净余额扣减) + (2)兑换码购订阅 + (3)到期提醒邮件; 他们有的我们必须得有." |
| Scope | Plan only. Read HUAKAI and reference source, then write this Codex-side independent plan. No implementation, no staging, no commit. |
| In scope | Direct external-payment subscription purchase, subscription voucher grant, expiry reminder email, refund/authorization/cross-tenant risks, slice order, discriminating tests, fusion delta. |
| Out of scope | Editing production code now, changing `internal/billing`, changing `billing_events.actual_cost_signed`, adding files under frozen `internal/gatewayhttp`, `internal/gateway`, or `internal/proto`, vendoring reference code. |
| Success criteria | Owner can approve or reject concrete implementation direction; every P3b feature has a safe HUAKAI implementation path; no feature is silently dropped. |
| Time estimate | Planning: 2-3 hours source read and synthesis. Implementation after approval: 5-7 small PR-sized slices, roughly 3-5 engineering days depending on handler/UI scope. |
| Blast radius | Payment fulfillment, subscription activation, voucher redemption, email delivery, DB migrations, tenant authorization, admin recovery. |
| Required Owner confirmation before implementation | DB migration, payment fulfillment behavior change, refund behavior, and any route wiring that must touch frozen packages. |

Metadata:
- Observed regions: 39
- Inferences: 8
- Open questions: 6
- new-api source caveat: `/home/codex/refs-latest/new-api-fresh` is a local snapshot without `.git` or a stored head SHA. Citations use `QuantumNous/new-api@local-snapshot-no-sha-2026-05-29`; before released-spec status, Owner should attach the exact upstream SHA.

## 1. Current HUAKAI Baseline

HUAKAI payment P1/P2 already has a usable order state machine. `CreateOrder` validates tenant, user, amount, currency, provider, and stable merchant order number before storing a payment order (`backend/internal/payment/service.go:58-120`). `Fulfill` moves through a recoverable in-progress state and then completes (`backend/internal/payment/service.go:147-171`). The Postgres store marks paid orders in progress, returns an idempotent result for completed orders, and retries serialization or uniqueness conflicts (`backend/internal/payment/store_postgres.go:213-283`).

Current payment fulfillment is balance-only. The completion transaction writes one `payment_credits` row, one `billing_events` row with payment-credit semantics, then marks the order complete (`backend/internal/payment/store_postgres.go:285-373`). Migration 0071 confirms `payment_orders` has no purchase kind or plan reference (`backend/sql/migrations/0071_payment_p1.up.sql:16-50`), and `payment_credits` is unique by order (`backend/sql/migrations/0071_payment_p1.up.sql:59-85`). This is the exact place where P3b must branch: top-up continues to credit balance; subscription purchase must not write credit or `billing_events`.

HUAKAI subscription P3a is quota-only and intentionally outside the money ledger. Migration 0073 states subscription grants do not create payment credits or billing events and that plan price is for display or future purchase (`backend/sql/migrations/0073_subscription.up.sql:1-14`). Plans already include price, currency, validity days, group, caps, and sale flags (`backend/sql/migrations/0073_subscription.up.sql:28-49`). User subscriptions carry source, period, plan snapshot, quota caps, and group (`backend/sql/migrations/0073_subscription.up.sql:61-86`). The current store creates subscription, quota policies, group upgrade, links, and audit in one transaction (`backend/internal/subscription/store.go:12-17`; `backend/internal/subscription/store_postgres.go:168-273`).

HUAKAI voucher is currently balance-only. Voucher records and redemptions carry amount fields (`backend/internal/voucher/types.go:43-80`), the service creates amount-bearing codes (`backend/internal/voucher/service.go:43-138`), and redemption writes voucher redemption plus `billing_events(voucher_redeemed)` in one serializable transaction (`backend/internal/voucher/store_postgres.go:184-283`). Migration 0023 also encodes amount-centric vouchers and redemption billing-event linkage (`backend/sql/migrations/0023_voucher_system.up.sql:9-100`; `backend/sql/migrations/0023_voucher_system.up.sql:124-167`).

HUAKAI email infrastructure is sufficient to reuse. Tenant SMTP settings, validation, encrypted password handling, sender construction, transient retry enqueue, and DLQ retry already exist (`backend/internal/email/types.go:12-118`; `backend/internal/email/settings_store.go:27-202`; `backend/internal/email/sender_factory.go:52-139`; `backend/internal/email/dlq_worker.go:37-128`). P3b should add subscription-reminder selection and durable reminder records, not a second SMTP subsystem.

## 2. Reference Observations

sub2api has observed behavior for both wallet-like payment and subscription payment paths. Its payment order logic stores enough purchase-category information to distinguish balance from subscription orders, validates subscription plan and group inputs during order creation, and stores subscription duration and group snapshot on the order (`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_order.go:112-219`). Its payment fulfillment branch sends balance orders through a credit-like path and subscription orders through an entitlement path (`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_fulfillment.go:210-366`). Its subscription assignment behavior extends from the current expiry when active and from now when expired (`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/subscription_service.go:163-251`). It also has a refund path that can shorten or revoke subscription entitlement and has a recovery branch when gateway refund fails after local mutation (`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_refund.go:250-323`; `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_refund.go:421-428`).

sub2api also has subscription-style redeem behavior. Its code redemption flow validates and consumes the code in a DB transaction, then applies either balance or subscription grant according to the code category (`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/redeem_service.go:273-405`). This is evidence that "redeem code buys subscription" is a parity feature, but HUAKAI should implement it through its own voucher tables and subscription transaction helper.

new-api has a separate subscription order structure and direct external-payment checkout/notification paths for subscription purchase (`QuantumNous/new-api@local-snapshot-no-sha-2026-05-29:model/subscription.go:204-219`; `QuantumNous/new-api@local-snapshot-no-sha-2026-05-29:controller/subscription_payment_epay.go:24-168`; `QuantumNous/new-api@local-snapshot-no-sha-2026-05-29:controller/subscription_payment_stripe.go:23-142`). Its subscription order completion path locks the order, validates provider identity, returns idempotently if already successful, creates the subscription snapshot in the same DB transaction, records a reporting entry, and only then marks the order successful (`QuantumNous/new-api@local-snapshot-no-sha-2026-05-29:model/subscription.go:520-629`). It also has a wallet-balance purchase path that deducts internal quota and creates a subscription in one transaction (`QuantumNous/new-api@local-snapshot-no-sha-2026-05-29:model/subscription.go:693-780`); HUAKAI must not copy that money source because Owner explicitly chose direct payment channel purchase, not net spendable balance deduction.

new-api redemption is observed as quota-only, not subscription grant, in the local snapshot (`QuantumNous/new-api@local-snapshot-no-sha-2026-05-29:model/redemption.go:14-27`; `QuantumNous/new-api@local-snapshot-no-sha-2026-05-29:model/redemption.go:115-156`; `QuantumNous/new-api@local-snapshot-no-sha-2026-05-29:controller/redemption.go:62-118`). It does have subscription expiry, downgrade, reset, and usage notification behavior in related subscription and notification code (`QuantumNous/new-api@local-snapshot-no-sha-2026-05-29:model/subscription.go:937-1022`; `QuantumNous/new-api@local-snapshot-no-sha-2026-05-29:service/quota.go:443-545`; `QuantumNous/new-api@local-snapshot-no-sha-2026-05-29:service/user_notify.go:51-115`). I did not observe a persistent expiry-reminder tier ledger in the read regions, so HUAKAI should design its own durable reminder ledger rather than infer one.

CLIProxyAPI has no observed commerce subscription equivalent in the scoped read. Its README describes usage/quota dashboard and management center, not paid subscriptions (`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:README.md:71-95`). Its config sample includes quota-exceeded switching and routing strategy, not purchase orders or vouchers (`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:config.example.yaml:107-115`). Its server route region exposes management, usage, proxy, key, and queue routes, with no observed payment/subscription-commerce route in that region (`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/server.go:580-604`). This is a no-equivalent baseline, not a reason to drop P3b.

## 3. Primary Design Choice: Extend `payment_orders`

Recommendation: extend HUAKAI `payment_orders`, do not create an independent `subscription_orders` order table.

The implementation should add an order category to `payment_orders`, with `topup` as the default for existing P1/P2 behavior and `subscription` for P3b direct plan purchase. Subscription orders should store a plan reference and an immutable purchase snapshot: plan id, displayed price, currency, validity days, granted group, relevant quota caps, and sale/version metadata. Fulfillment must branch exactly once:

- `topup` order: current path remains unchanged; write `payment_credits` and `billing_events(payment_credited)`.
- `subscription` order: activate or extend subscription; write no `payment_credits`; write no `billing_events`; return subscription fulfillment metadata.

Reasoning:

1. HUAKAI already has a payment state machine with provider identity, merchant order number idempotency, paid/in-progress/completed recovery, audit, and serializable retries (`backend/internal/payment/service.go:58-171`; `backend/internal/payment/store_postgres.go:213-373`). Duplicating that in a second order table would create two payment machines.
2. new-api's separate order structure is evidence that subscription orders need distinct fulfillment data, not evidence that HUAKAI must split its table. HUAKAI can get the same product behavior by adding a category plus a subscription effect ledger (`QuantumNous/new-api@local-snapshot-no-sha-2026-05-29:model/subscription.go:204-219`; `QuantumNous/new-api@local-snapshot-no-sha-2026-05-29:model/subscription.go:520-629`).
3. sub2api's observed behavior also separates balance and subscription purchase effects at the order/fulfillment level (`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_order.go:112-219`; `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_fulfillment.go:210-366`).
4. Extending `payment_orders` keeps refund, callback, admin search, and recovery surfaces on one order identity while preserving the Owner's red line: subscription purchase does not mutate net spendable balance or `internal/billing`.

Add a subscription fulfillment effect ledger, not a second order table. The ledger should be analogous to `payment_credits` for top-ups:

- unique by `(tenant_id, payment_order_id)`;
- stores user id, plan id, resulting user subscription id, validity applied, previous expiry if extended, new expiry, and refund/reversal state;
- read on idempotent completed replay;
- used by refund logic to reverse the exact entitlement effect.

This ledger is needed because storing only `fulfilled_subscription_id` on `payment_orders` would make extension/refund recovery weak. If a renewal extends an existing active subscription, refund needs the exact applied duration and previous expiry, not just the subscription id.

## 4. Subscription Activation Transaction Boundary

Core difficulty: payment completion or voucher redemption and subscription activation must be one DB transaction. HUAKAI's current subscription assignment method opens and owns its own transaction (`backend/internal/subscription/store_postgres.go:145-273`), while payment and voucher fulfillment also open their own transactions (`backend/internal/payment/store_postgres.go:263-373`; `backend/internal/voucher/store_postgres.go:184-283`). P3b cannot call the existing subscription service from inside payment/voucher fulfillment because that would create nested independent commits.

Plan:

1. Extract a transaction-bound activation helper inside `internal/subscription`.
2. The helper accepts an existing Postgres transaction plus a compact activation input: tenant, user, source kind, source id, plan snapshot, actor, and activation mode.
3. The helper performs the same tenant/user locking, plan snapshot application, quota-policy installation, group update, link/audit writes, and expiry calculation as P3a, but does not begin or commit the transaction.
4. Payment and voucher stores call this helper from their existing serializable transactions.
5. Memory stores mirror the same behavior for unit tests.

Activation semantics:

- If no active subscription exists for the target group, create a new active subscription.
- If an active subscription exists for the same target group, paid purchase or subscription voucher renews by extending from `max(now, current_expires_at)`, not by silently no-oping. This preserves the user-visible purchase.
- If the new plan's group differs from the current active group, use the existing P3a group-upgrade/downgrade mechanics where applicable; if policy conflict is ambiguous, fail before provider charge for new checkout creation, and fail safely for unpaid voucher redemption.
- If a paid order has already been charged and the plan later becomes disabled, fulfillment should use the immutable order snapshot and complete unless the tenant/user is invalid or authorization fails. Disabled plan should block new order creation, not strand already-paid orders.

Payment idempotency:

- Order creation stores category and subscription snapshot.
- Provider callback/admin-confirm validates provider, amount, currency, order category, and snapshot consistency.
- Fulfillment changes `paid` to in-progress under lock as today.
- Top-up branch writes the existing credit effect and completes.
- Subscription branch checks the subscription effect ledger; if absent, activates subscription and inserts the effect row in the same transaction; then marks the order complete.
- Completed replay reads the effect row and returns the same result.
- A crash before commit leaves no activation; retry re-enters. A crash after commit returns the completed effect. Concurrent callbacks converge through row locks, unique effect row, and serializable retry.

Voucher idempotency:

- Redemption keeps the current tenant/code/idempotency-key guard.
- Balance voucher continues to write voucher redemption plus `billing_events(voucher_redeemed)`.
- Subscription voucher writes voucher redemption plus subscription activation in the same transaction; it must not write a billing event.
- Reusing the same idempotency key returns the original result only when tenant, user, voucher, grant kind, and target plan match; mismatch fails.

## 5. Voucher Grant Model

Recommendation: add a voucher grant category with two values:

- `balance`: current behavior, amount required, billing event required.
- `subscription`: plan reference required, amount absent, subscription activation required, no billing event.

Schema direction:

- Add grant category and optional plan reference to voucher batch and voucher records.
- Extend voucher redemption records with grant category, optional amount, optional plan reference, optional user subscription id, and nullable billing event id.
- Add constraints so balance redemptions require amount and billing event, while subscription redemptions require plan/subscription linkage and forbid billing event linkage.
- Preserve existing amount vouchers as `balance` by default in migration.

Service/API direction:

- Admin voucher creation must choose one grant category.
- Subscription voucher creation validates plan exists, is enabled, belongs to tenant, and is sale/grant eligible.
- Redeem response returns either balance result or subscription result; do not overload balance fields with fake zero values.
- Existing balance voucher tests must continue to pass unchanged.

This implements sub2api's observed subscription-code capability as a HUAKAI-native voucher grant type without copying its table layout or service structure (`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/redeem_service.go:273-405`).

## 6. Expiry Reminder Email

Recommendation: add a subscription reminder worker and durable reminder delivery ledger; reuse `internal/email`.

Cadence and tiers:

- Default reminder offsets: 7 days, 3 days, 1 day, and day-of-expiry.
- Worker cadence: hourly by default, configurable.
- Scan active subscriptions with `expires_at` within the maximum offset window.
- For each due threshold, claim a delivery row with unique `(tenant_id, user_subscription_id, reminder_key)`.
- Send only when tenant setting enables reminders and a usable recipient email exists.

Settings:

- Add tenant settings using the existing email/settings mechanism: reminder enabled flag, offset list, from/display defaults if needed, and optional quiet-hour policy.
- Proposed default: feature enabled only when SMTP settings validate for the tenant, otherwise worker records skipped or configuration-missing status. This avoids filling DLQ for tenants that never configured email.

Failure and retry:

- Delivery ledger statuses: claimed/queued, sent, retryable_failed, permanent_failed, skipped_config, skipped_no_recipient.
- Transient SMTP failures should use the existing email retry/DLQ path (`backend/internal/email/sender_factory.go:120-139`; `backend/internal/email/dlq_worker.go:71-128`).
- Successful delivery stores sent timestamp and provider/message metadata if available.
- Re-running the worker after a sent row exists must not send the same tier again.

Truth-first caveat: SMTP cannot guarantee exactly-once user inbox delivery if the process crashes after the remote server accepts mail but before HUAKAI records success. HUAKAI can guarantee durable internal de-duplication before retry and no duplicate after a successful ledger update. The residual crash window should be documented in the ops runbook.

## 7. Refund, Authorization, and Cross-Tenant Rules

Refunds:

- Top-up refund behavior is out of P3b unless P1/P2 already owns it.
- Subscription order refund must not touch `payment_credits`, `billing_events.actual_cost_signed`, or `internal/billing`.
- Use a refund state machine on the payment order/effect ledger: requested, provider_refunding, provider_refunded_local_pending, completed, failed_manual_review.
- Safer sequence: record refund intent, call provider once with idempotency key, then reverse local subscription entitlement in a retryable DB transaction. If provider succeeds but local reversal fails, keep local pending state for operator retry rather than issuing a second provider refund.
- Reversal uses the subscription effect ledger. If the purchase created the active subscription and no later entitlement depends on it, cancel it. If the purchase extended an existing subscription, subtract the applied duration or restore the recorded previous expiry, then run existing downgrade/expiry mechanics if the result is expired.
- Voucher subscription redemption has no cash refund. Admin cancellation of the resulting subscription can be a separate support action, but the voucher redemption record remains append-only.

Authorization and tenant isolation:

- User checkout can only create orders for self within tenant.
- Admin purchase or grant actions require admin scope and explicit tenant.
- Provider callbacks locate tenant/order from stored order identity and provider metadata; they must not trust callback tenant/user parameters.
- Every order, voucher, redemption, subscription, effect ledger, and reminder query must include tenant id.
- Cross-tenant order fulfillment, voucher redemption, refund, and reminder sending are S1 test cases.
- Disabled plan blocks new checkout and new voucher creation; in-flight paid orders use their order snapshot to avoid paid-without-service.

Security/abuse:

- Keep stable provider order identifiers; reject amount/currency/provider mismatch before activation.
- Rate-limit voucher redeem attempts as current voucher flow permits; preserve max-redemption and eligible-user checks.
- Do not include sensitive SMTP settings or provider secrets in audit payloads.
- Reminder emails must not expose admin-only quota internals; include plan name, expiry date, and renewal link only.

## 8. Slice Order

P3b should land as small closed slices. Each slice must run local tests and a per-commit Codex review before commit.

1. P3b-0 specification and Owner approval
   - Finalize this plan, write/adjust product spec and acceptance matrix.
   - Owner confirms DB migration/payment/refund direction.
   - No code behavior change.

2. P3b-1 payment order category and effect ledger
   - Add migration for order category, subscription snapshot columns, and subscription fulfillment effect ledger.
   - Preserve existing top-up defaults.
   - Tests: old orders migrate to top-up; subscription order cannot have credit effect; invalid category/plan constraints fail.

3. P3b-2 transaction-bound subscription activation
   - Add activation helper in `internal/subscription`.
   - Support create-or-renew semantics and return previous/new expiry data.
   - Tests: create, renew active, expired base-from-now, group conflict, tenant mismatch.

4. P3b-3 direct payment subscription fulfillment
   - Extend payment create/fulfill inputs and store branch.
   - Subscription fulfillment writes effect ledger and subscription; no balance credit and no billing event.
   - Provider/admin confirm paths validate amount/currency/provider and return idempotent subscription result.

5. P3b-4 subscription voucher grant
   - Add voucher grant category and redemption shape.
   - Balance vouchers remain backward-compatible.
   - Subscription voucher redemption uses the same activation helper and no billing event.

6. P3b-5 expiry reminder worker
   - Add delivery ledger, settings integration, worker, and email sender adapter.
   - Use existing email DLQ for retryable failures.
   - Tests cover tier de-duplication, disabled setting, missing recipient, transient failure, and sent replay.

7. P3b-6 refund and admin recovery
   - Add subscription-order refund state and local entitlement reversal.
   - Add admin retry view/service hooks without adding files to frozen packages.
   - Tests cover provider success/local retry, provider failure/no reversal, idempotent refund replay.

8. P3b-7 API/UI/ops wiring and release gate
   - Wire handlers with no new files in frozen packages; if unavoidable, modify existing files minimally or create non-frozen package plus a small existing-file registration.
   - Update docs, run acceptance tests, and perform cross-review.

## 9. File Scope and Package Discipline

Allowed implementation areas after Owner approval:

- `backend/internal/payment`: existing files plus new non-frozen files if needed; owns order category, payment fulfillment branch, subscription effect ledger, refund state.
- `backend/internal/subscription`: activation helper, renewal semantics, reminder worker if subscription-owned.
- `backend/internal/voucher`: grant category and subscription redemption.
- `backend/internal/email`: small generic tenant email sending adapter if existing auth sender cannot send subscription reminders cleanly.
- `backend/sql/migrations`: next migration after 0073 for P3b schema.
- `docs/`: specs, acceptance tests, risk notes, release gates.

Forbidden or high-risk areas:

- Do not modify `LICENSE`.
- Do not touch production secrets.
- Do not touch `internal/billing` or net spendable balance logic.
- Do not add files under frozen `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`.
- Database migrations, payment fulfillment changes, and refund changes require Owner confirmation before execution.

## 10. Mutation-Discriminating Test Matrix

Payment direct subscription:

- Subscription order completion creates/extends subscription and writes exactly zero `payment_credits`.
- Subscription order completion writes no `billing_events(payment_credited)`.
- Top-up order completion still writes exactly one credit and one payment billing event.
- Mutant "ignore order category and always top-up" must fail because subscription remains absent and balance changes.
- Mutant "activate subscription and also credit balance" must fail because credit/event count is nonzero.
- Replayed callback after completed order returns the same subscription result without extending again.
- Concurrent callbacks produce one effect row and one entitlement delta.
- In-progress crash simulation before commit retries to one activation; after commit replays completed result.
- Provider amount/currency/provider mismatch leaves order unfulfilled and subscription absent.
- Disabled plan after order creation still fulfills from snapshot; disabled plan before order creation fails.

Voucher subscription:

- Balance voucher behavior remains unchanged and writes voucher billing event.
- Subscription voucher redemption creates/extends subscription and writes no voucher billing event.
- Mutant "use amount-only redemption path for subscription voucher" must fail because no subscription exists.
- Same idempotency key with same voucher returns same result; same key with different voucher or grant kind fails.
- Max-redemption and eligible-user limits apply to subscription vouchers.
- Cross-tenant code redemption fails without side effects.

Subscription activation:

- No active subscription creates a new active row and quota policy links.
- Active same-group subscription renews from current expiry, not from now and not no-op.
- Expired same-group subscription renews from now.
- Ambiguous plan/group conflict fails before side effects.
- Group downgrade/cancel path after refund does not leave user in upgraded group.

Reminders:

- A subscription entering 7d/3d/1d/day-of windows queues one delivery per tier.
- Re-running worker after sent row does not send again.
- Disabled tenant setting produces no send.
- Missing recipient produces skipped record, not DLQ spam.
- Transient SMTP failure enters retry/DLQ path.
- Permanent email failure marks permanent failure and does not retry forever.
- Cross-tenant reminder scan cannot send tenant A subscription to tenant B settings.

Refund/admin recovery:

- Provider refund failure leaves subscription unchanged and order recoverable.
- Provider success plus local reversal failure records local-pending state and retry reverses once.
- Refund of a created subscription cancels it and downgrades group if needed.
- Refund of an extension restores or subtracts the exact applied term.
- Replayed refund does not call provider twice and does not subtract twice.

## 11. Fusion Delta

Architecture delta:

- HUAKAI should fuse reference behavior into one existing payment-order state machine plus effect-ledger branches. This preserves new-api/sub2api user outcomes while fitting HUAKAI's P1/P2 order infrastructure.
- Subscription activation becomes a transaction-bound capability reused by payment and voucher, rather than a nested service call.
- Reminder email reuses HUAKAI's tenant SMTP/DLQ infrastructure instead of building a parallel mail stack.

Algorithm delta:

- Exactly one purchase effect is enforced by order category, unique effect rows, serializable transaction retry, and replay reads.
- Paid subscription renewal extends active entitlement from current expiry, preventing paid no-op.
- Reminder de-duplication is persistent by subscription and tier, so scheduler retries do not duplicate successful sends.

Ecosystem delta:

- sub2api contributes evidence that payment orders and redeem codes can grant subscriptions.
- new-api contributes evidence for direct external-payment subscription checkout and transactional completion.
- CLIProxyAPI has no commerce equivalent; its absence should be recorded as no-equivalent, not used to reduce P3b scope.
- HUAKAI improves the combined outcome by keeping spendable-balance ledger separate from subscription purchase, adding explicit refund recovery state, and adding durable expiry-reminder de-duplication.

## 12. Failure Modes and Mitigations

| Failure mode | Mitigation |
| --- | --- |
| Paid order completes but subscription is not opened | Single serializable transaction writes activation, effect ledger, and order completion together. |
| Subscription opens twice on duplicate callback | Unique effect row by order, completed replay reads existing result, concurrent callback retry. |
| Subscription purchase also credits balance | Order-category branch tests and DB constraints; subscription branch has no credit/billing-event write. |
| Voucher subscription writes fake zero balance | Grant-category constraints; subscription redemption has nullable amount and no billing event. |
| Paid renewal silently no-ops because active group already exists | Activation mode must renew/extend active entitlement and store applied delta. |
| Refund provider succeeds but local reversal fails | Persistent local-pending state and idempotent operator retry; no second provider call. |
| Reminder sends duplicate emails | Unique delivery row per subscription/tier, sent-state replay, DLQ tied to delivery key. |
| Reminder never sends after transient SMTP failure | Existing email DLQ/retry path plus retryable delivery status. |
| Cross-tenant leakage | Tenant id on every query and unique key; cross-tenant tests in every touched subsystem. |
| Frozen package growth | No new files in frozen packages; route wiring via existing files or non-frozen package. |

## 13. Decision Points for Owner

1. Approve extending `payment_orders` instead of creating separate `subscription_orders`.
2. Approve adding a subscription fulfillment effect ledger.
3. Approve paid renewal semantics: active same-group purchase extends entitlement rather than no-op.
4. Decide whether different-plan same-group purchase should be allowed as renewal, treated as upgrade, or rejected until a dedicated upgrade spec.
5. Approve refund sequence: provider-first with local-pending recovery, versus local-reverse-first with rollback attempt.
6. Approve reminder default: enabled only when tenant SMTP validates, versus globally enabled with configuration-missing records.

## 14. Pre-Execution Checklist

Before implementation:

1. Owner approves synthesized P3b plan.
2. Confirm current branch and migration number after 0073.
3. Re-check package budgets for `payment`, `subscription`, `voucher`, and `email`.
4. Verify no required handler file would add a file under frozen packages.
5. Write acceptance tests first for top-up unchanged, subscription order no-credit, voucher no-billing-event, and reminder de-duplication.
6. Implement one slice at a time; run targeted tests.
7. Stage only intended diff; run `codex exec review --uncommitted --full-auto --sandbox read-only` before each commit.
8. Record any S2/S3 deferred review findings; fix S0/S1 before commit.

## Open Questions

1. Should P3b expose subscription checkout through existing payment APIs only, or add subscription-specific API paths for UX clarity?
2. What exact provider set must support subscription purchase in the first slice: manual admin confirm only, current provider callbacks, or Stripe-like checkout?
3. Should same-group but different-cap plan purchase merge caps, replace caps, or reject until upgrade policy is specified?
4. Do reminder emails need localization/templates in P3b, or is plain tenant-branded text acceptable for first release?
5. Should voucher subscription grants be sale-plan only or allow admin-only hidden plans?
6. What exact user email source should reminder worker use if tenant has multiple identity/email fields?

Source files read:
- HUAKAI: `backend/internal/payment/types.go`; `backend/internal/payment/service.go`; `backend/internal/payment/store.go`; `backend/internal/payment/store_postgres.go`; `backend/sql/migrations/0071_payment_p1.up.sql`
- HUAKAI: `backend/internal/subscription/types.go`; `backend/internal/subscription/store.go`; `backend/internal/subscription/service.go`; `backend/internal/subscription/store_postgres.go`; `backend/internal/subscription/worker.go`; `backend/sql/migrations/0073_subscription.up.sql`
- HUAKAI: `backend/internal/voucher/types.go`; `backend/internal/voucher/store.go`; `backend/internal/voucher/service.go`; `backend/internal/voucher/store_postgres.go`; `backend/sql/migrations/0023_voucher_system.up.sql`
- HUAKAI: `backend/internal/email/types.go`; `backend/internal/email/smtp_sender.go`; `backend/internal/email/settings_store.go`; `backend/internal/email/sender_factory.go`; `backend/internal/email/dlq_worker.go`; `backend/sql/migrations/0025_email_settings.up.sql`
- sub2api: `backend/internal/service/payment_order.go`; `backend/internal/service/payment_fulfillment.go`; `backend/internal/service/payment_refund.go`; `backend/internal/service/redeem_service.go`; `backend/internal/service/subscription_service.go`; `backend/internal/service/subscription_expiry_service.go`
- new-api: `model/subscription.go`; `controller/subscription.go`; `controller/subscription_payment_epay.go`; `controller/subscription_payment_stripe.go`; `model/redemption.go`; `controller/redemption.go`; `service/quota.go`; `service/user_notify.go`; `common/email.go`
- CLIProxyAPI: `README.md`; `config.example.yaml`; `internal/api/server.go`
Lane: specifier
Agent: GPT-5 Codex, codex parallel planner
UTC timestamp: 2026-05-29T08:52:23Z
