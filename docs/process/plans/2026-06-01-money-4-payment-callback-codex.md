# MONEY-4 Payment Callback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` to implement this plan task-by-task. This plan follows HUAKAI `AGENTS.md` plan-before-execute rules; it is saved under `docs/process/plans/` per repo policy.

**Goal:** Implement verified payment callback fulfillment for PENDING recharge orders: valid callbacks complete one order and credit `user_balances` exactly once; replay, amount tampering, and bad signatures do not double-credit.

**Architecture:** Keep payment logic in the non-frozen `backend/internal/payment` package. Use PostgreSQL serializable transactions and DB compare-and-swap status transitions, not process-local locks. Extend `billing_events` and add a redacted `payment_audit_log` table so recharge credits are visible in the append-only money event stream without storing raw webhook bodies or signatures.

**Tech Stack:** Go, pgx, PostgreSQL migrations, `shopspring/decimal`, existing HUAKAI `user_balances`, `recharge_orders`, and `billing_events`.

---

| Owner directive | "Do ONE task now... MONEY-4 支付回调验签+幂等 PAID 转移+履约入账(复用桥接 credit 原语)" |
| Scope | In: `internal/payment` callback verification, fulfillment, balance credit primitive, payment audit log migration, billing event CHECK extension, discriminating unit/integration tests. Out: real third-party provider SDKs, frozen `gatewayhttp/gateway/proto` files, production deployment, landing merge. |
| Success criteria | Valid signed callback for a $50 PENDING order sets order to `COMPLETED`, increases `user_balances.balance` by `50.00000000`, and writes one `billing_events.balance_recharged` row. Replaying the same callback is a no-op with balance still $50. A $5 callback for a $50 order is rejected, leaves balance unchanged, and writes `PAYMENT_AMOUNT_MISMATCH`. Bad signature maps to provider-retry HTTP 400 behavior. |
| Time estimate | 4-6 wall-clock hours including tests, migrations, Codex review, and push. Integration DB availability may add time. |
| Blast radius | Money path and schema. Runtime code stays in non-frozen `backend/internal/payment`; migrations touch `payment_audit_log` and `billing_events` constraints. No route wiring unless a later audited task expands scope. |
| Failure modes | Missing status guard can double-credit replay; amount comparison bug can credit underpayment; audit table could log raw secrets; CHECK migration could reject existing billing rows; currency or decimal scale mismatch could corrupt balances; clean-room leakage from non-MIT references. Mitigation: TDD with replay and underpayment mutation tests, no raw payload persistence, migration mirrors existing `0023` constraint style, USD-only validation, no reference-source re-read in implementation lane. |
| Decision points | Board assignment plus successful `task.sh start MONEY-4` authorizes implementation and push for review only. Because this is money/schema risk, dispatcher/Owner still control landing and approval; worker will not mark done or deploy. If HTTP route mounting becomes required beyond the listed scope files, park or create a follow-up task rather than editing unclaimed route files. |
| Pre-execution checklist | Read `CLAUDE.md` #8/#11/#12/#13/#14, `.coordination/DISPATCH.md`, task contract, and existing money-loop plans. Start task and claim files. Keep citations out of code comments. Write failing tests before implementation. Run targeted tests, integration tests when DB is available, migration checks when possible, Codex review, commit, push `HEAD:work/money-4`, then `task.sh review MONEY-4 "branch work/money-4 @ <sha> + self-review result"`. |

## File Scope

- Create `backend/internal/payment/credit.go`: HUAKAI-owned `creditUserBalanceTx` helper using `INSERT ... ON CONFLICT (tenant_id,user_id) DO UPDATE` with `balance += amount`, `version += 1`, and `updated_at`.
- Create `backend/internal/payment/callback.go`: signed mock-provider callback input, canonical signature verification, amount/currency/order reference validation, and provider-retry status mapping.
- Create `backend/internal/payment/fulfillment.go`: service method `HandleCallback(ctx, CallbackInput)` and result types. This layer calls the store after validation.
- Modify `backend/internal/payment/order.go`: add callback/fulfillment errors and result/status constants only.
- Modify `backend/internal/payment/store_postgres.go`: add serializable `FulfillCallback` transaction that locks the order, writes redacted audit rows, uses status guard `WHERE status='PENDING'`, credits balance, writes `billing_events.balance_recharged`, and marks order `COMPLETED`.
- Add tests under `backend/internal/payment/`: unit tests for signature/canonical amount validation and integration tests for valid callback, replay no-op, amount mismatch audit, and bad signature mapping.
- Create `backend/sql/migrations/0062_payment_audit_log.up.sql` and `.down.sql`.
- Create `backend/sql/migrations/0063_billing_events_balance_recharged.up.sql` and `.down.sql`.

`backend/internal/payment` is not frozen. `backend/sql/migrations` is expected for this task but is schema/high-risk, so landing remains dispatcher/Owner-gated. Do not add files under frozen `backend/internal/{gatewayhttp,gateway,proto}`.

## Clean-Room Guard

This implementation session will not re-read LGPL/AGPL reference source. The behavior contract comes from the already assigned task and existing source-cited money-loop plans. Do not use forbidden upstream identifiers as Go symbols or comments. Do not vendor reference code. Commit/review notes may include reference citations if needed; code comments must describe HUAKAI behavior only.

## Execution Steps

- [ ] Write callback unit tests:
  - `TestVerifyMockCallbackRejectsBadSignature`: same body and timestamp with a wrong signature returns `ErrInvalidSignature` and `CallbackResult.HTTPStatus == 400`.
  - `TestVerifyMockCallbackRejectsAmountMismatchBeforeStore`: expected order amount $50 and callback paid amount $5 returns `ErrPaymentAmountMismatch`.
  - Mutation checks: bypass signature compare makes the first test red; bypass amount compare makes the second test red.

- [ ] Write integration tests in `store_postgres_integration_test.go` or a focused `callback_integration_test.go`:
  - Seed active tenant/user and a $50 PENDING order through existing `OpenRecharge`.
  - Valid signed callback: assert `recharge_orders.status='COMPLETED'`, `user_balances.balance='50.00000000'`, exactly one `billing_events` row with `event_type='balance_recharged'`, and one successful audit row.
  - Replay same callback: assert result idempotent/no-op, balance remains `50.00000000`, and `balance_recharged` count remains 1.
  - Amount mismatch: use the same $50 order with paid amount `$5`; assert error `ErrPaymentAmountMismatch`, balance row absent or zero, and audit reason `PAYMENT_AMOUNT_MISMATCH`.
  - Mutation checks: deleting `WHERE status='PENDING'` or allowing non-pending transitions must double-credit replay and turn the replay test red; deleting the amount comparison must turn the mismatch test red.

- [ ] Run expected-red targeted tests:
  - `cd backend && go test ./internal/payment`
  - `cd backend && go test -tags=integration_pg ./internal/payment` when `HUAKAI_DATABASE_URL` is set.

- [ ] Implement `callback.go`:
  - Canonical string: `tenant_id + "\n" + external_trade_no + "\n" + paid_amount + "\n" + currency_code + "\n" + provider_event_id + "\n" + timestamp_unix`.
  - Signature: HMAC-SHA256 hex using a task-local mock provider secret.
  - Normalize currency to uppercase and require USD.
  - Reject stale/zero timestamps only if testable without wall-clock flake; otherwise keep timestamp in signature only for this slice.

- [ ] Implement `credit.go`:
  - Validate positive USD decimal with existing `fitsMoneyColumn`.
  - `INSERT INTO user_balances ... ON CONFLICT ... DO UPDATE` matching MONEY-1 behavior.
  - Return new balance for assertions and future callers.

- [ ] Implement `fulfillment.go` and store transaction:
  - Lock order by `(tenant_id, external_trade_no)` for update.
  - If order is already `COMPLETED`, return idempotent result without crediting.
  - If order is not `PENDING` or expected amount/currency/provider does not match, write audit failure and return a typed error.
  - On valid PENDING order, update to `PAID` with `WHERE status='PENDING'`; if 0 rows, reload and treat `COMPLETED` as replay no-op.
  - Credit balance once, insert `billing_events.balance_recharged`, update order to `COMPLETED`, and write success audit in the same transaction.

- [ ] Add migrations:
  - `0062_payment_audit_log.up.sql`: `payment_audit_log` with tenant/order/user ids, external trade id, provider event id, outcome, reason, redacted metadata, and timestamps. Provider event id is support correlation only; idempotency stays on the recharge order DB-CAS status guard so replay audits can be recorded without a unique-conflict failure.
  - `0063_billing_events_balance_recharged.up.sql`: add nullable `recharge_order_id`, FK `(tenant_id,recharge_order_id)` to `recharge_orders`, extend `billing_events_event_type_check`, and replace `billing_events_claim_or_voucher_check` so `balance_recharged` requires `claim_id IS NULL`, `voucher_redemption_id IS NULL`, and `recharge_order_id IS NOT NULL`.
  - Down migrations restore prior constraints and drop additive objects in reverse order.

- [ ] Run verification:
  - `cd backend && go test ./internal/payment`
  - `cd backend && go test -tags=integration_pg ./internal/payment` if DB is configured.
  - `cd backend && go test ./internal/voucher ./internal/billing` to catch CHECK/credit regressions where possible.
  - Migration up/down parsing or local round-trip if DB tooling/DSN is available; otherwise record why skipped.

- [ ] Stage intended diff and run the required review:
  - `codex exec review --uncommitted -m gpt-5.5 -c model_reasoning_effort=xhigh`
  - Normalize findings to S0/S1/S2/S3. Commit only if no unresolved S0/S1.

- [ ] Commit and hand off:
  - Commit with clean-room/parity notes and deferred findings if any.
  - Push exact commit: `git push origin HEAD:work/money-4`.
  - Mark review: `bash .coordination/task.sh review MONEY-4 "branch work/money-4 @ <sha> + self-review result"`.

## Self-Review

- Spec coverage: valid callback, replay idempotency, underpayment rejection, signature failure, audit, balance credit, and `billing_events.balance_recharged` are all mapped to tests and implementation steps.
- Placeholder scan: no TBD/TODO placeholders remain in this plan.
- Package discipline: new files are in non-frozen `internal/payment`; no frozen package additions.
- Clean-room: no reference-source re-read planned in this implementation lane.
