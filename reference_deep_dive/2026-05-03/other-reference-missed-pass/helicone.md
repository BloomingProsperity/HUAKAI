# Helicone missed pass

## Version

- Branch: `main`
- Commit: `3f4bd44b85f9`
- Tag: `deploy-20260502-004858`
- Files: 4820

## Source areas read

- Worker gateway router.
- Generate router.
- Cache utilities.
- Rate-limit policy parser and bucket client.
- Cost calculation package.
- ClickHouse migrations.

## Behavior-confirmed capabilities

- Gateway target URL is validated, normalized to origin-only, rate-limited for unapproved domains, and mapped to provider identity by target URL pattern before forwarding.
- Fallbacks can remap headers, target URL, body key overrides, and only stop on configured status-code ranges rather than all errors.
- Generate route validates high-level parameters, resolves provider configuration, reads provider-specific API key headers, and forwards through the gateway layer.
- Cache key includes cache control headers, ignored key list, request body, URL, and seed; cached responses add cache-hit headers to the response.
- Rate-limit policy supports request-count and money-unit (cents) limits, global/user/property segments, schema validation, limit response headers, fail-open/fail-closed configuration, and post-request cost recording for cents-denominated policies.
- Cost calculation accounts for prompt tokens, completion tokens, cache write/read tokens, audio tokens, image tokens, per-call costs, and registry matching by provider and model name.
- ClickHouse observability schema evolves request/response tracking to include request IDs, cache metrics, session grouping, organization properties, per-request cost, gateway router ID, deployment target, prompt content, and request indexes across separate migration files.

## HUAKAI gap

HUAKAI has observability and billing primitives, but Helicone's strongest lesson is that observability becomes a routing input and a cost-control input. Logs are not just a dashboard.

## Upgrade design

- Convert request properties into typed routing/limit dimensions: tenant, user, key, model, pool, provider, account, session, custom tags.
- Cost limits should support request-count and money units with pre-check and post-settle phases.
- Cache keys must include tenant, model snapshot, redaction policy, and credential-independent request semantics.
- Gateway target override must be admin-only and audited; public keys must not choose arbitrary upstream targets.

## Suggested Feature IDs

- `F-OBS-PROPERTY-001` L2: typed request properties as query, routing, and limit dimensions.
- `F-COST-LIMIT-001` L2: money-based limit with post-request settlement.
- `F-CACHE-KEY-SAFE-001` L2: tenant/model/policy-aware cache key.
- `F-TARGET-OVERRIDE-GUARD-001` L1: target override authorization and audit.

## Acceptance test direction

- User property limit blocks only that user segment and emits remaining/reset headers.
- Cost policy without known price fails in the configured fail-open/fail-closed mode.
- Target override from normal API key is rejected and audited.

## Open questions

- Whether HUAKAI should run ClickHouse early or keep Postgres and export later.
- Which custom properties should be allowed from client headers versus admin-enriched only.

---
Source files read: helicone worker/src/routers/gatewayRouter, worker/src/routers/generateRouter, worker/src/lib/util/cache/cacheFunctions, worker/src/lib/rate-limit/policyParser, worker/src/lib/rate-limit/segmentExtractor, worker/src/lib/rate-limit/bucketClient, packages/cost/index, clickhouse/migrations (schema_0 through schema_71 series)
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
