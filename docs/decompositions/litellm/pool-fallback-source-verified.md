# LiteLLM Pool / Fallback Source Verification for F-POOL-001

| Field | Value |
| --- | --- |
| Status | Source-verified decomposition |
| Author | Codex |
| Date | 2026-04-28 |
| Reference | LiteLLM at commit `62920a0cb29f11912edb5bacee470f1b1c044def` |
| Lane | Specifier-lane source verification |
| Scope | F-POOL-001 cross-reference: fallback, cooldown, and concurrency semantics |
| Output constraint | New decomposition file only; no ledger/spec/synthesis edits |

## 0. Scope and Clean-Room Boundary

This file verifies LiteLLM behavior from local MIT source under `.omc/reference-src/litellm/`.
It is a specifier-lane evidence artifact, not implementer-lane code guidance.

HUAKAI terms used below:

- Provider Account Deployment: one concrete upstream deployment/credential entry inside a logical Pool.
- Pool: the HUAKAI-visible set of Provider Account Deployments that can serve a logical model.
- Cooldown quarantine: a temporary exclusion window after runtime failures.
- Typed fallback branch: a fallback path selected because the failure class has product meaning.
- Admission/concurrency guard: a pre-provider-call limiter that bounds simultaneous work.

Primary source files read: `router.py`, `router_utils/cooldown_handlers.py`,
`router_utils/cooldown_cache.py`, `router_utils/fallback_event_handlers.py`,
`litellm_core_utils/fallback_utils.py`, router strategy files, concurrency helpers,
`utils.py`, and relevant cooldown tests under `.omc/reference-src/litellm/tests/`.

## 1. TODO-3 Verdict

Verdict: DIFFERENT-PATTERN-FOUND.

The earlier TODO wording was: verify whether LiteLLM has a pattern where the LAST remaining
Account in a small Pool is exempt from cooldown on a single transient probe miss.

What source supports:

- LiteLLM detects one configured deployment in a logical model group. Evidence:
  `router_utils/cooldown_handlers.py:190-194`.
- In the default path, 429 cooldown and ordinary percentage-threshold cooldown skip that
  one-deployment group. Evidence: `router_utils/cooldown_handlers.py:223-239`.
- Single transient misses are also protected by traffic-volume floors. Evidence:
  `constants.py:88-93`, `router_utils/cooldown_handlers.py:226-239`,
  `tests/router_unit_tests/test_router_cooldown_utils.py:419-470`.
- `APIConnectionError` strings are ignored by cooldown eligibility. Evidence:
  `router_utils/cooldown_handlers.py:57-63`.
- Tests confirm one configured deployment avoids single 429 cooldown while multiple configured
  deployments do not. Evidence: `tests/local_testing/test_router_cooldown_handlers.py:253-286`.

What source does NOT support:

- I did not find a generic "last remaining healthy Account in a small Pool" exemption.
  The source checks configured model-group size, not remaining-after-cooldown size. Evidence:
  `router_utils/cooldown_handlers.py:190-194`, `router.py:10283-10304`.
- The single-deployment protection is not absolute. If all requests fail and traffic is
  high enough, the deployment can still enter cooldown. Non-retryable cooldown-eligible
  statuses can also still cooldown a single deployment. Evidence:
  `router_utils/cooldown_handlers.py:226-232`,
  `router_utils/cooldown_handlers.py:241-247`, `utils.py:6714-6740`,
  `tests/router_unit_tests/test_router_cooldown_utils.py:314-330`.
- If `allowed_fails` or `allowed_fails_policy` is configured, single deployments can still
  cooldown through the legacy/policy path. Evidence:
  `router_utils/cooldown_handlers.py:195-201`, `router_utils/cooldown_handlers.py:250-255`,
  `router_utils/cooldown_handlers.py:398-430`,
  `tests/local_testing/test_router_cooldown_handlers.py:337-435`.
- LiteLLM does have a separate safety net that bypasses cooldown filtering when health-check
  routing plus allowed-fail policy would otherwise put every deployment in cooldown. That is
  not the same as a general last-remaining exemption. Evidence: `router.py:9536-9547` and
  `router.py:10010-10018`.

Corrected source-backed statement:

LiteLLM protects exactly-one-configured-deployment model groups from default 429 and ordinary
failure-rate cooldown, and avoids first-failure flapping through minimum traffic thresholds.
It does not implement a broad "last remaining Account in a small Pool" exemption. Policy-driven
allowed-fail cooldown, high-traffic all-fail cooldown, and non-retryable cooldown-eligible
status branches can still quarantine the single configured deployment.

## 2. LiteLLM Actual Fallback Algorithm in HUAKAI Vocabulary

### 2.1 Provider Account Deployment Selection

Before fallback across logical models, LiteLLM first chooses one Provider Account Deployment
inside the requested logical model group.

The selection pipeline is:

1. Resolve candidates for the logical model group and caller context.
2. Apply team/model/web-search filters where relevant.
3. Optionally filter health-check-unhealthy deployments.
4. Filter cooldown-quarantined deployments.
5. Apply pre-call checks when enabled.
6. Apply target order filtering when the request is intentionally moving through ordered tiers.
7. Choose a deployment through the configured routing strategy.

Evidence:

- Async healthy-candidate pipeline: `router.py:9467-9591`.
- Sync healthy-candidate pipeline: `router.py:9970-10114`.
- Cooldown filter removes candidates by deployment id: `router.py:10283-10304`.
- Health-check filter has a "do not remove all" safety net: `router.py:10306-10344`
  and `router.py:10346-10375`.

### 2.2 Routing Strategy Shapes

LiteLLM is not one single selector. It is a candidate-filtering router with pluggable final
ranking/randomization strategies:

- Random or weighted-random by weight/RPM/TPM. Evidence: `router_strategy/simple_shuffle.py:21-64`.
- Least in-flight by per-model-group request count. Evidence: `router_strategy/least_busy.py:1-8`,
  `router_strategy/least_busy.py:192-249`.
- Lowest TPM/RPM usage with projected request fit. Evidence: `router_strategy/lowest_tpm_rpm.py:161-249`.
- Lowest TPM/RPM v2 with random choice among tied lowest-usage candidates. Evidence:
  `router_strategy/lowest_tpm_rpm_v2.py:327-442`.
- Lowest latency or time-to-first-token, with random choice inside a latency buffer. Evidence:
  `router_strategy/lowest_latency.py:416-558`.

### 2.3 Retry Before Fallback

LiteLLM exhausts retry handling for the current logical model group before entering fallback
handling.

Evidence:

- `async_function_with_fallbacks` calls `async_function_with_retries` first and only enters
  fallback common utilities on exception. Evidence: `router.py:5593-5643`.
- Retry logic obtains healthy deployments, applies retry policy overrides, decides sleep,
  and loops through current-group attempts before raising the latest exception. Evidence:
  `router.py:5693-5888`.
- Retry sleep is zero when other healthy deployments exist in the same group, except the
  single-deployment base case. Evidence: `router.py:6060-6107`.

### 2.4 Fallback Chain

After current-group retry fails, fallback proceeds in this order:

1. Ordered deployment tiers for the same logical model group, when multiple order values exist.
   This branch is skipped for context-window and content-policy failure classes. Evidence:
   `router.py:5368-5434`.
2. Non-standard caller-provided fallback objects/lists, passed through directly. Evidence:
   `router.py:5436-5457`.
3. Context-window typed fallback, if the failure is a context-window overflow and a matching
   context-window fallback list exists. Evidence: `router.py:5459-5481`.
4. Content-policy typed fallback, if the failure is a content-policy violation and a matching
   content-policy fallback list exists. Evidence: `router.py:5495-5517`.
5. Regular fallback list lookup: exact logical model match, provider-stripped match, then
   generic wildcard fallback. Evidence: `router.py:5530-5563`,
   `router_utils/fallback_event_handlers.py:20-82`.
6. Recursive fallback execution skips the original logical model, increments depth, stops at
   max fallback depth, and raises the most recent fallback exception if all candidates fail.
   Evidence: `router_utils/fallback_event_handlers.py:85-161`.

Default fallback depth is five unless overridden. Evidence:
`constants.py:13`, `router.py:533-538`.

### 2.5 Non-Router Core Fallback

LiteLLM also has a simpler completion fallback helper outside the Router path. It prepends the
original model to the caller's fallback list, tries models sequentially, and raises after all
attempts fail. Evidence: `litellm_core_utils/fallback_utils.py:14-76`.

That helper is not the same as Router Pool selection. It is useful evidence that LiteLLM keeps
a simple sequential fallback primitive in addition to the richer Router algorithm.

## 3. Cooldown, Typed Branches, and Concurrency Verification

### 3.1 Cooldown Duration

Default cooldown is `DEFAULT_COOLDOWN_TIME_SECONDS`, currently five seconds unless environment
overrides it. Evidence: `constants.py:43`.

Per failure, duration priority is:

1. Deployment-level configured cooldown if non-negative.
2. Provider response retry-after header if non-negative.
3. Router default cooldown.

Evidence: `router.py:6305-6324`.

The cooldown cache stores exception summary, status, timestamp, and cooldown time, and sets TTL
equal to the cooldown duration. Evidence: `router_utils/cooldown_cache.py:43-60`,
`router_utils/cooldown_cache.py:69-98`.

### 3.2 Cooldown Trigger Conditions

Cooldown is not even considered when:

- deployment id is missing or not mapped to a model group;
- explicit cooldown duration is zero/effectively zero;
- router cooldowns are disabled;
- the exception is not cooldown-eligible;
- the deployment is a provider-default wildcard deployment.

Evidence: `router_utils/cooldown_handlers.py:98-163`.

Cooldown eligibility by status/error class:

- No cooldown for `APIConnectionError` strings. Evidence: `router_utils/cooldown_handlers.py:57-63`.
- 429, 401, 408, and 404 are cooldown-eligible. Evidence: `router_utils/cooldown_handlers.py:70-84`.
- Other 4xx errors are not cooldown-eligible. Evidence: `router_utils/cooldown_handlers.py:85-87`.
- Non-4xx errors are cooldown-eligible. Evidence: `router_utils/cooldown_handlers.py:89-91`.

Default current logic cools down when:

- status is 429 and the group is not a single configured deployment;
- all requests failed and traffic is at or above the single-deployment traffic threshold;
- failure rate exceeds the default percentage threshold, minimum request volume has been met,
  and the group is not a single configured deployment;
- the status is not retryable.

Evidence: `router_utils/cooldown_handlers.py:223-249`.

Policy/legacy logic cools down when updated failure count exceeds configured allowed failures.
Evidence: `router_utils/cooldown_handlers.py:398-430`.

### 3.3 Typed Fallback Branches

Typed context-window and content-policy fallback branches are confirmed.

Evidence:

- Context-window branch: `router.py:5459-5481`.
- Content-policy branch: `router.py:5495-5517`.
- Content-policy fallback can be triggered from a content-filter finish reason when fallback
  exists. Evidence: `router.py:6475-6511`.

Correction: the typed branches are not simply "after generic fallback." In source, after
order/non-standard handling, context-window and content-policy branches are checked before the
regular exact/stripped/generic fallback lookup. Evidence: `router.py:5459-5563`.

### 3.4 Concurrency Claims

E-LM-DEEP-012 is source-supported for Router-level Provider Account Deployment concurrency:

- LiteLLM derives a deployment semaphore limit in this priority order:
  explicit max parallel requests, RPM, TPM-derived estimate, router default. Evidence:
  `utils.py:4852-4892`.
- It stores the semaphore in the Router cache with `local_only=True`. Evidence:
  `router_utils/client_initalization_utils.py:16-37`.
- Router calls retrieve the semaphore with `local_only=True`, lazily initializing it if absent.
  Evidence: `router.py:9051-9077`.
- The async provider call runs inside the deployment semaphore when present. Evidence:
  `router.py:2171-2189`.

Conclusion: this is process-local concurrency for the Router deployment semaphore. It does not
provide cluster-wide Provider Account Deployment concurrency by itself.

Important nuance:

- Usage-based routing v2 does use Redis-facing counters for TPM/RPM usage and batches in-memory
  increments to Redis for multi-instance usage accounting. Evidence:
  `router_strategy/lowest_tpm_rpm_v2.py:32-43`,
  `router_strategy/base_routing_strategy.py:55-93`,
  `router_strategy/base_routing_strategy.py:95-166`.
- That does not change the semaphore conclusion: the semaphore object itself is stored and read
  `local_only=True`.
- Proxy-level API-key max-parallel request limiting is a separate layer from Router deployment
  semaphores. It tracks API-key descriptors and decrements max-parallel counters after response.
  Evidence: `proxy/hooks/parallel_request_limiter_v3.py:498-503`,
  `proxy/hooks/parallel_request_limiter_v3.py:904-928`,
  `proxy/hooks/parallel_request_limiter_v3.py:1591-1605`.

## 4. Comparison to Sub2API

Comparison basis:

- Codex prior source-mining file: `docs/decompositions/_cross-cutting/pool-selection-codex.md`.
- Source-corrected Claude v2 file: `docs/decompositions/_cross-cutting/pool-selection-claude-v2.md`.

### 4.1 LiteLLM Patterns Sub2API Does Not Have

LiteLLM has typed fallback lists for context-window and content-policy failures. Sub2API's
verified Pool selection centers on Provider Account eligibility, load, sticky routing, wait
plans, and retry exclusion; it does not expose these semantic fallback branches.

LiteLLM has ordered deployment-tier fallback inside one logical model group. Sub2API has
operator priority and load-aware ordering, but as selection order, not as a prepended fallback
chain before external fallbacks.

LiteLLM has a single-configured-deployment default cooldown guard, provider-stripped fallback
matching, generic wildcard fallback matching, and several runtime routing strategies. Sub2API's
corrected source read is more specific: model routing, sticky-within-routing, sticky standalone,
load-aware selection, then fallback wait queue.

LiteLLM has process-local per-deployment semaphores derived from RPM/TPM/defaults. Sub2API's
prior notes describe cache-backed Provider Account slot acquisition and bounded wait plans.
Neither is HUAKAI's desired money-grade database-authoritative admission boundary.

### 4.2 Sub2API Patterns LiteLLM Does Not Show Here

Sub2API has sticky/session-aware Provider Account affinity with explicit revalidation, separate
sticky/fallback wait budgets, and request-level failed Account exclusion. The LiteLLM
fallback/cooldown source verified here does not show the same queue/wait-plan abstraction or a
Sub2API-style unified per-request excluded Provider Account set.

## 5. Evidence Row Update Recommendations

### E-LM-DEEP-005: PATCH

Current row should not be kept as-is.

Patch to:

"Single-configured-deployment cooldown guard: in the default cooldown path, a logical model
group with exactly one configured Provider Account Deployment is protected from 429 and ordinary
failure-rate cooldown, and minimum request thresholds prevent first-failure flapping. This is
not a general last-remaining-healthy Account exemption; high-traffic all-fail cases and
allowed-fail policies can still cooldown the single configured deployment, as can non-retryable
cooldown-eligible status branches."

Why: source confirms a related safeguard but disproves the broader "last remaining Account"
reading. Evidence: `router_utils/cooldown_handlers.py:190-255`,
`tests/local_testing/test_router_cooldown_handlers.py:253-435`.

### E-LM-DEEP-009: PATCH

Typed fallback branches are confirmed, but the order needs correction.

Patch to:

"Router fallback order: retry current logical model group first; then, after retry exhaustion,
try order-based same-group deployment tiers when applicable; then non-standard caller fallbacks;
then typed context-window or content-policy fallback branches; then regular fallback lookup
using exact logical model match, provider-stripped match, and generic wildcard; recursion skips
the original group and stops at max fallback depth."

Why: current wording places special typed lists after generic fallback, but source checks
context-window/content-policy branches before regular exact/stripped/generic fallback lookup.
Evidence: `router.py:5368-5563`, `router_utils/fallback_event_handlers.py:45-161`.

### E-LM-DEEP-012: KEEP

The row is accurate for Router-level Provider Account Deployment semaphores.

Keep with optional clarifying note:

"This is process-local for the semaphore object (`local_only=True`). Usage-based routing v2 may
sync usage counters through Redis, and proxy API-key max-parallel limits are a separate layer,
but the Router deployment semaphore itself is not cluster-wide."

Evidence: `utils.py:4852-4892`, `router_utils/client_initalization_utils.py:16-37`,
`router.py:9051-9077`, `router.py:2171-2189`.

## 6. KEEP / IMPROVE / AVOID for HUAKAI

### KEEP

- Typed fallback branches for user-visible failure classes: context-window overflow and
  content-policy refusal should not be treated as generic provider failure.
- Fallback recursion limits and attempted-fallback metadata; HUAKAI should add cost and
  latency budgets on top.
- A precisely named `single_configured_deployment_default_cooldown_guard`, plus minimum traffic
  thresholds before failure-rate cooldown.
- Provider retry-after as a cooldown duration input behind operator-configured deployment
  cooldown.
- Latency-aware routing as an operator-selectable policy, with signal and buffer visible in
  HUAKAI's `routing_reason`.

### IMPROVE

- Split "single deployment protection" into explicit policy cases: only configured Account,
  last healthy Account after filters, hard failure, transient probe miss, and operator-forced
  fail-closed behavior.
- Record structured cooldown reason codes, alerts, manual override, and staged re-entry after
  cooldown expiry.
- Replace process-local deployment semaphores with HUAKAI's tenant-scoped admission boundary,
  where final Provider Account selection reserves quota and concurrency before provider spend.
- Carry one logical request identity across retries/fallbacks, with every attempt under one
  Billing Ledger claim and attempt-level route reason.
- Make typed fallback disclosure configurable because context-window fallback can change
  model/provider behavior.

### AVOID

- Treating "single configured deployment" as equivalent to "last remaining healthy deployment."
- A blanket no-cooldown rule; LiteLLM keeps high-traffic all-fail and policy-driven escape
  hatches.
- Process-local semaphores as sole Provider Account concurrency control in SaaS Edition.
- Evidence wording that implies typed fallback order without checking exception-class branches.
- Silent fallback across materially different model behavior without durable Usage Record and
  Audit Event attribution.

## 7. Owner Summary (Chinese)

本次核查结论是：LiteLLM 有一个相近但更窄的模式，不是“Pool 里最后一个健康账号永不冷却”。源码确认的是：当某个逻辑模型组只有一个已配置的 Provider Account Deployment 时，默认冷却逻辑会避免因为单次 429 或普通失败率阈值把它直接拉黑；同时还有最小请求量门槛，避免一次失败就触发失败率冷却。但它仍然允许高流量且 100% 失败、非重试类状态分支、以及 `allowed_fails` / `allowed_fails_policy` 这类策略把单部署组冷却。因此 E-LM-DEEP-005 应该 PATCH，而不是 KEEP。

同时，E-LM-DEEP-009 的“typed fallback”方向是对的，但顺序需要 PATCH：LiteLLM 是先重试当前模型组，再处理 order-based fallback、非标准调用方 fallback、context-window / content-policy typed fallback，最后才进入常规 exact / provider-stripped / generic fallback 查找。E-LM-DEEP-012 可以 KEEP：Router 级 Provider Account Deployment 并发限制确实是本地缓存的 `asyncio.Semaphore`，限额来源是显式 max-parallel、RPM、TPM 推算、router 默认值；Redis 只补充了部分 usage / proxy 限流场景，不改变 deployment semaphore 是进程内控制这一点。
