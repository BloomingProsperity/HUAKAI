# 2026-05-20 stream billing settlement

| Owner directive | "只要打到上游、上游有反馈消耗了额度,就计费。... 上游零交付 → abort, 不 commit。" |
| Scope | In: HUAKAI Go backend stream settlement branch in `gatewayhttp`, affected gatewayhttp/billing tests and local test mocks. Out: reference projects, schema, quota enforcement, auth, billing ledger internals, runtime dependencies, git staging/commit/push/reset/checkout/stash/clean. |
| Success criteria | Chargeable stream attempts still call commit settlement and record idempotency replay when capture is within limit. Non-chargeable zero-delivery stream attempts call `Settler.Abort`, do not call `Settle`, do not record replay, and same idempotency key can reserve again. Requested build and full test commands complete with real output. |
| Time estimate | 30-60 minutes wall clock; one Codex work unit. |
| Blast radius | Streamed chat completions after upstream dispatch; idempotency replay behavior for streaming responses; tests using gatewayhttp settler mocks. |
| Failure modes | Accidentally gating chargeable partial streams on forward errors; mitigation: branch only on `streamAttempt.State.Chargeable()`. Accidentally preserving replay for aborted zero-delivery streams; mitigation: replay write remains only in commit branch. Mock drift hiding real behavior; mitigation: record abort calls explicitly and assert commit/abort counts. |
| Decision points | None expected. Stop for Owner confirmation only if implementation requires schema, billing ledger internals, quota enforcement, auth, real secrets, destructive commands, new dependencies, or `LICENSE`. |
| Pre-execution checklist | Read `backend/internal/gatewayhttp/chat_completions_stream.go`, `backend/internal/billing/state.go`, and affected stream/idempotency tests. Confirm `Chargeable()` is the sole branch. Patch implementation with Chinese Owner-policy comment. Reconcile affected tests. Run targeted tests, then requested full build and full test suite. |

## Concrete Execution Order

1. Update `forwardSSEAndSettle` so `streamAttempt.State.Chargeable()` selects commit + replay, otherwise abort + no replay.
2. Extend/adjust gatewayhttp stream test settler mocks to record `Abort`.
3. Rewrite zero-delivery stream replay tests to assert abort, no replay, and successful re-reserve on retry.
4. Preserve chargeable stream replay assertions for graceful, partial/EOF, and forward-error-after-delivery cases.
5. Run `go test ./internal/gatewayhttp/... ./internal/billing/...` from `backend`.
6. Run the Owner-requested `go build ./...` and `go test ./... -count=1 -timeout 480s` with the specified `GOCACHE`.
