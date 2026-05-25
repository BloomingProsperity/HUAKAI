# Source-read research: Helicone, Envoy AI Gateway, All API Hub

Lane: specifier (read source -> behavior summary; no copy)
Agent: general-purpose (parallel sub-agent dispatched by Claude PM)
UTC timestamp: 2026-05-09T05:30Z

Repos and commits in scope:

- `~/refs/helicone/` — Helicone/helicone @ `3f4bd44b85f9837feb4a696cce4bba6c99fbdc7e`, Apache-2.0
- `~/refs/envoy-ai-gateway/` — envoyproxy/ai-gateway @ `4d3eae8b35c4ccc41643d94bb5f69280846561b0`, Apache-2.0
- `~/refs/all-api-hub/` — qixing-jk/all-api-hub @ `893e832d0f9211763f549a17abb7364ea9b39ce0`, **AGPL-3.0** with MIT-derived portions from `fxaxg/one-api-hub` (combined notice in LICENSE)

License posture summary (read first per clean-room policy):

- Helicone and Envoy AI Gateway are Apache-2.0 — patent grant intact, NOTICE preservation required if any file copied (we copy nothing).
- **all-api-hub is AGPL-3.0**, the strictest of the three. Source-read for behavior is fine, but ANY mechanical translation, structure mirroring, or distinctive identifier reuse would tag a HUAKAI service as a derivative work. The MIT-licensed upstream `one-api-hub` portion is not separated by directory in the LICENSE, so we treat the whole repo as AGPL for safety. **Decision: do not copy any function name, struct field, comment, or file path. Paraphrase only.**

---

## A. Cache & fan-out — three-direction novelty verification

### A.1 Helicone — does the proxy cache prompt content cross-request?

**Yes, but only when a customer header opts in. There is no implicit cross-request caching.**

What `cacheFunctions.ts` does (worker/src/lib/util/cache/cacheFunctions.ts:1-90 read):

- Builds a cache key by hashing: a salt header (`helicone-cache-*` family), the request URL, the request body (with caller-listed ignore keys removed), and an authorization header derived only from `Helicone-Auth` plus non-Google-flavored `Authorization`. A counter index lets the same key serve N stored responses (the "free index").
- The key is per-tenant and per-customer-credential; there is no cache sharing across organisations or users.
- The cache stores final upstream responses. There is no prefix-level partial reuse, no semantic match, no embedding similarity — pure exact-key hash.
- The store is Cloudflare KV with a small in-memory shim (`inMemoryCache.ts`) and an encrypted variant (`secureCache.ts`).
- Cache writes use a back-off retry up to 5 attempts.

What this means for HUAKAI's PASR / cache-aware routing claim:

- Helicone caches the **response**, never the prompt prefix; it does NOT route based on which provider already ingested similar prefixes; it does NOT signal cache locality between attempts. This is end-result memoization keyed by exact content.
- HUAKAI's PASR (cache-locality-aware ranking + miss demotion) is therefore not duplicated by Helicone.

### A.2 Envoy AI Gateway — prompt-cache-aware routing?

**No. Prompt-cache fields are passed through as billing/metering metadata only; routing never reads them.**

Evidence:

- Schema-level: the OpenAI request schema accepts `prompt_cache_key` and `prompt_cache_retention` and the Anthropic/OpenAI translators forward those fields verbatim (`internal/apischema/openai/openai.go:2241,2295,5948,5953` for the field shape; tests at `internal/translator/openai_openai_test.go:319,458` show cached-token usage data flows through unchanged).
- LLMRequestCost spec recognises three cache-related cost types — `CachedInputToken`, `CacheCreationInputToken` — and emits them as Envoy dynamic-metadata for downstream BackendTrafficPolicy rate limits and billing math (`api/v1alpha1/shared_types.go:117-143`, `api/v1alpha1/quota_policy.go:58,87`, `api/v1alpha1/ai_gateway_route.go:96-144`).
- Tracing layer attaches `llm.token_count.prompt_details.cache_read` / `cache_creation` for OTel/OpenInference (`internal/tracing/openinference/openinference.go:161,166`).
- No file under `internal/extproc/`, `internal/translator/`, or `internal/controller/` consults a cache hint when picking a backend. Backend selection is the standard Envoy HTTPRoute logic plus AI Gateway's body-extracted model-name match (`api/v1alpha1/ai_gateway_route.go:67-83`).

Conclusion: Envoy's contribution is observability + cost (downstream policy can reduce a budget by cached-input tokens), not routing. HUAKAI's PASR remains novel.

### A.3 All API Hub — cache-replication or request-decomposition?

**No.** all-api-hub is a browser extension management plane (Chrome/Edge/Firefox WebExtension via `wxt.config.ts`); there is no proxy server in the codebase. The only cache mentions are:

- Per-vendor in-memory token caches inside auth helpers (`src/services/apiService/octopus/auth.ts:104-166`, `src/services/apiService/axonHub/index.ts:183-468`) — short-lived bearer-token reuse to avoid re-login. Not request caching.
- `src/services/apiService/common/compatHeaders.ts:13,50` — uses the word "fan-out" but means **HTTP header value fan-out**: writing the same `userId` value across multiple compatibility header names (`New-API-User`, `Veloera-User`, `voapi-user`, `User-id`, `Rix-Api-User`, `neo-api-user`) so different forks of new-api accept the same call. This is per-request header expansion, not request fan-out / decomposition.

No prompt-cache, no cross-request replication, no sub-prompt routing.

### A.4 Three-direction novelty — net result

| Direction | Helicone | Envoy AI GW | All API Hub | HUAKAI claim still novel? |
|---|---|---|---|---|
| Prompt-prefix cache-aware routing (PASR) | No (exact-key response cache) | No (passthrough metadata) | No (extension, no proxy) | **Yes** |
| Sub-prompt fan-out / decomposition | No | No | No | **Yes** |
| Cache-locality scoring + miss demote | No | No | No | **Yes** |

---

## B. Failover / retries

### B.1 Helicone — at what layer?

Helicone has **two distinct retry surfaces**:

1. **AI Gateway sequential attempts** — `worker/src/lib/ai-gateway/SimpleAIGateway.ts:240-345` reads:
   - Caller specifies one or more model strings, comma-separated (`parseAndPrepareRequest` at line 397). Optional `!provider` syntax globally excludes a vendor.
   - `AttemptBuilder.buildAttempts` produces an ordered list of `{endpoint, providerKey, plugins, source, authType}` records.
   - The handler iterates attempts; on each provider error it pushes the structured error into an array and continues. On a Helicone-internal 429 (insufficient credit OR upstream-rate-limited as judged by Helicone) when no prior errors yet exist, the loop breaks early. Otherwise loops to the next candidate. First success returns.
   - Each attempt may reserve escrow before it runs. Optimistic path skips waiting for escrow when wallet KV cache shows >$4.50 effective balance; otherwise blocking path waits for escrow result and triggers auto-topoff if escrow fails for credit-limit reason (`AttemptExecutor.ts:88-183`).
   - On error, the reserved escrow is cancelled in the background (`waitUntil`).
   - This is **provider-list ordered retry with credit reservation/cancel**; it is **not** speculative/parallel/hedged.
2. **Cache write retries** — bounded retry inside `cacheFunctions.ts` (`CACHE_BACKOFF_RETRIES = 5`).

There is no explicit hedging or speculative parallel execution.

### B.2 Envoy AI Gateway — retry / fallback / hedging policies

**Envoy AI Gateway delegates retry and failover entirely to upstream Envoy Gateway primitives.** It does not invent its own retry loop.

- `api/v1alpha1/ai_gateway_route.go:28-30,214-219` (read) explicitly says: "you can configure the retry fallback behavior by attaching BackendTrafficPolicy" and "you can achieve fallback behavior by configuring multiple backends combined with the BackendTrafficPolicy of Envoy Gateway. Please refer to https://gateway.envoyproxy.io/docs/tasks/traffic/failover/ as well as https://gateway.envoyproxy.io/docs/tasks/traffic/retry/."
- For InferencePool routes, "Fallback behavior is handled by the InferencePool's endpoint picker" (line 214) — pushed down to the Gateway API Inference Extension.
- The single ai-gateway-specific retry mention is `processor_impl.go:297` `onRetry()` which only signals "Envoy is retrying this request, force re-mutate body/headers" — used to re-write the request body when a translator must reapply mutation on a retried attempt. This is **plumbing for Envoy's retry, not its own retry policy**.
- No hedge / speculative dispatch / latency-bound parallel retry exists in any file under `internal/`.

Net: Envoy AI Gateway expects the operator to express retry as a Kubernetes `BackendTrafficPolicy`, in line with envoyproxy/gateway primitives. The novelty (if any) is purely in **request-body remutation across retry attempts** — relevant for HUAKAI when we change vendor between retries (request body must be re-translated, not just resent).

---

## C. Account-hub feature inventory — the differentiation question

Decisive finding: **all-api-hub is a client-side WebExtension, not a server**. It is the **management plane / aggregator UI on top of existing new-api / veloera / one-api / voapi / anyrouter / claude-code-hub deployments**. It is not itself a gateway. The README is explicit: "一站式管理 New API 兼容中转站账号".

This is a critical clarification: HUAKAI memory framing "all-api-hub is the differentiation" needs to be read as **"all-api-hub demonstrates the OPERATOR/CONSUMER UX layer that HUAKAI must build on top of its gateway"** — not as a competing gateway implementation.

### C.1 Capabilities inventory

| Capability | Implementation file (cited path is for source attribution; not copied) | One-line behavior |
|---|---|---|
| Account registration / signup automation | `src/services/accounts/accountOperations.ts` + `src/services/siteDetection/autoDetectService.ts` + `src/services/siteDetection/detectSiteType.ts` | The user pastes a base URL; auto-detect probes the site to classify which fork (new-api/veloera/etc.), reads cookies via the WebExtension cookie API, fetches user-info from the matched fork's user endpoint, then persists a `SiteAccount`. There is no signup automation per se — this is **bring-your-own-account discovery + classification** |
| Balance / quota dashboard | `src/features/AccountManagement/`, `src/features/UsageAnalytics/`, `src/features/BalanceHistory/` (UI) backed by `src/services/accounts/accountStorage.ts` and `src/services/accounts/autoRefreshService.ts` | A single-interval timer polls each saved site for balance/usage with backoff and broadcasts results to any open popup/options page; UI then renders a multi-site overview table + heatmap + slow-request analysis |
| Auto check-in (daily) | `src/services/checkin/autoCheckin/scheduler.ts` (2633 LoC) + `src/services/checkin/autoCheckin/storage.ts` + `src/services/checkin/autoCheckin/providers/{newApi,anyrouter,veloera,wong}.ts` + `shared.ts` | One daily Chrome-alarm per local day fires inside a configured time window (deterministic time OR randomized within window); per-account check-in goes through a vendor-specific provider (each provider implements `canCheckIn` + `checkIn`); failures go on a separate retry alarm; success triggers a post-checkin balance refresh; UI surfaces are notified via runtime messages; there is a UI-open pre-trigger that may run early when the user opens the extension during the window |
| Key management / one-click key issuance | `src/services/accounts/accountKeyAutoProvisioning/{ensureDefaultToken,perOriginQueue,repair}.ts` + `src/services/managedSites/tokenBatchExport.ts` + `src/services/apiCredentialProfiles/` | When a `SiteAccount` lacks a usable API key, "ensure default token" provisions one through the site's `POST /api/token/` (paraphrased endpoint name), serialised per-origin via a small queue to avoid concurrent duplicate creation; the credential profile store keeps `URL+key+model-set` triples that the user can tag and one-click export to AI tools |
| Price comparison | `src/services/apiCredentialProfiles/modelCatalog.ts` + UI under `src/features/ModelList/` | Fetches each site's model price multiplier + group-multiplier; renders a comparison table allowing the user to identify the cheapest site for a given model (the README calls this "实际折合单价") |
| Health / availability checks | `src/services/verification/aiApiVerification/{apiVerificationService.ts,suiteRunner.ts,probeRegistry.ts,probes.ts}` + `src/services/verification/cliSupportVerification/` + `src/services/verification/webAiApiCheck/` + `src/types.ts:SiteHealthStatus` | A probe-registry-driven verification suite tests `(baseUrl, apiKey, modelId)` triplets; individual probes can be re-run; CLI-proxy compatibility (Cherry, ClaudeCodeRouter, Kilo) is a separate verification dimension; SiteHealthStatus is rolled up per account |
| Channel management (admin operations on managed sites) | `src/services/managedSites/managedSiteService.ts` + `channelMatch.ts` + `channelMatchResolver.ts` + `channelMigration.ts` + `channelModelFilterRules.ts` + `tokenChannelStatus.ts` + `providers/{newApi,axonHub,claudeCodeHub,doneHubService,octopus,veloera}.ts` | For sites where the user is admin, the extension can search/list/create/update upstream **channels** (the new-api term for "an upstream provider account configured inside the relay site") via the site's admin API; channel-match resolves which channel a token's call would land on; model filter rules and migration helpers help operators reorganize channels after model renames |
| Redemption codes | `src/services/redemption/redeemService.ts` + `redemptionAssist.ts` | Apply a code against an account by calling each fork's redeem endpoint and updating local balance display |

### C.2 Is this a SaaS UI or a full gateway?

**It is a client-side aggregator UI.** Strict evidence:

- `wxt.config.ts` exists; entrypoints live in `src/entrypoints/{background,content,options,popup,sidepanel}` (WebExtension folders).
- No Express/Fastify/Next API route handler that proxies LLM calls.
- All "API service" calls (e.g., `src/services/apiService/openaiCompatible/`, `src/services/apiService/anthropic/`, `src/services/apiService/google/`) are **the user's browser calling the relay site directly** — there is no HUAKAI-style middle proxy.
- The "auto-detect" path leans heavily on the WebExtension cookie API (`src/utils/browser/cookieString.ts`) — only possible inside a browser, not a server.

So all-api-hub answers a different question than HUAKAI:
- **HUAKAI = the gateway (server-side request forwarder + billing + caching + routing)**
- **all-api-hub = the consumer UX on top of N gateways (browser-side aggregator + auto-checkin + price comparison + bring-your-own-key)**

The two are complementary. The HUAKAI memory note "account-hub features are the real differentiation" should be re-interpreted: among the three directions in scope, **the all-api-hub-style consumer/operator UX is the layer HUAKAI does not yet have, and is the most defensible product moat against bare gateways like Envoy AI GW or pure observability like Helicone.** The differentiation lever is not "we're a better gateway than envoy" — it's "HUAKAI is the only stack that ships gateway + observability + consumer/operator account hub as one product."

### C.3 Concrete account-hub features HUAKAI should adopt (as Safe Equivalent paraphrases, NOT line-by-line)

Highest-leverage and lowest license risk:

1. **Auto-checkin scheduler** (vendor-pluggable) — the all-api-hub model is right; HUAKAI implements its own with at most a one-line conceptual cite in the design doc, no code-level reuse. The design model worth adopting: separate daily-alarm and retry-alarm; in-flight guard keyed by local-day; vendor providers behind a tiny `{canCheckIn, checkIn}` interface; deterministic-OR-random schedule mode within a user time window.
2. **Per-account auto-refresh of balance/usage** with broadcast to UI surfaces.
3. **Verification probe registry** — `(baseUrl, apiKey, modelId) -> {model probe, token compat probe, CLI compat probe}`; HUAKAI wires CLI-compat as a first-class signal because we ship our own CLI clients.
4. **Credential profile store** — `URL + key + tag + favourite model` triples that one-click-export to Cherry / Cursor / Kilo / claude-code-router.
5. **Price comparison view** — pulls from HUAKAI's own price registry plus user-added external sites.
6. **Channel migration helpers** — for HUAKAI, this is **internal admin tooling on our own routing table**; same UX shape, different backing store.
7. **Bring-your-own-account flow** with auto-detect + cookie-based bring-up — useful for users migrating from existing relay sites.

What we will NOT adopt:
- AGPL-flavoured implementation copies. Any code must be written fresh against the behavior summary above.
- Anything that depends on browser-cookie scraping if it would be done server-side from HUAKAI (this is the cleanroom + privacy boundary).

---

## D. Observability patterns (Helicone's strength)

### D.1 Async usage write — batches/queues + failure recovery

Helicone's path (worker/src/lib/clients/producers/HeliconeProducer.ts read):

- A `MessageProducerFactory` selects between three implementations: Kafka (Upstash), AWS SQS, or a `DualWriteProducer` that fans the same message to both Kafka and SQS at once.
- A fourth path is HTTP fallback to Valhalla (`/v1/log/request`), used when the manual access key matches a special internal value or when the producer is null.
- `HeliconeProducer.setLowerPriority()` exists — the producer can be downgraded by the worker for non-critical paths.
- The DBLoggable path on the worker (`worker/src/lib/dbLogger/DBLoggable.ts`) packages all the per-request data — request/response bodies, prompt template hydration, attempt info, escrow info, time-to-first-token, threat flags, country code — into a single `MessageData` record and pushes once.
- On the consumer side (`valhalla/jawn/src/lib/consumer/consumeMiniBatch.ts`), Jawn pulls mini-batches and runs a chain-of-responsibility through `AbstractLogHandler` subclasses: AuthenticationHandler, RateLimitHandler, LoggingHandler, PromptHandler, ExperimentHandler, OnlineEvalHandler, RequestBodyHandler, ResponseBodyHandler, S3ReaderHandler, PostHogHandler, LytixHandler, SegmentLogHandler, StripeIntegrationHandler, StripeLogHandler, WebhookHandler. Each handler can short-circuit; failures are isolated per handler.
- Failure recovery: dual-write to Kafka+SQS gives at-least-once across two unrelated queues; the HTTP fallback rescues when neither is available; per-handler isolation in the consumer means one downstream (e.g., PostHog or Stripe) failing does not block S3 body archival.

What HUAKAI's billing/settler should learn:

1. **Hot path writes one message; cold path is a chain of handlers**. HUAKAI's settle path can adopt this shape — one durable message into our queue, then a chain of separable consumers (settle wallet, post to admin metrics, fan out to webhook, write S3 body) with each one short-circuit-isolated.
2. **Dual-write across two unrelated queues** is the cheap durability lever when one queue has a regional outage. Worth a HUAKAI-Phase-2 flag.
3. **`setLowerPriority()` knob** for non-critical observability traffic so it cannot starve billing.
4. **Manual-access-key escape hatch**: a special key value bypasses the queue and writes synchronously via HTTP. This matters for debug/replay scenarios.

### D.2 Per-account metrics collection

Helicone collects per-organisation metrics by passing `organizationId` as the partition key on every queue message; consumer handlers stamp clickhouse rows with the org id; the `MetricsManager` and `WalletManager` aggregate from clickhouse. Wallet state lives both in a Cloudflare Durable Object (`worker/src/lib/durable-objects/Wallet.ts`) **and** is mirrored to a KV cache (`WalletKVSync.ts`) so the AI-gateway hot path can read balance optimistically without a Durable Object round trip.

Lessons:
- KV-mirrored wallet for hot-path balance reads is the right shape — HUAKAI should ensure the gateway's per-request quota check is similarly cache-fronted with bounded staleness.
- The auto-topoff alarm pattern (`Wallet.ts:918-964`): a Durable Object stores an alarm; when the alarm fires, it runs the topoff logic. Same pattern HUAKAI's settler/notifier can use for cron-like work without an external scheduler.

### D.3 Anything HUAKAI's billing/settler should learn?

Concrete, non-copy, paraphrased items for the HUAKAI roadmap:

1. **Escrow reserve before request, cancel on failure, settle on success** — this prevents over-spend during retries when a request is in flight. Helicone reserves worst-case-cost = `contextLength * inputPrice + maxCompletionTokens * outputPrice`. We should adopt the same shape with our own pricing function.
2. **Optimistic vs blocking path** — when balance is well above worst-case, don't await escrow; otherwise block. HUAKAI's PASR + escrow combination becomes a two-axis decision: cache-locality (best provider) AND escrow-mode (optimistic if rich, blocking if tight).
3. **Pre-flight `cache_creation_input_tokens` cost type as a separate budget bucket** (see Envoy A.2 ref) — per-customer rate-limit on cache-creation tokens prevents prompt-warmup abuse.
4. **Per-handler isolation in the consumer chain** — never let a Stripe outage block our wallet update.

---

## E. Enterprise gateway patterns (Envoy AI Gateway)

### E.1 Provider abstraction model — how is a "provider account" represented?

Envoy AI Gateway models a provider account as a graph of three k8s-native CRDs:

- `AIGatewayRoute` — top-level routing rules, maps incoming model header `x-ai-eg-model` to one or more `AIServiceBackend` references. Read: `api/v1alpha1/ai_gateway_route.go:13-83,196-260`.
- `AIServiceBackend` — wraps a single external provider endpoint (OpenAI / Anthropic / Bedrock / etc.) with a schema declaration and per-backend header/body mutations. Read: `api/v1alpha1/ai_service_backend.go:1-130`.
- `BackendSecurityPolicy` — credential reference (api-key Secret OR AWS/Azure/GCP cloud-IAM identity OR OIDC) attached to a backend.

Key insight: the provider abstraction is **per-endpoint-credential** rather than per-provider — meaning "OpenAI account A in us-east" and "OpenAI account B in eu-west" are two distinct AIServiceBackend objects. HUAKAI's account pool already does this at the data-model level (each row is a single credential); this is a confirmation our model is right.

Backend auth is dispatched at runtime by `internal/backendauth/auth.go:NewHandler` (read; 33 lines) which switches on which sub-config is set: `AWSAuth | APIKey | AzureAPIKey | AzureAuth | GCPAuth | AnthropicAPIKey`. Each handler implements a uniform `BackendAuthHandler` interface that mutates request headers/body to add the right auth before forwarding.

### E.2 Auth / credential rotation — anything novel?

Yes. Envoy AI Gateway has a real rotation framework that HUAKAI should learn from.

Read: `internal/controller/rotators/aws_oidc_rotator.go:1-120` and the surrounding files (`azure_token_rotator.go`, `gcp_oidc_token_rotator.go`, `gcp_token_rotator.go`).

The shape:

- **`preRotationWindow`** — each rotator stores expiration on a k8s Secret annotation and the controller pre-rotates **before** expiry (e.g., the AWS OIDC rotator computes `preRotationTime = expirationTime - preRotationWindow`).
- **OIDC -> STS exchange** for AWS: the rotator takes an OIDC identity provider ref + role ARN and uses STS AssumeRoleWithWebIdentity (paraphrased) to mint a short-lived AWS credential on a schedule.
- **Per-vendor token providers** (`internal/controller/tokenprovider/`) — separate Azure-client-secret and GCP-OIDC providers, each behind a uniform `TokenProvider` interface. The controller composes provider + rotator and writes the rotated material back to a k8s Secret.
- **Policy-driven**: rotation behavior is declared in `BackendSecurityPolicy`, not buried in code; operator changes the policy and the controller reconciles.

Lessons for HUAKAI:

1. HUAKAI today does long-lived API-key storage. Adopt the **pre-rotation window** concept: every credential record can carry an `expiresAt` and a `rotateBefore` (e.g., 30 minutes). A background rotator runs in time and produces a new credential before the old expires; the gateway reads from the latest `Active` credential row.
2. HUAKAI should structure credential acquisition as `TokenProvider` interface with vendor implementations (anthropic-oauth, openai-oauth, google-oidc, aws-sts, etc.), so future cloud-IAM credentials slot in without changing the gateway hot path.
3. The **k8s-Secret annotation as rotation state** has a HUAKAI parallel: a small `credentials_rotation_state` table keyed by credential id, holding `expires_at`, `last_rotated_at`, `next_attempt_at`, `attempt_count`, plus an exponential backoff column when rotation fails.
4. Most novel: **OIDC-to-cloud STS exchange** lets a HUAKAI deployment in a customer's k8s cluster mint cloud creds without storing long-lived secrets at all. Mark as `Mandatory Roadmap` for the enterprise tier.

---

## Summary cross-reference table for the planning lane

| HUAKAI claim | Confirmed novel vs Helicone | Confirmed novel vs Envoy AI GW | Confirmed novel vs all-api-hub |
|---|---|---|---|
| PASR (prompt-prefix-cache-aware ranking) | Yes — Helicone caches responses, not prefix locality | Yes — Envoy treats cache fields as billing metadata only | Yes — all-api-hub is not a proxy |
| Sub-prompt fan-out / decomposition | Yes — Helicone does ordered retry, not decomposition | Yes — no fan-out logic in extproc/translator | Yes — extension only |
| Cache-miss demote + locality scoring | Yes | Yes | Yes |
| Escrow reserve/cancel | **No — Helicone does this**. HUAKAI should adopt as Safe Equivalent | N/A | N/A |
| Provider-list ordered retry | **No — Helicone does this with comma-separated model strings**. HUAKAI should keep our own retry semantics but borrow the multi-error response shape | Indirect via BackendTrafficPolicy | N/A |
| Pre-rotation window for credentials | N/A | **No — Envoy rotators do this**. HUAKAI should adopt | N/A |
| Per-handler-isolated async log consumer chain | **No — Helicone Jawn does this**. HUAKAI should adopt | N/A | N/A |
| Vendor-pluggable auto-checkin scheduler | N/A | N/A | **No — all-api-hub does this**. HUAKAI should adopt with paraphrased re-implementation (AGPL-clean) |
| Verification probe registry | N/A | N/A | **No — all-api-hub does this**. HUAKAI should adopt |
| Credential profile + one-click CLI export | N/A | N/A | **No — all-api-hub does this**. HUAKAI should adopt |
| Price comparison UI | N/A | N/A | **No — all-api-hub does this**. HUAKAI should adopt |

---

## Source files read

- `~/refs/all-api-hub/LICENSE`
- `~/refs/all-api-hub/README.md`
- `~/refs/all-api-hub/src/services/checkin/autoCheckin/scheduler.ts` (lines 1-549)
- `~/refs/all-api-hub/src/services/checkin/autoCheckin/storage.ts` (header)
- `~/refs/all-api-hub/src/services/checkin/autoCheckin/providers/index.ts` (full)
- `~/refs/all-api-hub/src/services/checkin/autoCheckin/providers/shared.ts` (full)
- `~/refs/all-api-hub/src/services/checkin/autoCheckin/providers/anyrouter.ts` (head)
- `~/refs/all-api-hub/src/services/checkin/autoCheckin/providers/newApi.ts` (head)
- `~/refs/all-api-hub/src/services/accounts/accountOperations.ts` (head)
- `~/refs/all-api-hub/src/services/accounts/accountKeyAutoProvisioning/index.ts` (full)
- `~/refs/all-api-hub/src/services/accounts/autoRefreshService.ts` (head)
- `~/refs/all-api-hub/src/services/managedSites/managedSiteService.ts` (head)
- `~/refs/all-api-hub/src/services/verification/aiApiVerification/apiVerificationService.ts` (head)
- `~/refs/all-api-hub/src/services/redemption/redeemService.ts` (head)
- `~/refs/all-api-hub/src/services/apiService/common/compatHeaders.ts` (full)
- (directory enumeration only) `~/refs/all-api-hub/src/services/{accounts,apiCredentialProfiles,apiService,checkin,history,importExport,integrations,managedSites,models,notifications,permissions,preferences,redemption,search,sharing,siteAnnouncements,siteDetection,tags,updates,verification,webdav}/`
- (directory enumeration only) `~/refs/all-api-hub/src/features/`
- `~/refs/envoy-ai-gateway/api/v1alpha1/ai_gateway_route.go` (lines 1-260)
- `~/refs/envoy-ai-gateway/api/v1alpha1/ai_service_backend.go` (full)
- `~/refs/envoy-ai-gateway/internal/backendauth/auth.go` (full)
- `~/refs/envoy-ai-gateway/internal/controller/rotators/aws_oidc_rotator.go` (lines 1-120)
- `~/refs/envoy-ai-gateway/api/v1alpha1/shared_types.go` (cache-related grep hits)
- `~/refs/envoy-ai-gateway/api/v1alpha1/quota_policy.go` (cache-related grep hits)
- `~/refs/envoy-ai-gateway/internal/extproc/processor_impl.go` (retry-related grep hits)
- `~/refs/envoy-ai-gateway/internal/apischema/openai/openai.go` (prompt-cache field grep hits)
- `~/refs/envoy-ai-gateway/internal/tracing/openinference/openinference.go` (cache token attribute grep hits)
- (directory enumeration only) `~/refs/envoy-ai-gateway/internal/{extproc,translator,backendauth,controller,controller/rotators,controller/tokenprovider}/`
- `~/refs/helicone/worker/src/lib/util/cache/cacheFunctions.ts` (lines 1-90)
- `~/refs/helicone/worker/src/lib/ai-gateway/AttemptExecutor.ts` (full, 543 lines)
- `~/refs/helicone/worker/src/lib/ai-gateway/SimpleAIGateway.ts` (lines 1-520)
- `~/refs/helicone/worker/src/lib/clients/producers/HeliconeProducer.ts` (header)
- `~/refs/helicone/worker/src/lib/dbLogger/DBLoggable.ts` (lines 1-200)
- `~/refs/helicone/valhalla/jawn/src/managers/creditsManager.ts` (header)
- (directory enumeration only) `~/refs/helicone/{worker/src/lib/{ai-gateway,clients/producers,db,dbLogger,durable-objects,managers,models,monitoring,rate-limit,util/cache},valhalla/jawn/src/{lib/consumer,lib/handlers,managers,managers/wallet}}/`

Lane: specifier
Agent: general-purpose
UTC timestamp: 2026-05-09T05:30Z
