# 2026-05-21 PR3 handler attempt loop skeleton

| Owner directive | "HUAKAI 方向 1 Phase 1，PR1+PR2 已提交。现在执行 PR3。" |
|---|---|
| Scope | In: chat completions handler attempt loop skeleton, `runAttempt` extraction, attempt sequence plumbing, excluded-account argument plumbing, streaming delivery tracker, delayed non-stream write. Out: enabling retry, billing/claim re-reserve semantics, DB schema/SQL, runtime dependencies, git operations. |
| Success criteria | Existing chat completions behavior remains equivalent with effective budget clamped to 1: one outbound attempt, same responses, same billing/abort paths, `attempt_seq=1`, same cache/idempotency behavior, same stream settlement behavior. |
| Time estimate | 2-4h wall clock; one Codex execution session. |
| Blast radius | `backend/internal/gatewayhttp` request execution path for `/v1/chat/completions`, `/v1/messages`, and `/v1/responses` because they share the handler. |
| Failure modes | Early client writes before settle, duplicate abort/settle, changed error body/status, stale attempt state between iterations, stream wrapper hiding optional interfaces, tests relying on hard-coded attempt sequence. Mitigation: keep budget=1, preserve existing helper logic, run gatewayhttp targeted tests before/after, then full build and full backend tests. |
| Decision points | None for PR3 unless implementation would require DB schema/SQL, billing claim semantics, auth core, quota enforcement, runtime dependency, or destructive file operation. Those are out of scope and require Owner confirmation. |
| Pre-execution checklist | Read synthesis and Codex design; inspect current handler files and tests; run existing gatewayhttp chat-completions test baseline; avoid changing test assertions; avoid git operations; run required verification commands. |

## Concrete execution order

1. Establish baseline with existing gatewayhttp chat-completions tests:
   `GOCACHE=/tmp/go-cache go test -C /home/codex/HUAKAI/backend ./internal/gatewayhttp -run 'ChatCompletions|Handler_|Responses|Streaming|SettleCompletion|HUAKAIModel|StreamBilling' -count=1 -timeout 600s`
2. Add attempt execution helpers in `backend/internal/gatewayhttp/chat_completions_attempt.go`:
   - clamp PR3 effective budget to 1;
   - wrap `http.ResponseWriter` with a delivery tracker for stream attempts;
   - implement `runAttempt` around the existing single-attempt flow.
3. Refactor `NewChatCompletionsHandler` in `chat_completions_handler.go`:
   - keep `reserveClaim` and L2 cache lookup outside the loop;
   - run exactly one attempt from the route plan;
   - write non-stream success only after `runAttempt` has completed.
4. Thread attempt state through existing helpers:
   - `selectPoolAccount` takes `AttemptSeq` and `ExcludedAccounts`;
   - `nonStreamingSettleRequest` uses current attempt sequence;
   - `streamingCompletionEvent` uses current attempt sequence;
   - `l2CacheHitInput` carries attempt sequence for the existing acquired-account cache path.
5. Split streaming attempt execution just enough to support delivery tracking while preserving existing settle/abort behavior for budget=1.
6. Re-run targeted gatewayhttp tests. If failures are true regressions, fix production code only; do not weaken existing assertions.
7. Run required verification:
   - `GOCACHE=/tmp/go-cache go build -C /home/codex/HUAKAI/backend ./...`
   - `GOCACHE=/tmp/go-cache go test -C /home/codex/HUAKAI/backend ./... -count=1 -timeout 600s`

## Behavior-preservation guardrails

- PR3 effective budget is hard-clamped to 1 even if the router plan has more attempts.
- Failed attempt abort/re-reserve is not introduced in PR3; existing helpers still write the final error on the only attempt.
- `ExcludedAccounts` is plumbed as an empty map for PR3, so pool selection excludes nothing.
- Streaming delivery tracker is observational in PR3; it does not trigger failover.
- Non-stream response body is returned from `runAttempt`, then written by the handler after settle, matching the current "settle before write" order.
