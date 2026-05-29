# 2026-05-29 Payment P3 Subscription Implementation Plan

> For agentic workers executing this plan: this is a plan-only artifact. Do not implement until the Owner has approved the synthesized plan and the migration gate.

| Field | Content |
| --- | --- |
| Owner directive | “为 HUAKAI 支付子系统切片 P3「订阅 (subscription)」写一份独立实施计划文档。仅写计划, 不写实现代码, 不 commit, 不 push, 不读任何 Claude 计划稿。” |
| Product decision already made | 订阅授予余额走 `billing_events` 入账路径；配额复用 `internal/quota` 多层策略；到期 worker 降级并保留购买前分组。 |
| Scope | Plan for P3 subscription model, user subscription lifecycle, grant/accounting path, quota policy integration, expiry/reset/renewal worker, HTTP/package placement, migration 0072, and mutation-discriminating tests. |
| Out of scope | Implementation code, commits, pushes, production rollout, provider-specific webhook rewrites, `internal/billing` changes, frozen package growth, and reading any Claude plan draft. |
| Success criteria | Owner can approve or edit one implementation path; every reference-observed shape has an explicit HUAKAI disposition; migration and money-path risks are Owner-gated; tests are mutation-discriminating; no feature is silently dropped. |
| Time estimate | Plan: this Codex session. Implementation estimate after approval: 2-3 focused engineering sessions plus DB review; PG integration tests may add time depending on local DB availability. |
| Blast radius | Payment balance correctness, append-only billing ledger constraints, quota policy behavior, tenant isolation, user entitlement/group restore, gateway lifecycle worker startup/shutdown, and admin/user subscription APIs. |
| Failure modes | Double credit, missing credit, granting on failed payment, cross-tenant grant/policy leakage, expiry not downgrading, expiry downgrading while another upgraded subscription is active, reset losing quota history, worker overlap, partial DB commit, and tests that pass without guarding the intended defect. |
| Decision points needing Owner sign-off | Migration 0072; any change to `payment_credits` or `billing_events` constraints; exact user group/entitlement storage; whether existing subscriptions keep plan snapshots or follow current plan changes; subscription HTTP surface; lifecycle worker schedule; admin manual grant permissions. |
| Pre-execution checklist | Confirm no Claude plan draft is consulted; re-run package budget check; stage migration design for Owner gate; run existing payment/quota tests before and after refactor; write failing PG tests before implementation; run per-commit Codex review before commit. |

## Truth-First Metadata

- Observed source regions: HUAKAI payment/quota/lifecycle/migration/test regions plus local reference mirrors listed in the tail block.
- Inferences: HUAKAI fit decisions are marked as “HUAKAI fit” and are derived from observed local seams plus observed reference behavior.
- Open questions: 9 Owner decisions are listed near the end.
- Clean-room stance: reference projects are evidence only. The implementation plan uses HUAKAI-owned package boundaries, DB names, and accounting seams; it does not copy upstream source, comments, object layouts, or algorithm order.

## Executive Shape

P3 should be implemented as a new subscription domain around two existing HUAKAI-owned primitives:

1. **Balance grant primitive**: a single trusted payment credit writer that records a subscription grant as a positive balance fact and its paired `billing_events` row in one serializable transaction. P1 already credits paid orders through `payment_credits` plus `billing_events(payment_credited)` and derives balance from `payment_credits`, so P3 should not create a second money ledger or write `internal/billing` directly (`backend/internal/payment/store_postgres.go:285`, `backend/internal/payment/store_postgres.go:402`, `backend/sql/migrations/0071_payment_p1.up.sql:59`, `backend/sql/migrations/0071_payment_p1.up.sql:116`).
2. **Quota entitlement primitive**: subscription plans instantiate or retire `internal/quota` policies instead of adding another rate/limit engine. The quota engine already supports tenant-scoped policy resolution, multiple scopes, multiple metrics, observe/enforce modes, windowing, reservations, settlement, and integration tests for strictest-scope behavior (`backend/internal/quota/types.go:9`, `backend/internal/quota/policy.go:32`, `backend/internal/quota/service.go:65`, `backend/internal/quota/service_integration_test.go:101`).

The implementation should create `backend/internal/subscription` and `backend/internal/subscriptionhttp`. Do not add files to frozen packages `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`.

## Reference Shape Inventory

### CLIProxyAPI no-equivalent note

CLIProxyAPI is a pure relay account-to-API proxy for this slice. Local reference grep found no payment, billing, or subscription domain file paths; the subscription-like hits are credential/account metadata and a relay-attribution header check, not a plan/subscription/billing subsystem (`CLIProxyAPI@21fad9db:internal/auth/codex/jwt_parser.go:40`, `CLIProxyAPI@21fad9db:internal/auth/codex/filename.go:9`, `CLIProxyAPI@21fad9db:internal/util/claude_attribution.go:8`). HUAKAI disposition: **no equivalent reference behavior**; P3 parity is measured against sub2api and new-api for subscription shape.

### Plan model

| Mirror | Observed shape | Modes/states | HUAKAI P3 disposition |
| --- | --- | --- | --- |
| sub2api | Plan definitions carry a group binding, display metadata, sale visibility, ordering, price values, validity duration/unit, feature text, and timestamps. Validation rejects missing group, non-positive price, and invalid duration before creation or update (`sub2api@91da81599373:backend/ent/schema/subscription_plan.go:31`, `sub2api@91da81599373:backend/internal/service/payment_config_plans.go:14`, `sub2api@91da81599373:backend/internal/service/payment_config_plans.go:62`). | Admin create/update/delete; list all; list sale-ready; delete is blocked when there are pending orders (`sub2api@91da81599373:backend/internal/service/payment_config_plans.go:115`, `sub2api@91da81599373:backend/internal/service/payment_config_plans.go:141`, `sub2api@91da81599373:backend/internal/service/payment_config_plans.go:182`). | Implemented Better: plan table with tenant scope, display fields, currency/price, duration, sale flag, sort, grant snapshot, quota template, optional entitlement group, and provider-independent product metadata. Delete should be soft-disable while active/pending dependencies exist. |
| new-api | Plan definitions include display text, money/currency, duration, provider checkout references, purchase cap, optional group upgrade, total included amount, reset cadence/custom duration, enable flag, order, and timestamps (`new-api@20d3e7373452:model/subscription.go:144`, `new-api@20d3e7373452:model/main.go:383`). | User sees enabled plans; admin creates and updates; controllers validate positive money and duration, allowed currency, optional reset settings, and provider metadata (`new-api@20d3e7373452:controller/subscription.go:27`, `new-api@20d3e7373452:controller/subscription.go:97`, `new-api@20d3e7373452:controller/subscription.go:178`). | Implemented Better: provider-specific checkout IDs remain optional metadata; actual purchase flows should reuse P1/P2 payment order and webhook intake. Plan reset cadence becomes quota policy period plus optional balance grant recurrence. |
| CLIProxyAPI | No plan domain equivalent. | No plan states. | No-equivalent footnote only; no HUAKAI feature may be dropped because CLIProxyAPI lacks it. |

### User subscription instance model

| Mirror | Observed shape | Modes/states | HUAKAI P3 disposition |
| --- | --- | --- | --- |
| sub2api | User instance binds user to a group, records start/end, status, window anchors, daily/weekly/monthly usage counters, assignment metadata, and notes (`sub2api@91da81599373:backend/internal/service/user_subscription.go:5`, `sub2api@91da81599373:backend/internal/repository/user_subscription_repo.go:22`). Repository reads active rows by user/group with status and expiry predicates (`sub2api@91da81599373:backend/internal/repository/user_subscription_repo.go:75`). | Direct assign, assign-or-extend, bulk assign, revoke/delete, extend, list active/all, cached active lookup, admin reset, and display normalization of expired rows (`sub2api@91da81599373:backend/internal/service/subscription_service.go:145`, `sub2api@91da81599373:backend/internal/service/subscription_service.go:169`, `sub2api@91da81599373:backend/internal/service/subscription_service.go:381`, `sub2api@91da81599373:backend/internal/service/subscription_service.go:657`, `sub2api@91da81599373:backend/internal/service/subscription_service.go:702`). | Implemented Better: `user_subscriptions` should be tenant-first, user-bound, plan-snapshot-based, statused, period-aware, and idempotent. It should support admin/manual activation and paid activation, but all money grants must pass one local credit seam. |
| new-api | User instance snapshots plan, total allowance, used amount, active window, reset cursor, source, optional group upgrade, and previous group value (`new-api@20d3e7373452:model/subscription.go:233`). Creation from a plan enforces purchase cap, can snapshot/upgrade group, and stores period/reset information in one transaction (`new-api@20d3e7373452:model/subscription.go:438`). | Active/all listing, admin bind, invalidate/delete, payment completion, due expiry, due reset, pre-consume, refund, and post-consume adjustment paths (`new-api@20d3e7373452:model/subscription.go:511`, `new-api@20d3e7373452:model/subscription.go:644`, `new-api@20d3e7373452:model/subscription.go:667`, `new-api@20d3e7373452:model/subscription.go:728`, `new-api@20d3e7373452:model/subscription.go:822`, `new-api@20d3e7373452:model/subscription.go:933`, `new-api@20d3e7373452:model/subscription.go:969`). | Safe Equivalent plus upgrade: HUAKAI should not create another consumption counter inside subscription when `internal/quota` already owns reservations/settlement. Store subscription state and plan snapshot; instantiate quota policies for consumption limits. |
| CLIProxyAPI | No subscription instance domain equivalent. | No lifecycle states. | No-equivalent footnote only. |

### Grant/accounting mechanism

| Mirror | Observed behavior | HUAKAI fit |
| --- | --- | --- |
| sub2api | Subscription mode gates group access and tracks periodic usage; gateway billing tests show subscription-mode consumption is separate from ordinary balance charging (`sub2api@91da81599373:backend/internal/service/gateway_service_subscription_billing_test.go:19`, `sub2api@91da81599373:backend/internal/service/gateway_service_subscription_billing_test.go:61`). | HUAKAI should not copy usage-counter accounting. Use `internal/quota` for limits and `payment_credits` plus `billing_events` for any balance grant. |
| new-api | Payment completion locks the order, checks provider result, treats already-successful completion as idempotent, creates a subscription instance, records a top-up-like audit fact, marks the order successful, and logs the event inside the completion path (`new-api@20d3e7373452:model/subscription.go:511`). Admin bind creates an instance without external payment (`new-api@20d3e7373452:model/subscription.go:644`). | Reuse P1/P2 payment order and webhook intake for external payment. Subscription activation should observe a completed HUAKAI payment order, create the subscription instance, and then call the same trusted credit writer used by P1. Admin activation uses the same subscription grant transaction without provider verification. |
| CLIProxyAPI | No grant/accounting equivalent. | No-equivalent. |

### Expiry, reset, renewal, and worker shape

| Mirror | Observed behavior | HUAKAI fit |
| --- | --- | --- |
| sub2api | Non-expired renewal extends from current expiry; expired renewal restarts at current time; maximum expiry is capped; expired reset shifts the active window and clears period counters; maintenance queue runs activation/reset work and a separate expiry service marks expired rows in batches (`sub2api@91da81599373:backend/internal/service/subscription_service.go:169`, `sub2api@91da81599373:backend/internal/service/subscription_service.go:253`, `sub2api@91da81599373:backend/internal/service/subscription_service.go:793`, `sub2api@91da81599373:backend/internal/service/subscription_maintenance_queue.go:19`, `sub2api@91da81599373:backend/internal/service/subscription_expiry_service.go:27`). | Implement active-renewal stacking and expired-renewal restart. Avoid direct quota counter mutation by creating new policy periods and grant records. Worker should be batchable, idempotent, and safe under restart. |
| new-api | A reset task is master-only, anti-overlap, and processes expiry, reset, and cleanup batches (`new-api@20d3e7373452:service/subscription_reset_task.go:29`). Expiry marks due subscriptions and downgrades group only when no newer active upgraded subscription remains (`new-api@20d3e7373452:model/subscription.go:822`). Reset advances the period cursor and clears period usage (`new-api@20d3e7373452:model/subscription.go:933`). | Implement a single HUAKAI worker with DB locking or lease semantics. Expiry must restore the pre-purchase group only if no later active subscription still owns an upgrade. Reset creates next-period grants/policies idempotently instead of resetting a second quota counter. |
| CLIProxyAPI | No worker equivalent. | No-equivalent. |

## HUAKAI Current Shape And Constraints

- P1 payment orders are tenant-scoped, use caller-stable external order IDs, and separate paid confirmation from fulfillment (`backend/internal/payment/service.go:58`, `backend/internal/payment/service.go:122`, `backend/internal/payment/service.go:147`).
- P1 fulfillment is already retry-aware and uses serializable DB work to create exactly one credit, one billing event, and an audit event for a paid order (`backend/internal/payment/store_postgres.go:213`, `backend/internal/payment/store_postgres.go:285`, `backend/internal/payment/store_postgres.go:465`).
- P2a webhook intake verifies provider payload, re-loads the local tenant-scoped order, validates amount/currency/provider, then calls the existing confirm-and-fulfill service (`backend/internal/payment/webhook.go:10`, `backend/internal/payment/webhook.go:28`, `backend/internal/payment/webhook.go:50`).
- `payment_credits` currently has a one-to-one order origin. P3 subscription grants need a new origin shape, so migration 0072 is high-risk and Owner-gated (`backend/sql/migrations/0071_payment_p1.up.sql:59`).
- `billing_events` has append-only protections and payment/voucher branches. P3 must extend this deliberately rather than bypassing it (`backend/sql/migrations/0002_observability_billing.up.sql:93`, `backend/sql/migrations/0023_voucher_system.up.sql:124`, `backend/sql/migrations/0039_money_path_append_only_triggers.up.sql:8`).
- `internal/quota` is a reusable engine with existing PG tests for strict scope precedence, request/cost caps, and observe mode; P3 should install policies rather than duplicate counters (`backend/internal/quota/service_integration_test.go:18`, `backend/internal/quota/service_integration_test.go:64`, `backend/internal/quota/service_integration_test.go:136`).
- Current gateway wiring already mounts payment HTTP outside frozen `gatewayhttp`, and lifecycle wiring starts/stops other workers in `backend/cmd/gateway` (`backend/cmd/gateway/routes.go:92`, `backend/cmd/gateway/routes.go:412`, `backend/cmd/gateway/lifecycle.go:26`, `backend/cmd/gateway/wiring.go:439`).
- Package budget check: `internal/payment` is under package file budget but contains a >500-line DB file; `internal/quota` is under package file budget but has large service/store files; frozen packages remain `gatewayhttp`, `gateway`, and `proto`. P3 should create new packages and avoid growing large existing files except for small interface/wiring edits.

## Recommended Architecture

### Domain packages

Create:

- `backend/internal/subscription`: plan model, user subscription state machine, grant orchestration, quota policy instantiation, worker, store interfaces, PG store, and test helpers.
- `backend/internal/subscriptionhttp`: user/admin handlers for plans, current subscription, activation/purchase intent lookup, manual bind, invalidate/cancel, and worker-visible admin operations if required.

Modify only as needed:

- `backend/internal/payment`: expose or extract a small internal credit writer that owns `payment_credits` + `billing_events` writes. Keep P1 fulfillment and subscription grants on this one path.
- `backend/cmd/gateway/routes.go`: mount subscription HTTP routes outside frozen packages.
- `backend/cmd/gateway/wiring.go`: construct subscription service after payment/quota stores are available.
- `backend/cmd/gateway/lifecycle.go`: add the subscription worker to runtime startup/shutdown. This is a serial coordination point with any other lifecycle edits; do not parallel-edit this file in another implementation slice without reconciliation.

Do not touch:

- `backend/internal/gatewayhttp`
- `backend/internal/gateway`
- `backend/internal/proto`
- `backend/internal/billing`
- production secrets
- `LICENSE`

### Core data flow

1. Admin creates or enables a subscription plan with price/currency, billing period, grant amount, quota template, optional entitlement group, and provider metadata.
2. User starts purchase through the existing P1 payment order path. The subscription intent stores `payment_order_id`, plan snapshot, and idempotency key.
3. P2a webhook confirms and fulfills the payment order exactly as it does today. Subscription activation then observes the completed order and creates the user subscription and first grant. The activation must not verify external provider payload itself.
4. Subscription grant creation inserts one deterministic grant record for `(tenant, subscription, period start, grant kind)`, calls the payment credit writer if the plan grants balance, and installs quota policies for the period.
5. Renewal before expiry extends from the current expiry; renewal after expiry starts from now. The renewed period creates a new deterministic grant/policy set.
6. Worker processes due expiry and reset. Expiry closes subscription-owned policies and restores the pre-purchase group only if no newer active upgraded subscription remains. Reset creates the next period grant/policy set exactly once.

### Single trusted accounting point

Implementation should refactor P1 and P3 toward this shape:

- A payment-owned credit writer takes tenant, user, amount, currency, source kind, source ID, idempotency key, audit actor, and metadata.
- The writer runs one serializable transaction and inserts the credit fact, its paired `billing_events` row, and audit event.
- P1 fulfillment calls the writer with payment-order origin.
- P3 grant calls the writer with subscription-grant origin.
- Balance remains derived from `payment_credits` only.

This is a schema and money-path change. It requires Owner approval before implementation.

### Quota policy integration

Plan quota templates should map into existing `quota_policies` rows:

- Scope: at minimum tenant + user; optionally API key, pool group, or model/pool dimension if the approved quota template includes it.
- Metrics: request count, token count, cost amount, and concurrency according to existing quota metrics.
- Window: subscription period or calendar/fixed windows supported by quota.
- Mode: enforce or observe; manual-first can be used for controlled rollout.
- Ownership marker: each policy row should be traceable to subscription ID and period via metadata or a dedicated relation table.

Avoid direct writes that reset quota counters. Prefer installing a new period policy and letting quota service windows/reservations remain auditable.

## Package And File Plan

| Path | Action | Reason | Risk |
| --- | --- | --- | --- |
| `backend/internal/subscription/types.go` | Add | HUAKAI-owned plan, instance, grant, lifecycle, and status types. | Low/medium; new non-frozen package. |
| `backend/internal/subscription/service.go` | Add | Activation, renewal, cancellation, admin bind, and worker orchestration. | Medium; money/quota coordination. |
| `backend/internal/subscription/store.go` | Add | Store interfaces for plans, instances, grants, worker locks, and policy ownership. | Low/medium. |
| `backend/internal/subscription/plan_store_postgres.go` | Add | Plan CRUD and tenant-scoped reads. | Medium; DB access. |
| `backend/internal/subscription/instance_store_postgres.go` | Add | User subscription state transitions with row locks. | Medium; lifecycle correctness. |
| `backend/internal/subscription/grant_store_postgres.go` | Add | Deterministic grant idempotency and linkage to payment credit writer. | High; money path. |
| `backend/internal/subscription/quota_templates.go` | Add | Convert approved plan quota templates to `internal/quota` policies. | Medium; quota behavior. |
| `backend/internal/subscription/worker.go` | Add | Due expiry/reset/cleanup worker with RunOnce tests. | Medium; lifecycle. |
| `backend/internal/subscriptionhttp/handler.go` | Add | Route grouping and shared dependencies. | Low/medium; non-frozen HTTP package. |
| `backend/internal/subscriptionhttp/user_handler.go` | Add | User plan listing, current subscription, purchase-intent endpoints. | Low/medium. |
| `backend/internal/subscriptionhttp/admin_handler.go` | Add | Admin plan CRUD, bind, cancel/invalidate, subscription list. | Low/medium. |
| `backend/internal/payment/credit_writer.go` | Add | Extract one trusted credit path from P1 and reuse for P3. | High; money path, Owner-gated. |
| `backend/internal/payment/store_postgres.go` | Modify minimally | Delegate existing P1 fulfillment to credit writer; keep existing behavior. | High; existing file is large and money-critical. |
| `backend/internal/payment/types.go` | Modify minimally | Add local source-kind/result types if needed by the credit writer. | Medium. |
| `backend/cmd/gateway/routes.go` | Modify | Mount subscription HTTP routes. | Medium; gateway surface. |
| `backend/cmd/gateway/wiring.go` | Modify | Construct subscription store/service/worker. | Medium; startup config. |
| `backend/cmd/gateway/lifecycle.go` | Modify serially | Start/stop subscription worker with explicit shutdown order. | Medium; coordinate with any new-machine lifecycle work. |
| `backend/sql/migrations/0072_payment_subscription.up.sql` | Add | Subscription tables and money-origin constraint changes. | High; Owner-gated schema. |
| `backend/sql/migrations/0072_payment_subscription.down.sql` | Add | Reversible dev/test rollback where safe. | High; no destructive production assumption. |

Frozen package check: no new files under `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`.

## Migration 0072 Proposal (Owner-Gated)

Migration 0072 should be reviewed as a high-risk DB change before code work starts.

Required tables:

- `subscription_plans`: tenant, display name, description, price, currency, period duration, sale/enabled flags, sort order, grant amount/currency, optional entitlement group, provider metadata, quota template version, audit timestamps.
- `user_subscriptions`: tenant, user, plan snapshot, status, start/end, current period start/end, next reset at, source, payment order link if paid, previous entitlement group snapshot, active upgrade marker, audit timestamps.
- `subscription_grants`: tenant, subscription, user, period start/end, grant kind, amount/currency, deterministic idempotency key, payment credit link, billing event link, status, error/retry metadata.
- `subscription_policy_links`: tenant, subscription, period, quota policy IDs, validity window, status.
- `subscription_audit_events`: tenant, actor, user, subscription, event type, before/after JSON, request/idempotency key, timestamps.

Money schema extension options:

1. Preferred: make `payment_credits` a unified positive-credit fact table with exactly one origin among payment order, subscription grant, or future credit source. Extend `billing_events` with a subscription-credit branch and keep append-only protections.
2. Backup: keep `payment_credits` payment-order-only and create a separate subscription balance table. This is not recommended because it splits derived balance and creates two money ledgers.
3. Rejected unless Owner explicitly accepts feature shrinkage: subscription grants quota only and do not credit balance. This violates the Owner decision that subscription grants balance.

Required DB constraints:

- Tenant-first unique indexes for plan external IDs, subscription idempotency keys, grant idempotency keys, and active entitlement ownership.
- Tenant predicates on every FK or lookup path that crosses user, order, credit, policy, and audit tables.
- Check constraints for positive money amounts, currency consistency, valid period ordering, and exactly-one credit origin.
- Worker indexes on due expiry, due reset, active status, and retryable grant status.
- Append-only/immutability protection for money facts consistent with existing money-path triggers.

## API Surface Plan

User routes, mounted outside frozen packages:

- List sale-ready plans for the authenticated tenant/user.
- Get current and historical subscription summary.
- Create a subscription purchase intent backed by P1 payment order.
- Read purchase/activation status.
- Optional user cancellation endpoint only if product policy allows self-cancel.

Admin routes:

- Plan list/create/update/disable.
- Manual bind/activate subscription.
- Cancel/invalidate subscription.
- List user subscriptions.
- Run worker once in non-production or admin maintenance mode if existing ops patterns allow it.

Provider callbacks remain in P2a payment webhook. Subscription should not add provider-specific callback handlers unless a future payment-provider slice requires it.

## Lifecycle Worker Plan

Worker responsibilities:

- Process due resets by creating the next period grant and quota policy set idempotently.
- Process due expiries by marking subscriptions expired, closing subscription-owned quota policies, and restoring the previous group/entitlement only when no later active upgraded subscription remains.
- Retry transient grant failures without double-crediting successful periods.
- Record audit events for every state transition.

Coordination details:

- Add worker to `gatewayRuntime` and stop it during shutdown before DB pool close (`backend/cmd/gateway/lifecycle.go:26`, `backend/cmd/gateway/lifecycle.go:97`).
- Start after payment, quota, and subscription stores are constructed (`backend/cmd/gateway/wiring.go:374`).
- Use DB row locking, `SKIP LOCKED`, or an equivalent lease to prevent cluster double processing. If the project has a master-only runtime flag, wire worker scheduling through that flag.
- Mark `backend/cmd/gateway/lifecycle.go` as serial coordination with any other new-machine work. If another branch touches lifecycle startup/shutdown, reconcile before implementation.

## Implementation Order After Owner Approval

1. Write PG integration tests first for activation, duplicate grant, tenant isolation, expiry downgrade, reset, and quota-policy behavior.
2. Add migration 0072 behind Owner approval; run local migration tests.
3. Extract the payment credit writer and prove existing P1/P2 tests still pass unchanged.
4. Add subscription domain package and PG stores.
5. Add quota template instantiation through existing quota store/service APIs.
6. Add activation, renewal, cancellation, admin bind, and worker RunOnce service paths.
7. Add HTTP package and gateway routes.
8. Wire lifecycle worker serially.
9. Run existing payment/quota tests plus new subscription PG tests.
10. Stage intended diff and run `codex exec review --uncommitted --full-auto --sandbox read-only` before commit.

## Mutation-Discriminating Test Matrix

| Test | Defect guarded | Discriminating fixture | Mutation that must turn red |
| --- | --- | --- | --- |
| Duplicate paid activation grants once | Webhook replay or repeated activation double-credits balance. | Same tenant/user/paid order/subscription intent invoked twice; expect one subscription, one grant, one `payment_credits` row, one `billing_events` row, balance +X once. | Remove grant unique key or idempotent lookup; balance becomes +2X or two money facts appear. |
| Subscription grant uses single credit path | Developer bypasses payment credit writer and writes ledger/balance inconsistently. | Activation checks subscription grant has credit row, billing event link, audit event, and derived balance; existing P1 fulfill test remains green. | Insert billing event without credit or credit without billing event; derived balance or linkage assertion fails. |
| Failed payment does not activate | Pending/failed/expired payment creates subscription or quota. | Payment order remains pending or failed; activation attempt returns no subscription, no grant, no policy, no balance. | Remove paid-status guard; rows appear and test fails. |
| Tenant isolation | Cross-tenant grant/policy leakage by external order or user ID. | Two tenants share external order text and same user-like UUID value; only tenant B order is paid. Tenant A must remain zero. | Drop tenant predicate in lookup; wrong tenant balance/policy changes. |
| Active renewal stacks time | Renewal before expiry loses remaining days or double-starts from now. | Existing active subscription ending in 20 days renewed for 30 days; new end is old end + 30 days and one next-period grant. | Always start from current time; expected end differs. |
| Expired renewal restarts from now | Expired subscription incorrectly extends from stale end. | Subscription ended 10 days ago; renewal should start at current clock and create current-period grant. | Always extend from old end; period is in the past. |
| Reset grant idempotency | Period reset worker double-grants on rerun. | Due reset row processed twice; expect one next-period grant/credit/policy and cursor advanced once. | Remove period idempotency; duplicate money/policy rows appear. |
| Expiry downgrade with newer active upgrade | Worker restores old group even though another active subscription still owns upgrade. | User has older due subscription and newer active upgraded subscription; worker expires older only. Current entitlement remains upgraded. | Always restore previous group; entitlement assertion fails. |
| Expiry downgrade when last upgrade ends | Worker fails to restore pre-purchase group. | Single active upgraded subscription due for expiry with previous group snapshot. Worker expires and restores prior group/entitlement. | Skip restore; group remains upgraded. |
| Quota template instantiation | Plan quota does not enforce the intended policy. | Plan grants user-level cost cap and request cap plus observe-only cost warning; activation creates policies, then quota reserve denies/observes as expected. | Wrong scope/mode/metric/window; quota decision differs. |
| Quota history not wiped on reset | Reset mutates old counters and destroys audit/history. | Old period has usage; reset opens new policy/window. Old rows remain historical; new request allowed under new period. | Reuse old period or zero old rows; either deny incorrectly or history assertion fails. |
| Concurrent activation | Race creates duplicate subscription/grant or returns inconsistent errors. | 32 goroutines activate same completed intent; assert one successful state and idempotent replay semantics. | Remove row lock/unique key; duplicates or spurious failures appear. |
| Admin invalidation cleanup | Cancelled subscription leaves active policies or future grants. | Admin invalidates active subscription; policies close, no worker grant later, group restore follows “no newer active upgrade” rule. | Delete/cancel without cleanup; reserve still allowed or future grant appears. |
| Partial failure rollback | Grant row commits without money credit, or money credit commits without grant state. | Inject credit-writer failure. Expected full rollback or retryable pending grant with no balance movement. | Commit partial rows; consistency assertions fail. |
| Plan snapshot behavior | Existing subscription unexpectedly changes when admin edits plan. | Existing subscription purchased plan A; admin changes grant/quota; renewal behavior follows Owner-approved snapshot/current-plan rule. | Use the opposite rule; expected amount/policy differs. |

Test rule: every test must assert the expected good state, not only “not bad”. Avoid fixtures where winner/loser share the distinguishing field. Do not skip PG tests because a field is zero; build fixtures that make the mutation visible.

## Fusion-Upgrade Delta

Architecture:

- sub2api-style group subscription and new-api-style plan/order/worker shape are fused into a HUAKAI-owned subscription package.
- External payment remains P1/P2 payment order and webhook, not provider-specific subscription callback duplication.
- Money grants converge on one local credit writer with append-only billing event evidence.
- Quota limits converge on `internal/quota`, avoiding a second consumption engine.

Algorithm:

- Deterministic idempotency keys per tenant/subscription/period/grant kind.
- Serializable money writes and tenant-first row locks for activation and grant.
- Due-worker batch processing with DB lock/lease and rerun-safe period advancement.
- Expiry downgrade checks for newer active upgrades before restoring prior group.
- Reset opens a new quota/grant period rather than erasing historical counters.

Ecosystem:

- Provider metadata can support existing and future payment providers, but provider callbacks remain centralized in payment.
- Admin and user APIs are separated into non-frozen HTTP packages.
- The design supports multi-tenant SaaS and personal/deployed editions because tenant predicates are mandatory and worker scheduling can be gated.
- CLIProxyAPI contributes no subscription parity requirement; its no-equivalent status is explicitly recorded instead of shrinking HUAKAI scope.

## Owner Decisions Still Open

1. Approve migration 0072 shape and whether `payment_credits` becomes a unified positive-credit fact table with multiple origins.
2. Decide where the runtime “group/entitlement” lives today. Current local source does not show a user group column equivalent; P3 can either add a subscription-owned entitlement table or wait for a broader user-profile/routing decision.
3. Decide whether existing subscriptions use immutable plan snapshots forever or pick up current plan definitions at renewal.
4. Decide whether reset grants balance every period, only on purchase/renewal, or according to plan-level grant cadence.
5. Decide whether admin bind can grant paid-balance credits or only quota/entitlement without balance.
6. Decide if user self-cancel is in P3 or admin-only for the first release.
7. Decide worker cadence, cluster gating, and whether manual RunOnce endpoint is permitted outside tests.
8. Decide exact public/admin route names and whether they need OpenAPI updates in the same slice.
9. Decide whether subscription-owned quota policies are hard-deleted on rollback in dev only, or always closed/expired append-only.

## Risk Register

| Risk | Severity | Mitigation |
| --- | --- | --- |
| Double credit from replay/concurrency | High | Deterministic grant key, serializable credit writer, PG concurrency test. |
| Money ledger split | High | One credit writer; no direct `internal/billing`; balance derived from `payment_credits`. |
| Cross-tenant leakage | High | Tenant-first indexes, tenant predicates in every lookup, PG tenant-isolation tests. |
| Migration breaks existing P1 credits | High | Owner gate; existing P1/P2 tests before/after; compatibility migration; no destructive migration. |
| Worker overlap | Medium/high | DB locks/leases, rerun-safe grant keys, RunOnce tests. |
| Expiry downgrades incorrectly | Medium/high | Store previous entitlement snapshot and check newer active upgrades. |
| Quota reset loses audit history | Medium | New period policy approach; assertions that old windows remain historical. |
| Frozen package violation | Medium | New subscription packages; only route/wiring edits outside frozen internals. |
| Clean-room contamination | High | Behavior-only summary; no upstream source names or copied code in implementation. |

## Source Coverage Proof

Observed reference contributions:

- sub2api plan service/schema: plan definition, validation, sale listing, update/delete guards.
- sub2api subscription service/repository/value: active lookup, assignment/extension, idempotency behavior, reset/expiry/usage windows, cache/maintenance shapes.
- sub2api tests: semantic idempotency, reset combinations, subscription-mode billing separation.
- new-api subscription model/controller/payment/task: plan fields, user instance snapshots, provider order completion, admin bind, expiry/reset/preconsume/refund, master-only reset worker, user/admin route surface.
- CLIProxyAPI account/relay files: confirmed subscription/billing hits are not a subscription subsystem.
- HUAKAI payment/quota/lifecycle/migration/tests: existing single-credit payment path, webhook reuse, append-only money constraints, quota policy engine, HTTP route placement, worker lifecycle shape, and mutation-test style.

Source files read:

- HUAKAI: `docs/RULES.md`
- HUAKAI: `backend/internal/payment/types.go`
- HUAKAI: `backend/internal/payment/service.go`
- HUAKAI: `backend/internal/payment/store.go`
- HUAKAI: `backend/internal/payment/store_postgres.go`
- HUAKAI: `backend/internal/payment/webhook.go`
- HUAKAI: `backend/internal/paymenthttp/handler.go`
- HUAKAI: `backend/internal/paymenthttp/webhook.go`
- HUAKAI: `backend/internal/payment/store_postgres_integration_test.go`
- HUAKAI: `backend/internal/payment/webhook_integration_test.go`
- HUAKAI: `backend/internal/quota/types.go`
- HUAKAI: `backend/internal/quota/policy.go`
- HUAKAI: `backend/internal/quota/service.go`
- HUAKAI: `backend/internal/quota/service_settle.go`
- HUAKAI: `backend/internal/quota/store.go`
- HUAKAI: `backend/internal/quota/rate_window.go`
- HUAKAI: `backend/internal/quota/service_integration_test.go`
- HUAKAI: `backend/sql/migrations/0002_observability_billing.up.sql`
- HUAKAI: `backend/sql/migrations/0023_voucher_system.up.sql`
- HUAKAI: `backend/sql/migrations/0039_money_path_append_only_triggers.up.sql`
- HUAKAI: `backend/sql/migrations/0070_quota_subsystem.up.sql`
- HUAKAI: `backend/sql/migrations/0071_payment_p1.up.sql`
- HUAKAI: `backend/cmd/gateway/lifecycle.go`
- HUAKAI: `backend/cmd/gateway/wiring.go`
- HUAKAI: `backend/cmd/gateway/routes.go`
- sub2api: `backend/internal/service/payment_config_plans.go`
- sub2api: `backend/ent/schema/subscription_plan.go`
- sub2api: `backend/internal/service/subscription_service.go`
- sub2api: `backend/internal/repository/user_subscription_repo.go`
- sub2api: `backend/internal/service/subscription_expiry_service.go`
- sub2api: `backend/internal/service/subscription_maintenance_queue.go`
- sub2api: `backend/internal/service/user_subscription.go`
- sub2api: `backend/internal/service/subscription_assign_idempotency_test.go`
- sub2api: `backend/internal/service/subscription_reset_quota_test.go`
- sub2api: `backend/internal/service/gateway_service_subscription_billing_test.go`
- new-api: `model/subscription.go`
- new-api: `model/main.go`
- new-api: `service/subscription_reset_task.go`
- new-api: `router/api-router.go`
- new-api: `controller/subscription.go`
- new-api: `controller/subscription_payment_epay.go`
- new-api: `controller/subscription_payment_stripe.go`
- new-api: `controller/subscription_payment_creem.go`
- CLIProxyAPI: `.huakai-head-sha`
- CLIProxyAPI: `internal/auth/codex/jwt_parser.go`
- CLIProxyAPI: `internal/auth/codex/filename.go`
- CLIProxyAPI: `internal/util/claude_attribution.go`

Lane: specifier
Agent: GPT-5 Codex / codex-payment-p3-plan-20260529
UTC timestamp: 2026-05-29T06:17:44Z
