# 05 Sticky session / context

## Sub2API behavior summary

Sub2API generates a session hash from request metadata, user identity, and fallback content hash. A cached account lookup by session hash with TTL refresh determines the sticky account for subsequent requests. Clear and break logic exists for the sticky binding. The scheduler handles previous-response tracking, sticky account binding, and wait planning. Both the OpenAI-format and Gemini-format handlers derive and bind sticky sessions with select, bind, switch, and break behaviors.

## Entity / fields

Sticky session uses session hash, previous response reference, metadata user ID, cached account ID, selected account, TTL, and break reason.

## Request chain

Derive session hash -> lookup cached account -> validate schedulability/capacity -> wait or break -> bind/rebind -> forward.

## State machine

`no_sticky -> sticky_bound -> sticky_refreshed -> sticky_waiting -> sticky_used | sticky_broken -> fallback_account_selected`.

## Failure modes

- Sticky account full: wait/fallback must be explicit.
- Sticky account disabled/rate-limited: break reason must be recorded.
- Switching account mid-context can affect cache/billing.

## Sub2API capability

Sticky hash, cached account, TTL refresh, wait plan and break behavior are behavior-confirmed.

## HUAKAI current capability

HUAKAI says `sticky_bindings` schema exists but logic is still effectively missing in `docs/02_HUAKAI_FUSION_ARCHITECTURE.md:91`.

## HUAKAI gap

`MISSED_BY_HUAKAI`: sticky binding without `break_reason`, `wait_ms`, `previous_account_id`, `selected_account_id` and `attempt_id` is not operationally enough.

## HUAKAI stronger design

Add `StickyDecision` to `AccountSelectionPlan`, persisted per request/attempt.

## Suggested Feature ID / level

- `F-ACCAPI-STICKY-001`: L1
- `F-ACCAPI-STICKY-AUDIT-001`: L2
- `F-SESSION-WAIT-001`: L1

## Acceptance tests

- Same session reuses schedulable account.
- Full sticky account waits then records used/broken decision.
- Rate-limited sticky account records break reason.

## Open questions

- open-question: sticky scope per key, tenant, route, or conversation.

---
Source files read: sub2api backend/internal/service/gateway_service, backend/internal/service/openai_account_scheduler, backend/internal/handler/openai_gateway_handler, backend/internal/handler/gemini_v1beta_handler
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
