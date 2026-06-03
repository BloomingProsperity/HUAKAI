# 2026-06-03 Quota Fail Open Codex Plan

| Owner directive | "TASK (Owner decision): change quota hot-path enforcement from FAIL-CLOSED to FAIL-OPEN on quota INFRASTRUCTURE errors..." |
| Scope | In: existing gateway hot-path quota reserve branch, existing quota settler decorator, focused discriminating tests, local verification. Out: schema changes, auth/billing ledger rewrites, quota deny semantics, new files in frozen packages, commits. |
| Success criteria | Non-deny quota reserve errors with enforcement on proceed without aborting the billing claim, emit a warning and `quota_reserve_failed_open_total`, and never return the previous 500. Genuine quota deny still returns 429 and aborts as `quota_denied`. Quota settler `Settle` and `CommitCacheHit` treat `quota.ErrReservationNotFound` as successful no-op after billing succeeds. Required build, vet, and test commands complete or blockers are recorded. |
| Time estimate | 1 hour wall clock; one Codex implementation and verification pass. |
| Blast radius | Gateway chat-completions admission control, money/quota consistency during quota-store outages, quota finalization after fail-open requests. |
| Failure modes | Accidentally fail-open genuine quota denies: preserve and test the deny branch before infra-error branch. Accidentally abort billing claims on infra errors: test settler abort count and fail-open response. Secret leakage in logs: log only tenant/request/claim IDs and error class string, no request body, payload, API key, or provider secret. Finalizer missing-reservation errors could still bubble after fail-open: add direct decorator tests. |
| Decision points | Owner has already authorized the quota hot-path policy change. No new runtime dependency, schema change, auth core change, billing ledger change, quota enforcement schema change, or frozen-package new file is planned. |
| Pre-execution checklist | Confirm working tree and branch. Read current `reserveQuota`, deny tests, `quotaenforce.Settler`, and test stubs. Write failing tests before production edits. Use existing package patterns. Run targeted red/green tests, mutation checks, then required build/vet/test commands. |

## Concrete Execution Order

1. Add a gateway test for non-deny quota reserve error fail-open: expect not-500, no billing abort, provider path reached, and expvar signal incremented.
2. Confirm existing quota deny test remains discriminating; adjust only if the fail-open branch could hide it.
3. Add quotaenforce decorator tests for missing reservation on `Settle` and `CommitCacheHit`.
4. Run the new tests and confirm the intended failures.
5. Add the fail-open expvar signal and warning log in the existing gateway dispatch file.
6. Update `quotaenforce.Settler` to ignore `quota.ErrReservationNotFound` for `Settle` and `CommitCacheHit`.
7. Run targeted tests and mutation checks.
8. Run `cd backend && go build ./...`, `go vet ./cmd/gateway/... ./internal/quotaenforce/...`, and `go test ./cmd/gateway/... ./internal/quotaenforce/... ./internal/gatewayhttp/... 2>&1 | tail -20`.
