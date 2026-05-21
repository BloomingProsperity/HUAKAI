# 2026-05-21 PR5 Retry Failover Codex Execution Plan

| Owner directive | "HUAKAI 方向 1 Phase 1，PR1-PR4 已提交。现在执行 PR5 —— 打开 retry/failover + 集成回归。这是 Phase 1 收官 PR。" |
|---|---|
| Scope | Open the PR3 attempt loop using the router plan budget, return classified pre-delivery failures from `runAttempt`, implement the dual retry gate including one 401 auth failover, enforce no failover after stream delivery starts, and add gatewayhttp regression coverage. |
| Out of scope | No billing/claim/settle implementation changes, no DB schema/SQL changes, no runtime dependency additions, no git operations. |
| Success criteria | Owner-listed non-stream, 429, 401, stream pre/post-delivery, all-attempt-fail, idempotency, and billing single-positive-settle scenarios are covered by tests or explicitly left for PG integration verification. Required build and Go tests run with real output. |
| Time estimate | 3-5 hours wall clock in this session; agent time dominated by test construction and full backend test verification. |
| Blast radius | Gateway `/v1/chat/completions` and `/v1/responses` handler retry behavior, channel health signals from retryable failures, stream delivery boundary behavior, and idempotency replay timing. |
| Failure modes | Retry loop could double-write HTTP responses, retry after stream bytes, skip `Settler.Abort` before re-reserve, retry 401 more than once, suppress channelhealth cooldown for 429, or create multiple positive settles. Mitigation: TDD tests for each scenario plus required full backend verification. |
| Decision points | Stop for Owner only if implementation would require billing code, schema/SQL, new dependency, auth core, quota enforcement, or destructive operations. |
| Pre-execution checklist | Read synthesis and Codex design plans; inspect `chat_completions_attempt.go`, handler loop, dispatch, stream, taxonomy, and existing tests; write failing tests before production code; run targeted tests red; implement minimal handler/executor changes; run targeted green; run required build and full tests. |
| Concrete execution order | 1. Add gatewayhttp PR5 regression tests using existing stubs. 2. Run targeted tests to confirm failures. 3. Replace PR3 budget clamp with real plan budget plus default-on env guard. 4. Convert pre-delivery selection, credential, dispatch, and streaming failures into `classifiedAttemptFailure`. 5. Add dual retry gate with `authFailoverUsed`. 6. Split streaming pre-delivery error handling from final error writing and preserve delivery tracker hard stop. 7. Re-run targeted tests, then required build and test commands. |

## Constraints Carried Into Execution

- `Settler.Abort` remains the only failed-attempt release path; PR5 only makes the existing PR4 path reachable.
- `deliveryTracker.started()` is the hard gate: once true, the handler loop must not try another account or pool.
- 401 uses `AttemptRetryDecision.CountsAgainstAuthFailoverBudget`; it is intentionally not added to `RoutePlan.RetryableEndClasses`.
- 429 and 5xx use `RoutePlan.RetryableEndClasses` and still record channelhealth signals.
- Positive settlement remains inside the successful attempt path and should execute at most once for a client request.
