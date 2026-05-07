# 07 Rate limit / cooldown / account state

## Sub2API behavior summary

Sub2API stores multi-axis runtime blocking state on each account: rate-limit timestamps and reset times, model-level limit cache, overload expiry, and temporary unschedulable flags with reasons. The scheduler pre-checks account and model state before selecting a candidate. Upstream 429 and overload responses update runtime state so subsequent requests avoid blocked accounts or models. Test coverage confirms account-level and model-level rate limit handling, pre-check and cache update interactions, and overload cooldown behavior.

## Entity / fields

Account-level blocking state covers rate-limit timestamps, rate-limit reset time, model limit cache, overload expiry, temporary unschedulable expiry, temporary unschedulable reason, and quota/window-cost tracking.

## Request chain

Scheduler pre-checks account/model state. Upstream 429/529-like responses update runtime state. Later requests avoid blocked accounts or models.

## State machine

`healthy -> account_rate_limited | model_rate_limited | overloaded | temp_unschedulable | quota_exhausted -> recovered`.

## Failure modes

- One coarse state hides actual block reason.
- Account cooldown can over-block unrelated models.
- Model cooldown without route context can keep selecting wrong account.

## Sub2API capability

Sub2API has multi-axis runtime state and tests around account/model cooldown and overload.

## HUAKAI current capability

HUAKAI audit asks for `account_state_view` in `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md:155`.

## HUAKAI gap

`MISSED_BY_HUAKAI`: `health_state` alone is too coarse.

## HUAKAI stronger design

`account_state_view` should expose `is_schedulable`, `blocking_axes[]`, `next_retry_at`, `model_blocks[]`, `cooldown_source`, `operator_action_required`, `last_state_event_id`.

## Suggested Feature ID / level

- `F-ACCAPI-STATE-001`: L1
- `F-ACCAPI-MODEL-COOLDOWN-001`: L2
- `F-ACCAPI-STATE-EVENT-001`: L2

## Acceptance tests

- Account 429 blocks account.
- Model 429 blocks only that model.
- Admin clear action clears only selected axes.

## Open questions

- open-question: view vs projection table.

---
Source files read: sub2api backend/ent/schema/account, backend/internal/service/account, backend/internal/service/antigravity_rate_limit_test, backend/internal/service/overload_cooldown_test
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
