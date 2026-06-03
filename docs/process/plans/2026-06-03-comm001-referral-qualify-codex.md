# 2026-06-03 COMM001 Referral Qualify Codex Plan

| Owner directive | "目标 F-COMM-001 GAP-1:返佣 referral qualify 触发器(最小闭环)." |
| Scope | In: verify existing referrals schema, registration pending write, and receipt/settlement hook; add `QualifyPendingReferral(ctx, tenantID, refereeUserID, billingEventID)` in non-frozen `backend/internal/community/invitation`; call it best-effort after successful settlement through the existing receipt hook wrapper; add discriminating unit tests and mutation proof. Out: reward issuance, `tier_progress`, schema changes, billing amount/quota/auth changes, git commit. |
| Success criteria | Pending referral for the settled referee changes to `qualified` and records `first_billing_event_id`; no pending row is a no-op; already qualified row stays unchanged; deleting the settle-time qualify call makes the hook test red; requested build/vet/test gate runs. |
| Time estimate | 45-75 minutes wall time; one Codex implementation pass plus verification. |
| Blast radius | Money-adjacent path after Tx2 settlement; expected blast is limited to a best-effort referral status update after billing success. Billing ledger, debit amount, reward ledger, quota enforcement, auth, and DB schema must not change. |
| Failure modes | Wrong hook point could qualify before a durable billing event: mitigate by using `DefaultSettler.Settle` result after successful commit. Qualifier failure could block billing: mitigate by logging/reporting and returning the original settle success. Weak tests could pass without the hook: mitigate with mutation run deleting the hook call. Structure rule breach: add new files only under non-frozen `internal/community/invitation`; modify existing files elsewhere. |
| Decision points | PARK for Owner: confirm that the existing receipt-settle wrapper is the correct first-billing trigger point for COMM001, and whether zero-cost cache-hit settlements should later qualify referrals. Current slice qualifies only `Settle` results with a positive billing event id; no reward/tier side effects. |
| Pre-execution checklist | 1. Read `AGENTS.md` and root `CLAUDE.md` because `backend/CLAUDE.md` is absent. 2. Verify schema and registration references with file:line. 3. Verify `user_cost_receipts` write path and settlement wrapper with file:line. 4. Claim edit files via `.coordination`. 5. Write failing tests before production changes. 6. Implement minimal qualification and best-effort hook. 7. Run focused tests, mutation proof, then requested gate. |

## Concrete Execution Order

1. Add RED community tests for pending/no-pending/already-qualified referral qualification using a discriminating in-memory store.
2. Add RED audit hook test proving settle-time qualification receives `(tenant_id, user_id, billing_event_id)` after successful settlement.
3. Implement `Service.QualifyPendingReferral` and `PostgresStore.QualifyPendingReferral`.
4. Return `BillingEventID`, `TenantID`, and `UserID` from successful `DefaultSettler.Settle`.
5. Add a receipt-hook settler option for a referral qualifier and wire it in `cmd/gateway/middleware.go`.
6. Run focused tests; temporarily remove the hook call and rerun the hook test to prove RED; restore and rerun.
7. Run `cd backend && go build ./... && go vet ./... && go test ./internal/community/... ./internal/billing/... ./internal/gatewayhttp/... ./cmd/gateway/... 2>&1 | tail -15`.

## Reference/Clean-Room Note

No non-HUAKAI reference source is read or summarized in this plan. This is an internal GAP patch against existing HUAKAI schema and billing/receipt code, so the project source-not-required carve-out applies; no reference-project capability or mechanism claim is made here.
