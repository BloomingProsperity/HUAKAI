# 2026-06-04 Wire Resilience Codex Plan

| Owner directive | "接线两个已造好但没接的准入韧性件...不新造逻辑,只把现成件连上线" |
| Scope | In: W1 OAuth hot-path refresh trigger after retryable auth failure; W2 non-streaming buffered dispatcher header/total timeout wiring; focused tests. Out: new provider refresh logic, new failover logic, stream forwarder timeout behavior, schema/auth/billing/quota changes, `/home/ubuntu/refs` reads, git commit. |
| Success criteria | W1 tests prove hot refresh is triggered once, non-blocking, and deduped; W2 tests prove non-streaming header stall becomes `upstream_header_timeout` and failovers; existing retry/failover suite remains green; requested backend gate runs. |
| Time estimate | 1-2 wall-clock hours; one Codex session. |
| Blast radius | Chat hot path attempt loop, credential refresh scheduler admission path, production dispatcher construction, non-streaming HTTP dispatch. Failures could delay responses, over-trigger refreshes, or alter retry classification. |
| Failure modes | Blocking current request on refresh: assert response returns before stub refresh is released. Refresh storm bypass: reuse scheduler admission method instead of calling bare refresher from handler. Duplicate refresh flood: add short-window dedupe around trigger. Stream timeout regression: gate dispatcher HTTP timeout with a non-streaming-only flag. Weak tests: fixtures must fail if trigger/timeout wiring is removed. |
| Decision points | Stop for Owner only if implementation would require new dependency, schema change, auth/billing/quota core change, reading `/home/ubuntu/refs`, or adding files under frozen packages. Current path needs none. |
| Pre-execution checklist | 1. Read `CLAUDE.md`, `AGENTS.md`, `docs/RULES.md`, `.coordination/README.md`. 2. Confirm no live edit conflicts. 3. Use existing files only in frozen packages. 4. Write failing tests before production edits. 5. Keep functions under 80 lines. 6. Run focused tests and final gate. |

## File Scope

- Modify existing frozen package files only:
  - `backend/internal/gatewayhttp/chat_completions_handler.go`
  - `backend/internal/gatewayhttp/chat_completions_retry_failover_test.go`
  - `backend/internal/gateway/forwarder_types.go`
  - `backend/internal/gateway/upstream_dispatcher.go`
  - `backend/internal/gateway/upstream_dispatcher_test.go`
- Modify non-frozen existing files:
  - `backend/internal/credentialworker/scheduler.go`
  - `backend/internal/credentialworker/scheduler_test.go`
  - `backend/cmd/gateway/middleware.go`
  - `backend/cmd/gateway/stream_timeout_test.go`
  - `backend/cmd/gateway/wiring.go`
  - `backend/cmd/gateway/routes.go`

No new files under `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`.

## Execution Order

1. TDD W1:
   - Add gatewayhttp tests using a stub hot refresher that records account IDs and can block.
   - Expected red: no refresher call today.
   - Add `CredentialHotRefresher` dependency and trigger on `RefreshOAuthHotPath` failure before retry handoff.
   - Add short-window in-flight/recent dedupe around the trigger.
2. TDD scheduler budget:
   - Add credentialworker test proving the exported hot refresh entry consults account, provider-endpoint, and global storm scopes before refresher.
   - Expected red: method does not exist.
   - Extract existing `processAccount` semantics behind a public `RefreshHotPath` method that accepts tenant/account/vendor.
3. TDD W2:
   - Add dispatcher test with a slow response-header server and non-streaming timeout config.
   - Expected red: call hangs past threshold or returns without `TransportErrorUpstreamHeaderTimeout`.
   - Wire `TimeoutConfig` into `UpstreamDispatcher` and enable only when `DispatchInput.NonStreamingBuffered` is true.
   - Add gatewayhttp failover test for raw buffered path when first account stalls headers and second succeeds.
4. Production wiring:
   - Build one outbound timeout config in `middleware.go`; inject into stream forwarder and dispatcher.
   - Add env defaults for `HUAKAI_UPSTREAM_HEADER_TIMEOUT` and `HUAKAI_UPSTREAM_REQUEST_TIMEOUT`.
   - Pass credential scheduler as chat hot refresher in `chatHandlerDeps`.
5. Verification:
   - Run focused red/green tests during development.
   - Run `cd backend && (sqlc generate>/dev/null 2>&1||true) && go build ./... && go vet ./... && go test ./internal/gatewayhttp/... ./internal/gateway/... ./internal/credentialworker/... ./cmd/gateway/... 2>&1 | tail -25`.

## Clean-Room Note

This plan uses only HUAKAI internal code and the Owner-provided PM specification. Reference project source under `/home/ubuntu/refs` is explicitly out of scope for Codex implementer work in this task.
