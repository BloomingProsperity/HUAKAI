# 2026-06-05 refund available balance guard - Codex plan

| Owner directive | "退款可用余额守卫 + 审批原子性(MONEY S1/S2,审计抓的真缺陷,极保守)" |
| --- | --- |
| Scope | In: HUAKAI-only clean-room implementation for PG refund available-balance enforcement, refund HTTP error mapping, and PG refund-request reject guard. Out: reading `/home/ubuntu/refs`, schema migrations, auth/billing/quota core rewrites, external transaction refactor for `RefundOrder`, frontend work, push. |
| Success criteria | Failing-first tests prove: full refund above `balance - held` returns `ErrRefundExceedsAvailable` with no refund/billing/order-status/balance writes; refund at/under available succeeds; held reduces availability; reject after the refund fact exists returns `ErrRefundRequestAlreadyResolved`; pure pending reject still works. Required gates pass before commit. |
| Time estimate | 2-4 wall-clock hours, mostly integration test and review time. |
| Blast radius | Money path for completed top-up refunds and admin refund-request decisions. Failure can block legitimate refunds, permit negative balances, or present false approval/rejection status. |
| Failure modes | Wrong SQL numeric comparison could reject valid refunds or allow negative balances; mitigation: integration tests cover under/edge/held. Checking request status only could miss refund fact; mitigation: PG test inserts/creates refund fact while request remains pending. Adding guard after refund/billing writes would leave partial facts; mitigation: test asserts zero writes and unchanged order/balance. Error mapping could fall through to 503; mitigation: HTTP unit test or existing handler test extension asserts error code. |
| Decision points | No Owner sign-off needed for low/medium-risk tests and small implementation in existing files. Stop for Owner confirmation before schema changes, runtime dependency additions, auth/billing ledger/quota-core rewrites, or destructive commands. |
| Pre-execution checklist | 1. Confirm clean-room: only HUAKAI repo read, no `/home/ubuntu/refs`. 2. Confirm target packages are not frozen-new-file changes. 3. Read existing refund transaction and request recorder behavior. 4. Write failing tests before production edits. 5. Keep function/file sizes within project limits. 6. Run required gates with `/usr/local/go/bin/go` and `GOCACHE=/tmp/go-build`. 7. Stage intended diff, run Codex review, then commit without push. |

## Concrete Execution Order

1. Add S1 PG integration tests in `backend/internal/payment/store_postgres_refund_integration_test.go`.
   - Scenario A: seed completed $100 top-up, manually reduce `user_balances.balance` to $70 to represent consumed funds, attempt $100 refund, expect `ErrRefundExceedsAvailable`.
   - Assert rollback evidence: `payment_refunds=0`, `payment_refunded billing_events=0`, `payment_orders.status='completed'`, `user_balances.balance` unchanged at $70, derived balance unchanged at $100 because no refund fact was written.
   - Scenario B: refund exactly available after balance is $70, expect success and `user_balances.balance=0`.
   - Scenario C: set `balance=100`, `held=30`, attempt $80 refund, expect `ErrRefundExceedsAvailable`; refund $70 succeeds.
2. Run targeted S1 test and verify it fails for the right reason before production changes.
3. Add S2 PG integration test in `backend/internal/paymenthttp/refund_request_postgres_integration_test.go`.
   - Create pending refund request for a completed order.
   - Execute `svc.RefundOrder` directly with idempotency key `refund-req:<requestID>` to simulate crash after money transaction but before request status update.
   - Call `RejectRefundRequest` and expect `ErrRefundRequestAlreadyResolved`.
   - Assert request remains pending and refund fact still exists.
   - Keep existing pure pending reject assertion as the positive reject path.
4. Run targeted S2 test and verify it fails for the right reason before production changes.
5. Add `payment.ErrRefundExceedsAvailable` in `backend/internal/payment/types.go` and map it in `backend/internal/paymenthttp/handler.go` to `409 refund_exceeds_available`.
6. Implement a refund-only balance guard in `backend/internal/payment/store_postgres_refund.go`.
   - Do not change `syncLegacyUserBalanceDeltaTx`, because credit paths reuse it.
   - Before inserting refund/billing facts, run a conditional `UPDATE user_balances SET balance = balance - $refund, version = version + 1, updated_at = $now WHERE tenant_id=$1 AND user_id=$2 AND balance - held >= $refund`.
   - If affected rows is zero, return `ErrRefundExceedsAvailable` and let the refund transaction roll back.
   - Remove the later refund call to the unconditional delta function for the non-idempotent path.
7. Add `paymenthttp.ErrRefundRequestAlreadyResolved` in `backend/internal/paymenthttp/refund_request_admin.go` and map it through `writeRefundRequestError` as `409 refund_request_already_resolved`.
8. Implement a PG-only reject guard in `backend/internal/paymenthttp/refund_request_postgres.go`.
   - While holding the pending request row lock, query for an existing `payment_refunds` row with idempotency key `refundRequestIdempotencyKey(req.ID)` and same tenant.
   - If present, return `ErrRefundRequestAlreadyResolved` without changing request status.
   - Optionally also treat the linked order already in `refunded` state as resolved if that path can be checked inside the same transaction without broadening scope.
9. Run `gofmt -w` on touched Go files.
10. Run targeted tests until green, then full required gates:
    - `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./...`
    - `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go vet ./internal/payment/... ./internal/paymenthttp/...`
    - `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go test ./internal/payment/... ./internal/paymenthttp/... -count=1`
    - If integration tests require `-tags integration_pg`, run the targeted PG tests with the available database DSN.
11. Stage intended diff and run `codex exec review --uncommitted --full-auto --sandbox read-only` from repo root if the local CLI supports it.
12. Commit after gates and review, with `Co-Authored-By: Codex (HUAKAI worker) <noreply@huakai.local>` and no push.
13. Perform mutation checks after commit:
    - Temporarily remove the SQL `balance - held >= refund` condition; targeted S1 test must fail; restore with `git checkout --` or equivalent non-destructive file restore for only touched files.
    - Temporarily remove the reject refund-fact check; targeted S2 test must fail; restore.
14. Report changed files, gates, commit SHA, discriminating tests, mutation outcomes, clean-room/security/feature-shrink status, and Owner confirmation needs in Chinese.

## Clean-Room Note

This plan is based only on the Owner brief and HUAKAI repository files read in this worktree. No reference-project source paths were read or cited.
