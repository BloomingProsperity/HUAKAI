# Helicone observability / wallet / rate-limit deep dive

## Snapshot

- Reference repo: `.omc/reference-src/helicone`
- Branch: `main`
- Commit: `3f4bd44b85f9837feb4a696cce4bba6c99fbdc7e`
- Tag / describe: `deploy-20260502-004858`
- Tracked file count: `4820`
- Working tree state: clean at read time.
- Clean-room stance: Helicone is GPL-3.0 in HUAKAI's plan, so treat this as behavior evidence only. Do not copy schema layout, controller shape, or worker code. HUAKAI should write local specs for retention, wallet, escrow, and investigation workflows before implementation.

## Source areas read

- `clickhouse/migrations/schema_17_request_response_log.sql`
- `clickhouse/migrations/schema_21_rate_limit_log.sql`
- `clickhouse/migrations/schema_40_request_response_versioned_bodies_ttl.sql`
- `clickhouse/migrations/schema_41_request_response_replacing_merge_tree.sql`
- `clickhouse/migrations/schema_48_cache_metrics.sql`
- `clickhouse/migrations/schema_49_sessions.sql`
- `clickhouse/migrations/schema_52_add_cost_to_request_response_rmt.sql`
- `clickhouse/migrations/schema_60_request_gateway_router_id.sql`
- `clickhouse/migrations/schema_61_request_gateway_deployment_target.sql`
- `clickhouse/migrations/schema_71_request_id_index.sql`
- `clickhouse/migrations/schema_74_add_ai_gateway_body_mapping.sql`
- `clickhouse/migrations/schema_76_size.sql`
- `worker/src/RequestBodyBuffer/*`
- `worker/src/lib/rate-limit/*`
- `worker/src/lib/durable-objects/BucketRateLimiterDO.ts`
- `worker/src/lib/durable-objects/Wallet.ts`
- `worker/src/lib/ai-gateway/*`
- `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts`
- `worker/src/lib/dbLogger/DBLoggable.ts`
- `worker/src/lib/db/ClickhouseWrapper.ts`
- `worker/src/routers/api/walletRouter.ts`
- `worker/src/lib/managers/WalletManager.ts`
- `worker/src/lib/managers/StripeManager.ts`

## Source-confirmed functions

### Request / response analytics storage

- `source-confirmed` `clickhouse/migrations/schema_17_request_response_log.sql:1-34` creates the early request/response log table with response id/time, latency, status, tokens, model, request id/time, auth hash, user, organization, proxy key, job/node, and threat columns.
- `source-confirmed` `clickhouse/migrations/schema_41_request_response_replacing_merge_tree.sql:1-37` creates `request_response_rmt` with organization/user/proxy key, provider, model, latency, status, TTFT, country, target URL, properties, scores, request/response bodies, assets, `ReplacingMergeTree`, month partitioning, and a primary/order key built for organization/provider/model/user/time/request queries.
- `source-confirmed` `clickhouse/migrations/schema_41_request_response_replacing_merge_tree.sql:21-32` applies three-month TTL to request/response bodies and adds bloom/ngram indexes for properties, scores, request body, and response body.
- `source-confirmed` `clickhouse/migrations/schema_40_request_response_versioned_bodies_ttl.sql:1-3` separately adds three-month TTL to request/response body columns on the versioned table.
- `source-confirmed` `clickhouse/migrations/schema_52_add_cost_to_request_response_rmt.sql:1-2` adds `cost`.
- `source-confirmed` `clickhouse/migrations/schema_60_request_gateway_router_id.sql:1-2` and `schema_61_request_gateway_deployment_target.sql:1-2` add gateway router/deployment target dimensions.
- `source-confirmed` `clickhouse/migrations/schema_71_request_id_index.sql:1-6` adds/materializes a bloom-filter index on `request_id`.
- `source-confirmed` `clickhouse/migrations/schema_74_add_ai_gateway_body_mapping.sql:1-2` records AI gateway body mapping.
- `source-confirmed` `clickhouse/migrations/schema_76_size.sql:1-2` adds `size_bytes`.

HUAKAI delta: F-OBS-001 already has the audit chain, but the operational query model is still too abstract. HUAKAI needs an explicit request investigation table/query contract: request id, attempt id, route decision, account alias, provider/model, status, latency, TTFT, cost, token buckets, body-retention pointer, redaction state, and billing session id.

### Sessions, cache metrics, properties, scores

- `source-confirmed` `clickhouse/migrations/schema_49_sessions.sql:1-45` creates a `session_rmt` table with session id/name, request id, token/cache token columns, model/provider/user/organization, cache flags, properties, scores, bodies with TTL, assets, and indexes.
- `source-confirmed` `clickhouse/migrations/schema_48_cache_metrics.sql:1-27` creates hourly cache metrics with request id, model/provider, hit count, saved latency, saved tokens, prompt cache read/write tokens, first/last hit, and bodies.
- `source-confirmed` `worker/src/lib/db/ClickhouseWrapper.ts:204-236` models `RequestResponseRMT` with cost, prompt/cache/audio/reasoning tokens, properties, scores, request/response bodies, assets, cache reference id, cache enabled, and AI gateway body mapping.
- `source-confirmed` `worker/src/lib/dbLogger/DBLoggable.ts:913-1020` builds Kafka/analytics payloads with cache reference, request properties, body size, target URL, provider, path, stream flag, gateway provider/model/body mapping, response status, TTFT, cost, detailed usage, cache tokens, audio tokens, and reasoning tokens.

HUAKAI delta: do not limit "usage logging" to rows for billing. The admin user needs dimensions that explain why money moved: session/user/key/provider/model/status/cost/latency/cache/body-retention and the route/attempt context.

### Request body buffering and retention

- `source-confirmed` `worker/src/RequestBodyBuffer/IRequestBodyBuffer.ts:5-47` defines the body-buffer contract: raw text, temporary override, body override, stream retrieval, stream/user/model metadata, upload to S3, length, reset/delete, and original OpenAI request preservation.
- `source-confirmed` `worker/src/RequestBodyBuffer/RequestBodyBufferBuilder.ts:72-121` chooses in-memory vs remote body buffer based on method, missing body, `Content-Length`, a 20 MiB threshold, and whether the remote container binding exists. The unknown-size remote path is commented out, so unknown-size currently falls back to in-memory in this file.
- `source-confirmed` `worker/src/RequestBodyBuffer/RequestBodyBuffer_InMemory.ts:53-84` protects body override from prototype pollution keys (`__proto__`, `constructor`, `prototype`).
- `source-confirmed` `worker/src/RequestBodyBuffer/RequestBodyBuffer_InMemory.ts:86-95` labels raw text access as unsafe and caches the request text after reading.
- `source-confirmed` `worker/src/RequestBodyBuffer/RequestBodyBuffer_InMemory.ts:215-245` stores versioned request/response bodies to S3, preserving original OpenAI request/response plus native provider request/response when available.
- `source-confirmed` `worker/src/RequestBodyBuffer/RequestBodyBuffer_Remote.ts:49-143` creates a remote buffer with generated unique id, ingests request body into a Cloudflare container, then fetches metadata.
- `source-confirmed` `worker/src/RequestBodyBuffer/RequestBodyBuffer_Remote.ts:186-205` tracks unsafe remote reads and returns empty string on failed remote read.
- `source-confirmed` `worker/src/RequestBodyBuffer/RequestBodyBuffer_Remote.ts:274-301` uploads stored remote bodies to S3, and `:303-307` deletes the remote body.
- `source-confirmed` `worker/src/lib/managers/RequestResponseManager.ts:19-41` uploads request/response raw body through the buffer and deletes the buffer as the last step.
- `source-confirmed` `worker/src/lib/dbLogger/DBLoggable.ts:838-904` skips S3 body storage in free-tier/omit-header cases except when pass-through billing failed to extract usage and needs the body for billing recovery.

HUAKAI delta: add a body-retention spec before production. It should define per-tenant/body-class retention, maximum body size, remote storage threshold, redaction state, "body omitted but metadata logged" semantics, and billing-recovery exception rules. For HUAKAI, default prompt body logging should remain off unless explicitly enabled.

### Token bucket and cost-based rate limits

- `source-confirmed` `worker/src/lib/rate-limit/policyParser.ts:51-135` parses policy strings as `quota;w=window;u=unit;s=segment`, supports decimal quotas, `request` or `cents` units, user/property/global segment, and enforces minimum 60-second and maximum one-year windows.
- `source-confirmed` `worker/src/lib/rate-limit/segmentExtractor.ts:52-102` extracts global/user/property segment identifiers and errors if required user/property headers are missing.
- `source-confirmed` `worker/src/lib/rate-limit/segmentExtractor.ts:116-140` builds durable-object keys scoped by organization, segment type/value, property name, and unit.
- `source-confirmed` `worker/src/lib/rate-limit/segmentExtractor.ts:172-189` sanitizes segment values and key parts.
- `source-confirmed` `worker/src/lib/rate-limit/bucketClient.ts:73-90` supports fail-open vs fail-closed behavior and defaults to fail-open with no default cost for cents.
- `source-confirmed` `worker/src/lib/rate-limit/bucketClient.ts:124-260` parses policy, extracts segment, builds policy id, traces the check, calls the durable object, and for `cents` without known cost uses check-only mode.
- `source-confirmed` `worker/src/lib/rate-limit/bucketClient.ts:379-484` records post-request actual cost for cents-based policies and treats recording failures as best-effort.
- `source-confirmed` `worker/src/lib/rate-limit/bucketClient.ts:489-520` determines request cost, allows check-only `cents` preflight with cost `0`, and requires actual cost for non-check-only cents updates.
- `source-confirmed` `worker/src/lib/durable-objects/BucketRateLimiterDO.ts:3-46` documents the distinction between request-based preemptive deduction and cents-based post-request deduction with possible one-request overdraft.
- `source-confirmed` `worker/src/lib/durable-objects/BucketRateLimiterDO.ts:112-190` performs atomic bucket load/refill/check/deduct/save inside `blockConcurrencyWhile`.
- `source-confirmed` `worker/src/lib/durable-objects/BucketRateLimiterDO.ts:223-250` handles policy changes by preserving/clamping existing tokens.
- `source-confirmed` `worker/src/lib/durable-objects/BucketRateLimiterDO.ts:257-289` lazily refills and clamps per-request cost to a maximum.
- `source-confirmed` `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:145-215` supports header-configured and DB-configured rate limits, converts DB policy into the header policy format, applies bucket checks, adds response headers, and marks bucket-rate-limited requests.
- `source-confirmed` `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:695-720` records post-request bucket usage for cost-based policies when the request was not cached/rate-limited.

HUAKAI delta: `F-SEC-001`, `F-SEC-004`, and `F-RATE-001` should not be conflated. Helicone gives a distinct user-facing quota/rate-limit feature: request/cost units, per-user/per-property/global segmentation, fail-open/fail-closed choice, and post-request actual-cost deduction.

### Wallet, escrow, credits, refunds, and disputes

- `source-confirmed` `worker/src/lib/durable-objects/Wallet.ts:101-170` initializes wallet-local SQL tables for processed webhook events, disallow list, escrows, credit purchases, aggregated debits with ClickHouse reconciliation fields, alert state, and disputes.
- `source-confirmed` `worker/src/lib/durable-objects/Wallet.ts:206-235` checks processed webhook event ids and records processed event ids when adding credits.
- `source-confirmed` `worker/src/lib/durable-objects/Wallet.ts:260-322` deducts credits for refund-like flows only if refund amount does not exceed effective balance, then inserts a negative credit purchase and processed webhook event.
- `source-confirmed` `worker/src/lib/durable-objects/Wallet.ts:369-452` builds wallet state including disallow list, total escrow, total credits, total debits, effective balance, dispute status, and active disputes.
- `source-confirmed` `worker/src/lib/durable-objects/Wallet.ts:455-532` reserves escrow only after checking unresolved disputes, credits, debits, escrows, credit line, and minimum reserve; it uses a generated escrow id rather than trusting user-supplied request id.
- `source-confirmed` `worker/src/lib/durable-objects/Wallet.ts:534-628` finalizes escrow by upserting aggregate debits, deleting the escrow, calculating remaining balance, and probabilistically clearing stale escrows older than one hour.
- `source-confirmed` `worker/src/lib/durable-objects/Wallet.ts:630-666` supports cancel escrow and direct debit fallback without escrow.
- `source-confirmed` `worker/src/lib/managers/WalletManager.ts:30-63` finalizes escrow and syncs spend through a manager around the wallet durable object.
- `source-confirmed` `worker/src/lib/managers/WalletManager.ts:65-92` attempts direct debit when escrow reservation failed and a valid cost exists, but skips that fallback for wallet suspension/dispute status.
- `source-confirmed` `worker/src/lib/managers/WalletManager.ts:117-152` finalizes escrow, updates wallet KV, handles disallow list, and syncs ClickHouse spend when wallet sync is stale.
- `source-confirmed` `worker/src/lib/managers/WalletManager.ts:193-221` adds a provider/model to disallow list when a successful non-stream request had no parsed cost, then invalidates disallow-list cache.
- `source-confirmed` `worker/src/lib/ai-gateway/WalletKVSync.ts:18-45` caches remaining wallet balance in KV for five minutes.
- `source-confirmed` `worker/src/lib/ai-gateway/DisallowListKVSync.ts:13-41` caches disallow list in KV for one hour and supports invalidation.
- `source-confirmed` `worker/src/lib/managers/StripeManager.ts:85-103` verifies Stripe webhook signature.
- `source-confirmed` `worker/src/lib/managers/StripeManager.ts:106-152` dispatches payment intent, refund, and dispute webhook events.
- `source-confirmed` `worker/src/lib/managers/StripeManager.ts:182-188` skips already-processed payment events.
- `source-confirmed` `worker/src/lib/managers/StripeManager.ts:229-259` adds credits for token-usage payments and resets auto-topoff failure/timestamp data after successful auto top-off.
- `source-confirmed` `worker/src/lib/managers/StripeManager.ts:262-268` currently disables automatic refund processing while in beta.
- `source-confirmed` `worker/src/lib/managers/StripeManager.ts:421-554` processes dispute creation by verifying currency/product/customer/org, checking idempotency, and adding the dispute to wallet.
- `source-confirmed` `worker/src/lib/managers/StripeManager.ts:556-607` processes dispute updates/closed events idempotently.

HUAKAI delta: payment in HUAKAI cannot be "just Stripe/Alipay plugin". The production feature is wallet state plus idempotent webhook, escrow/reserve/finalize/cancel, refund safety, dispute suspension, stale escrow cleanup, and admin recovery.

### Admin support workflows

- `source-confirmed` `worker/src/routers/api/walletRouter.ts:13-43` validates admin auth with a manual access key using constant-time comparison.
- `source-confirmed` `worker/src/routers/api/walletRouter.ts:51-80` exposes current wallet state for the authenticated org.
- `source-confirmed` `worker/src/routers/api/walletRouter.ts:118-143` exposes admin wallet state for any org.
- `source-confirmed` `worker/src/routers/api/walletRouter.ts:145-213` exposes paginated wallet-local table inspection, with table names restricted to an allowlist.
- `source-confirmed` `worker/src/routers/api/walletRouter.ts:215-300` exposes admin balance modification with required amount/type/reason/reference/admin user fields and logs the modification.
- `source-confirmed` `worker/src/routers/api/walletRouter.ts:303-352` exposes admin removal from disallow list.
- `source-confirmed` `worker/src/routers/api/walletRouter.ts:354-408` receives Stripe webhooks, checks secret/signature/body, verifies the event, handles the event, maps refund-over-effective-balance to 400, and returns 500 for other handling errors.

HUAKAI delta: Admin Lite needs real incident workflows, not only CRUD. Minimum production workflow: search request id, inspect route/account/billing attempt, inspect wallet/ledger table, apply correction with reason/reference, remove provider/model from disallow list, and replay/reconcile failed webhook.

### AI gateway fallback and disallow list behavior

- `source-confirmed` `worker/src/lib/ai-gateway/SimpleAIGateway.ts:166-220` builds all attempts and loads disallow list only when pass-through billing attempts exist.
- `source-confirmed` `worker/src/lib/ai-gateway/SimpleAIGateway.ts:241-349` tries attempts in order, validates unsupported response formats, skips disallowed PTB attempts, bails early for Helicone-generated credit/rate-limit 429s, otherwise continues to the next attempt for provider errors.
- `source-confirmed` `worker/src/lib/ai-gateway/SimpleAIGateway.ts:632-667` loads disallow list from KV or wallet durable object, caches it, and matches provider/model or provider wildcard.
- `source-confirmed` `worker/src/lib/ai-gateway/SimpleAIGateway.ts:742-790` prioritizes returned gateway errors: 403 wallet suspension, 401 auth, non-429 BYOK errors, invalid format, disallowed, all-429, otherwise 500.
- `source-confirmed` `worker/src/lib/ai-gateway/AttemptExecutor.ts:88-183` supports optimistic wallet path using cached balance and blocking escrow path when balance is low, with tracing and auto-topoff trigger on insufficient credit.
- `source-confirmed` `worker/src/lib/ai-gateway/AttemptExecutor.ts:185-270` reserves PTB escrow, executes provider request, cancels escrow on provider failure, and tags PTB traces.
- `source-confirmed` `worker/src/lib/ai-gateway/AttemptExecutor.ts:282-463` builds provider request body, authenticates, preserves original OpenAI request for non-OpenAI response formats, applies auth headers, times provider request, and turns Helicone-generated `429 rate_limited` into a distinct error type.
- `source-confirmed` `worker/src/lib/ai-gateway/AttemptExecutor.ts:465-541` estimates worst-case PTB cost from context length/max completion/pricing, reserves escrow with credit line, and cancels escrow by id.
- `source-confirmed` `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:654-693` finalizes escrow on non-cached responses and cancels escrow for cached responses.

HUAKAI delta: this maps directly to HUAKAI's account-pool and billing correctness problem: do not just "fallback on 429". Separate platform-generated 429 (balance/rate limit), upstream provider 429, disallowed provider/model, wallet suspension, and gateway-local failure.

## Inferred items

- `inferred` Helicone's operational edge is not a single feature; it is the closure between request logging, body retention, analytics, wallet state, and admin recovery.
- `inferred` HUAKAI can avoid GPL implementation risk by adopting a smaller local model: PostgreSQL primary ledger + optional object storage for bodies + rollup tables/materialized views later.
- `inferred` Helicone tolerates one-request overspend for cost-based rate limits. HUAKAI should make that an explicit product decision, not an accidental consequence.

## Open questions

- `open-question` The full async ingestion path from Kafka/worker to ClickHouse was not completely traced in this pass.
- `open-question` The exact hosted-product redaction rules for prompt/response bodies are not fully confirmed from the files read.
- `open-question` Wallet reconciliation with ClickHouse has hooks and staleness fields, but the full reconciliation job path was not fully traced.
- `open-question` The remote request body buffer's unknown-size remote path is commented out in the builder read here; confirm whether a separate branch or deployment flag enables it.

## HUAKAI feature insertions

| Feature ID | Name | Level | Status vs current HUAKAI plan | Recommendation |
| --- | --- | --- | --- | --- |
| `F-OBS-QUERY-001` | Request investigation API | L1/L2 | 覆盖太粗 | Add query/detail path from request id to user/key/route/account/attempt/usage/billing/audit, with redaction and body pointer state. |
| `F-RETENTION-001` | Body retention and redaction policy | L1 | 完全缺失 / implicit | Define default-off body logging, max size, object storage threshold, TTL, omit semantics, billing-recovery exception, and delete path. |
| `F-OBS-ROLLUP-001` | Cost/latency/token/status rollups | L2 | 覆盖太粗 | Add rollups for request count, cost, latency, TTFT, status, model, provider, key/user, cache tokens, and reasoning/audio tokens. |
| `F-RATE-USER-001` | User-facing request/cost rate limits | L2 | 被 F-RATE-001 混淆 | Separate client quota/rate limit from upstream account cooldown. Support request/cost units and user/property/global segments. |
| `F-WALLET-ESCROW-001` | Wallet reserve/finalize/cancel flow | L2 | F-PAY-001 太粗 | Payment plugin must include escrow, processed-webhook idempotency, effective balance, credit line, stale escrow cleanup, and direct-debit fallback policy. |
| `F-WALLET-RECOVERY-001` | Admin wallet recovery workflow | L2 | 完全缺失 | Admin can inspect wallet tables, adjust credit/debit with reason/reference, inspect processed events, remove disallow entries, and audit the action. |
| `F-PAY-DISPUTE-001` | Dispute suspension and resume | L2/L3 | 完全缺失 | Track active payment disputes; suspend paid traffic; resume only after dispute state resolves. |
| `F-PAY-REFUND-002` | Refund effective-balance guard | L2 | 覆盖太粗 | Reject refunds/deductions that exceed effective balance, with operator-visible recovery path. |
| `F-PTB-DISALLOW-001` | Provider/model disallow list after unbillable success | L2 | 完全缺失 | If a successful paid request cannot produce billable cost, temporarily disable that provider/model and require admin recovery. |
| `F-REQ-BODY-STORE-001` | Versioned native/openai body archive | L3 | 缺失 | When body storage is enabled, store normalized user-facing body and native provider body separately for investigation. |

## Priority critique

- L1: request investigation, safe response metadata, body retention/redaction, and request/cost rate-limit basics. These prevent "we charged someone and cannot explain why."
- L2: wallet escrow, webhook idempotency, admin correction, disallow-list recovery, and analytics rollups. These are the difference between a demo gateway and a commercial gateway.
- L3: native/OpenAI dual body archive, advanced sessions, cache-savings analytics, and full dispute lifecycle UI.
- L4: Helicone-style broad prompt/session/feedback platform can wait unless HUAKAI intentionally becomes an observability SaaS.

