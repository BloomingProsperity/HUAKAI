# 11 Usage / billing / settlement

## Sub2API behavior summary

Sub2API records usage with a rich set of linkages: each usage log ties together user, API key, account, request, group, and subscription identifiers, plus token counts, costs, rate multipliers, latency, and stream flag. A RecordUsage routine calculates cost, constructs the log entry, applies billing rules, and writes to the store. A sync fallback exists for best-effort usage writes. Account stats pricing can apply configurable pricing rules. A pricing service syncs and caches model prices from upstream or admin configuration.

## Entity / fields

Usage stores user/key/account/request/group/subscription/model/tokens/cost/rate/latency/stream linkages.

## Request chain

Upstream usage -> reconcile tokens/cache -> resolve pricing -> apply billing -> write usage -> update account stats.

## State machine

`usage_extracted -> pricing_resolved -> cost_calculated -> billing_applied -> usage_written -> fallback_or_failed`.

## Failure modes

- Retry/account switch without attempt link hides cost source.
- Mapped vs billing model mismatch.
- Usage writer overflow loses billing evidence.

## Sub2API capability

Sub2API has detailed usage logs, balance/subscription billing, pricing service and write fallback.

## HUAKAI current capability

Audit notes `usage_records` misses `pool_group_id` and binding linkage in `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md:44`.

## HUAKAI gap

`MISSED_BY_HUAKAI`: usage must explain key -> binding -> pool -> account -> credential -> attempt -> cost.

## HUAKAI stronger design

Extend `usage_records` with `api_key_binding_id`, `pool_group_id`, `provider_account_id`, `credential_version`, `request_attempt_id`, `route_plan_id`, `billing_model`, `upstream_model`, `cost_snapshot`.

## Suggested Feature ID / level

- `F-USAGE-A2A-TRACE-001`: L1
- `F-BILLING-MODEL-001`: L1
- `F-USAGE-WRITE-001`: L1

## Acceptance tests

- Switched-account request links usage to final attempt and all attempts.
- Usage queue overflow follows fallback policy.
- Billing model differs from requested model and is captured.

## Open questions

- open-question: failed attempts cost rows vs attempt-only rows.

---
Source files read: sub2api backend/ent/schema/usage_log, backend/internal/service/gateway_service, backend/internal/service/account_stats_pricing, backend/internal/service/pricing_service
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
