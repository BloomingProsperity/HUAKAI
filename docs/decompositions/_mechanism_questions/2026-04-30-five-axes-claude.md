# 5-Axis × 7-Projects Mechanism Question Matrix — Claude Independent Draft

| Field | Value |
| --- | --- |
| Owner directive | "sub2api 核心复杂度集中在'上下文状态 + 渠道调度 + 协议转换 + 计费补偿 + 异步任务'抓刀了？" |
| Trigger | Claude's earlier 21-file decomposition + 8-souls table failed Owner depth bar; Owner identified 5 complexity axes that the decomposition must cover at mechanism (not feature) level. |
| Date | 2026-04-30 |
| Lane | This file is a **question list**, NOT a behavior summary. No source has been re-read for this draft. Pre-specifier-lane scoping artifact. |
| Independence note | Codex is dispatched in parallel to produce its own version at `2026-04-30-five-axes-codex.md`. Claude has not read the codex draft; codex has not read this draft. Synthesis comes after both land. |
| Self-grade | sub2api: axis 1=0 / axis 2=0.5 / axis 3=0.5 / axis 4=1.0 (B.5 deep) / axis 5=0; total 2/5. Other 6 projects approximately 0/5 to 1.5/5 across axes. |

---

## How to read this file

Each axis is one section. Within an axis, each subsection is one project, and within each project subsection there is a list of **mechanism questions** that current decomposition does NOT answer. Each question:

- Is **falsifiable** (a yes/no, a number, an enum value, a contract specification — not "explain X")
- Is **fusion-relevant** (the answer changes a HUAKAI design decision; trivia is excluded)
- Is **lane-clean** (asking for behavior, not implementation lines; clean-room safe to dispatch)

Each question carries a `→ HUAKAI impact` line saying which HUAKAI module/spec/code path the answer informs.

If a question has already been answered in existing decomposition material, it is omitted (not duplicated). The list is the GAP after the prior 21 files.

---

## Axis 1 — 上下文状态 (Conversation context state)

The hardest part of a multi-account gateway: how to keep a multi-turn conversation working when the upstream provider holds session state on the chosen account, and any account swap mid-conversation loses context.

### sub2api

1. What input on the inbound HTTP request makes a request "continuation" vs "fresh"? (e.g., header name, body marker, prior message id?)
   → HUAKAI: handler input contract; affects /v1/messages + /v1/chat/completions field set.
2. Once a continuation marker is present, what's the lookup key? `(tenant_id, conversation_id)` only? Or also `(model_family)`? Or `(client_session_id)`?
   → HUAKAI: `sticky_bindings` PK definition; current schema has tenant_id + session_hash but field meaning was inferred not observed.
3. When the previously-bound account is now temp_unsched / health-unhealthy, does sub2api: (a) fail the request, (b) silently switch account and accept context loss, (c) try to replay context from the gateway side?
   → HUAKAI: failure path policy; current spec doesn't specify.
4. Is "continuation" enforced as Provider-Account-affinity OR upstream-credential affinity? (different when one account holds N OAuth tokens for failover)
   → HUAKAI: this changes whether sticky binds to `provider_accounts.id` or to `(provider_account_id, credential_index)`.
5. What's the TTL on a continuation binding, and what triggers refresh of the TTL — every successful turn? Only on creation? On every read?
   → HUAKAI: `sticky_bindings.expires_at` default + refresh policy.
6. When a user starts a NEW conversation but the model id is the same as a recent conversation, does sub2api ever speculatively bind to the same account for cache-friendliness, or always randomize within the eligible set?
   → HUAKAI: cache-warmup heuristic; affects p99 latency.
7. Does sub2api's "session hash" derive from the request body content, or from a separate session-id header? If from body, what fields participate? (system prompt + tool list + first message?)
   → HUAKAI: Phase E end-user SDK contract; affects how clients can opt into sticky.

### one-api / new-api

1. one-api's "channel" — is it a 1-to-1 with provider account, or 1-to-N (one channel = many accounts)? What dictates which account inside a channel handles the request?
   → HUAKAI: schema mapping; current HUAKAI has `pool_groups → channels → provider_accounts` 3-level — does this collapse vs one-api?
2. Does one-api preserve conversation context across channel switches at all? Or is it stateless per request?
   → HUAKAI: if stateless, HUAKAI's sticky design is purely additive (sub2 contribution).

### portkey

1. Portkey's "virtual key" — is it a per-tenant/per-customer mapping, or a per-conversation mapping? Does it carry routing state inside it, or just identity?
   → HUAKAI: this is the customer-facing API key model; affects DR-002 SaaS surface.
2. When a portkey config has `cache: { mode: "semantic", similarity: 0.95 }`, what input fields go into the embedding? Just the user message? Full message array including system? Tool definitions?
   → HUAKAI: cache-key composition; affects per-tenant cache hit rate.

### helicone

1. Does helicone preserve any context state, or is it purely transparent (just logs everything)?
   → HUAKAI: if purely transparent, helicone contributes nothing to axis 1; it's an axis 4/5 contributor only.

### litellm

1. LiteLLM's `Router` retry on failure — does it re-bind to a different model, a different deployment, or both? What's the rule that picks the next deployment?
   → HUAKAI: fallback-chain definition; affects portkey + sub2api fusion.
2. How does litellm handle Anthropic's prompt caching block markers when the request is routed across deployments mid-conversation?
   → HUAKAI: Anthropic-specific cache-token continuity.

### all-api-hub

1. (Out of scope per DR-000 + RB-4 — clean-room not entering this project for axis 1.)

### envoy-ai-gateway

1. Does the EAG CRD model carry any sticky-routing concept (like envoy's session affinity), or is each request independent at the routing layer?
   → HUAKAI: declarative-config feasibility check.

---

## Axis 2 — 渠道调度 (Channel / account scheduling)

We have F-POOL-001 9-gate chain at the layer level; mechanism level is incomplete.

### sub2api

1. For each of the 9 gates, what is the EXACT failure verb the gate emits when it rejects? (skip-this-account / mark-temp-unsched / hard-fail-request / requeue?)
   → HUAKAI: `pool_routing_audit_events.gate_outcome` enum; current code uses generic strings.
2. When `cap_concurrency` is at the limit but a request arrives, does sub2api queue (with `cap_queue_fallback`), reject, or skip to next-priority account?
   → HUAKAI: `cap_queue_fallback` semantics; current adapter populated MaxWaiting but the wait loop behavior is inferred.
3. What is the BroadTopK heuristic — does it pick top-K AT THE ROUTING LAYER and fan out concurrently, or top-K with sequential fall-through?
   → HUAKAI: `RoutingPolicy.BroadTopK` + `TopKDefault` semantics.
4. When session_window_5h_status flips to 'limited', is the account ever re-eligible before the window expiry, or always blocked until reset?
   → HUAKAI: this is the Anthropic Pro 5h window — drives whether HUAKAI can re-attempt mid-window.
5. Does sub2api's selector record per-attempt latency back to the account row (for next-call priority adjustment), and what's the EWMA factor / window?
   → HUAKAI: `provider_accounts.last_dispatch_at` purpose — currently HUAKAI uses for ordering only, no latency.

### one-api

1. one-api group → channel routing — when group contains multiple channels, is the dispatch round-robin, weighted, or quota-aware?
   → HUAKAI: schema mapping for `pool_groups` ↔ `channels`.

### portkey

1. portkey's `loadbalance` strategy — does it carry per-target weights (`{target: x, weight: 0.7}`) or only `{target: x}` with implicit equal weight?
   → HUAKAI: WithRoutingPolicy v2 fields.
2. portkey's `fallback` chain — is each entry a target+condition, or just a target with implicit "on-error"? What conditions are first-class? (status code? response time? token count?)
   → HUAKAI: fallback DSL design.

### litellm

1. LiteLLM `model_group` — when multiple deployments exist for one model, what's the default selection (random, round-robin, least-loaded)? Configurable per-group?
   → HUAKAI: comparison vs sub2api 9-gate.

### envoy-ai-gateway

1. EAG `BackendTrafficPolicy` — what fields are mandatory? What's the failure semantics when a backend is down (drop / route to fallback / 503 with retry-after)?
   → HUAKAI: declarative failure policy.

---

## Axis 3 — 协议转换 (Protocol translation)

We have F-PROTO-002 + HCSF + AnthropicAdapter. Capability matrix mostly empty.

### sub2api

1. For Anthropic Messages → OpenAI Chat Completions translation, how does sub2api map `tool_use` blocks to `tool_calls`? Specifically: how is `tool_use_id` reconciled with `tool_call_id`? What format is the function_call.arguments JSON serialization (single-quote? double? escaped newlines?)
   → HUAKAI: `internal/proto` adapter completeness; currently AmbiguousUsage path is partial.
2. What's the behavior on streaming events that have NO equivalent in target protocol (e.g., Anthropic `ping`, OpenAI `[DONE]`)? Drop, translate, error?
   → HUAKAI: `ProtocolLossEntry.Verdict` per event type.
3. For Anthropic's `message_delta { stop_reason: "end_turn" }`, what's the OpenAI equivalent in `chat.completion.chunk`? Is `finish_reason` written on the LAST chunk only, or every chunk after end?
   → HUAKAI: terminal frame protocol; affects forwarder Phase D.
4. How does sub2api handle Anthropic's `cache_creation_input_tokens` vs `cache_read_input_tokens` when emitting OpenAI-format usage?
   → HUAKAI: usage merging; currently UsageRecordDraft has separate fields but emit policy unclear.

### portkey / litellm

1. Both project's protocol normalization — at what granularity? Per-message? Per-event? Per-stream? What are their canonical types?
   → HUAKAI: HCSF design comparison.

### helicone

1. Helicone is transparent — does it touch protocol at all, or just observe and forward bytes verbatim?
   → HUAKAI: if verbatim, helicone is an axis 4 contributor (logging-only), not axis 3.

---

## Axis 4 — 计费补偿 (Billing compensation)

This is the only axis HUAKAI has fully mined (F-OBS-001 Tx1+Tx2 + 50 invariants). Remaining gaps are:

### sub2api

1. When sub2api commits Tx2 with `usage_source = inferred`, what's the default confidence_score? Is the row eligible for sweep-back-to-real-cost later, and what triggers that sweep?
   → HUAKAI: `usage_records.pending_reconciliation` flag triggers; currently set but no consumer.
2. What's the precise refund/adjustment row shape when an end-user disputes a charge after settle? Is it a paired adjustment row (immutable spec line 170), or does sub2api mutate?
   → HUAKAI: `billing_ledger_adjustments` schema is in 0002 migration but unused; behavior intent unclear.
3. For idempotency replay (TX1_OPP_HIT), does sub2api literally replay the cached response bytes, or just return 409? If replay, where are response bytes stored — inline on claim row, separate cache table, S3?
   → HUAKAI: Phase C.3 returns 409-without-cache; Phase E needs the answer to wire replay.

### one-api

1. one-api 额度包 — how is the deduction order (multiple packages, FIFO / LIFO / cheapest-first / nearest-expiry-first)?
   → HUAKAI: balance algorithm; affects gift-card UX.
2. one-api per-channel cost markup — is this stored as a multiplier on the channel row, or a separate price-list table?
   → HUAKAI: pricing model schema.

### portkey

1. Portkey cost tracking — does it count tokens at request time (prediction) or settle time (actual)? Both? How does it reconcile?
   → HUAKAI: matches HUAKAI Tx1 predicted vs Tx2 actual; verify portkey same.

### helicone

1. helicone's logging — synchronous (blocks request until logged) or async (fire-and-forget)?
   → HUAKAI: this is the F-OBS-001 H8 invariant equivalent; verify helicone doesn't drop on PG outage.

---

## Axis 5 — 异步任务 (Async background workers)

HUAKAI has 0 lines on this axis. All sub2api 异步 mechanism is open.

### sub2api

1. Orphan-sweep worker — what's the trigger interval? How does it detect orphan claims (`status=reserving AND lease_expires_at < NOW()` only? Or also age + last-modified-by?)
   → HUAKAI: spec mentions orphan-sweep; mechanism missing.
2. When orphan sweep finds a candidate, what's the action? (Force-abort the claim with reason='orphan-sweep'? Re-attempt the upstream call? Just notify and let operator decide?)
   → HUAKAI: orphan-sweep procedure unspecified.
3. DLQ replay — what's the retry policy (exponential? linear? fixed?)? Max retries before manual intervention? What lane runs the replay (worker pool? per-tenant? global)?
   → HUAKAI: F-OBS-001 H8 → Phase 4.5.
4. Cross-threshold outbox consumer — does sub2api have one, or is it shipped externally? If internal, polling interval and batch size?
   → HUAKAI: scheduler_outbox table; consumer unimplemented.
5. Token cache invalidation worker — when an OAuth refresh fails permanently (invalid_grant), is there a worker that proactively scrubs all in-flight cache entries for that account, or only request-path lazy invalidation?
   → HUAKAI: `auth.TokenCache` invalidation; current code is request-path lazy.
6. Rate-limit-window rollover worker — F-RATE-001 spec mentions auto-rollover (5h / 1d / 7d windows). Is rollover lazy (on-read) or scheduled (periodic UPDATE on expired windows)?
   → HUAKAI: F-RATE-001 implementation choice.

### one-api / new-api

1. Topup order state machine — what's the worker that polls payment status? Stripe webhook + worker? Polling Alipay? Both?
   → HUAKAI: Phase L0 payment integration.
2. Invitation code — is generation eager (pre-generate batch) or lazy (per-request)? Anti-bruteforce mechanism (rate-limit on /redeem)?
   → HUAKAI: new-api UI features.

### portkey

1. portkey's cache eviction — is it pure TTL, LRU within memory limit, or hybrid?
   → HUAKAI: cache lifecycle for L1 fusion.

### helicone

1. helicone's batch flush worker — interval? batch size? backpressure when PG is slow?
   → HUAKAI: F-OBS-001 H8 implementation; helicone is a reference for sync-fallback strategy.

### envoy-ai-gateway

1. EAG controller reconcile loop — interval? Watch-based or polling? What CRDs trigger reconcile?
   → HUAKAI: declarative-config reconciler design (but we don't run K8s).

---

## Synthesis hooks

After Codex's parallel `-codex.md` lands, compare:
- **Agreements** = high-confidence question set; queue for specifier-lane dispatch
- **Conflicts** = surface to Owner; either both ask + Owner picks, or merge into one richer question
- **Gaps** = questions one draft missed (this is where parallel-plan adds value)

Then write `2026-04-30-five-axes-mechanism-questions.md` (no suffix) as the synthesized authoritative question list. ONLY THEN dispatch specifier-lane Codex (with the clean-room guard) on the questions, one project per dispatch.

---

## Source files read

NONE. This is a pre-specifier scoping artifact — no reference-project source touched.
Lane: pre-specifier scoping (not the formal specifier lane that requires source reading).
Agent: Claude Opus 4.7 (1M context).
UTC timestamp: 2026-04-30T00:55Z.
