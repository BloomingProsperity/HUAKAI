# Helicone reference delta

## Repo snapshot

- Repo: `.omc/reference-src/helicone`
- Branch: `main`
- Commit: `3f4bd44b85f9`
- Tag: `deploy-20260502-004858`
- File count: `4702`
- State: clean.

## Source areas read

- Public controllers: `.omc/reference-src/helicone/valhalla/jawn/src/controllers/public/*`
- Private/admin controllers: `.omc/reference-src/helicone/valhalla/jawn/src/controllers/private/*`
- ClickHouse migrations: `.omc/reference-src/helicone/clickhouse/migrations/*`
- Worker proxy/request handling: `.omc/reference-src/helicone/worker/src/lib/*`
- Request body buffer and rate-limit code: `.omc/reference-src/helicone/worker/src/RequestBodyBuffer/*`, `.omc/reference-src/helicone/worker/src/lib/rate-limit/*`

## Source-confirmed features

| Status | Feature | Evidence |
| --- | --- | --- |
| source-confirmed | Request explorer supports count/query, ClickHouse query, request detail, inputs, ids, feedback, properties, assets, and scoring. | `.omc/reference-src/helicone/valhalla/jawn/src/controllers/public/requestController.ts:68`, `:72`, `:114`, `:159`, `:182`, `:216`, `:237`, `:281` |
| source-confirmed | Session explorer supports query/count/name, metrics, feedback, and tag set/update. | `.omc/reference-src/helicone/valhalla/jawn/src/controllers/public/sessionController.ts:70`, `:74`, `:125`, `:143`, `:163` |
| source-confirmed | Metrics API exposes request, cost, latency, TTFT, token, threat, user, status, model, country, and quantile metrics. | `.omc/reference-src/helicone/valhalla/jawn/src/controllers/public/metricsController.ts:78`, `:84`, `:100`, `:132`, `:151`, `:170`, `:432` |
| source-confirmed | Prompt management supports prompt/version/environment CRUD and query APIs. | `.omc/reference-src/helicone/valhalla/jawn/src/controllers/public/prompt2025Controller.ts:34`, `:177`, `:198`, `:222`, `:284`, `:349` |
| source-confirmed | API key and provider-key controllers support CRUD, proxy-key creation, and provider-key listing/patching. | `.omc/reference-src/helicone/valhalla/jawn/src/controllers/public/apiKeyController.ts:37`, `:66`, `:108`, `:150`, `:187`, `:232` |
| source-confirmed | Credits and admin wallet controllers expose balance, payments, spend breakdown, invoices, discounts, balance modification, settings, and analytics. | `.omc/reference-src/helicone/valhalla/jawn/src/controllers/public/creditsController.ts:32`, `:36`, `:51`, `:94`, `.omc/reference-src/helicone/valhalla/jawn/src/controllers/private/adminWalletController.ts:94`, `:261`, `:512`, `:638` |
| source-confirmed | Rate limit controller supports request/cents units and CRUD. | `.omc/reference-src/helicone/valhalla/jawn/src/controllers/private/rateLimitController.ts:24`, `:41`, `:47`, `:63`, `:83`, `:101` |
| source-confirmed | ClickHouse request table includes threat, TTFT, properties, scores, TTL, bloom indexes, primary/order keys, and cache-savings metrics in later migrations. | `.omc/reference-src/helicone/clickhouse/migrations/schema_41_request_response_replacing_merge_tree.sql:1`, `:14`, `:22`, `:27`, `.omc/reference-src/helicone/clickhouse/migrations/schema_48_cache_metrics.sql:1`, `:17` |
| source-confirmed | Worker enforces request body buffering limits and has token-limit handling with truncation/middle-out strategies. | `.omc/reference-src/helicone/worker/src/RequestBodyBuffer/RequestBodyBufferBuilder.ts:6`, `:21`, `.omc/reference-src/helicone/worker/src/lib/RequestWrapper.ts:325`, `:371`, `:396` |
| source-confirmed | Proxy forwarder applies DB-configured and header-configured rate limits, bucket checks, cost policies, and metrics tracing. | `.omc/reference-src/helicone/worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:148`, `:175`, `:193`, `:702`, `:725` |

## Inferred features

- inferred: Helicone is the strongest reference for post-request operations: request explorer, metrics, session/property/score dimensions, cost analytics, and ClickHouse retention.
- inferred: HUAKAI's "Admin Lite" will be operationally weak unless it includes request-to-attempt investigation, cost/latency/token aggregates, and log retention from the start.

## Open questions

- open-question: Full ingestion pipeline from proxy event to ClickHouse storage was not fully traced in this pass.
- open-question: Threat scoring and prompt management are likely L3/L4 for HUAKAI unless product direction changes.

## HUAKAI delta

- `F-OBS-001` and `F-OPS-001` need more concrete request explorer acceptance tests.
- Billing visibility needs both current balance and historical spend breakdown, not just ledger rows.
- Retention/backfill/indexing must be explicit before production volume arrives.

## Suggested Feature IDs

| Feature ID | Name | Level | Delta |
| --- | --- | --- | --- |
| `F-OBS-QUERY-001` | Request explorer and investigation API | L2 | Query/count/detail/inputs/attempts/properties/feedback/scoring, with sanitized payload access. |
| `F-METRICS-ROLLUP-001` | Cost/latency/token rollups | L2 | Request count, total cost, avg latency, TTFT, token usage, status/model/provider dimensions. |
| `F-RATE-COST-001` | Cost-aware rate limits | L2/L3 | Request/cents/token units, segmented buckets, fail-open policy, and admin CRUD. |
| `F-RETENTION-CH-001` | Analytical log retention/index plan | L2/L3 | TTL, bloom indexes, backfill window, cache-savings metrics, and cleanup policy. |
