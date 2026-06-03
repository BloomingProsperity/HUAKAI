# 2026-06-03 tenant retry budget - Codex plan

| Owner directive | "实现+验证...per-tenant 重试预算上限...默认禁用(预算=0 → 不限,行为与现在完全一致)" |
| Scope | Add a process-local per-tenant upstream retry budget, env wiring, and discriminating tests. In scope: `backend/internal/retrybudget`, existing `backend/internal/gatewayhttp` retry loop file/test, non-frozen `backend/cmd/gateway` wiring/routes/tests. Out of scope: DB schema, auth, billing ledger, quota enforcement, production deployment, git commit. |
| REFERENCE PROJECTS IN SCOPE | CLIProxyAPI + sub2api + new-api are listed per AGENTS.md default-mirror rule; this Codex patch does not read or summarize non-HUAKAI reference source and makes no reference-project behavior claims. Domain context named by Owner: LiteLLM/CLIProxy-style retry amplification risk. |
| Success criteria | Budget `0` is behaviorally unlimited; configured budget counts only retry edges, not the initial attempt; tenant A exhaustion does not affect tenant B; the retry loop stops before launching an over-budget upstream retry and returns the last classified failure; `go build`, `go vet`, and targeted tests pass. |
| Time estimate | 60-90 minutes wall clock; one Codex implementation session. |
| Blast radius | Gateway retry/failover hot path and gateway boot wiring. Default-off env keeps current behavior unless configured. |
| Failure modes | Off-by-one could charge first attempt or allow one extra retry; shared key bug could cross-contaminate tenants; invalid env could silently disable protection; mutex mistakes could race under concurrent retries. Mitigation: tests for budget=2, budget=0, tenant isolation, first-attempt no charge, env parse rejection, and concurrent Allow cap. |
| Decision points | No Owner sign-off needed unless implementation requires DB schema, quota/auth/billing core changes, runtime dependency, or frozen-package new files. Current plan avoids all of those. |
| Pre-execution checklist | 1. Read `CLAUDE.md` and `AGENTS.md`. 2. Confirm retry loop file:line and existing rate/quota packages. 3. Claim edit files via `.coordination`. 4. Write failing tests first. 5. Implement minimal code. 6. Run requested verification gate. 7. Release coordination lock. |

## Concrete execution order

1. Add `backend/internal/retrybudget/budget_test.go` with RED tests for disabled budget, per-tenant isolation/window behavior, and concurrency cap.
2. Add RED gateway retry/failover tests to existing `backend/internal/gatewayhttp/chat_completions_retry_failover_test.go`: budget=2 stops before the third retry, tenant B still succeeds with the same shared budget, budget=0 reaches the legacy later success, and a first-attempt success leaves a one-token budget available.
3. Add RED cmd/gateway wiring tests for env defaults, configured limit/window, invalid budget, and invalid/non-positive window.
4. Implement `backend/internal/retrybudget/budget.go` as a small mutex-protected sliding-window timestamp counter. `limit <= 0` returns true without recording.
5. Add a `RetryBudget` interface field to `ChatHandlerDeps`; in `NewChatCompletionsHandler`, call `Allow(ident.TenantID)` only after `shouldRetryAttemptFailure` returns retry=true and before `prepareNextAttemptAfterAbort`.
6. Parse `HUAKAI_TENANT_RETRY_BUDGET` and `HUAKAI_TENANT_RETRY_WINDOW` in `cmd/gateway/wiring.go`; construct the budget during runtime build; inject it through `routes.go`.
7. Run focused RED/GREEN tests, then full requested gate: `cd backend && go build ./... && go vet ./... && go test ./internal/retrybudget/... ./internal/gatewayhttp/... ./cmd/gateway/... 2>&1 | tail -16`.

## Clean-room and structure notes

- No non-HUAKAI source is copied or translated.
- New implementation package is `backend/internal/retrybudget`, which is not frozen.
- Frozen package `backend/internal/gatewayhttp` gets only edits to existing files; no new files are added there.
- No runtime dependency, schema, auth, billing ledger, quota enforcement, secrets, or deployment files are changed.
