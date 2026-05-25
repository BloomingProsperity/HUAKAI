# Source-Read Summary: sub2api + new-api (specifier lane)

> Clean-room paraphrased behaviour summary. No verbatim identifiers, comments,
> field names, or path-prose. Source files appear only in the tail block.

## Repos & commits

- Wei-Shaw/sub2api @ `dbc8ae658cfc1c012160752582925e45115e2f3a` (LGPL-3.0)
- Calcium-Ion/new-api @ `d146e45e2f9515ff78078a3954c40023cc79baee` (AGPL-3.0)

Time-box: ~30 files inspected (routing / dispatch / cache / pool / sticky modules).
UI / migration / billing files were skipped.

---

## A. Cache mechanism

### A.1 Per-account prompt-cache state tracking

- **new-api** maintains a hybrid (memory + Redis) keyed store specifically
  for "which upstream channel last served which client-derived cache key".
  The store is keyed on a tuple of (rule-name, optional model, optional
  group, fingerprint of the affinity value), value = channel ID, TTL is
  per-rule (default 1h, max-entries 100k). It also keeps a parallel
  observed-stats store recording per-key hit / total counters, prompt /
  completion / total / cached / cache-hit token totals, and a derived
  rate mode tag (cached-over-prompt vs cached-over-prompt-plus-cached vs
  mixed) per relay format. This is genuine per-channel prompt-cache
  awareness, surfaced to admins via a stats endpoint.
- **sub2api** has no cache-token-aware structure. It tracks per-account
  RPM, concurrency, window cost, schedulability, last-used-at, and
  rate-limit state; cache hit rate is logged per-request but never
  retained as a per-account KPI used in selection. There is no
  per-account "cached prefix set" representation.

### A.2 Proactive idle warm-up on backup accounts

- **new-api**: searched for warmup / preheat / keep-alive / shadow /
  replay across `service/`, `relay/`, `middleware/`, `controller/`. Zero
  matches on warm-request issuance against an idle channel. The single
  channel-level proactive call is the operator-facing "channel test"
  endpoint, which is admin-triggered, not background prefix replay.
- **sub2api**: there is a per-credential warm-up interception flag
  (boolean, account-scoped). **Important: it is the inverse of HUAKAI's
  fabric idea.** When the flag is enabled on a backend account, the
  gateway *intercepts* short client probe requests (the kind of
  3-5-word title-extraction or keep-alive shapes that some client
  agents emit between turns) and returns a synthetic short mock
  response **without forwarding to the upstream account at all**,
  in order to *protect* that account's quota from client-side
  keep-alive traffic. There is no logic that *issues* a warm-up
  request from the gateway to an idle account. No replay, no shadow,
  no preheat. (Upstream identifier omitted; see Source files block
  for file:line evidence.)

### A.3 Cross-account cache replication

- Neither repo replicates prompt-cache state across accounts. Both treat
  every upstream account / channel as a cache-independent island. The
  closest operation in either is *binding* a session or affinity key to
  one account so the *upstream's own* server-side prefix cache stays warm
  for that one account; nothing transports the cached prefix to a peer
  account.

---

## B. Account pool / selection

### B.1 Selection algorithm

- **new-api** picks channels via a priority-then-weighted-random pool
  driven by ability rows (model × group × channel). Priority is
  high-to-low; within a priority tier, a random pick weighted by an
  integer weight column is used. The retry path increments a priority
  index so each retry drops to the next priority tier. There is also an
  "auto group" wrapper that exhausts each group's full priority ladder
  before moving to the next group. So: priority + weighted-random,
  with cross-group failover and per-priority retry.
- **sub2api** picks accounts via a deterministic two-level rank:
  (a) lower priority value first, (b) among ties, prefer never-used,
  else oldest-last-used-at, with an OAuth-preferred tiebreak when the
  group platform is Gemini. There is no random / weighted dimension at
  the equal-priority level — it is strictly LRU-style.

### B.2 Cache-locality bias in selection

- **new-api**: yes, *exists as a first-class affinity layer*. The
  distributor middleware, **before** running the priority+weight pool,
  attempts an affinity lookup using configured rules. Two built-in
  rules ship enabled by default (admin-modifiable): one keyed on the
  request-body field `prompt_cache_key` for the OpenAI Responses path
  on `gpt-*` models, one keyed on a body field under `metadata` for
  Anthropic `/v1/messages` on `claude-*` models. If the rule extracts
  a non-empty value and the cache has a binding, the pool returns the
  bound channel directly; on success the binding is refreshed (or
  rebound to whichever channel actually succeeded if the
  switch-on-success flag is on). On failure the rule may force
  "skip retry" so the request fails fast rather than landing on a
  cold channel. Default TTL 3600s. This is exactly cache-locality
  bias on selection.
- **sub2api**: the locality bias is session-driven, not
  prefix-content-driven. A "session hash" is computed from
  client-supplied identifiers (Anthropic / OpenAI). The session hash
  is bound to one account for a TTL, and that account is preferred on
  subsequent requests bearing the same session hash, subject to
  schedulability and group-membership re-checks. For the OpenAI
  Responses path there is also a binding from `previous_response_id`
  to the account that produced that response (TTL'd) so the
  Responses chain stays on one upstream — this is again
  conversation-level affinity, not prefix-set locality. There is no
  rule layer extracting `prompt_cache_key` and binding it to the
  channel that "saw" that prefix. Session hash exists separately
  from prompt content.

### B.3 Failover

- **new-api**: reactive. A retry loop in the relay path increments a
  priority cursor and re-asks the distributor for the next-priority
  channel. Cross-group fallback is gated on a token-level flag.
  Channel monitor exists but classifies channels as
  operational/degraded/failed/error after observed checks; it does
  not pre-empt selection before an error.
- **sub2api**: reactive. Errors (5xx, rate-limit signals) trigger
  `excludedIDs` accumulation and re-selection within a max-switches
  cap; rate-limited accounts are temporarily de-listed at the
  scheduler-snapshot layer. Sticky-session rebinding to a different
  account happens *after* the failure. There is *no* code path that
  swaps accounts before a failure.

---

## C. Request handling — decomposition / fan-out

- **Inbound→multiple-upstream decomposition:** evidence not found in
  either repo. Searched for fanout / fan-out / parallel-request /
  split-request / chunk-account / broadcast-account / decompose /
  sub-request / simultaneous across both `service/` and `relay/` /
  `controller/` trees. Each inbound request maps 1:1 to one chosen
  upstream account and is forwarded.
- **Speculative parallel execution to multiple accounts:** evidence
  not found. Goroutines exist for streaming pumps, billing flushes,
  notification delivery, expiry sweeps, snapshot refresh, but none
  fan out the same client request to N accounts. The only
  multi-account path is sequential retry on failure.
- **Long-request chunk-splitting across accounts:** evidence not
  found. There is request-format conversion (Anthropic ⇄ OpenAI ⇄
  Gemini, Claude→OpenAI-Responses compaction, model-mapping for
  cross-vendor dispatch), but the body remains a single
  inbound→single outbound transaction.

---

## D. Session / sticky / migration

### D.1 Sticky binding granularity

- **new-api**: not sticky in the classical "session-id" sense. The
  affinity layer binds at (rule, model, group, prompt-cache-key
  fingerprint) granularity, controlled by per-rule
  include-flag toggles. Without affinity rules matched, no stickiness
  exists.
- **sub2api**: sticky at session-hash granularity per (group,
  session-hash). Session hash is derived from client identifiers
  (e.g. for OpenAI it folds in originator / session-id / conversation
  identifiers, with a fallback seed). There is also a parallel
  Response-ID stickiness for OpenAI Responses chains so each
  follow-up turn lands on the upstream that owns that response.
  Default sticky TTL ~10 minutes for OpenAI bucket; rule-driven for
  Anthropic.

### D.2 Sticky behaviour on account failure

- **new-api**: on a non-2xx response from the affinity-bound channel,
  if the rule has skip-retry-on-failure set, the gateway returns the
  failure to the caller without retry. If skip-retry is off the
  ordinary retry loop runs and may land on a different channel; the
  affinity record is then *rebound* to whichever channel actually
  succeeded if the switch-on-success flag is on. Otherwise the
  affinity record is left and naturally expires.
- **sub2api**: on failure the bound account is added to the
  per-request excludedIDs set, the session sticky entry is *deleted*
  for unschedulable cases (clearing it from cache), retry pulls a new
  account, and the new account is re-bound for subsequent requests in
  the same session. There is no attempt to ferry KV cache or context
  state to the replacement account — the upstream simply re-encodes
  the prefix on first call to the new account, paying the cold-cache
  cost.

### D.3 Predictive migration before failure

- Evidence not found in either repo. No code path observes a
  degradation signal (rising latency, climbing 429 rate, rate-limit
  reset proximity, quota burn-down) and *pre-emptively* binds the
  next request to a backup account *before* the current account
  errors. Sub2api has rate-limit window awareness in scheduler
  snapshots (rate-limited accounts get demoted from selection
  candidates) but this is an *eligibility filter*, not a context
  pre-warm on a backup account. New-api's channel monitor labels
  channels operational/degraded/failed but the labels feed the
  admin dashboard, not pro-active context migration.

---

## E. Differentiation reality-check

### E.1 Overlap with HUAKAI's three directions

| HUAKAI direction | sub2api evidence | new-api evidence |
|---|---|---|
| (1) Account Cache Fabric — proactive cross-account warm-up replay | None. The only related code path describes *intercepting* (dropping) client probe traffic to save quota — opposite intent to fabric replay. | None. No background prefix replay. |
| (2) Multi-Account Request Decomposition — split one request into N sub-requests | None. 1:1 inbound→outbound across all relay paths. | None. 1:1 inbound→outbound. |
| (3) Predictive Session Migration — pre-warm backup before failure | None. Reactive failover only; sticky entry is deleted on failure, replacement account starts cold. | None. Reactive retry; channel monitor is observational/admin-only. |

The "no other project carries any of these" claim is **substantiated**
against these two repos at the cited SHAs. None of the three direction
ideas is present, even in skeleton form.

### E.2 What is genuinely novel in sub2api / new-api vs vanilla
round-robin gateway

- **new-api** novelty:
  - first-class **prompt-cache affinity layer** keyed on configurable
    request-body fields (e.g. `prompt_cache_key`, Anthropic
    `metadata.user_id`), with per-rule TTL, fingerprinting, and
    switch-on-success rebinding;
  - per-affinity-key observed-cache-stats counters surfaced to
    admin (hit / total / cache-token totals / rate-mode classification);
  - param-override-template merged onto the channel override when the
    affinity rule fires (so e.g. Codex-CLI / Claude-CLI pass-through
    headers automatically apply only when that traffic class is
    detected);
  - skip-retry-on-failure flag per rule (admit fast-failure for
    cache-locality-critical requests rather than dilute the cache
    onto a cold channel).
- **sub2api** novelty (relative to round-robin):
  - **session-hash sticky** with separate handling for Anthropic
    `/v1/messages`, OpenAI Responses, Gemini, and a secondary
    `previous_response_id` binding for chained Responses turns;
  - **load-aware selection layer** combining priority, last-used-at,
    per-account concurrency batched lookup, RPM prefetch, window-cost
    prefetch, with a wait-plan instead of immediate failure when the
    sticky account is busy;
  - **mixed scheduling**: Antigravity OAuth accounts can be
    selectively pulled into an Anthropic group's candidate set via a
    per-account flag, allowing one transport to serve another
    platform's requests;
  - **client-side probe interception** (not warm-up issuance): drop
    short client-side keep-alive / title-extraction probes with a
    synthetic mock to save upstream quota — useful for upstream
    accounts that bill on every messages-endpoint call;
  - **routing-account override**: per-(group, model) explicit account
    routing list applied before the regular priority pool.

Both projects are clearly past "vanilla round-robin", but neither
implements the three HUAKAI directions. The closest adjacency is
new-api's affinity layer to direction (1), but it is *passive
co-location of a request with its prior channel via fingerprint*, not
*active replication of cached prefix state to peer accounts*. The
HUAKAI direction (1) treats cache state as a transferable asset; new-api
treats cache state as a sticky-locality observation. These are
distinct mechanisms with distinct cost profiles (warm-up RPS budget,
cross-account latency, prefix-leakage policy).

---

## Source files read

- sub2api/backend/internal/service/account.go:943 (warm-up-intercept flag site — upstream identifier omitted; M2 corrected post-review 2026-05-09)
- sub2api/backend/internal/service/account_intercept_warmup_test.go (upstream-side test naming preserved as path evidence only)
- sub2api/backend/internal/service/openai_messages_dispatch.go
- sub2api/backend/internal/domain/openai_messages_dispatch.go
- sub2api/backend/internal/service/openai_sticky_compat.go
- sub2api/backend/internal/service/openai_account_scheduler.go (lines 1015-1100, 1249 LoC)
- sub2api/backend/internal/service/gateway_service.go (lines 1340-1545, 2995-3200)
- sub2api/backend/internal/service/openai_ws_forwarder.go (lines 3910-4002 — previous_response_id sticky)
- sub2api/backend/internal/service/openai_gateway_service.go (Select* family)
- sub2api/backend/internal/service/gemini_messages_compat_service.go (Select* family)
- sub2api/backend/internal/handler/gateway_handler.go (lines 1640-1760 — mock-warmup intercept body)
- sub2api/backend/internal/handler/gateway_handler_warmup_intercept_unit_test.go
- new-api/service/channel_affinity.go (full 966 LoC)
- new-api/service/channel_select.go (full 162 LoC)
- new-api/middleware/distributor.go (full 435 LoC)
- new-api/setting/operation_setting/channel_affinity_setting.go (full 121 LoC)
- new-api/model/channel.go (selection / priority / weight structure)
- new-api/relay/channel/* (directory listing only — verified no fan-out)
- new-api/service/* (grep coverage for warmup/decompose/predictive — none found)
- new-api/relay/common/* (directory listing — verified relay common has no decomposition)
- new-api/controller/uptime_kuma.go (heartbeat reference, observational only)
- new-api/repository (degraded-status references — admin reporting only)

Searches performed (negative-result queries that contributed to "evidence not found"):
- `warmup`, `warm_up`, `warm.up`, `preheat`, `keepalive`, `shadow`, `replay`, `prompt.cache` — both repos
- `select.*account`, `round.robin`, `load.*balanc`, `least.conn`, `weighted` — both repos
- `predict`, `degradation`, `pre.warm`, `migration.*context`, `backup.*account`, `fan.*out`, `fanout`, `decompose`, `split.*request` — both repos
- `replicate.*cache`, `cross.*account.*cache`, `share.*cache.*account` — both repos

## Lane / Agent / UTC

- Source files read: see block above
- Lane: specifier
- Agent: general-purpose
- UTC timestamp: 2026-05-09T08:12Z
