# `litellm` — Cooldown Handler + Retry Policy Hierarchy (Claude deep decomposition)

| Field | Value |
| --- | --- |
| Status | Deep decomposition (Claude lane, peer to Codex R3 specifier output) |
| Reference | LiteLLM (MIT, [E-LIC-005](../../07_REFERENCE_EVIDENCE_LEDGER.md)) |
| Feature in HUAKAI matrix | F-CH-002 (L2) + F-GW-004 (L1) |
| Specifier session | Claude PM-Orchestrator (Opus), 2026-04-29 |
| Source-reading delegate | Sonnet Explore agent — 8 source files; structured factual report retained |
| Companion artifacts | docs/decompositions/litellm/cooldown-retry-hierarchy-source-verified.md (Codex R3), .omc/artifacts/decomp-critic/C4-litellm-cooldown-retry.md (Codex critic) |
| **Truth-discipline** | **Observed regions: 8** / **Inferences: 3** / **Open questions: 6** |

> **Lane discipline**: Independent of any Codex specifier or critic for this feature. Behavior claims tagged `[region-N]`; inferences explicitly marked.

---

## 1. WHY (motivation)

LiteLLM is a python multi-provider router used as both a Python SDK and a hosted gateway. Its scale (~250K LOC, 100+ providers) creates routing pressures other gateways don't share:

**Pressure 1 — provider failure modes are heterogeneous**: 401 from one upstream is auth-fail; 401 from another is rate-limit disguised; some upstreams use 429 only on quota; some use 5xx on transient capacity. A single classifier rule misroutes most providers. LiteLLM's classifier is empirically derived from 100+ provider observations, then exposed as opt-in per-exception-type retry policy `[region-5, region-6]`.

**Pressure 2 — cooldown vs retry as separate concerns**: A naive design conflates "this deployment failed once → retry elsewhere" with "this deployment is broken → don't send anyone here". LiteLLM separates them: retry is a single-request action; cooldown is a deployment-level state with TTL `[region-1, region-2]`. The two interact precisely once: when a healthy peer exists in the same group, retry sleep is zero `[region-7]`.

**Pressure 3 — single-deployment exemption**: When a model group has only ONE deployment, cooling it down means total outage. LiteLLM defaults to **not cooling down single-deployment groups** unless the failure is rate-limit `[region-1]`. This is a product-level decision: better to serve a degraded request than zero requests.

**Pressure 4 — fallback as a typed lattice**: Some failures (context-window exceeded, content-policy violation) need different fallback paths than generic failures. LiteLLM models this as separate fallback maps, not a flat list `[region-3]`.

---

## 2. WHAT (algorithm in HUAKAI vocabulary)

### Sub-behaviors S-1..S-22 (observed-only)

**S-1: Cooldown decision (5-axis evaluation)** `[region-1]`. A deployment enters cooldown when all of:
- The error class qualifies (S-2)
- The traffic-volume floor is met (S-3)
- The failure-rate threshold is breached (S-4)
- It is NOT a single-deployment group OR error is 429 (S-5)
- Cooldowns are not globally disabled

**S-2: Error-class taxonomy for cooldown** `[region-1]`. Status codes drive the decision:
- ALWAYS cool down: 429 (rate-limit), 401 (auth), 408 (timeout), 404 (not-found), all 5xx.
- NEVER cool down: APIConnectionError (string-match precedes status-code logic), other 4xx (400, 403).
- DEFAULT cool down: unmapped errors fall to True (conservative).

The 401 branch is notable: most gateways treat 401 as "credential broken" and disable; LiteLLM treats it as "cooldown candidate" — same disable effect but with a TTL recovery window.

**S-3: Traffic-volume floor (two thresholds)** `[region-1]`. Per-minute counters of success + failure per deployment:
- Multi-deployment groups: floor = `DEFAULT_FAILURE_THRESHOLD_MINIMUM_REQUESTS` default 5 requests in current minute window.
- Single-deployment groups: floor = `SINGLE_DEPLOYMENT_TRAFFIC_FAILURE_THRESHOLD` default 1000 requests. Effectively never fires under normal traffic.

The minute-window is a **calendar-aligned bucket**, not a rolling window. This means at xx:00:00 the counter resets — a deployment that failed 4 times at 12:59:59 starts the 13:00 minute fresh.

**S-4: Failure-rate threshold** `[region-1]`. `percent_fails = num_fails / (num_successes + num_fails)`. Default threshold 0.5 (50%). Comparison: `>` (strict). A deployment with exactly 50% failure rate does NOT cool down.

**S-5: Single-deployment exemption** `[region-1]`. When the model group has only one deployment, cooldown is skipped unless the error is exactly 429. Rationale: rate-limit means upstream-imposed back-off (the only safe-to-honor signal); other errors might be transient and total outage is worse than degraded.

**S-6: Connection-error short-circuit** `[region-1]`. Before status-code logic, the exception string is searched for "APIConnectionError". If matched, cooldown returns False unconditionally. Rationale: connection errors are likely client-network rather than upstream-state; cooling down a healthy upstream because of one's own network blip is worse than the alternative.

**S-7: Cooldown duration with 3-tier override** `[region-2]`. Priority order when computing TTL:
1. Per-deployment dynamic `cooldown_time` (if exception or deployment metadata sets it)
2. Router-level default `cooldown_time` (operator config)
3. Constant `DEFAULT_COOLDOWN_TIME_SECONDS` (5 seconds)

The TTL is applied to a `DualCache` entry; the deployment automatically resumes serving after expiry without an explicit health probe.

**S-8: Cooldown state storage** `[region-2]`. `DualCache` provides in-memory + Redis layers. Cooldown writes go to both layers. In multi-replica deploy, Redis-shared cooldown means all replicas see the same cooled-down deployments.

**S-9: Auto-recovery via TTL only** `[region-2]`. No explicit health-test runs to re-enable a cooled-down deployment. When TTL expires, the deployment is eligible again. The next request that lands on it serves as a probe; if it fails, the cooldown cycle repeats.

**S-10: Retry hierarchy — 4 levels** `[region-3, region-4, region-5]`. Configuration sources from LOWEST to HIGHEST priority during resolution (which inverts to: HIGHEST priority at top of fallback ladder):

| Level | Source | Where set | Default |
|---|---|---|---|
| L1 | Global | router constructor / module-level `litellm.num_retries` | OpenAI default |
| L2 | Per-request | kwargs `num_retries` per call | none |
| L3 | Per-deployment | deployment metadata `litellm_params.num_retries` | none |
| L4 | Per-exception-type retry policy | `RetryPolicy` mapping exception class → retry count | none |

**S-11: Retry hierarchy resolution order (DETERMINISTIC)** `[region-3]`. The router checks in this exact order during failure handling:
1. **L4 first** — if `retry_policy` or `model_group_retry_policy` is set, evaluate against the exception class. If matched, the L4 retry count is used AND a flag `_retry_policy_applies = True` is set, which **skips the L1-L3 hard-constraint check** (`should_retry_this_error`).
2. **Hard-constraint check** (only if L4 didn't apply) — e.g., never retry 401 if single deployment.
3. **L3 deployment override** — if exception object has `num_retries` attribute (set by deployment-metadata handler), use it.
4. **L2 per-request override** — extracted at call entry; used if no L3/L4 override.
5. **L1 global** — fallback if all above are silent.

Effective precedence (highest wins): **L4 retry policy > L3 per-deployment > L2 per-request > L1 global**.

The non-obvious aspect: L4's `_retry_policy_applies` flag bypasses L2/L3/hard-constraint checks. An operator-defined retry policy is "trust me, retry this exception" — overrides safety guards.

**S-12: Retry-After header parsing (RFC 7231)** `[region-6]`. The gateway parses both formats: integer seconds and HTTP-date. Uses python `email.utils` for date parsing. The parsed value is capped at 60 seconds — longer values are ignored in favor of exponential backoff.

**S-13: Retry-After does NOT consume a retry** `[region-6]`. The Retry-After value affects sleep duration, not the retry count. After sleeping `retry_after` seconds, the request retries normally and the counter decrements as usual.

**S-14: Exponential backoff fallback** `[region-6]`. When Retry-After is absent or out-of-range (>60s), the gateway uses `INITIAL_RETRY_DELAY * 2^(num_retries - remaining_retries)` clamped to `MAX_RETRY_DELAY`. Jitter is added to avoid thundering herd.

**S-15: Same-group healthy-peer zero-sleep** `[region-7]`. Critical optimization: before computing exponential backoff, the gateway calls `_async_get_healthy_deployments(model)`. If the result list is non-empty, sleep is zero — retry immediately on the healthy peer. This converts retry latency from "exponential backoff" to "round-trip to peer" when the failure is local.

**S-16: Fallback chain — typed lattice** `[region-3]`. Three independent fallback maps:
- `fallbacks`: generic, model-group keyed (with `*` wildcard support).
- `context_window_fallbacks`: triggered only on `ContextWindowExceededError`.
- `content_policy_fallbacks`: triggered only on `ContentPolicyViolationError`.

The matching is exception-class first; if the exception is one of the typed cases, only the typed map is consulted. Otherwise the generic map.

**S-17: Order-based deployment fallback within a group** `[region-3]`. Deployments within a model group can have a `_get_deployment_order()` attribute. The gateway tries the next-higher-order deployment first before exhausting and falling out to a different model group. This is an in-group fallback "tier" before the typed lattice.

**S-18: Fallback recursion bound** `[region-3]`. Each fallback attempt increments a depth counter. If depth ≥ `max_fallbacks` (default `litellm.ROUTER_MAX_FALLBACKS`), the original exception is re-raised. Cycle detection is implicit via depth — no explicit cycle graph check.

**S-19: Cooldown updates on every failure** `[region-1]`. `_set_cooldown_deployments()` is called on each failure independent of whether the request retries or fails over. The failure counter increments toward S-4 threshold regardless of how the request was ultimately served. A successful retry/fallback does NOT subtract from the failure counter — counters are absolute per minute.

**S-20: Allowed-fails policy (orthogonal to retry policy)** `[region-5]`. Separate config: `AllowedFailsPolicy` maps exception class → "max failures before cooldown qualifies". This is a per-exception-class traffic-floor override. Used to e.g. cool down faster on auth errors than on rate-limit errors.

**S-21: Wildcard model fallback** `[region-3]`. The generic fallback map supports `*` as catch-all key — any model with no specific fallback uses the wildcard list. Provider-stripped match (e.g., `gpt-4` matches `openai/gpt-4`) is also supported.

**S-22: Cooldown callback fires for telemetry** `[region-1]`. When a deployment enters cooldown, a callback runs (Prometheus metric emit, etc.). The callback runs synchronously within the cooldown logic; slow callbacks slow the cooldown decision path.

### 2-bis Lifecycle traces (3 observed)

**L-1 Happy retry on healthy peer**: Request lands on deployment A. A returns 429. Cooldown updates A's failure counter (S-19). Retry hierarchy resolves to L4 policy say num_retries=3 (S-11). Healthy-peer check (S-15) finds B in same group → sleep 0 → retry on B. B returns 200. Total user-visible latency = upstream-to-A + tiny + upstream-to-B.

**L-2 Cooldown without retry exhaustion**: Same as L-1 but A continues failing across multiple requests in the same minute. Counter reaches floor (S-3) → failure rate breaches (S-4) → A enters cooldown for 5s default (S-7). Future requests in the next 5s skip A entirely; they go to B directly via S-15-equivalent selection.

**L-3 Typed-fallback context-window**: Request body too large for current model. Provider returns ContextWindowExceededError. S-16 detects exception class → consults `context_window_fallbacks` map → finds 'gpt-4' → ['gpt-4-32k']. Routes to gpt-4-32k. NO retry on original; immediate fallback.

---

## 3. INPUTS (data structures touched)

**Per-Request inputs**: model id, kwargs.num_retries (L2), kwargs.fallbacks (per-call override), exception class on failure, exception object's num_retries attribute (L3), Retry-After header from response.

**Per-Deployment state**: success counter for current minute, failure counter for current minute, cooldown TTL entry in DualCache, deployment metadata (cooldown_time, num_retries, _get_deployment_order).

**Per-Model-Group state**: list of deployments, healthy_deployments cached subset, model_group_retry_policy, fallback maps (generic + context-window + content-policy).

**Per-Process state**: DualCache (in-memory + Redis), router-level defaults (num_retries, cooldown_time, retry_policy), fallback chain config, max_fallbacks limit.

**Persistent state**: None directly observed in the cooldown/retry path. Cooldown state in Redis if configured but recovers from in-memory loss. No DB writes for routing decisions in this scope.

---

## 4. FAILURE MODES (observed-only)

| FM-id | Trigger | Observable outcome | Operator signal | Recovery | Blast radius |
|---|---|---|---|---|---|
| FM-1 | Single-deployment group fails non-429 | Cooldown skipped (S-5); requests keep failing on same deployment | none | wait for upstream / manual intervention | single-group outage |
| FM-2 | Connection error misclassified | Cooldown never fires for transient network issues (S-6); but neither for genuine network failures | none | manual reclassification | no degradation in single-deployment; in multi-deployment retries try peers |
| FM-3 | Calendar-minute boundary at high failure rate | Counter resets at xx:00; a deployment 90% failed at 59s starts 100% fresh at 00s; cooldown delayed | metric noise | next minute's failures rebuild the case | one-minute delay in cooldown |
| FM-4 | Retry policy override allows retrying 401 with single deployment | `_retry_policy_applies` skips hard-constraint check (S-11); 401 retried hopefully `num_retries` times all failing | none | wait for retry exhaustion | per-request latency increase |
| FM-5 | Multi-replica without Redis | Each replica has independent cooldown counter; same deployment may be cooled by some replicas not others | inconsistency | configure Redis | scattered routing |
| FM-6 | Retry-After > 60s | Capped to exponential backoff < 60s; ignores upstream's request | premature retry | none | upstream rate-limit re-fires |
| FM-7 | Cooldown callback slow (Prometheus push timeout) | Cooldown decision path slows; retry latency increases | metric path latency | unblock callback | per-failure latency increase |
| FM-8 | Fallback recursion at max_fallbacks | Original exception re-raised; user sees the first failure not the chain | trace | tune max_fallbacks | per-request |
| FM-9 | model_group_retry_policy missing for a group with global retry_policy set | Global policy applies; per-group customization silently ignored | none | use group-keyed policy | all groups affected the same way |

---

## 5. INTERFACES TO HUAKAI

**Personal Edition**:
- HUAKAI's existing `provider_accounts` table maps to LiteLLM's "deployment". The cooldown TTL in F-AUTH-005 (`temp_unsched_until`) is structurally analogous to LiteLLM's DualCache TTL.
- The 4-level retry hierarchy from S-10 is more detailed than HUAKAI's current 1-level (per-Route timeout). HUAKAI Phase 4.5 should adopt L1+L2 (global + per-request); L3+L4 are operator-tunable layers worth deferring.

**SaaS Edition**:
- Multi-replica cooldown coordination: HUAKAI's PostgreSQL-backed `temp_unsched_until` is the equivalent of Redis-shared cooldown. PostgreSQL provides the consistency LiteLLM-without-Redis lacks (FM-5 eliminated).
- Per-tenant retry policy: each tenant's RetryPolicy map should be per-tenant (DR-001 isolation). Operator-platform fallback for tenants who don't customize.

**Cross-feature**:
- F-POOL-001 9-gate Health gate → S-2 error-class taxonomy (mostly compatible; HUAKAI must add APIConnectionError-equivalent handling).
- F-GW-002 retry orchestration above the streaming forwarder → S-15 zero-sleep peer selection should be HUAKAI's default.
- F-OBS-001 audit row for retry/fallback → S-22 callback pattern translates to HUAKAI's billing event row write inside Tx2.

---

## 6. RISKS HUAKAI MUST GUARD AGAINST

**R-1 [DR-006 PostgreSQL — calendar-minute counter (FM-3)]**: LiteLLM's per-minute calendar bucket is in-process; HUAKAI's PostgreSQL implementation must use **time-windowed queries** with proper indexing, not a calendar-aligned bucket. Recommend a sliding 60-second window.

**R-2 [DR-001 multi-tenant — retry policy escape (FM-4)]**: LiteLLM's `_retry_policy_applies` flag bypasses safety checks. In HUAKAI multi-tenant, a malicious or misconfigured tenant's policy that retries 401 a hundred times causes excess upstream calls and potential rate-limit on the whole pool. **Mitigation**: per-tenant retry policy MUST have a hard ceiling (e.g., max 5 retries even if policy says more) regardless of operator config.

**R-3 [Connection-error short-circuit (S-6)]**: HUAKAI's edge runtime may have legitimate connection errors mixed with network blips. Pure string-match on "APIConnectionError" is fragile. **Mitigation**: a typed error class hierarchy with explicit `is_transient_network_error()` predicate, configurable per-provider.

**R-4 [DR-002 SaaS Edition — retry policy as DOS vector]**: An operator can set per-deployment num_retries = 10 across many deployments; one bad request becomes 100 upstream calls. Limit per-tenant total-retries-per-minute as a budget, not just per-call.

**R-5 [Fallback chain DAG vs tree (S-18)]**: LiteLLM uses depth-only recursion bound; no cycle detection. A bad config like "A → B → A" loops via depth limit only. **Mitigation**: parse fallback config at boot; reject cyclic definitions.

**R-6 [Single-deployment exemption (S-5) in SaaS]**: Each tenant's pool may have a single-account group. Under DR-001, the exemption applies per-tenant — a tenant with one account in group X should be exempted; a different tenant with two accounts should not. **Mitigation**: exemption check must use per-tenant deployment count, not global.

**R-7 [Cooldown callback synchronous (FM-7)]**: HUAKAI's Tx2 settler is the analog of "callback after disable". The settler is already asynchronous (outbox pattern); ensure cooldown analog uses similar asynchrony — never let metrics emission hold the request path.

**R-8 [Retry-After cap = 60s ignores upstream's actual signal (FM-6)]**: HUAKAI should NOT cap Retry-After at 60s. If the upstream says 600s, honor it (and surface to operator via metrics). LiteLLM's cap is opinionated; HUAKAI's multi-tenant must respect upstream contract.

**R-9 [Healthy-peer zero-sleep + claim row interaction]**: When HUAKAI retries through F-POOL-001 to a healthy peer, the F-OBS-001 claim row must update its `provider_account_id` (Pattern B writeback) on the new account. LiteLLM's mechanism doesn't have an analog of claim row, so the integration is HUAKAI-specific. Verify the writeback is atomic per retry attempt.

---

## 7. SAFE ADAPTATION (concrete divergences)

1. **Sliding 60-second window** instead of calendar-aligned bucket for cooldown counters.
2. **Per-tenant retry-budget cap** (e.g., 5 retries / minute / tenant) regardless of policy override.
3. **Typed error-class predicate** `is_transient_network_error()` replacing string-match on "APIConnectionError".
4. **Per-tenant single-deployment exemption** (DR-001 isolation).
5. **PostgreSQL-shared cooldown** via `temp_unsched_until` columns; consistent across all replicas (eliminates FM-5).
6. **Honor Retry-After up to a per-route configurable cap** (not hardcoded 60s).
7. **Explicit cycle detection on fallback config at boot** (not just depth limit).
8. **Asynchronous cooldown telemetry** via outbox; never block request path.

---

## 8. EVIDENCE LEDGER ROWS (proposed additions)

- **E-LM-DEEP-001..014**: existing inventory rows — promote with deep contents.
- **E-LM-DEEP-NEW-1**: 4-level retry hierarchy with deterministic precedence + L4 policy bypass `[region-3]`.
- **E-LM-DEEP-NEW-2**: typed fallback lattice (3 maps: generic + context-window + content-policy) `[region-3]`.
- **E-LM-DEEP-NEW-3**: same-group healthy-peer zero-sleep optimization `[region-7]`.
- **E-LM-DEEP-NEW-4**: cooldown TTL with 3-tier override `[region-2]`.

---

## 9. OPEN QUESTIONS (for synthesis)

1. **Q-1 ROUTER_MAX_FALLBACKS default value**: not directly read; affects depth bound.
2. **Q-2 _async_get_healthy_deployments full logic**: how does the gateway decide a peer is "healthy" — is it only "not-cooled-down" or does it include a separate health probe?
3. **Q-3 Retry-After>60 deliberate cap reason**: was this empirically observed harmful? affects whether HUAKAI inherits the cap or removes it.
4. **Q-4 Cross-replica cooldown without Redis**: documented requirement or undocumented config gap?
5. **Q-5 Cooldown counter on partial failure**: if a streaming response started OK but failed mid-stream, does this count as failure for cooldown purposes? — affects HUAKAI's F-GW-002 end-class taxonomy mapping.
6. **Q-6 max_fallbacks per-request override**: can a caller set this per-call or only at router init?

---

## 10. SOURCE COVERAGE PROOF (Sonnet Explore agent reading, ~30min, 8 files)

| Region | URL | Contribution |
|---|---|---|
| region-1 | github.com/BerriAI/litellm/main/litellm/router_utils/cooldown_handlers.py | 5-axis cooldown decision, error-class taxonomy, traffic floor, single-deployment exemption, connection-error short-circuit |
| region-2 | .../litellm/router_utils/cooldown_cache.py | DualCache TTL storage, 3-tier cooldown_time override |
| region-3 | .../litellm/router.py (lines 5334-5942 + 6060-6084) | Retry hierarchy resolution, fallback chain (typed lattice + order-based), healthy-peer zero-sleep, cycle/depth bound |
| region-4 | .../litellm/router_utils/get_retry_from_policy.py | Retry policy resolution function |
| region-5 | .../litellm/types/router.py (lines 457-487) | RetryPolicy class definition (per-exception map), AllowedFailsPolicy class |
| region-6 | .../litellm/utils.py (lines 6714-6800) | HTTP status taxonomy, Retry-After header parsing (RFC 7231), exponential backoff fallback |
| region-7 | .../litellm/router.py (lines 6076-6084) | _time_to_sleep_before_retry zero-return when healthy peer exists |
| region-8 | .../litellm/constants.py (lines 37-92) | Default constants: failure threshold percent, minimum requests, single-deployment threshold, default cooldown seconds |

---

## 11. ROUND-2 CRITIC FINDINGS (C4 litellm)

> Codex critic file at `.omc/artifacts/decomp-critic/C4-litellm-cooldown-retry.md`. This Claude-deep file is written without reading C4 per cross-validation discipline. Synthesis stage merges Codex specifier-deep + C4 critic + this Claude-deep.

---

## Owner Chinese summary

本 deep 拆解依据 Sonnet Explore agent 真读 8 个 litellm 源文件（30min），由我（Claude Opus）合成 22 个 sub-behavior + 3 个 lifecycle + 9 个 failure 模式 + 9 个 HUAKAI-fit 风险 + 8 项 safe adaptation。**最关键发现**：(1) 4 级 retry 层级有**确定优先级** L4>L3>L2>L1，且 L4 策略一旦命中**会绕过硬约束**——HUAKAI 多租户必须加 per-tenant 总重试预算上限（R-2/R-4）；(2) 同组健康对等部署的**零等待 retry**（S-15）是 SLA 的关键优化；(3) cooldown 用 **per-minute 日历桶**不是滑窗——HUAKAI 该改滑窗（R-1）；(4) 单部署组豁免（S-5）需按租户隔离（R-6）；(5) Retry-After 被硬截到 60s 是 LiteLLM 的固执选择，HUAKAI 多租户应尊重上游契约（R-8）。本文件未读 codex specifier 或 critic 输出，第二独立视角。
