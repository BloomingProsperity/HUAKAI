# Portkey Gateway missed pass

## Version

- Branch: `main`
- Commit: `351692fd9236`
- Tag: `351692fd`
- Files: 765

## Source areas read

- Global response/header keys.
- Request config schema.
- Cache middleware and cache service.
- Request validator.
- Main middleware wiring.

## Behavior-confirmed capabilities

- The gateway exposes retry attempt count, provider identity, trace ID, cache hit/miss status, virtual key reference, and retry-attempt index through structured response headers.
- Request config supports strategy modes including single, load-balance, fallback, and conditional routing, plus cache policy, retry rules, forwarded header allowlists, and guardrail hooks.
- Forbidden forwarded headers are blocked by validation before reaching any adapter.
- Cache middleware derives a request hash, performs a cache lookup, and stores responses; cache hits add cache-hit headers to the response.
- Cache service has get, set, delete, exists, stats, and get-or-set operations across multiple named cache families.
- Main server wiring enables logger and cache middleware at startup.

## HUAKAI gap

HUAKAI has internal routing plans, but Portkey shows the API contract layer: users/operators need to see trace, retry, cache, provider, and safety decisions without reading backend logs.

## Upgrade design

- Add response/debug headers for `x-huakai-request-id`, `x-huakai-attempt-count`, `x-huakai-provider-account`, `x-huakai-cache`, and `x-huakai-retry-class` with redaction-safe defaults.
- Put request config through a schema validator before route planning; reject unsafe forwarded headers before they reach adapters.
- Build cache as a policy and evidence surface, not only a performance optimization.

## Suggested Feature IDs

- `F-TRACE-HEADER-001` L1: safe request/attempt/cache/provider response headers.
- `F-REQUEST-CONFIG-SCHEMA-001` L2: validated route/request config object.
- `F-FORWARDED-HEADER-GUARD-001` L1: forbidden forwarded header blocklist.
- `F-CACHE-EVIDENCE-001` L2: cache policy + hit/miss audit.

## Acceptance test direction

- Request that tries to forward `authorization` or provider key headers is rejected or stripped with audit.
- Fallback response includes attempt count and final provider/account reference.
- Cache hit preserves tenant isolation and emits a cache-hit audit event.

## Open questions

- Whether debug headers are always enabled for operators or gated by API key role.
- Whether cache should enter L1 for cost control or L2 after routing stabilizes.

---
Source files read: portkey-gateway src/globals, src/middlewares/requestValidator/schema/config, src/middlewares/requestValidator/index, src/middlewares/cache/index, src/shared/services/cache/index, src/index
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
