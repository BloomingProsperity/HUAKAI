# LiteLLM missed pass

## Version

- Branch: `litellm_internal_staging`
- Commit: `c94a8d651493`
- Tag: `1.84.0-dev.2-488-gc94a8d6514`
- Files: 6828

## Source areas read

- Router constructor and retry/fallback parameters.
- Cache setup.
- Cooldown/health cache.
- Routing strategy and callbacks.
- Provider budget limiter.

## Behavior-confirmed capabilities

- Router constructor exposes explicit knobs for retries, fallbacks, context-window fallbacks, content-policy fallbacks, pre-call checks, allowed failures, cooldown period, routing strategy, provider budget, deployment affinity, and health checks as distinct parameters.
- Router can configure Redis cache and dual-cache (in-memory plus Redis) behavior as constructor-level options.
- Allowed-failures count, cooldown duration, and health-state cache are constructor-level concerns, not runtime overrides.
- Retry and fallback attempt counts are explicit constructor parameters with separate defaults.
- Fallbacks are split by failure reason into normal, context-window, and content-policy categories.
- Routing strategy and success/failure callbacks are formal extension points in the constructor.
- A provider budget limiter can participate as a pre-call check before forwarding.

## HUAKAI gap

HUAKAI already has router skeleton, but LiteLLM shows that reliability policy must be decomposed by reason. A single `retryable` flag is too weak for production.

## Upgrade design

- Error classifier returns `retry_class`: transport, rate-limit, auth, context-window, content-policy, provider-overload, streaming-after-first-byte, non-retryable.
- Retry budget is per tenant, per API key, and per upstream account to prevent retry storms.
- Deployment affinity should be a first-class option: sticky to same provider/account when safe, break when health/capacity says so.
- Router decisions should emit `routing_reason`, `fallback_reason`, and `cooldown_reason`.

## Suggested Feature IDs

- `F-RETRY-CLASS-001` L1: reasoned retry/fallback classifier.
- `F-RETRY-BUDGET-001` L2: per-tenant/key/account retry budget.
- `F-ROUTER-STRATEGY-001` L2: strategy plugin point with deterministic test harness.
- `F-DEPLOYMENT-AFFINITY-001` L3: affinity policy for account/provider stickiness.

## Acceptance test direction

- Context-window error falls back to larger-context model, not random account.
- Auth error cooldowns only the credential/account path, not the whole provider.
- Retry storm test proves tenant budget caps amplified requests.

## Open questions

- How many retry classes HUAKAI should expose to operators versus keep internal.
- Whether provider budget checks belong before ClaimGate or inside account selection.

---
Source files read: litellm litellm/router
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
