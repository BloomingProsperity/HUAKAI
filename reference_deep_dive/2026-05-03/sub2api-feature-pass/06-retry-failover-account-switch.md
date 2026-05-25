# 06 Retry / failover / account switch

## Sub2API behavior summary

Sub2API has an explicit failover loop with an action and state model. Error handling can retry on the same account, mark the account as temporarily failed, exhaust the retry budget, or switch to a different account. Provider-specific smart retry logic, model-level rate limit handling, and empty-stream failover are also present. Maximum switch counts and retry budgets are configurable. An ops layer stores retry attempts and supports both pinned and upstream-directed retry operations.

## Entity / fields

Failover state includes failed account IDs, switch count, same-account retry count, retry budget, error class and selected action.

## Request chain

Attempt fails -> classifier decides same-account retry / switch / cooldown / stop -> next account excludes failed accounts -> attempt audit records reason.

## State machine

`attempt_started -> upstream_error -> retry_same | temp_unschedule | switch_account | exhausted -> success_or_fail`.

## Failure modes

- Streaming partial output may be unsafe to retry.
- Invalid request should not switch accounts.
- Account switch without billing/cache correction can misbill.

## Sub2API capability

Sub2API has explicit failover loop, account switch limits, provider-specific retry and ops retry attempts.

## HUAKAI current capability

HUAKAI notes real multi-attempt fallback is still missing in `docs/02_HUAKAI_FUSION_ARCHITECTURE.md:135`; audit proposes attempts in `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md:70`.

## HUAKAI gap

`MISSED_BY_HUAKAI`: failover must be account-aware, credential-aware, stream-aware and billing-aware.

## HUAKAI stronger design

Create `request_attempts` before real upstream with attempt index, binding, pool, account, credential version, slot lease, error class, retry action, switch reason, stream phase and billing adjustment.

## Suggested Feature ID / level

- `F-ACCAPI-ATTEMPT-001`: L1
- `F-UPSTREAM-ERR-CLASSIFY-001`: L1
- `F-UPSTREAM-RETRY-BUDGET-001`: L1

## Acceptance tests

- Retryable 429 switches account and records both attempts.
- Invalid request does not switch.
- Streaming failure after first byte is marked non-auto-retry unless policy permits.

## Open questions

- open-question: partial stream billing policy.

---
Source files read: sub2api backend/internal/handler/failover_loop, backend/internal/service/antigravity_gateway_service, backend/internal/config/config, backend/internal/service/ops_retry
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
