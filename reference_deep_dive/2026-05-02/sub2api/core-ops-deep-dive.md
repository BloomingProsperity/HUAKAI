# Sub2API core operations deep dive

Date: 2026-05-02
Reference repo: `.omc/reference-src/sub2api`
Snapshot: `main`, commit `48912014a16e`, tag `v0.1.121-1-g48912014`
Status: source tree clean; local `.omc/` tool state ignored

## Scope

This is the second-pass source review for the parts of Sub2API that look closest to HUAKAI's commercial operating model. It intentionally focuses on behavior, state machines, and production failure handling, not implementation copying.

Read depth in this pass:

- Account scheduling and account health: `service/openai_account_scheduler.go`, `repository/account_repo.go`
- Payment order lifecycle, webhook recovery, fulfillment, refunds: `service/payment_*.go`, `handler/admin/payment_handler.go`
- Channel monitoring: `service/channel_monitor_*.go`, `repository/channel_monitor_repo.go`
- Usage retention and write-path backpressure: `service/usage_cleanup_*.go`, `service/usage_record_worker_pool.go`

## Account scheduler and health

Source-confirmed behavior:

- Scheduler selection is not a simple random channel picker. It attempts previous-response stickiness, transport compatibility, bind stickiness, session hash routing, and then weighted load balancing before returning a selected account. Evidence: `.omc/reference-src/sub2api/service/openai_account_scheduler.go:242`, `.omc/reference-src/sub2api/service/openai_account_scheduler.go:272`, `.omc/reference-src/sub2api/service/openai_account_scheduler.go:288`.
- The load-balancing path filters accounts by schedulable state, account type, group privacy, request compatibility, transport compatibility, and excluded IDs before scoring. Evidence: `.omc/reference-src/sub2api/service/openai_account_scheduler.go:588`, `.omc/reference-src/sub2api/service/openai_account_scheduler.go:630`.
- Scores combine concurrency load, queue pressure, error rate, TTFT, priority, and manual load factor. Evidence: `.omc/reference-src/sub2api/service/openai_account_scheduler.go:635`, `.omc/reference-src/sub2api/service/openai_account_scheduler.go:675`, `.omc/reference-src/sub2api/service/openai_account_scheduler.go:733`.
- Compact-provider support is treated as a compatibility dimension, with stale/unknown compact support only used as a fallback path. Evidence: `.omc/reference-src/sub2api/service/openai_account_scheduler.go:658`, `.omc/reference-src/sub2api/service/openai_account_scheduler.go:788`.
- If no concurrency slot can be acquired, the scheduler returns a wait plan rather than immediately failing. Evidence: `.omc/reference-src/sub2api/service/openai_account_scheduler.go:846`.
- Account repository queries exclude expired, overloaded, rate-limited, and temporarily unschedulable accounts. Evidence: `.omc/reference-src/sub2api/repository/account_repo.go:917`, `.omc/reference-src/sub2api/repository/account_repo.go:941`, `.omc/reference-src/sub2api/repository/account_repo.go:964`.
- Overload, temporary unschedulable, rate-limit clear, and quota updates enqueue scheduler outbox events so routing state can be refreshed. Evidence: `.omc/reference-src/sub2api/repository/account_repo.go:1084`, `.omc/reference-src/sub2api/repository/account_repo.go:1098`, `.omc/reference-src/sub2api/repository/account_repo.go:1136`, `.omc/reference-src/sub2api/repository/account_repo.go:1898`.

HUAKAI delta:

- `F-ACC-HEALTH-001` should be split into concrete subfeatures, otherwise it is too coarse to implement safely.
- Suggested split:
  - `F-ACC-SCHED-001`: account schedulability filters: active, not expired, not overloaded, not rate-limited, not temporarily disabled.
  - `F-ACC-SCHED-002`: scheduler scoring inputs: concurrency, queue depth, error rate, TTFT, priority, manual load factor.
  - `F-ACC-SCHED-003`: sticky routing: previous response, bind key, session hash, plus explicit timeout and cache invalidation rules.
  - `F-ACC-SCHED-004`: wait-plan behavior when no account slot is available.
  - `F-ACC-SCHED-005`: account-change outbox and scheduler snapshot refresh.
- Recommended level: L1 for basic schedulability filters, L2 for scoring, wait plan, and outbox. Sticky routing can be L2 unless the first market release depends heavily on long-running conversations.

Clean-room risk:

- Do not copy the weighted formula or type structure. HUAKAI should specify the behavior contract: "prefer accounts with lower active load, lower recent failure rate, lower observed TTFT, and higher admin priority," then implement locally.

## Payment lifecycle, recovery, and refunds

Source-confirmed behavior:

- Order creation is transactional. It validates type-specific inputs, payment enablement, amount bounds, provider selection, pending-order limits, daily limits, expiry, provider snapshot, and unique `out_trade_no`. Evidence: `.omc/reference-src/sub2api/service/payment_order.go:23`, `.omc/reference-src/sub2api/service/payment_order.go:90`, `.omc/reference-src/sub2api/service/payment_order.go:107`, `.omc/reference-src/sub2api/service/payment_order.go:125`, `.omc/reference-src/sub2api/service/payment_order.go:200`.
- Provider invocation failure marks the created order failed instead of leaving an ambiguous pending state. Evidence: `.omc/reference-src/sub2api/service/payment_order.go:23`.
- Payment notification intentionally treats unknown orders as a 2xx webhook acknowledgement to stop provider retry storms. Evidence: `.omc/reference-src/sub2api/service/payment_fulfillment.go:21`, `.omc/reference-src/sub2api/handler/payment_webhook_handler.go:117`.
- Fulfillment validates provider binding, provider metadata, and paid amount tolerance before marking an order paid. Evidence: `.omc/reference-src/sub2api/service/payment_fulfillment.go:70`.
- Cancelled or recently expired orders can be recovered to paid with audit records, while old expired orders are only audited as late payments. Evidence: `.omc/reference-src/sub2api/service/payment_fulfillment.go:134`, `.omc/reference-src/sub2api/service/payment_fulfillment.go:173`.
- Balance and subscription fulfillment are idempotent. Balance uses redeem/action guards; subscription fulfillment checks success audit logs before extending again. Evidence: `.omc/reference-src/sub2api/service/payment_fulfillment.go:213`, `.omc/reference-src/sub2api/service/payment_fulfillment.go:241`, `.omc/reference-src/sub2api/service/payment_fulfillment.go:308`.
- Admin retry is a first-class workflow for paid or failed orders, with state checks and audit logs. Evidence: `.omc/reference-src/sub2api/service/payment_fulfillment.go:520`, `.omc/reference-src/sub2api/handler/admin/payment_handler.go:104`.
- Refunds are pinned to the original provider instance or stored provider snapshot; ambiguous legacy fallback is blocked. Evidence: `.omc/reference-src/sub2api/service/payment_refund.go:22`, `.omc/reference-src/sub2api/service/payment_refund.go:46`, `.omc/reference-src/sub2api/service/payment_refund.go:77`.
- User refund request, admin prepare refund, gateway refund, rollback, partial refund, and refund failure states are distinct. Evidence: `.omc/reference-src/sub2api/service/payment_refund.go:150`, `.omc/reference-src/sub2api/service/payment_refund.go:201`, `.omc/reference-src/sub2api/service/payment_refund.go:275`, `.omc/reference-src/sub2api/service/payment_refund.go:364`, `.omc/reference-src/sub2api/service/payment_refund.go:376`.
- Resume tokens are HMAC-signed and bind order/user/provider/payment type. Return URLs are canonicalized and restricted to internal payment-result paths. Evidence: `.omc/reference-src/sub2api/service/payment_resume_lookup.go:12`, `.omc/reference-src/sub2api/service/payment_resume_service.go:235`, `.omc/reference-src/sub2api/service/payment_resume_service.go:332`.
- Webhook provider resolution prefers original order binding and only uses fallback when unambiguous. Evidence: `.omc/reference-src/sub2api/service/payment_webhook_provider.go:15`, `.omc/reference-src/sub2api/service/payment_webhook_provider.go:96`.
- Webhook body is size-limited and debug logging truncates raw body. Evidence: `.omc/reference-src/sub2api/handler/payment_webhook_handler.go:25`, `.omc/reference-src/sub2api/handler/payment_webhook_handler.go:64`, `.omc/reference-src/sub2api/handler/payment_webhook_handler.go:99`.

HUAKAI delta:

- `F-PAY-ORDER-001` should not just say "payments". It needs an explicit order state machine.
- Suggested states: `pending`, `paid`, `recharging`, `completed`, `failed`, `cancelled`, `expired`, `refund_requested`, `refunding`, `refunded`, `partially_refunded`, `refund_failed`.
- Suggested additional feature IDs:
  - `F-PAY-WEBHOOK-001`: webhook verification, unknown-order 2xx policy, body-size cap, debug truncation.
  - `F-PAY-RECOVERY-001`: paid-after-cancelled or paid-after-expired recovery with grace window and audit.
  - `F-PAY-FULFILL-001`: idempotent balance/subscription fulfillment plus admin retry.
  - `F-PAY-REFUND-001`: provider-pinned refund flow, rollback strategy, partial refund state.
  - `F-PAY-RESUME-001`: signed resume token and strict return URL canonicalization.
- Recommended level: L2. Payment recovery and refund rollback should land before wide public billing, even if provider count remains small.

Clean-room risk:

- Payment provider adapters are high-risk because Sub2API has concrete platform details. HUAKAI should write its own provider contracts and state-machine tests from behavior specs only.

## Channel monitor

Source-confirmed behavior:

- The runner schedules one goroutine/ticker per enabled monitor, supports CRUD schedule rebuild, prevents duplicate in-flight checks per monitor, uses a bounded worker pool, and recovers from panics. Evidence: `.omc/reference-src/sub2api/service/channel_monitor_runner.go:12`, `.omc/reference-src/sub2api/service/channel_monitor_runner.go:25`, `.omc/reference-src/sub2api/service/channel_monitor_runner.go:199`, `.omc/reference-src/sub2api/service/channel_monitor_runner.go:233`.
- Monitor HTTP clients use SSRF-safe transport and re-resolve hosts at dial time to prevent DNS rebinding. Evidence: `.omc/reference-src/sub2api/service/channel_monitor_checker.go:19`, `.omc/reference-src/sub2api/service/channel_monitor_ssrf.go:9`, `.omc/reference-src/sub2api/service/channel_monitor_ssrf.go:99`.
- Endpoint validation requires HTTPS origin only and rejects metadata/private/loopback/link-local destinations without leaking resolved IPs. Evidence: `.omc/reference-src/sub2api/service/channel_monitor_validate.go:24`, `.omc/reference-src/sub2api/service/channel_monitor_validate.go:29`.
- Provider checks can use generated challenge prompts, body override modes, header merging, model-specific text extraction, and degraded status on slow latency. Evidence: `.omc/reference-src/sub2api/service/channel_monitor_checker.go:44`, `.omc/reference-src/sub2api/service/channel_monitor_checker.go:90`, `.omc/reference-src/sub2api/service/channel_monitor_checker.go:130`, `.omc/reference-src/sub2api/service/channel_monitor_checker.go:216`.
- API keys are encrypted at rest; decrypt failure is recorded and the runner refuses to execute checks with a failed key. Evidence: `.omc/reference-src/sub2api/service/channel_monitor_service.go:60`, `.omc/reference-src/sub2api/service/channel_monitor_service.go:86`, `.omc/reference-src/sub2api/service/channel_monitor_service.go:392`.
- History insertion, latest-result lookup, recent history, rollups, watermark, history cleanup, and rollup cleanup are explicit repository/service behavior. Evidence: `.omc/reference-src/sub2api/repository/channel_monitor_repo.go:184`, `.omc/reference-src/sub2api/repository/channel_monitor_repo.go:211`, `.omc/reference-src/sub2api/repository/channel_monitor_repo.go:338`, `.omc/reference-src/sub2api/service/channel_monitor_service.go:300`, `.omc/reference-src/sub2api/service/channel_monitor_service.go:335`.

HUAKAI delta:

- `F-CH-MON-001` should be upgraded from "channel monitor" to "safe channel monitor with retention and rollup".
- Suggested split:
  - `F-CH-MON-001`: monitor CRUD and encrypted credential storage.
  - `F-CH-MON-002`: bounded runner, per-monitor in-flight guard, timeout, panic recovery.
  - `F-CH-MON-003`: SSRF-safe endpoint validation and dial-time DNS rebinding protection.
  - `F-CH-MON-004`: result history, latest status, availability rollups, retention cleanup.
  - `F-CH-MON-005`: operator UI for latest status, history, degraded reason, and manual run.
- Recommended level: L2 for monitor CRUD/runner/SSRF guard/history. Rollups can be L3 if traffic is low, but retention cleanup should not wait.

Clean-room risk:

- The challenge prompt file appears to reference an external behavior clone. Do not reuse its text or expected challenge contract. HUAKAI should define its own minimal health-check behavior: "model returns parseable response for a generated request within timeout".

## Usage retention and write-path backpressure

Source-confirmed behavior:

- Usage cleanup is modeled as asynchronous tasks with validation, creator identity, cancellation, stale-running reclaim, batch delete, progress tracking, failure truncation, and dashboard recompute. Evidence: `.omc/reference-src/sub2api/service/usage_cleanup_service.go:124`, `.omc/reference-src/sub2api/service/usage_cleanup_service.go:156`, `.omc/reference-src/sub2api/service/usage_cleanup_service.go:306`, `.omc/reference-src/sub2api/repository/usage_cleanup_repo.go:119`, `.omc/reference-src/sub2api/repository/usage_cleanup_repo.go:285`.
- The usage-record write path uses a bounded worker pool instead of unbounded goroutines. It has queue limits, timeout, overflow policy, sampled sync fallback, drop logging, and autoscale up/down logic. Evidence: `.omc/reference-src/sub2api/service/usage_record_worker_pool.go:17`, `.omc/reference-src/sub2api/service/usage_record_worker_pool.go:76`, `.omc/reference-src/sub2api/service/usage_record_worker_pool.go:134`, `.omc/reference-src/sub2api/service/usage_record_worker_pool.go:207`.

HUAKAI delta:

- `F-LOG-RET-001` should include operator-visible cleanup tasks rather than only a retention setting.
- Suggested feature IDs:
  - `F-USAGE-CLEAN-001`: admin-created cleanup tasks with filters, max range, batch size, progress, cancellation, stale reclaim.
  - `F-USAGE-WRITE-001`: bounded usage write worker pool with backpressure, timeout, and drop/sync-fallback metrics.
- Recommended level: L2 before real paid traffic. This is one of the "already eaten production pain" areas: without bounded writes and cleanup, usage logs can become both an outage source and a cost problem.

## Immediate backlog insertions

High-confidence insertions for HUAKAI:

1. `F-ACC-SCHED-005`: account-change outbox and scheduler snapshot refresh.
   - Level: L2
   - Acceptance direction: account overload/rate-limit/temp-offline changes become invisible to new routing decisions within bounded time; stale snapshot path is explicit.
2. `F-PAY-RECOVERY-001`: payment recovery and webhook idempotency.
   - Level: L2
   - Acceptance direction: unknown webhook is acked, late paid order is audited, completed order is not fulfilled twice, admin can retry failed fulfillment.
3. `F-PAY-REFUND-001`: refund state machine with provider pinning and rollback.
   - Level: L2
   - Acceptance direction: refund cannot use a different provider instance than the original order unless explicitly migrated; rollback failure is operator-visible.
4. `F-CH-MON-003`: SSRF-safe monitor endpoint and dial-time rebinding guard.
   - Level: L2
   - Acceptance direction: localhost, metadata, private IP, link-local, and DNS-rebinding targets are rejected; error messages do not leak resolved private IPs.
5. `F-USAGE-WRITE-001`: bounded usage write path.
   - Level: L2
   - Acceptance direction: full queue does not spawn unbounded goroutines; fallback/drop counters are observable.

## Open questions for next pass

- Gateway multi-attempt execution and billing settlement still need a separate read against request handlers and provider adapters.
- Account OAuth/token refresh and TLS/browser-fingerprint logic were not fully audited in this pass.
- Payment provider implementations such as WeChat, Alipay, Easypay, and Stripe need provider-specific behavior extraction before implementation planning.
- Channel monitor schema/migration indexes should be checked to confirm whether the repository query patterns are backed by the right indexes.
- Admin frontend workflows need a separate UI pass: this file only confirms backend/admin handler capability.
