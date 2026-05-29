# HUAKAI 支付子系统 P2a 自动入账回调独立实施计划

=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: specifier
  - specifier = read source files, produce behavior-only summary
  - reviewer  = verify behavior summary WITHOUT re-reading source
  - On the same artifact, lane MUST be a different agent session than
    any prior lane (no same agent doing both lanes on same file).

PRIOR LANES ON THIS ARTIFACT: none

REFERENCE PROJECTS IN SCOPE: new-api / sub2api / CLIProxyAPI
  (CLIProxyAPI no-equivalent reconciliation, added post-draft per
   CLAUDE.md #16 三镜规则 — 本稿起草时间早于该规则: CLIProxyAPI 是纯
   relay account→API 代理, 无 payment/order/billing/subscription 模块
   [CLIProxyAPI@21fad9db: grep `payment|billing|webhook|recharge`
   全仓命中皆为 antigravity_credits vendor-quota + websocket relay,
   internal/ 无 payment 包], 故支付域无等价物可对照。)

HARD PROHIBITIONS:
  - NEVER copy function names verbatim
  - NEVER copy struct field names verbatim
  - NEVER copy comments verbatim
  - NEVER do line-by-line algorithmic translation; behaviors must be
    expressed in different sentence structure than upstream code ordering
  - NEVER paste raw upstream code blocks (even small snippets)
  - When upstream uses a distinctive identifier, rename in summary

CITATION POLICY (reconciled 2026-05-10 with CLAUDE.md #12):
  - file:line citations are ALLOWED in prose as evidence anchors —
    `<repo>@<sha>:<file>:<line>` style satisfies #12 per-claim citation
  - the cited identifier itself must NOT appear verbatim in the prose
    surrounding the citation; reference it by paraphrased role only
  - "Source files read" tail block remains required (see below)

REQUIRED OUTPUT TAIL (must appear at end of every artifact):
  Source files read: <relative paths>
  Lane: <specifier | reviewer>
  Agent: <model + ID>
  UTC timestamp: <ISO 8601>

ESCALATION: if you cannot honestly produce behavior summary without
violating the prohibitions, RETURN A NO-OP "cannot summarize within
clean-room" rather than violating. The Owner prefers a partial gap to
a clean-room breach.

=== END CLEAN-ROOM LANE GUARD ===

> **For agentic workers:** REQUIRED SUB-SKILL: use `test-driven-development`, `systematic-debugging` for failures, and `verification-before-completion`. This plan is only a plan artifact; do not implement, commit, or push from this document-writing task.

| Owner directive | "HUAKAI 支付子系统切片 P2a 独立实施计划(仅写计划文档,不写实现代码,不 commit,不 push)。" |
| --- | --- |
| Scope | P2a automatic paid callback path using only HUAKAI test provider HMAC simulation, coexisting with P1 manual confirmation. |
| Success criteria | A future implementer can add a public webhook endpoint, verify a signed test-provider callback, resolve the tenant-scoped local order, compare amount, call P1 `ConfirmPaid` then `Fulfill`, and prove replay/forgery/tampering/cross-tenant cases with true PostgreSQL tests. |
| Time estimate | Planning artifact: 1-2 hours. Implementation after Owner approval: 0.5-1.5 engineering days, plus review/test time. |
| Blast radius | Payment money path, public unauthenticated HTTP surface, payment audit event types, `cmd/gateway` route wiring. |
| Failure modes | False credit from forged callback; double credit on replay; valid payment rejected due lookup ambiguity; cross-tenant credit; audit gap; public endpoint exposing raw callback material. |
| Decision points | Owner must approve the audit migration, callback ack policy for verified-but-business-rejected events, test-provider runtime enablement, and whether durable callback-event de-dup is P2a or P-RealMoney. |
| Pre-execution checklist | Do not read `docs/process/plans/2026-05-29-payment-p2a-claude.md`; re-check frozen package rule; run P1 payment tests first; write failing PG tests before code; stage intended diff only; run Codex per-commit review before commit. |

## Source-Verified Context

Observed regions: 18. Inferences: 7. Open questions: 5.

HUAKAI P1 already has the correct local money primitive for P2a: the store exposes tenant-scoped order lookup, paid confirmation, two-phase fulfillment, listing audit events, and balance derivation [backend/internal/payment/store.go:12]; manual confirmation already calls `ConfirmPaid` and then `Fulfill` [backend/internal/payment/service.go:114], while fulfillment idempotently returns the existing credit for completed orders and otherwise writes one credit plus one `payment_credited` billing fact inside a serializable transaction [backend/internal/payment/service.go:138] [backend/internal/payment/store_postgres.go:263]. P2a should therefore add the webhook verification and decision layer, not a second crediting path.

P1 persistence already protects several P2a invariants: local orders are unique per tenant and external order number [backend/sql/migrations/0071_payment_p1.up.sql:45], each order can have at most one credit [backend/sql/migrations/0071_payment_p1.up.sql:74], and `ConfirmPaid` accepts pending orders while treating already-confirmed or completed orders as idempotent [backend/internal/payment/store_postgres.go:144]. Current audit event types do not include webhook receipt or rejection [backend/sql/migrations/0071_payment_p1.up.sql:96], so P2a's requested "webhook received" audit needs a small schema migration before implementation.

Reference behavior, paraphrased: one reference gates payment callback availability on compliance/configuration and required secret material before accepting provider traffic [QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/payment_webhook_availability.go:14]. Its channel-specific callback paths parse the raw request, verify provider proof, check event success state, resolve a local recharge order, then only credit when the local order is still pending [QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/topup_stripe.go:147] [QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/topup_creem.go:253] [QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/topup.go:309]. It also uses local locking/row-level checks around fulfillment to avoid repeat processing [QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/topup.go:80] [QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/topup.go:478].

Another reference uses a provider-neutral callback handler: the HTTP layer limits body size, selects candidate provider instances, asks the provider abstraction to verify and normalize the event, then passes a normalized payment notification to the service layer [Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/handler/payment_webhook_handler.go:70] [Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/payment/types.go:166]. Its service verifies provider identity, metadata fit, and paid amount before moving the local order into paid and starting fulfillment [Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_fulfillment.go:70]. Its fulfillment path treats already-completed orders as safe replay and uses a paid-to-processing state transition before applying value [Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_fulfillment.go:142] [Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_fulfillment.go:221]. HUAKAI should keep the provider-neutral abstraction and amount/provider checks, but reuse P1's append-only billing seam instead of copying any reference implementation.

## Scope

In scope:

- Add a public webhook route mounted from `internal/paymenthttp`, not from frozen `internal/gatewayhttp`, `internal/gateway`, or `internal/proto`.
- Add provider-side webhook verification and normalization for the existing test provider only.
- Accept a signed test-provider callback containing a trusted tenant id, local external order number, paid amount in cents, currency, event id, and timestamp.
- On valid proof: resolve the local order by tenant and external order number, compare provider kind, amount, and currency, call P1 `store.ConfirmPaid`, then call P1 `Service.Fulfill`.
- Add order-scoped audit for trusted webhook receipt, paid confirmation, rejection after valid proof, and crediting. Unverified traffic must not be written as order audit because the order id in an unsigned body is attacker-controlled.
- Write true PostgreSQL mutation-discriminating tests for legal callback, forged signature, replay, amount tamper, and cross-tenant isolation.

Out of scope:

- Stripe/Alipay/WeChat/epay/Airwallex SDKs, merchant credentials, real provider secrets, refunds, subscription grants, and production real-money activation.
- New balance table, independent crediting code, or import of `internal/billing` into `internal/payment`.
- Editing frozen packages `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`.
- Reading or depending on any Claude P2a plan.

## Package And File Plan

All new files below are outside frozen packages. `backend/internal/paymenthttp` currently has 1 non-test source file; `backend/internal/payment` currently has 9 non-test source files, so both remain below the package budget after P2a.

| File | Target package | Action | Frozen-package check | Responsibility |
| --- | --- | --- | --- | --- |
| `backend/internal/payment/webhook.go` | `payment` | Create | Not `gatewayhttp`, `gateway`, or `proto` | Define local webhook input/result types, verifier interface, service orchestration method, and webhook-specific errors. |
| `backend/internal/payment/test_provider_webhook.go` | `payment` | Create | Not frozen | Add HMAC verification/normalization behavior for the existing test provider. No real SDK or production secret. |
| `backend/internal/payment/audit.go` | `payment` | Modify | Not frozen | Add local audit constants for trusted webhook receipt and valid-proof rejection. |
| `backend/internal/payment/store.go` | `payment` | Modify | Not frozen | Add a store audit append method and extend the internal confirm record so system/webhook confirmation can reuse `ConfirmPaid` without pretending to be an admin. |
| `backend/internal/payment/store_postgres.go` | `payment` | Modify | Not frozen | Implement actor-aware confirmation audit and top-level order-scoped audit append using existing redaction. |
| `backend/internal/payment/store_memory.go` | `payment` | Modify | Not frozen | Keep unit tests aligned with the store contract; not money-path acceptance authority. |
| `backend/internal/payment/webhook_integration_test.go` | `payment` | Create test | Not frozen | True PG tests with signed callbacks and discriminating fixtures. |
| `backend/internal/paymenthttp/webhook.go` | `paymenthttp` | Create | Not frozen | Public raw-body HTTP endpoint, body limit, route mount helper, provider path parsing, and error-to-status mapping. |
| `backend/internal/paymenthttp/webhook_test.go` | `paymenthttp` | Create test | Not frozen | Fast handler tests for raw body forwarding, signature header preservation, and status mapping with a fake service. |
| `backend/cmd/gateway/routes.go` | `main` | Modify | Not frozen package | Mount `POST /v1/payments/webhooks/{provider_kind}` outside session/admin middleware. |
| `backend/cmd/gateway/wiring.go` | `main` | Possibly modify | Not frozen package | Only if Owner approves a dev/test runtime flag for registering the test provider in a running gateway; default production remains manual-only. |
| `backend/sql/migrations/0072_payment_p2a_webhook_audit.up.sql` | SQL migration | Create | Not Go package; high-risk DB schema | Extend `payment_audit_events` allowed event types for trusted webhook receipt and valid-proof rejection. |
| `backend/sql/migrations/0072_payment_p2a_webhook_audit.down.sql` | SQL migration | Create | Not Go package; high-risk DB schema | Symmetric rollback that refuses to run if P2a audit rows exist, unless Owner chooses a different rollback policy. |

The preferred route is `POST /v1/payments/webhooks/{provider_kind}`. It is public because payment providers cannot hold HUAKAI user/admin sessions; all trust comes from provider verification. The endpoint must use `http.MaxBytesReader` or equivalent 1 MiB cap, read raw bytes exactly once, and pass raw bytes plus normalized headers into the payment service.

## Provider Verification Interface Design

Use a local optional verifier extension rather than forcing every current provider to handle callbacks. The existing provider abstraction remains responsible for creating a payment intent [backend/internal/payment/provider.go:13]. P2a adds a small callback verifier contract implemented by the test provider and absent from manual provider.

Local design:

- `WebhookVerifyRequest`: provider kind, raw body bytes, headers as `map[string][]string`, receive time, and HUAKAI request id.
- `VerifiedWebhookEvent`: tenant id, external order number, paid amount cents, currency code, provider event id, provider kind, signed timestamp, and a redacted payload hash.
- `WebhookVerifier`: verifies authenticity and returns a `VerifiedWebhookEvent`. A provider that cannot verify callbacks is treated as unsupported.
- Test-provider HMAC: stdlib HMAC-SHA256 over a deterministic local envelope containing tenant id, external order number, amount cents, currency, event id, and timestamp. Verification uses constant-time comparison, rejects missing/old timestamps, and never accepts an unsigned tenant id.

This mirrors the reference-level shape of "raw callback -> provider proof -> normalized event" without importing any upstream names or SDK patterns [Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/payment/types.go:199] [Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/handler/payment_webhook_handler.go:173].

## P1 Reuse Points

P2a must not create a second entry path into `payment_credits` or `billing_events`. After verification and amount checks:

1. Call `GetOrderByOutTradeNo(ctx, tenantID, outTradeNo)` to resolve the tenant-scoped order. This relies on P1's tenant/order uniqueness [backend/sql/migrations/0071_payment_p1.up.sql:45].
2. Compare local `ProviderKind`, `AmountCents`, and `CurrencyCode` against the verified event before any state transition. This upgrades the reference behavior that checks provider and paid amount before fulfillment [Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_fulfillment.go:80] [Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_fulfillment.go:96].
3. Append `webhook_received` audit only after proof passes and the local order is resolved.
4. Call `store.ConfirmPaid` with actor kind `system`, actor id empty, reason class `webhook_confirmed:<provider_kind>`, and request id set to the provider event id or HTTP request id. P1's current confirm path already CASes pending to paid and treats later states idempotently [backend/internal/payment/store_postgres.go:158].
5. Call `Service.Fulfill` with actor kind `system`. P1's two-phase fulfill already handles recharging retry and completed replay [backend/internal/payment/store_postgres.go:213] [backend/internal/payment/store_postgres.go:296].

## Idempotency Model

P2a has three monetary idempotency layers:

- External order number is unique per tenant, so a callback resolves to one tenant-scoped order, not a global ambiguous match [backend/sql/migrations/0071_payment_p1.up.sql:45].
- `ConfirmPaid` is a CAS/idempotent state transition: pending moves to paid, already paid/recharging/completed returns safely, terminal rejected states do not credit [backend/internal/payment/store_postgres.go:144].
- `Fulfill` writes at most one credit per order and returns the existing credit on completed replay [backend/sql/migrations/0071_payment_p1.up.sql:74] [backend/internal/payment/store_postgres.go:296].

Replay behavior for P2a: the same valid callback may be received more than once; it must return a success-class response and report idempotent fulfillment, but must not create a second credit, second billing event, or second paid-confirmed audit. A repeated `webhook_received` audit row is acceptable in P2a because it records actual traffic, but durable event-id de-dup is an Owner decision for P-RealMoney unless Owner wants a new event ledger in P2a.

## Security Controls

Forged signature:

- HTTP layer passes raw body bytes to the provider verifier without JSON normalization.
- Test provider verifies HMAC with constant-time comparison and rejects missing, malformed, or stale timestamp/signature.
- On failure: return 401/400, do not resolve order, do not call `ConfirmPaid`, do not call `Fulfill`, and do not write order audit. Write only redacted security logs because unsigned order ids are not trustworthy.

Tampered amount:

- A validly signed but wrong amount is still rejected because the service compares `PaidAmountCents` and currency against the local order before `ConfirmPaid`.
- On mismatch: append order-scoped `webhook_rejected` audit with expected/received cents and currency after proof and order resolution, return a rejection status, and leave order/credits/billing unchanged.

Replay:

- HMAC timestamp rejects stale bodies.
- Within the allowed timestamp window, replay is handled by P1 idempotency: completed orders return existing credit and no second billing event.
- Replay is visible in metrics/audit but not money-impacting. Durable provider-event de-dup would require a new unique event table or unique audit key; keep that as an Owner decision rather than silently adding schema.

Cross-tenant attack:

- Tenant id must be inside the signed provider envelope for P2a test provider. The service must never accept tenant id from URL query, path, or unsigned JSON.
- Lookup must include tenant id and external order number. Two tenants may have the same external order number; the signed tenant chooses the namespace.
- P-RealMoney must either carry tenant id in signed provider metadata or switch to globally unique external order numbers before real SDK activation. This is an explicit P-RealMoney carry-forward.

## Mutation-Discriminating True PG Tests

All money-path tests must run with `-tags=integration_pg` against PostgreSQL. Memory tests are allowed only for fast validation; they do not prove the money path.

| Test | Defect guarded | Discriminating fixture | Expected result | Mutation that must turn red |
| --- | --- | --- | --- | --- |
| Legal signed callback credits exactly once | Missing webhook-to-P1 orchestration or skipped audit | Create pending test-provider order for tenant A/user A, sign body with matching tenant, external order number, cents, currency, event id | HTTP 200; order completed; one credit; one `payment_credited`; balance delta equals cents; audit includes trusted receipt, paid confirmation, and credited | If `Fulfill` is skipped, balance/event missing; if audit append skipped, audit assertion fails |
| Forged signature rejects zero credit | Bypassing verifier or trusting unsigned body | Same pending order, same body, invalid HMAC header | 401/400; order remains pending; zero credit; zero payment billing event; no paid/credited audit | If verifier is bypassed, order credits and test fails |
| Replay is idempotent | Duplicate callback double-credit | Send the same valid signed callback twice for the same order | Both calls success-class; exactly one credit/event; balance equals one amount; second service result marks idempotent | If credit uniqueness or completed replay is broken, counts/balance exceed one |
| Amount tamper rejects | Signed callback amount not compared to local order | Create order for 5000 cents, sign body for same order with 4900 cents | Rejection response; order not completed; zero credit/event; `webhook_rejected` audit records mismatch | If amount comparison is removed, order credits with wrong provider proof |
| Cross-tenant isolation | Lookup by external order number without tenant predicate | Tenant A and B create same external order number and same amount; sign callback for tenant B | Only tenant B completes/credits; tenant A remains pending; A balance zero; B balance exact amount | If lookup omits tenant id, tenant A may receive B's callback and test fails |

Additional non-PG handler tests:

- Raw body is passed unchanged to service and body size is capped.
- Missing provider path returns deterministic safe error and does not call service.
- Payment service error mapping distinguishes invalid signature, business mismatch, replay success, and backend failure.

## Concrete Execution Order

- [ ] Confirm Owner decisions in the Open Points section before touching schema or runtime provider enablement.
- [ ] Write `backend/internal/payment/webhook_integration_test.go` with the five PG tests above; run the first test and verify it fails because no webhook service exists.
- [ ] Add local webhook verifier types and service orchestration in `backend/internal/payment/webhook.go`; keep it independent of HTTP.
- [ ] Add test-provider HMAC signing/verifying in `backend/internal/payment/test_provider_webhook.go`; use stdlib crypto only.
- [ ] Extend store audit/confirmation contract so webhook confirmation records actor kind `system` while admin confirmation remains actor kind `admin`.
- [ ] Add migration 0072 for the new payment audit event types after Owner confirms schema change.
- [ ] Implement Postgres and memory store changes; rerun payment package unit tests and the targeted PG tests.
- [ ] Add `backend/internal/paymenthttp/webhook.go` and handler tests; verify raw-body and status mapping.
- [ ] Mount the public route in `backend/cmd/gateway/routes.go`; do not touch frozen `gatewayhttp`, `gateway`, or `proto`.
- [ ] If Owner approves a dev-only runtime flag, update `wiring.go` to register the test provider only when explicitly enabled; otherwise leave runtime default manual-only and rely on tests for P2a.
- [ ] Run verification: `go test ./internal/payment ./internal/paymenthttp` and `HUAKAI_DATABASE_URL=... go test -tags=integration_pg ./internal/payment -run PaymentWebhook`.
- [ ] Stage intended files only and run `codex exec review --uncommitted --full-auto --sandbox read-only` before any commit.

## Blast Radius And Mitigations

- Public unauthenticated route: mitigate with provider proof, raw body cap, no session trust, and no order audit for unverified traffic.
- Money path: mitigate by reusing P1 `ConfirmPaid` and `Fulfill`; no direct writes to credit or billing tables outside P1 store.
- Audit schema: migration 0072 changes a money-path audit enum and is high-risk under project rules; Owner sign-off required before implementation.
- Test provider exposure: default production wiring remains disabled. Runtime enablement needs an explicit dev/test flag or separate test-only construction.
- Clean-room: this plan is specifier-lane only. Implementer should read this plan and HUAKAI code, not the reference source.

## Fusion-Upgrade Delta

Architecture:

- HUAKAI keeps `internal/paymenthttp` as a small public ingress package and `internal/payment` as the provider-neutral decision/money service. This follows the reusable callback-shape observed in one reference without copying its handler layout [Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/handler/payment_webhook_handler.go:70]. It improves the more channel-specific callback shape observed in another reference by making the local service path provider-neutral from P2a [QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/topup_stripe.go:176].

Algorithm:

- HUAKAI's local algorithm is proof-first, tenant-scoped, amount-checked, then P1 CAS and two-phase fulfillment. This combines reference-observed amount/provider validation [Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_fulfillment.go:80] with HUAKAI's stronger append-only billing seam and one-credit-per-order invariant [backend/internal/payment/store_postgres.go:321].

Ecosystem:

- P2a ships a safe simulated provider and acceptance tests now, while real SDKs and credentials remain P-RealMoney Owner-gated. This preserves the feature outcome without importing real-money supply-chain risk into the current slice.

## Open Points For Owner

1. Approve or reject migration 0072 for new payment audit event types. Without it, "webhook received" can only be a structured log, which would shrink the requested audit behavior.
2. Choose callback ack policy for valid proof but business rejection: strict 4xx/409 for amount mismatch and unknown order, or 200-with-rejection-log to stop provider retries. I recommend 409 for amount mismatch in P2a tests and 200 for unknown order only after real provider retry semantics are specified.
3. Decide whether the test provider is test-only or has a dev-only runtime flag. I recommend test-only unless Owner needs manual local callback demos.
4. Confirm that signed tenant id is acceptable for P2a test provider. For P-RealMoney, each real provider must carry trusted tenant identity in signed metadata or HUAKAI must move external order numbers to global uniqueness before activation.
5. Decide whether durable provider-event de-dup is required in P2a. I recommend deferring it to P-RealMoney because P1 already prevents double credit; adding a callback-event table now expands schema blast radius.

## Clean-Room Self-Check

- No upstream source code, comments, schema, file structure, or tests are copied into this plan.
- Upstream behavior is paraphrased at the workflow and risk-control level only.
- Local file names and local method names are HUAKAI-owned P1 surfaces already present in this repository.
- Every reference-project behavior claim above has a source citation.
- The plan preserves the requested feature. Real-money SDK work is not dropped; it is explicitly gated to P-RealMoney as requested.

Source files read: HUAKAI `docs/RULES.md`; `docs/05_CLEAN_ROOM_POLICY.md`; `docs/11_ACCEPTANCE_TEST_MATRIX.md`; `backend/internal/payment/store.go`; `backend/internal/payment/service.go`; `backend/internal/payment/provider.go`; `backend/internal/payment/types.go`; `backend/internal/payment/audit.go`; `backend/internal/payment/store_postgres.go`; `backend/internal/payment/store_memory.go`; `backend/internal/payment/store_postgres_integration_test.go`; `backend/internal/payment/service_test.go`; `backend/internal/payment/idempotency.go`; `backend/internal/payment/privacy.go`; `backend/internal/payment/test_helpers_test.go`; `backend/internal/paymenthttp/handler.go`; `backend/cmd/gateway/routes.go`; `backend/cmd/gateway/wiring.go`; `backend/sql/migrations/0071_payment_p1.up.sql`; `docs/process/plans/2026-05-29-payment-p1-synthesis.md`. Reference `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/payment_webhook_availability.go`; `controller/topup.go`; `controller/topup_stripe.go`; `controller/topup_creem.go`; `controller/topup_waffo.go`; `controller/topup_waffo_pancake.go`; `service/webhook.go`; `model/topup.go`. Reference `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_order.go`; `backend/internal/service/payment_fulfillment.go`; `backend/internal/service/admin_service.go`; `backend/internal/handler/payment_webhook_handler.go`; `backend/internal/handler/admin/payment_handler.go`; `backend/internal/payment/types.go`; `backend/internal/payment/registry.go`; `backend/internal/payment/provider/stripe.go`; `backend/internal/payment/provider/easypay.go`; `backend/internal/payment/provider/airwallex.go`; `backend/internal/service/payment_fulfillment_order_not_found_test.go`.
Lane: specifier
Agent: GPT-5 Codex, current Codex session
UTC timestamp: 2026-05-29T05:07:07Z
