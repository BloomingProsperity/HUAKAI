# Source Read: one-api / Portkey-AI gateway / LiteLLM

Verifying the three-direction-evaluation claim that "no project does cross-account cache replication, request decomposition, or predictive migration".

Lane: specifier (clean-room behavior summary; no verbatim copy)
Pinned commits:

- `songquanpeng/one-api@8df4a2670b98266bd287c698243fff327d9748cf` (MIT)
- `Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23` (MIT)
- `BerriAI/litellm@b5d3a5fc856ed1cf9b101d37bd0ec6d6d44751b2` (mixed; `enterprise/` SKIPPED — separate license)

Citations use the `<repo>@<sha>:<file>:<line>` form. Identifiers below are HUAKAI-side paraphrases — they are not the upstream symbol names.

---

## TL;DR for HUAKAI's three-direction claim

| Claim                                                  | Verdict after source read                                                                                                                                                                                          |
| ------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| "No project does cross-account prompt-cache locality"  | **PARTIALLY FALSE.** LiteLLM has a working pre-call filter that pins prompts with `cache_control` markers to the deployment that previously served the same cacheable prefix.                                       |
| "No project proactively warms caches on backup accounts" | **TRUE** for these three. None warm a backup deployment's cache. Warmup terminology in litellm is for spend counters / Swagger / OAuth tokens, not prompt cache.                                                  |
| "No project decomposes one inbound request into N upstream calls" | **TRUE for chat-style requests** in all three. The only fan-out is litellm's `batch_completion`, which is N inbound `messages` lists in one Python call (user-driven), not gateway-internal split. |
| "No project does predictive migration / pre-error failover" | **TRUE.** All three are reactive (cooldown / circuit-open after failure). No predictive demotion based on rising-latency or rising-error trend before threshold.                                                  |

**Key correction for HUAKAI architecture deck**: the "first-mover" framing on prompt-cache locality is incorrect. LiteLLM (MIT) ships it as `PromptCachingDeploymentCheck`. HUAKAI's PASR remains differentiated on (a) cross-account replication of cache fingerprint, (b) score-based locality+headroom blending, (c) cache-miss demotion — none of which appear in litellm's per-prefix pin model.

---

## A. Cache awareness

### A1. Prompt-cache locality routing — LiteLLM has it

LiteLLM ships a router pre-call filter that, for prompts containing `cache_control: {"type": "ephemeral"}` markers, pins the request to the same deployment ID that previously served the same cacheable prefix.

How it works (paraphrased):

1. The cacheable prefix is reconstructed by walking messages and stopping at the last `cache_control` marker (covers both content-block-level and message-level markers).
2. A SHA-256 fingerprint is computed over the canonicalized (sorted-key JSON) prefix plus the tools array.
3. The fingerprint maps to a `model_id` in a dual-tier (memory + Redis-style) cache with a 300-second TTL. Lookup happens before deployment selection; if a stored model_id matches a healthy deployment, the filter narrows the candidate set to that single deployment.
4. The handler also requires that the prompt would actually trigger upstream prompt caching (`is_prompt_caching_valid_prompt`, e.g. >1024 tokens for Anthropic).

Citations:

- `litellm@b5d3a5fc:litellm/router_utils/prompt_caching_cache.py:31` — class definition
- `litellm@b5d3a5fc:litellm/router_utils/prompt_caching_cache.py:55-142` — cacheable-prefix extraction (handles both message-level and content-block-level markers)
- `litellm@b5d3a5fc:litellm/router_utils/prompt_caching_cache.py:144-180` — SHA-256 fingerprinting; key namespaced as `deployment:<hash>:prompt_caching`
- `litellm@b5d3a5fc:litellm/router_utils/prompt_caching_cache.py:196-220` — `add_model_id` writes with 300 s TTL
- `litellm@b5d3a5fc:litellm/router_utils/pre_call_checks/prompt_caching_deployment_check.py:23-49` — pre-call filter narrows healthy_deployments to the stored model_id when it matches
- `litellm@b5d3a5fc:litellm/router_utils/pre_call_checks/prompt_caching_deployment_check.py:51-100` — `async_log_success_event` writes the model_id back to cache after a successful chat / `anthropic_messages` call

What litellm does NOT do (PASR's differentiators still hold):

- No replication of the locality marker across accounts (fingerprint binds to **one** deployment_id; if it's down, the filter falls back to the full healthy_deployments set without any locality awareness).
- No score blending (locality + headroom). It's hard pin: "if cache hit recorded, narrow to that one deployment, else use baseline strategy."
- No cache-miss demotion. If the pinned deployment turns out not to actually have the cache (e.g. Anthropic evicted), the response is still served and counted as success — no penalty applied to the locality cache itself, no fallback narrowing logic.
- No support for non-`cache_control` providers (i.e. providers that auto-cache without explicit markers) — `extract_cacheable_prefix` returns empty, so no key, so no locality.

There is also a distinct `AnthropicCacheControlHook` (`litellm@b5d3a5fc:litellm/integrations/anthropic_cache_control_hook.py:1-60`) which **injects** `cache_control` markers at user-specified message indices. This is a request-shaping hook, not a routing decision — it does not consult any locality store.

### A2. Cache warming on backup accounts

None of the three repos do this for prompt caches. Searches for `warm`, `prefetch`, `preload`, `prewarm` in litellm only surface:

- spend-counter cache warming (`litellm@b5d3a5fc:litellm/proxy/db/spend_counter_reseed.py:31,147,150`)
- Swagger plugin warmup (`litellm@b5d3a5fc:litellm/proxy/_lazy_features.py:327,331`)
- OAuth token cache rewarming (`litellm@b5d3a5fc:litellm/proxy/_experimental/mcp_server/server.py:1037`)
- Fireworks-AI training warm-start parameter (`portkey-gateway@351692fd:src/providers/fireworks-ai/types.ts:70`) — different concept entirely (LoRA fine-tune init)

No code path proactively replays a recent prompt to a backup deployment to seed its prompt cache. PASR's "auto-replicate top-K prefixes to N-1 backup accounts" appears to be unique to HUAKAI in this set.

### A3. LiteLLM Router fallback / retry × cache state

LiteLLM does **not** consult the prompt-cache locality during fallback. The pre-call filter runs once during initial deployment selection. On a cooldown event (`litellm@b5d3a5fc:litellm/router_utils/cooldown_handlers.py:260-320`), the deployment is removed from healthy_deployments for `time_to_cooldown` seconds; the next attempt re-runs deployment selection, which re-runs the prompt-caching filter, which may now return an empty set if the previously-pinned deployment is in cooldown — at which point the filter returns the broad healthy_deployments and routing falls through to the default strategy. So locality is **lost on failover**, not migrated.

### A4. Portkey "fallbacks" granularity

Portkey's fallback is a recursive target tree under one of four strategy modes. Granularity is per-target, not per-account; targets compose recursively (a target can itself be another `loadbalance` / `fallback` / `conditional` block):

- `single` — one provider, no fallback
- `fallback` — try `targets[0]`, on failure (configurable status codes via `onStatusCodes`) try `targets[1]`, etc.
- `loadbalance` — weighted random pick among targets, each weight defaults to 1
- `conditional` — query-DSL evaluator (`$eq`, `$ne`, `$gt`, `$gte`, `$lt`, `$lte`, `$in`, `$nin`, `$regex`, `$and`, `$or`) over `metadata` / `params` / `url.pathname`; routes to a named target

Citations:

- `portkey-gateway@351692fd:src/types/requestBody.ts:23-25,232-233` — `StrategyModes` enum (note: `'scientist'` exists in the type union but no implementation reaches it — `StrategyModes.LOADBALANCE`, `FALLBACK`, `CONDITIONAL`, `SINGLE` are the only branches in `tryTargetsRecursively`)
- `portkey-gateway@351692fd:src/handlers/handlerUtils.ts:476` — `tryTargetsRecursively` entry
- `portkey-gateway@351692fd:src/handlers/handlerUtils.ts:663-690` — `FALLBACK` mode loops targets until response.ok or non-matching `onStatusCodes`
- `portkey-gateway@351692fd:src/handlers/handlerUtils.ts:693-723` — `LOADBALANCE` mode does weighted random with default weight=1
- `portkey-gateway@351692fd:src/services/conditionalRouter.ts:32-156` — full conditional DSL evaluator

No prompt-cache-aware variant. The cache subsystem (`portkey-gateway@351692fd:src/middlewares/cache/index.ts`, `src/handlers/services/cacheService.ts`, `src/shared/services/cache/index.ts`) is a **response cache** (Redis / memory / Cloudflare KV / file backends), not a routing input.

---

## B. Account / channel selection

### B1. one-api channel selection

Algorithm: priority-bucketed random within the highest-priority bucket. On retry, exclude the highest-priority bucket and pick uniformly from the rest.

Behavior steps (paraphrased):

1. On request, look up `(group, model)` → ordered list of channels in `group2model2channels`, sorted descending by `priority`.
2. Find the run length of the top-priority bucket (`endIdx`) — channels with priority equal to `channels[0].priority`.
3. First attempt: `idx = rand.Intn(endIdx)` (uniform within top bucket).
4. Retry attempt with `ignoreFirstPriority=true`: pick from `[endIdx, len(channels))` (lower-priority channels only).

Citations:

- `one-api@8df4a267:model/cache.go:170-217` — `InitChannelCache` builds `group → model → []*Channel` sorted by descending `GetPriority()`
- `one-api@8df4a267:model/cache.go:227-255` — `CacheGetRandomSatisfiedChannel` priority-bucketed random
- `one-api@8df4a267:model/ability.go:22-51` — DB fallback path uses `MAX(priority)` subquery + DB-side `RAND()` / `RANDOM()`
- `one-api@8df4a267:middleware/distributor.go:20-62` — middleware entry; `Distribute()` calls `CacheGetRandomSatisfiedChannel(userGroup, requestModel, false)`
- `one-api@8df4a267:controller/relay.go:65-91` — retry loop with `ignoreFirstPriority=true` for retries; reuses `OriginalModel` and re-runs `SetupContextForSelectedChannel`

No weight, no cost, no latency awareness. Strict priority bucket + random within bucket. No prompt-cache awareness.

### B2. Portkey load-balance strategies

Implemented strategies (`portkey-gateway@351692fd:src/handlers/handlerUtils.ts:662-779`):

- `FALLBACK` — sequential, status-code-gated
- `LOADBALANCE` — weighted random (weight defaults to 1, total weight summed at request time)
- `CONDITIONAL` — query-DSL routing to a named target
- `SINGLE` — direct route

Validator-side (`portkey-gateway@351692fd:src/middlewares/requestValidator/schema/config.ts:20-25`) accepts `'single' | 'loadbalance' | 'fallback' | 'conditional'`. `'scientist'` is in the TypeScript union (`requestBody.ts:233`) but not in the validator and not in the strategy switch — it appears to be an aspirational type-only entry.

No least-busy / lowest-latency / lowest-cost. Portkey's load balancing is intentionally simple-weighted; it offloads observability-driven routing to operators editing the config.

### B3. LiteLLM Router strategies

Far richer than the other two. From `litellm@b5d3a5fc:litellm/router_strategy/`:

| Strategy file                       | Mechanism                                                                                                                                                  |
| ----------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `simple_shuffle.py:21-64`           | Weighted random by `weight` / `rpm` / `tpm` (first non-None wins); else uniform random                                                                     |
| `least_busy.py:11-80`               | In-flight request counter per `(model_group, model_id)`; pick min                                                                                          |
| `lowest_latency.py:23-558`          | Per-deployment rolling latency window (default 10 entries, 1 h TTL); for streaming uses time-to-first-token; filters by tpm/rpm budget; lowest latency + buffer; random pick within buffer |
| `lowest_tpm_rpm_v2.py`              | Pick deployment with most remaining tpm/rpm headroom (v2 of the original)                                                                                  |
| `lowest_cost.py`                    | Pick deployment with cheapest `input_cost_per_token + output_cost_per_token`                                                                               |
| `tag_based_routing.py:get_deployments_for_tag` | Filter by user-supplied tag (e.g. `["free-tier"]`) before applying base strategy                                                                    |
| `budget_limiter.py`                 | Global / per-deployment USD budget caps                                                                                                                    |
| `auto_router/`, `complexity_router/`, `adaptive_router/`, `quality_router/` | Newer routers (referenced from `litellm/router.py:197-207, 476-479`) that pick model based on prompt complexity / quality / adaptive scoring |

Pre-call checks (filter healthy_deployments before strategy picks):

- `prompt_caching_deployment_check.py` (described in §A1)
- `cooldown_handlers.py` removes cooled-down deployments
- Plus `pre_call_checks/` directory has additional filters

---

## C. Request fan-out / decomposition

### C1. None of the three split one inbound request into N upstream calls

Searches for `asyncio.gather` / `asyncio.as_completed` / `ThreadPoolExecutor` in routing paths returned only:

- `litellm@b5d3a5fc:litellm/router.py:2855,2867,2918,4618,4942,5130` — `asyncio.gather(*_tasks)` calls. Each is operator-facing batch APIs: parallel `aimage_generation`, parallel `aembedding`, batched health checks, etc. None are inside the per-request chat/completion path.
- `litellm@b5d3a5fc:litellm/router.py:1945` — `asyncio.gather(*pending, return_exceptions=True)` is for graceful shutdown (collect pending tasks).

The `tryTargetsRecursively` recursion in Portkey is a `for ... await` chain, not parallel. Even `loadbalance` makes one weighted pick and awaits one upstream.

### C2. LiteLLM `batch_completion` is user-driven, not gateway-internal split

`litellm@b5d3a5fc:litellm/batch_completion/main.py:11-120`:

- Function signature takes `messages: List` where each element is itself a message list (i.e. user passes N independent conversations).
- A `ThreadPoolExecutor` (default `max_workers=100`) is opened; each conversation submits one `litellm.completion` call.
- Special-case for vllm: `vllm_handler.batch_completions` (a true single-batch upstream call).
- Results are collected via `future.result()` and returned as a list.

So the inbound shape is N conversations → N upstream calls. **Not** "1 inbound conversation → N parallel upstream attempts." Speculative / hedged / racing requests do not exist in this codebase (greps for `speculative`, `hedge`, `race_request` in non-test, non-enterprise code returned zero non-trivial hits).

### C3. Portkey "conditional routing" is selection only

`portkey-gateway@351692fd:src/services/conditionalRouter.ts:44-62` resolves to exactly one target via `findTarget(name)`. Loop over conditions stops at the first match; `default` target is used if no match. There is no path that returns multiple targets or runs multiple in parallel.

### C4. Vertex multimodal-batch is the only "1 → N decomposition" path

There is one narrow case in litellm where a single inbound semantic request becomes multiple upstream HTTP calls: Vertex AI batch handling (`vertex_ai_batches_instance.create_batch` at `litellm/batches/main.py:342`). But that is a batch-API endpoint mapping, not chat-completion fan-out, and the inbound is still a batch object.

**Verdict on C**: HUAKAI's "request decomposition" framing — splitting one chat into multiple parallel upstream attempts (e.g. for cost / latency hedging, or for cross-account redundancy) — is genuinely not in any of these. If HUAKAI ships it, that is novel for this set.

---

## D. Failover semantics

### D1. All three are reactive, not predictive

| Project   | Trigger                                                                                                       | Granularity            | Cache state preserved? |
| --------- | ------------------------------------------------------------------------------------------------------------- | ---------------------- | ---------------------- |
| one-api   | After a failed relay, retry up to `RetryTimes` with a different bucket; auto-disable channel after rolling success rate threshold breached | Per-channel            | No (each retry replays request body fresh) |
| Portkey   | `onStatusCodes` match → next target in `fallback` chain; circuit breaker disables `isOpen` targets per session | Per-target             | No                     |
| LiteLLM   | Exception → cooldown deployment for `time_to_cooldown` seconds; on stream failure, MidStreamFallbackError triggers `stream_with_fallbacks` to switch deployment **mid-stream** with a continuation prompt synthesized from accumulated chunks | Per-deployment         | Partial — see D3       |

### D2. one-api auto-disable

Two failure-driven channel states:

1. **Hard-disable** on specific upstream errors: 401, `insufficient_quota`, `authentication_error`, `permission_error`, `forbidden`, `invalid_api_key`, `account_deactivated`, plus a string-match list including "credit", "balance", "permission denied", "your access was terminated", "已欠费", etc. (`one-api@8df4a267:monitor/manage.go:11-44`)
2. **Soft-disable on rolling success rate**: a per-channel bool ring buffer of size `MetricQueueSize`. When buffer is full and `successRate < MetricSuccessRateThreshold`, the channel is auto-disabled and an email/message-pusher notification fires. (`one-api@8df4a267:monitor/metric.go:7-79`, `one-api@8df4a267:monitor/channel.go:31-45,47-61,63-77`)

There is no time-based cooldown. Disabled channels stay disabled until manually re-enabled (`EnableChannel` is admin-triggered) or `ShouldEnableChannel` returns true on a successful health-test. No predictive component.

### D3. LiteLLM mid-stream fallback (notable design pattern)

`litellm@b5d3a5fc:litellm/router.py:2052-2194` (`_acompletion_streaming_iterator`):

When a streaming response raises `MidStreamFallbackError` part-way through, the wrapper:

1. Reconstructs the partially-generated content from accumulated chunks (`stream_chunk_builder`).
2. Synthesizes a continuation prompt: original messages + a system message instructing the next model to continue from the partial assistant text + the partial assistant message with `prefix: True`.
3. Triggers the standard fallback chain with the new messages.
4. Fallback stream's chunks are forwarded to the client; usage objects are merged so the partial-stream usage is added to the fallback-stream usage on the same chunk (`_combine_fallback_usage`, `litellm/router.py:2032-2050`).
5. On client disconnect, both streams are closed via `aclose()` shielded from cancellation (anyio).

This is request-state migration (partial generation) but **not** prompt-cache state migration. The new deployment will have to recompute its own prompt cache. There is no mechanism that says "and please prime deployment B's cache with prefix X first."

### D4. Portkey circuit breaker

The user-facing recursion checks `isOpen` on each target before fallback / loadbalance branches:

```
const healthyTargets = (currentTarget.targets || [])
  .map((t, index) => ({...t, originalIndex: index}))
  .filter((t) => !t.isOpen);
```

(`portkey-gateway@351692fd:src/handlers/handlerUtils.ts:646-658`)

After a request, if `isHandlingCircuitBreaker` (i.e. an inherited `id` is present), the gateway calls `c.get('handleCircuitBreakerResponse')` with the response and `cbConfig` (`portkey-gateway@351692fd:src/handlers/handlerUtils.ts:792-799`). The handler itself is injected via `c.set('handleCircuitBreakerResponse', ...)` from outside the open-source repo — this is a hook point for Portkey's hosted control plane. The OSS repo accepts the circuit state but does not contain the threshold/decision logic. So in OSS-only deployments, `isOpen` defaults false and the circuit is effectively disabled.

This is a consequential observation for HUAKAI: **the sophisticated parts of Portkey routing live in their hosted plane, not in the MIT-licensed gateway**. Anyone borrowing must build that decision layer themselves.

### D5. No predictive demotion

None of the three has any pattern matching:

- "deployment latency p95 climbing for last 30 s → demote before user sees timeout"
- "error rate trend slope > X → cool down before threshold"
- "TPM consumption rate suggests budget exhaust in N seconds → drain new traffic now"

LiteLLM's lowest_latency is reactive (uses observed latency for ranking, not for trend detection). one-api's success-rate ring is binary (above/below threshold). Portkey's circuit breaker is delegated to the hosted plane.

---

## E. Production patterns worth borrowing (paraphrased; HUAKAI may consider)

### E1. LiteLLM cooldown_cache callback fan-out

`litellm@b5d3a5fc:litellm/router_utils/cooldown_handlers.py:303-320` — when a deployment enters cooldown, an `asyncio.create_task` fires a `router_cooldown_event_callback`. This decouples observability hooks from the request hot path. PASR could adopt this for "deployment cooled → notify cache-warm replicator to prime backups."

### E2. LiteLLM rolling-window latency with stream-aware variant

`litellm@b5d3a5fc:litellm/router_strategy/lowest_latency.py:38-160` keeps a per-deployment rolling list of last 10 latencies, separately tracking time-to-first-token for streaming vs full-response time for non-streaming. PASR currently treats latency as one signal; splitting TTFT vs full-response latency for streaming requests would let HUAKAI rank Anthropic streaming requests on the dimension users actually feel.

### E3. LiteLLM cacheable-prefix extraction (for HUAKAI's cache fingerprinting)

`litellm@b5d3a5fc:litellm/router_utils/prompt_caching_cache.py:55-142` correctly handles both message-level and content-block-level `cache_control` markers, plus the "last marker wins" semantics that match Anthropic's actual cache behavior. Worth borrowing the **idea** (clean-room reimplementation): walk messages, find the deepest cache_control marker, hash everything up to and including that block. HUAKAI should hash the prefix per-vendor-family because OpenAI and Anthropic disagree on what's cacheable.

### E4. one-api priority bucketing as a primitive

`one-api@8df4a267:model/cache.go:227-255` — the "highest priority bucket gets first try, lower buckets are retry pool" pattern is simple and effective. HUAKAI's PASR could expose `priority_band` as a first-class field independent of score, so operators have a predictable knob ("VIP accounts always tried first; main pool only on retry").

### E5. Portkey conditional DSL

`portkey-gateway@351692fd:src/services/conditionalRouter.ts:44-156` — compact MongoDB-style DSL (`$eq`, `$gt`, `$in`, `$regex`, `$and`, `$or`) over request metadata / params / URL. HUAKAI's claim_gate could expose a similar declarative router for "if `metadata.tier == 'premium'` and `model in ['claude-3-opus', 'gpt-4-turbo']` → route via VIP pool" without writing Go.

### E6. LiteLLM `MidStreamFallbackError` (high-leverage idea, hard to clean-room well)

The "synthesize continuation prompt with `prefix: True` + merge usage objects" pattern (`litellm@b5d3a5fc:litellm/router.py:2085-2194`) is a real production differentiator: streaming-time deployment swap with cost accounting preserved. The "prefix: True" is Anthropic-specific (their assistant-prefill feature), so the cross-vendor case is harder. HUAKAI's R5/R7/R8 stability layer should consider whether streaming continuation is in scope; if yes, this is the best reference behavior for clean-room reimplementation.

### E7. Portkey strategy composition (recursive targets)

`portkey-gateway@351692fd:src/handlers/handlerUtils.ts:476-779` — targets can themselves contain `targets`, allowing patterns like "fallback chain where each link is a load-balance pool." HUAKAI's pool layer is currently flat; nesting would let operators express "VIP pool (load-balanced 3 keys) → fallback to main pool (load-balanced 5 keys) → fallback to free-tier" without re-implementing the chain logic.

### E8. one-api's `OriginalModel` retry context

`one-api@8df4a267:middleware/distributor.go:72`, `controller/relay.go:62-91` — saves the originally requested model name on the gin context so retries don't get confused by per-channel model mapping. HUAKAI should verify forwarder preserves the original `model` field through retries — a model-mapping rename on attempt 1 must not bind attempt 2 to attempt 1's mapped name.

### E9. one-api success-rate ring buffer is cheap and effective

`one-api@8df4a267:monitor/metric.go:11-38` — `[]bool` ring per channel, fail-rate computed only when ring is full. Channel auto-disabled on fail-rate threshold. PASR could borrow the "wait for N samples before scoring" guard, which currently is implicit in PASR's MissCount-2 threshold but not stated as a stability requirement.

### E10. LiteLLM tag-based filter as a pre-call check

`litellm@b5d3a5fc:litellm/router_strategy/tag_based_routing.py:get_deployments_for_tag` — runs before the strategy picks. This composition pattern (filter → strategy) is cleaner than baking tag awareness into each strategy. HUAKAI's PASR scoring could likewise be split into "filter (cooldown, capacity, locality, tag) → score (locality + headroom)."

---

## What HUAKAI's three-direction claim should say

Suggested rewrite (clean-room friendly; specific to what was actually verified):

> **Cross-account prompt-cache locality replication**: LiteLLM has within-deployment locality pinning (one prefix → one deployment_id, 5-minute TTL). It does not replicate the locality marker to a backup, does not blend locality with headroom, does not penalize cache-miss outcomes. HUAKAI's PASR extends locality from a hard pin to a score, adds cache-miss demotion, and adds cross-account replication of hot prefixes — none of which appear in litellm.
>
> **Request decomposition into parallel upstream calls per inbound**: Verified absent in one-api, Portkey, and litellm for chat / completion / messages endpoints. If HUAKAI ships speculative parallel attempts or per-token hedging, that is novel for this peer set.
>
> **Predictive (pre-error) migration based on trend signals**: Verified absent. All three are reactive (post-failure cooldown / circuit-open / disable). HUAKAI's plan to demote on trend-derivative signals before threshold breach is novel for this peer set.

---

Source files read:

- `~/refs/one-api/middleware/distributor.go`
- `~/refs/one-api/controller/relay.go`
- `~/refs/one-api/model/cache.go`
- `~/refs/one-api/model/ability.go`
- `~/refs/one-api/monitor/channel.go`
- `~/refs/one-api/monitor/metric.go`
- `~/refs/one-api/monitor/manage.go`
- `~/refs/portkey-gateway/src/handlers/handlerUtils.ts`
- `~/refs/portkey-gateway/src/services/conditionalRouter.ts`
- `~/refs/portkey-gateway/src/types/requestBody.ts` (lines 23-25, 79, 188, 214, 232-233 only)
- `~/refs/portkey-gateway/src/middlewares/requestValidator/schema/config.ts` (lines 20-25, 71)
- `~/refs/portkey-gateway/src/handlers/retryHandler.ts` (lines 115-147)
- `~/refs/litellm/litellm/router_utils/prompt_caching_cache.py`
- `~/refs/litellm/litellm/router_utils/pre_call_checks/prompt_caching_deployment_check.py`
- `~/refs/litellm/litellm/router_utils/cooldown_handlers.py` (lines 240-370)
- `~/refs/litellm/litellm/router_utils/fallback_event_handlers.py` (lines 1-260)
- `~/refs/litellm/litellm/router_strategy/least_busy.py` (lines 1-80)
- `~/refs/litellm/litellm/router_strategy/simple_shuffle.py`
- `~/refs/litellm/litellm/router_strategy/lowest_latency.py` (lines 1-100, 450-620)
- `~/refs/litellm/litellm/router.py` (lines 197-207, 261-540, 1763-1945, 2030-2330, 2380-2510, 7124-7165 only)
- `~/refs/litellm/litellm/batch_completion/main.py` (lines 1-120)
- `~/refs/litellm/litellm/integrations/anthropic_cache_control_hook.py` (lines 1-60)

Lane: specifier
Agent: general-purpose
UTC timestamp: 2026-05-09T08:13Z
