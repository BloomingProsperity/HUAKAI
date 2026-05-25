# 2026-05-02 HUAKAI Algorithm Upgrade Plan - Codex

| Field | Value |
| --- | --- |
| Date | 2026-05-02 |
| Author | Codex |
| Status | Draft for Owner review |
| Scope | Algorithm-level upgrades only. Not a refactor plan, not a gap-filling checklist. |
| Naming | Uses codenames only. Mapping source: `docs/reference_delta/2026-05-02/codename-mapping.md`. |
| Clean-room stance | This plan consumes behavior summaries and released HUAKAI specs only. It does not copy source code, schemas, comments, UI source, or distinctive implementation structure from reference projects. |
| Forbidden-material check | Did not read any Owner-forbidden framing/comparison plan or excluded HUAKAI variant-planning document. |

## 0. Goal

HUAKAI is an open-source AI gateway competition product. The target is:

1. **Commercial-Pool-Ref 100% core capability preservation** for account pool, sticky routing, quota/billing, payment/recovery, account health, and operator workflows.
2. **Algorithm-level stronger behavior** on every critical surface: scheduling, sticky migration, credential leasing, quota settlement, retry DAGs, error normalization, pricing, capacity forecast, cross-vendor capacity allocation, monitor state, client identity detection, and streaming buffer/drain.

The plan below intentionally avoids "add a feature" phrasing. Each item defines a stronger algorithm or state machine with measurable signals.

## 1. Algorithm Upgrades

**A01 Binding-Aware Five-Layer Scheduler [P0] [类型: 算法升级]**

基线-开源（代号引用）: Commercial-Pool-Ref has a strong multi-layer selection path: previous-response stickiness, transport compatibility, bind/session affinity, and load-aware weighted selection. The limitation for HUAKAI is that this does not start from the Account-to-API spine: local API key binding is not the first routing constraint, so premium/free key products can be accidentally represented as later filters instead of hard contracts. Clean-Arch-Ref exposes direct credential-to-API shape, but simpler strategy knobs do not cover pool-grade health and billing admission.

基线-官方（Vendor-X 代号）: Vendor-X1 and Vendor-X2 treat API keys/workspaces/projects as policy boundaries. Vendor-Meta exposes provider routing/sticky behavior as an explicit request policy. The algorithm must preserve key/project entitlement before provider selection.

HUAKAI 升级:

原算法（伪代码 / 数学公式 / 状态转换）:

```python
def select_old(api_key, model, req):
    pool = resolve_pool(req.tenant_id, model, api_key.user_group)
    candidates = filter_schedulable(pool.accounts, model, req.capabilities)
    sticky = sticky_cache.lookup(req.session_key)
    if sticky in candidates:
        return acquire(sticky)
    ranked = rank_by_priority_load_lru(candidates)
    return acquire_first_available(ranked)
```

新算法（伪代码 / 数学公式 / 状态转换）:

```python
def select(api_key, model, req):
    bindings = binding_index.active(api_key.id, req.tenant_id)
    if bindings.empty():
        bindings = [ensure_tenant_default_binding(api_key)]

    plan = []
    for b in bindings.sorted_by_priority():
        target_accounts = expand_binding_target(b)
        snapshot = capability_snapshot.freeze(target_accounts, at=req.started_at)
        eligible = []
        for account in target_accounts:
            if hard_gates_pass(account, snapshot, model, req):
                eligible.append(account)
        plan.append((b, eligible))

    for b, eligible in plan:
        chosen = select_with_sticky_then_score(b, eligible, req)
        if chosen:
            return acquire_with_claim_writeback(api_key, b, chosen, req)

    return wait_or_fail(plan, ttl=min_recovery_eta(plan))
```

复杂度对比: Old `O(A log A)` per pool. New `O(B + A log K)` where `B` is key bindings and `A` is expanded accounts. Space adds `O(B + A)` route-plan snapshot. Consistency improves because `binding_id` becomes part of Tx1/Tx2 and attempt audit.

数据结构变化: Add or prioritize `api_key_bindings`; add route-plan cache keyed by `(tenant_id, api_key_id, binding_policy_version, model)`; Usage Record and attempt rows must carry `binding_id`.

为什么更强: Mis-dispatch from key to unauthorized pool becomes a hard error instead of a late filter miss. Expected unauthorized-pool selection rate target: 0 under race tests. Operator support can answer "which binding authorized this account" in one query.

信号: `route_binding_missing_total`, `route_binding_fallback_total`, `route_unauthorized_pool_total == 0`, AT-ACCAPI-001/002, AT-POOL-010.

对应 F-* IDs: `F-ACCAPI-BIND-001`, `F-ACCAPI-CORE-001`, `F-POOL-001`, `F-KEY-001`, `F-OBS-QUERY-001`.

Effort: 14 小时

**A02 Pareto Band Account Scoring [P0] [类型: 算法升级]**

基线-开源（代号引用）: Commercial-Pool-Ref scores concurrency, queue pressure, error rate, TTFT, priority, and manual load factor. Multi-Provider-Ref confirms health/cooldown-aware deployment selection. Limitation: a single blended score can hide a dominated account if a high manual weight offsets a severe reliability or quota deficit.

基线-官方（Vendor-X 代号）: Vendor-X3 uses dynamic shared quota and provisioned throughput ideas; Vendor-X4 cross-region profiles imply routing should respect capacity and fault-domain constraints, not only local load.

HUAKAI 升级:

原算法:

```python
score = (
    w1 * concurrency_load
  + w2 * queue_depth
  + w3 * error_rate
  + w4 * ttft_p95
  - w5 * admin_priority
  + w6 * manual_load_factor
)
return min(candidates, key=score)
```

新算法:

```python
def pareto_band(candidates):
    vectors = []
    for a in candidates:
        v = {
            "load": a.in_flight / max(1, a.cap_concurrency),
            "queue": a.queue_depth / max(1, a.queue_cap),
            "err": ewma(a.error_rate_5m),
            "ttft": p95(a.ttft_ms_10m) / route_slo_ms(a.model),
            "quota": 1 - min(1, a.quota_remaining / req.estimated_cost),
            "fresh": snapshot_staleness_penalty(a.snapshot_age_ms),
        }
        vectors.append((a, v))

    non_dominated = [
        a for a, v in vectors
        if not exists(bv for b, bv in vectors if dominates(bv, v, eps=0.03))
    ]
    band = same_priority_band(non_dominated)
    return weighted_random(band, weight=lambda a: exp(-risk_energy(a)))

def risk_energy(a):
    return (
        2.0 * load(a) +
        1.5 * err(a) +
        1.0 * ttft(a) +
        1.2 * quota_risk(a) -
        0.4 * admin_priority_bonus(a)
    )
```

复杂度对比: Old `O(A)`. New naive Pareto `O(A^2)`, optimized with buckets `O(A log A)` for common `A <= 200`. Space `O(A)`. Fault tolerance improves because dominated accounts are excluded before randomization.

数据结构变化: Add per-account rolling metrics cache: `load`, `queue`, `error_rate`, `ttft`, `quota_remaining`, `snapshot_age`. Add `routing_score_vector` to structured route reason.

为什么更强: Prevents one high-priority but failing account from being chosen. Target: account-level P99 first-token latency improves 20-30% in mixed healthy/degraded pools; dominated-account dispatch rate < 1%.

信号: `routing_dominated_candidate_excluded_total`, `route_ttft_p99_by_pool`, `selected_score_vector`, canary comparing old vs new shadow decisions.

对应 F-* IDs: `F-POOL-001`, `F-ROUTER-HEALTH-001`, `F-ROUTE-001`, `F-AIGW-METRICS-001`.

Effort: 12 小时

**A03 Strategy Enum With Compatibility Floor [P1] [类型: 算法升级]**

基线-开源（代号引用）: Clean-Arch-Ref exposes round-robin and fill-first strategy knobs. Commercial-Pool-Ref has stronger implicit layered behavior. Limitation: HUAKAI's stronger scheduler can become opaque if there is no named strategy surface for operators.

基线-官方（Vendor-X 代号）: Vendor-Meta lets operators express provider order and fallback behavior. Vendor-X1 project/admin surfaces separate entitlement from routing policy.

HUAKAI 升级:

原算法:

```python
strategy = "default"
return select_by_internal_layers(candidates)
```

新算法:

```python
def select_by_strategy(policy, candidates, req):
    if policy.strategy == "compat_priority_lru":
        return priority_then_load_then_lru(candidates)
    if policy.strategy == "round_robin":
        return consistent_rr(policy.route_id, candidates)
    if policy.strategy == "fill_first":
        return first_until_threshold(candidates, threshold=policy.fill_threshold)
    if policy.strategy == "risk_pareto":
        return pareto_band(candidates)
    raise ConfigError("unknown_strategy")
```

复杂度对比: Same selection complexity per chosen strategy. Adds config validation cost `O(1)`. Consistency improves because every route decision stores `strategy_version`.

数据结构变化: `route_policy.strategy`, `strategy_version`, dry-run validator, route reason `strategy`.

为什么更强: Preserves Commercial-Pool-Ref-compatible behavior while letting operators choose simpler modes for debugging or predictable capacity drain.

信号: `route_strategy_distribution`, strategy dry-run diff, AT-GW-POLICY-001.

对应 F-* IDs: `F-GW-POLICY-001`, `F-POOL-001`, `F-AIGW-CONFIG-001`.

Effort: 8 小时

**A04 Sticky Migration Loss Function [P0] [类型: 算法升级]**

基线-开源（代号引用）: Commercial-Pool-Ref and Billing-Engine-Ref both preserve affinity/sticky choices. Clean-Arch-Ref shows multiple concrete session identity sources. Limitation: sticky break is usually reason-based, not cost-optimized across context-loss, capacity, credential freshness, and user-visible continuity.

基线-官方（Vendor-X 代号）: Vendor-Meta exposes sticky routing expectations. Vendor-X1/Vendor-X2 long-running conversations and cached prompts make context continuity economically relevant.

HUAKAI 升级:

原算法:

```python
if sticky_account and eligible(sticky_account):
    return sticky_account
return select_fresh(candidates)
```

新算法:

```python
def sticky_decision(sticky, candidates, session):
    if sticky is None:
        return MIGRATE(select_fresh(candidates), reason="no_sticky")

    loss = (
        0.40 * context_loss_prob(session, sticky) +
        0.20 * cache_loss_cost(session, sticky) +
        0.20 * sticky_load_pressure(sticky) +
        0.10 * credential_expiry_risk(sticky) +
        0.10 * cooldown_near_risk(sticky)
    )
    stay_cost = admission_delay_ms(sticky) / req.deadline_ms + overload_risk(sticky)
    migrate_cost = loss + best_candidate_risk(candidates)

    if hard_gates_fail(sticky):
        return MIGRATE(best_candidate(candidates), reason=hard_gate_reason(sticky))
    if migrate_cost + hysteresis(session) < stay_cost:
        return MIGRATE(best_candidate(candidates), reason="lower_expected_loss")
    return STAY(sticky)
```

复杂度对比: Old `O(1)` after candidate build. New `O(C)` for candidate best-risk plus constant loss components. Space adds session loss cache `O(active_sessions)`.

数据结构变化: Sticky cache stores `context_class`, `last_account_id`, `cache_token_value`, `session_age`, `migration_count`, `last_migration_reason`.

为什么更强: Avoids two bad extremes: staying on a dying account until failure, or migrating too eagerly and losing upstream context. Target: sticky-context failure rate cut by 50%; hot account in-flight skew reduced by 25%.

信号: `sticky_migration_reason_total`, `context_may_be_lost_total`, `sticky_hotspot_gini`, replay test with account health flip.

对应 F-* IDs: `F-SESSION-001`, `F-ACCAPI-STATE-001`, `F-POOL-001`, `F-OBS-QUERY-001`.

Effort: 12 小时

**A05 Sticky Hotspot Rebalance With Hysteresis [P1] [类型: 算法升级]**

基线-开源（代号引用）: Commercial-Pool-Ref has sticky wait behavior and load scoring. Limitation: sticky accumulation can create per-account hotspots even when fresh traffic is balanced.

基线-官方（Vendor-X 代号）: Vendor-X3 dynamic quota and Vendor-X4 cross-region behavior require not concentrating all long sessions in one fault/capacity bucket.

HUAKAI 升级:

原算法:

```python
def admit_sticky(account):
    if account.in_flight < account.cap:
        return account
    return wait_or_fallback()
```

新算法:

```python
def rebalance_sticky(pool):
    target = pool.active_sessions / max(1, pool.healthy_account_count)
    for account in pool.accounts:
        debt = account.sticky_sessions - target
        if debt <= pool.policy.sticky_debt_threshold:
            continue
        movable = sessions_on(account).filter(lambda s: s.loss_score < 0.25)
        for s in movable.sorted_by("loss_score"):
            dst = best_account_for_session(s, exclude=[account])
            if dst and projected_skew_after_move(account, dst) < current_skew(pool):
                enqueue_soft_migration(s, dst, at=s.next_request)
```

复杂度对比: Periodic `O(S log S + A)` per pool, not on hot request path. Space `O(S)` session stats.

数据结构变化: `sticky_session_stats` or cache; per-account `sticky_session_count`; migration queue with TTL.

为什么更强: Rebalances only low-loss sessions and only at next request. Target: per-account sticky-session Gini < 0.25 in steady state.

信号: `sticky_rebalance_enqueued_total`, `sticky_rebalance_applied_total`, session Gini, context-loss complaints.

对应 F-* IDs: `F-SESSION-001`, `F-ROUTE-001`, `F-OPS-003`.

Effort: 8 小时

**A06 Credential Lease Version State Machine [P0] [类型: 状态机升级]**

基线-开源（代号引用）: Commercial-Pool-Ref and Clean-Arch-Ref both show account credentials as operational assets. HUAKAI audit identified the missing per-request lease: current plan can know account and key, but not exactly which credential version served an attempt.

基线-官方（Vendor-X 代号）: Vendor-X1/Vendor-X2 API keys and OAuth-like sessions can rotate/revoke. Vendor-X3 service-account JWTs and Vendor-X4 IAM-style auth make versioned credential identity mandatory for incident review.

HUAKAI 升级:

原算法:

```python
creds = account.credentials
inject(req, creds)
```

新算法（状态转换）:

```text
VALID(v)
  -- lease(req) --> LEASED(v, request_id, attempt_id, expires_at)
LEASED(v)
  -- refresh_writes(v+1) --> LEASED_STALE(v)  # in-flight may finish
LEASED_STALE(v)
  -- upstream_401 --> RETRY_REQUIRED(v+1)
LEASED(v)
  -- success/final_error --> RELEASED(v)
VALID(v)
  -- revoke/manual_disable --> REVOKED(v), new leases denied
```

```python
def acquire_credential_lease(account, req):
    row = lock_account(account.id)
    v = row.credential_version
    if not credential_gate(row):
        return LeaseDenied(reason=state_reason(row))
    lease = Lease(
        request_id=req.id,
        attempt_id=req.attempt_id,
        account_id=account.id,
        kind=row.credential_kind,
        version=v,
        expires_at=min(row.credential_expires_at, req.deadline)
    )
    return lease
```

复杂度对比: Old `O(1)` but opaque. New `O(1)` with row lock/CAS. Space adds one lease tuple per attempt or fields on `request_attempts`.

数据结构变化: `credential_version`; `request_attempts.credential_kind/version`; optional L2 `credential_leases`; audit event on stale use.

为什么更强: Rotation safety and forensics become deterministic. Target: 100% attempts can answer credential version used; mid-request refresh never corrupts settlement or account state.

信号: `credential_lease_acquired_total`, `lease_stale_retry_total`, `attempt_credential_version_present == 100%`.

对应 F-* IDs: `F-ACCAPI-LEASE-001`, `F-ACCAPI-ATTEMPT-001`, `F-AUTH-005`.

Effort: 10 小时

**A07 Three-Scope Refresh Storm Controller [P0] [类型: 算法升级]**

基线-开源（代号引用）: Commercial-Pool-Ref has refresh/cache behavior; Clean-Arch-Ref uses bounded refresh workers. HUAKAI's released credential spec already targets account/provider/global storm controls. Limitation to avoid: worker-count-only control can still concentrate all slots on one upstream endpoint or tenant.

基线-官方（Vendor-X 代号）: Vendor-X1/Vendor-X2 OAuth/session endpoints and Vendor-X3 service tokens may impose independent rate limits. Refresh traffic must not become a self-inflicted outage.

HUAKAI 升级:

原算法:

```python
if token_expired(account):
    refresh(account)
```

新算法:

```python
def refresh_budget(account, provider_endpoint):
    scopes = [
        ("account", account.id, 1, ttl=30s),
        ("endpoint", provider_endpoint, endpoint_limit(provider_endpoint), ttl=10s),
        ("global", "oauth_refresh", global_limit(), ttl=1s),
    ]
    grants = []
    for scope, key, limit, ttl in scopes:
        grant = token_bucket.try_acquire(scope, key, limit, ttl)
        if not grant:
            release_all(grants)
            mark_temp_unsched(account, reason="refresh_storm_budget", retry_after=ttl)
            return DENY
        grants.append(grant)
    return ALLOW(grants)

def refresh_or_join(account):
    if singleflight.exists(account.id):
        return wait_for_result_or_stale(account)
    with singleflight(account.id):
        grant = refresh_budget(account, account.oauth_endpoint)
        if grant.denied:
            return grant
        return refresh_with_cas(account)
```

复杂度对比: Old `O(1)` per refresh but unbounded herd. New `O(3)` token bucket checks + singleflight. Space `O(active_accounts + endpoints)`.

数据结构变化: Refresh budget buckets, per-account singleflight state, `refresh_attempt_window`, temp-unsched reason.

为什么更强: Reduces refresh storm fan-out from `N expired accounts` to controlled `min(account, endpoint, global)` concurrency. Target: 200 simultaneous expiries create <= configured endpoint concurrency and no request-path collapse.

信号: `refresh_singleflight_join_total`, `refresh_budget_denied_total`, OAuth endpoint error rate, AT-AUTH-005-008/012.

对应 F-* IDs: `F-AUTH-005`, `F-ACCAPI-STATE-001`, `F-RATE-001`.

Effort: 10 小时

**A08 Stale-While-Refresh Admission Guard [P1] [类型: 状态机升级]**

基线-开源（代号引用）: Commercial-Pool-Ref preserves some stale/refresh grace behavior; Clean-Arch-Ref bounded workers are simpler. Limitation: using stale credentials blindly risks repeated upstream 401s; denying all stale use wastes usable grace windows.

基线-官方（Vendor-X 代号）: Vendor-X1/Vendor-X2 credential expiry and Vendor-X3 JWT lifetimes require deterministic expiry skew and retry behavior.

HUAKAI 升级:

原算法:

```python
if expired:
    refresh()
if refresh_failed:
    fail()
return token
```

新算法:

```python
def token_for_request(account, req):
    freshness = account.expires_at - now()
    if freshness > provider_skew(account.provider):
        return USE_CURRENT
    if freshness > 0 and refresh_in_progress(account):
        if req.class_ in {"short_non_stream", "low_cost"}:
            return USE_STALE_WITH_LEASE(max_age=freshness)
        return WAIT_FOR_REFRESH(max_wait=req.policy.refresh_wait_ms)
    if freshness <= 0:
        return REFRESH_OR_FAILOVER
```

复杂度对比: Constant time. Adds policy matrix and counters. Better fault tolerance under rolling expiry waves.

数据结构变化: Per-provider skew table; request class; lease marks `stale_allowed`.

为什么更强: Keeps short low-risk traffic flowing while protecting long streams from guaranteed mid-stream auth failure. Target: 401 retry rate from stale path < 0.5%; refresh-induced 503 reduced by 40%.

信号: `stale_while_refresh_used_total`, stale path 401 ratio, refresh wait latency.

对应 F-* IDs: `F-AUTH-005`, `F-GW-002`, `F-ACCAPI-LEASE-001`.

Effort: 6 小时

**A09 Attempt-Aware Two-Phase Quota Reserve [P0] [类型: 算法升级]**

基线-开源（代号引用）: Commercial-Pool-Ref and Legacy-Ref show pre-consume and post-settle patterns. Billing-Engine-Ref adds per-request billing session and funding-source behavior. Limitation: simple reserve/settle can become ambiguous when multiple attempts cross accounts with different prices.

基线-官方（Vendor-X 代号）: Vendor-X1/Vendor-X2 Usage+Costs surfaces separate actual usage from request admission. Vendor-X4 token burndown and Vendor-X3 quota classes imply attempt-aware attribution.

HUAKAI 升级:

原算法:

```python
reserve(api_key, estimated_cost)
resp = call_upstream(account)
settle(api_key, actual_cost(resp))
```

新算法:

```python
def tx1_reserve(req):
    claim = upsert_claim(req.idempotency_key, req.fingerprint)
    reserve_dimensions = estimate_max_cost(req)
    lock_order(["api_key", "user", "subscription", "provider_account", "rate_windows"])
    reserve_all(claim, reserve_dimensions)
    return claim

def record_attempt(claim, account, lease, status):
    append_attempt(
        claim_id=claim.id,
        attempt_no=next_attempt_no(claim),
        account_id=account.id,
        binding_id=claim.binding_id,
        credential_version=lease.version,
        price_snapshot_id=current_price_snapshot(account, req.model),
        status=status,
    )

def tx2_settle(claim, final_attempt, usage):
    policy = claim.attribution_policy  # succeeded_on | dollar_weighted | first_tried
    cost = evaluate_attempt_cost(final_attempt.price_snapshot_id, usage)
    adjust_reserved_to_actual(claim, cost)
    mark_claim_committed(claim, final_attempt.id)
```

复杂度对比: Old constant settlement but loses attempt attribution. New `O(N_attempts)` storage and final evaluation; N bounded by retry policy. Consistency improves via append-only attempts and immutable claim.

数据结构变化: `request_attempts`, `billing_claim.attribution_policy`, `attempt.price_snapshot_id`, `usage_records.final_attempt_id`.

为什么更强: Eliminates split-price ambiguity. Target: every retry/fallback claim has exact succeeded attempt and attribution policy; reconciliation drift detectable at query time.

信号: `claim_attempt_count_histogram`, `split_attempt_cost_total`, `settlement_without_attempt_total == 0`, AT-OBS-004/020.

对应 F-* IDs: `F-OBS-001`, `F-ACCAPI-ATTEMPT-001`, `F-BILL-SESSION-001`, `F-BILL-SNAPSHOT-001`.

Effort: 14 小时

**A10 Quantile Reserve Estimator [P1] [类型: 算法升级]**

基线-开源（代号引用）: Legacy-Ref and Billing-Engine-Ref reserve predicted quota then settle delta. Obs-Ref shows escrow/direct-debit patterns. Limitation: static max-token reservation either over-reserves and rejects valid users, or under-reserves and risks negative balance.

基线-官方（Vendor-X 代号）: Vendor-X1/Vendor-X2 token usage and prompt cache behavior produce variable actual cost. Vendor-X4 token burndown also makes conservative but not absurd reserve important.

HUAKAI 升级:

原算法:

```python
estimated_cost = max_tokens * model_unit_price
reserve(estimated_cost)
```

新算法（公式）:

```text
reserve_cost =
  price_snapshot(model, account)
  * Q_p(tokens_out | api_key_tier, model, endpoint, prompt_len_bucket, cache_hint)
  + fixed_input_cost

p = 0.95 for normal users
p = 0.99 for risky/new users
p = 0.90 for trusted wallet users with credit line
```

```python
def estimate_reserve(req, user):
    features = bucketize(req.model, req.endpoint, req.prompt_tokens, req.cache_hint, user.tier)
    q = quantile_model.predict(features, p=reserve_percentile(user))
    return min(q * unit_price(req), req.operator_max_reserve)
```

复杂度对比: Old `O(1)`. New `O(1)` lookup/update for online quantiles; training/update `O(log buckets)` per settled request. Space `O(model * endpoint * tier * buckets)`.

数据结构变化: Quantile sketches per `(tenant, model, endpoint, tier, prompt_len_bucket, cache_hint)`; reserve policy table.

为什么更强: Reduces false 402 from over-reserve while bounding one-request overdraft. Target: reserve overhang p50 down 30%; insufficient-settle events < 0.1%.

信号: `reserve_overhang_ratio`, `settle_exceeds_reserve_total`, `false_quota_reject_total`.

对应 F-* IDs: `F-BILL-PRE-001`, `F-WALLET-ESCROW-001`, `F-BILL-SESSION-001`.

Effort: 10 小时

**A11 Bounded Multi-Attempt DAG Planner [P0] [类型: 算法升级]**

基线-开源（代号引用）: Retry-Policy-Ref has retry/fallback status handling and stop conditions. Multi-Provider-Ref has fallback lists and separate content/context fallbacks. Obs-Ref has attempt ordering with platform-generated error distinctions. Limitation: linear retry lists cannot express dependencies like "refresh credential before retry same account" or "model-substitution only after account spillover fails".

基线-官方（Vendor-X 代号）: Vendor-Meta provider order/fallback and Vendor-X1/Vendor-X2 Retry-After/rate limit behavior require ordered and bounded attempt planning.

HUAKAI 升级:

原算法:

```python
for target in fallback_targets:
    for i in range(max_retries):
        resp = call(target)
        if success(resp) or not retryable(resp):
            return resp
```

新算法:

```python
def build_attempt_dag(req, route_policy):
    g = DAG()
    root = g.add("initial", account=select(req), model=req.model)
    g.add_edge(root, "same_account_after_refresh",
               when="auth_401", action=refresh_then_retry, budget=1)
    g.add_edge(root, "same_model_next_account",
               when="upstream_429|5xx|timeout_pre_content", action=spill_account, budget=route_policy.account_fallbacks)
    g.add_edge("same_model_next_account", "model_substitution",
               when="quota_model_exhausted", action=substitute_model, budget=route_policy.model_fallbacks)
    g.add_edge("*", "terminal",
               when="gateway_local_error|client_error|budget_exhausted", action=stop)
    return g

def execute_dag(g):
    for node in topological_budget_order(g):
        if total_elapsed() > g.policy.max_elapsed_ms:
            return terminal("retry_budget_exhausted")
        result = run(node)
        append_attempt(result)
        if result.success or result.terminal:
            return result
        enqueue_edges_matching(result.error_class)
```

复杂度对比: Old `O(T * R)`. New `O(V + E)` planning and execution bounded by policy; same upper bound if V is capped. Space adds attempt DAG per request `O(V+E)`.

数据结构变化: `attempt_plan_json`, `request_attempts.parent_attempt_id`, retry budget counters, edge reason taxonomy.

为什么更强: Prevents accidental retry amplification and makes every branch auditable. Target: no local gateway exception triggers provider fallback; max attempts strictly enforced.

信号: `attempt_dag_nodes_total`, `attempt_stop_reason_total`, `gateway_exception_fallback_total == 0`, AT-UPSTREAM-RETRY-002.

对应 F-* IDs: `F-UPSTREAM-RETRY-002`, `F-UPSTREAM-FALLBACK-001`, `F-ACCAPI-ATTEMPT-001`, `F-GW-004`.

Effort: 16 小时

**A12 Stream-Safe Retry Boundary Planner [P0] [类型: 状态机升级]**

基线-开源（代号引用）: Multi-Provider-Ref has streaming fallback behavior; HUAKAI's released streaming spec already blocks mid-stream failover by default. Limitation: retry planning must distinguish before-first-token, after-first-token, after-tool-call, and client-disconnect drain paths.

基线-官方（Vendor-X 代号）: Vendor-X1 Responses/streaming and Vendor-X2 Messages streaming can produce partial usage and tool events; retrying after content risks duplicated user-visible output.

HUAKAI 升级:

原算法:

```python
if stream_error and retryable(status):
    retry_next_account()
```

新算法（状态转换）:

```text
BEFORE_UPSTREAM
  -> BEFORE_FIRST_TOKEN
  -> CONTENT_STARTED
  -> TOOL_SIDE_EFFECT_STARTED
  -> TERMINAL

Retry allowed:
  BEFORE_UPSTREAM: yes
  BEFORE_FIRST_TOKEN: yes if no client bytes flushed
  CONTENT_STARTED: no unless idempotent_stream_replay=true
  TOOL_SIDE_EFFECT_STARTED: never by default
  CLIENT_DISCONNECT: drain only, no client retry
```

```python
def retry_allowed(stream_state, error, req):
    if error.local_gateway:
        return False
    if stream_state in {"BEFORE_UPSTREAM", "BEFORE_FIRST_TOKEN"}:
        return error.retryable
    if stream_state == "CONTENT_STARTED":
        return req.headers.get("Idempotent-Stream-Replay") == "true"
    return False
```

复杂度对比: Constant-time state decision. Space adds per-stream state field and flushed-byte counter.

数据结构变化: `request_attempts.stream_state_at_failure`, `usage_records.end_class`, replay flag in route policy.

为什么更强: Prevents duplicate partial outputs and double billing. Target: mid-content automatic retry count = 0 unless explicit idempotent replay.

信号: `stream_retry_blocked_reason_total`, `stream_replay_opt_in_total`, AT-GW-002-13/14.

对应 F-* IDs: `F-GW-002`, `F-UPSTREAM-RETRY-002`, `F-OBS-001`.

Effort: 8 小时

**A13 Provider Error Normalization Table [P0] [类型: 算法升级]**

基线-开源（代号引用）: Commercial-Pool-Ref and Legacy-Ref classify upstream errors for disable/cooldown. Retry-Policy-Ref stops fallback on gateway-local exceptions. Billing-Engine-Ref has skip-retry and auto-disable rules. Limitation: substring/status logic spread across code paths creates inconsistent account state transitions.

基线-官方（Vendor-X 代号）: Vendor-X1/Vendor-X2 expose provider-specific rate limit and auth failure semantics. Vendor-X3/Vendor-X4 errors may mean quota, IAM, region capacity, or policy denial. Vendor-Meta distinguishes provider failure from platform policy failures.

HUAKAI 升级:

原算法:

```python
if status == 429:
    retry()
elif status in [401, 403]:
    disable_or_refresh()
elif status >= 500:
    retry()
```

新算法:

```python
ERROR_RULES = [
  # high precedence local/platform rules
  Rule(source="huakai", match=local_gateway_error, class_="local_gateway", retry=False, transition="none"),
  Rule(source="huakai", match=client_quota_429, class_="client_rate_limited", retry=False, transition="none"),

  # provider rules
  Rule(provider="*", status=401, body=revoked_marker, class_="auth_revoked", retry=False, transition="disabled"),
  Rule(provider="*", status=401, body=refreshable_marker, class_="auth_refresh", retry=True, transition="needs_refresh"),
  Rule(provider="*", status=429, header=retry_after, class_="upstream_rate_limited", retry=True, transition="cooling_down"),
  Rule(provider="*", status=529, class_="upstream_overloaded", retry=True, transition="cooling_down"),
  Rule(provider="*", status="5xx", class_="upstream_5xx", retry=True, transition="degraded"),
]

def classify(resp, body, context):
    for rule in ERROR_RULES.sorted_by_precedence():
        if rule.matches(resp, body, context):
            return Classification(rule.class_, rule.retry, rule.transition, rule.retry_after(resp, body))
    return Classification("unknown_upstream_error", False, "needs_manual_recovery", None)
```

复杂度对比: Old ad hoc `O(1)` but inconsistent. New `O(R)` rule scan; R small and compiled per provider. Space `O(R)`.

数据结构变化: Versioned `error_classification_rules`, provider override table, `classification_version` stored on attempts.

为什么更强: Same error yields same retry/state/billing behavior across streaming, buffered, monitor, and admin test paths. Target: 100% errors map to closed taxonomy; unknown rate < 0.5%.

信号: `error_class_unknown_total`, `classification_version`, retry decisions by class, AT-RATE-019.

对应 F-* IDs: `F-ACCAPI-ERR-CLASSIFY-001`, `F-RATE-001`, `F-UPSTREAM-FALLBACK-001`.

Effort: 12 小时

**A14 Retry-After Harmonizer With Jittered Cooldown [P1] [类型: 算法升级]**

基线-开源（代号引用）: Commercial-Pool-Ref parses multiple reset sources and uses cooldowns. Retry-Policy-Ref respects provider retry headers. Limitation: simultaneous identical resets can create return-to-service stampedes.

基线-官方（Vendor-X 代号）: Vendor-X1/Vendor-X2 rate-limit headers and Vendor-X3 dynamic quotas can reset multiple accounts near the same time. Vendor-X4 cross-region traffic should not all resume in one region at once.

HUAKAI 升级:

原算法:

```python
cooldown_until = parse_retry_after(resp) or now() + default_cooldown
account.cooldown_until = cooldown_until
```

新算法:

```python
def harmonize_retry_after(resp, account, pool):
    base = parse_provider_reset(resp) or now() + default_cooldown(account.provider)
    jitter_span = min(pool.policy.max_jitter, 0.15 * (base - now()))
    shard = stable_hash(account.id, resp.error_class) / UINT64_MAX
    jitter = (2 * shard - 1) * jitter_span
    cooldown_until = clamp(base + jitter, now() + min_cooldown, now() + max_cooldown)
    return cooldown_until
```

复杂度对比: Constant time. Space none beyond state fields.

数据结构变化: Store `cooldown_base`, `cooldown_jitter_ms`, `cooldown_until`, `cooldown_source`.

为什么更强: Spreads account recovery and reduces thundering herd. Target: return-to-service rate spread across ±15%; retry storm after reset down 50%.

信号: `cooldown_jitter_ms_histogram`, `post_cooldown_429_rate`, AT-RATE-016/017.

对应 F-* IDs: `F-RATE-001`, `F-ROUTER-HEALTH-001`.

Effort: 6 小时

**A15 Versioned Pricing Vector Evaluation [P0] [类型: 算法升级]**

基线-开源（代号引用）: Billing-Engine-Ref freezes pricing snapshot and evaluates tiered expressions; Declarative-Ref highlights cache-aware token burn dimensions; Obs-Ref records cache/reasoning/audio tokens. Limitation: expression syntax and provider-specific details must not be copied; HUAKAI needs a bounded local pricing vector.

基线-官方（Vendor-X 代号）: Vendor-X1/Vendor-X2 prompt caching and reasoning tokens, Vendor-X3 context caching, and Vendor-X4 token burndown require separate token classes.

HUAKAI 升级:

原算法:

```python
cost = (prompt_tokens + completion_tokens) * model_price
```

新算法（公式）:

```text
token_vector =
  [input_fresh, input_cache_read, input_cache_write,
   output, reasoning, audio_input, audio_output, image, tool]

cost =
  dot(price_snapshot.unit_prices, token_vector)
  * group_multiplier_snapshot
  + route_fixed_fee
  + provider_surcharge_snapshot
```

```python
def freeze_price(req, account):
    return PriceSnapshot(
        version=pricing_policy.current_version,
        account_id=account.id,
        model=req.model,
        unit_prices=pricing_policy.vector(req.model, account.provider),
        multipliers=group_policy.snapshot(req.user_group),
        frozen_at=now(),
    )

def settle_cost(snapshot, usage):
    vector = normalize_usage_to_token_vector(usage)
    return decimal_dot(snapshot.unit_prices, vector) * snapshot.multipliers.group
```

复杂度对比: Old `O(1)` scalar. New `O(D)` vector with `D <= 10`; exact decimal math. Space adds snapshot per claim/attempt.

数据结构变化: `pricing_snapshots`, `usage_records.token_vector`, `price_snapshot_id`, policy version table.

为什么更强: Handles prompt cache/reasoning/multimodal cost without price drift. Target: price change mid-request changes 0 historical settlements; money precision invariant exact.

信号: `pricing_snapshot_version`, `cost_replay_diff == 0`, AT-OBS-014, AT-BILL-SNAPSHOT-001.

对应 F-* IDs: `F-BILL-001`, `F-BILL-SNAPSHOT-001`, `F-BILL-TOKEN-NORM-001`, `F-TOKEN-QUOTA-POLICY-001`.

Effort: 12 小时

**A16 Pricing Drift Reconciliation Algorithm [P1] [类型: 算法升级]**

基线-开源（代号引用）: Billing-Engine-Ref has fallback on dynamic evaluation failure; Obs-Ref appends wallet/ledger events and reconciles analytics. Limitation: snapshot correctness still needs an operator path for late authoritative usage or discovered pricing bug.

基线-官方（Vendor-X 代号）: Vendor-X1/Vendor-X2 Usage+Costs APIs can produce later authoritative costs. Vendor-X3/Vendor-X4 billing systems may lag real-time traffic.

HUAKAI 升级:

原算法:

```python
if authoritative_usage_late:
    update_usage_record(actual)
```

新算法:

```python
def reconcile_authoritative_cost(usage_record, authoritative):
    old = usage_record.actual_cost
    new = settle_cost(usage_record.price_snapshot, authoritative.usage_vector)
    delta = new - old
    if abs(delta) <= policy.small_delta_ignore:
        append_reconciliation_event(usage_record.id, delta, "ignored_small_delta")
        return
    append_adjustment_pair(
        original_usage_id=usage_record.id,
        debit_or_credit=delta,
        reason="authoritative_usage_delta",
        snapshot_version=usage_record.price_snapshot.version,
    )
```

复杂度对比: Constant per record; batch scan `O(N_pending)`. Space append-only adjustment rows.

数据结构变化: `billing_adjustments`, `reconciliation_events`, pending queue by `(tenant_id, pending_reconciliation, created_at)`.

为什么更强: Preserves immutable records while correcting money. Target: 0 mutable historical usage cost edits; all corrections auditable.

信号: `billing_adjustment_total`, `immutable_usage_update_attempt_total == 0`, reconciliation lag.

对应 F-* IDs: `F-OBS-001`, `F-BILL-SETTLE-FALLBACK-001`, `F-OBS-QUERY-001`.

Effort: 8 小时

**A17 Capacity Depletion Forecast [P0] [类型: 算法升级]**

基线-开源（代号引用）: Commercial-Pool-Ref has account health and queue/load signals. Operator-Tool-Ref exposes external account telemetry snapshots. Limitation: pool operations often react after quota/capacity is already exhausted.

基线-官方（Vendor-X 代号）: Vendor-X1/Vendor-X2 spend/rate-limit surfaces and Vendor-X3 provisioned/dynamic quota behavior require capacity planning. Vendor-X4 cross-region inference can shift available capacity by region.

HUAKAI 升级:

原算法:

```python
if quota_remaining <= threshold:
    alert("low quota")
```

新算法（公式）:

```text
burn_rate_t = EWMA(cost_per_minute, half_life=30m)
burst_rate_t = P95(cost_per_minute over last 24h same hour)
eta_empty =
  quota_remaining / max(burn_rate_t, burst_rate_t * burst_weight)
capacity_risk =
  sigmoid((policy.min_eta_minutes - eta_empty) / policy.eta_slope)
```

```python
def forecast_account(account):
    burn = ewma(account.cost_per_minute)
    burst = seasonal_p95(account.pool_id, account.model_family, hour_of_week(now()))
    eta = account.quota_remaining / max(burn, 0.6 * burst, epsilon)
    return CapacityForecast(eta_minutes=eta, risk=sigmoid((120 - eta) / 30))
```

复杂度对比: Periodic `O(A)` per interval. Space `O(A + pool*model*hour)` sketches.

数据结构变化: `capacity_forecasts`, time-bucket rollups, external telemetry snapshots, account risk field.

为什么更强: Moves from threshold alert to time-to-empty prediction. Target: low-capacity alert at least 60 minutes before pool exhaustion in 95% of replayed incidents.

信号: `capacity_eta_minutes`, `forecast_mape`, `pool_exhausted_without_prior_alert_total`.

对应 F-* IDs: `F-ACC-BALANCE-001`, `F-OPS-TELEMETRY-001`, `F-OBS-ROLLUP-001`, `F-ACC-SCHED-002`.

Effort: 12 小时

**A18 Replenishment Recommendation Optimizer [P1] [类型: 算法升级]**

基线-开源（代号引用）: Commercial-Pool-Ref has payment/recharge operations; Operator-Tool-Ref shows external account telemetry and duplicate/account management. Limitation: operators still need to decide which account/pool to replenish under budget pressure.

基线-官方（Vendor-X 代号）: Vendor-X1/Vendor-X2 usage costs and Vendor-X3 provisioned throughput imply replenishment choices have cost/capacity tradeoffs.

HUAKAI 升级:

原算法:

```python
if pool_low:
    recommend("add quota")
```

新算法（公式）:

```text
maximize sum_i risk_reduction_i(x_i)
subject to sum_i topup_cost_i(x_i) <= operator_budget
           x_i in allowed_topup_steps_i
           account_i not disabled / under dispute
```

```python
def recommend_restock(accounts, budget):
    items = []
    for a in accounts:
        for step in a.allowed_topup_steps:
            benefit = risk(a.forecast) - risk(project_forecast(a, step))
            items.append((a.id, step, benefit / step.cost, benefit, step.cost))
    return bounded_knapsack(items, budget, objective="max_benefit")
```

复杂度对比: Old none. New knapsack `O(items * budget_units)` or greedy `O(items log items)` when budget continuous. Space `O(items)`.

数据结构变化: Top-up step catalog, account eligibility, recommendation snapshot, accepted/dismissed feedback.

为什么更强: Converts telemetry into prioritized operator action. Target: reduce pool exhaustion incidents by 30% after operator follows recommendations.

信号: `restock_recommendation_accept_rate`, `post_restock_eta_delta`, incident correlation.

对应 F-* IDs: `F-PAY-TOPUP-001`, `F-OPS-TELEMETRY-001`, `F-ACC-BALANCE-001`.

Effort: 10 小时

**A19 Cross-Vendor Capacity Graph Min-Residual Routing [P0] [类型: 算法升级]**

基线-开源（代号引用）: Commercial-Pool-Ref focuses on account pool capacity; Multi-Provider-Ref supports lowest TPM/cost/latency routing; Declarative-Ref has backend weights/priority. Limitation: per-account selection does not optimize residual capacity across vendor/model/fault-domain graph.

基线-官方（Vendor-X 代号）: Vendor-X3 dynamic shared quota/provisioned throughput and Vendor-X4 cross-region profiles make capacity graph allocation a first-class routing problem. Vendor-Meta provider order/fallback confirms cross-provider planning.

HUAKAI 升级:

原算法:

```python
candidate = best_account_for_model(model)
```

新算法（公式）:

```text
Graph G = (Demand nodes D, Capacity nodes C, edges E)
edge(d,c) exists if account c can satisfy demand d under protocol/capability policy.
residual(c) = min(
  quota_remaining_cost(c),
  rpm_remaining(c) * avg_cost_per_req,
  tpm_remaining(c) * avg_cost_per_token,
  concurrency_remaining(c) * avg_cost_per_inflight
)
choose c maximizing min_residual_after_assignment:
  argmax_c min_{k in fault_domain(c)} residual_after(k)
```

```python
def graph_select(req, graph):
    demand = demand_node(req.model, req.protocol, req.capabilities)
    feasible = graph.neighbors(demand).filter(hard_gates_pass)
    return max(feasible, key=lambda c: min_residual_after(c, req.estimated_cost))
```

复杂度对比: Old `O(A)`. New `O(E_d)` per request where `E_d` is feasible edges for demand; graph rebuild periodic `O(E)`. Space `O(D + C + E)`.

数据结构变化: `capacity_graph_edges`, per-capacity-node residual vector, fault-domain tags `(vendor, region, account_group, provider_project)`.

为什么更强: Prevents draining a single vendor/fault domain while alternatives have spare capacity. Target: cross-fault-domain residual variance down 35%; failover recovery time from vendor degradation reduced from minutes to one routing interval.

信号: `capacity_residual_min_by_domain`, `domain_drain_skew`, `graph_route_reason`, chaos test disabling one vendor domain.

对应 F-* IDs: `F-ROUTE-001`, `F-ROUTE-002`, `F-ROUTER-HEALTH-001`, `F-EDGE-TOPOLOGY-001`.

Effort: 16 小时

**A20 Fault-Domain Spillover Guard [P1] [类型: 算法升级]**

基线-开源（代号引用）: Retry-Policy-Ref and Multi-Provider-Ref both support fallback. Limitation: fallback can unintentionally stay in the same vendor/project/region failure domain.

基线-官方（Vendor-X 代号）: Vendor-X4 cross-region inference profiles and Vendor-X3 project quotas require fault-domain-aware fallback.

HUAKAI 升级:

原算法:

```python
for account in ranked_accounts:
    if account != failed:
        try(account)
```

新算法:

```python
def spillover_candidates(failed_attempt, candidates):
    failed_domains = domains(failed_attempt.account)
    scored = []
    for c in candidates:
        overlap = len(domains(c) & failed_domains)
        distance = 1 - overlap / max(1, len(failed_domains))
        residual = normalized_residual(c)
        scored.append((c, 0.7 * distance + 0.3 * residual))
    return [c for c, s in sorted(scored, key=lambda x: -x[1]) if s >= policy.min_spillover_score]
```

复杂度对比: `O(A log A)` per fallback. Space fault-domain tags per account.

数据结构变化: Account fault-domain labels, fallback `domain_distance`, attempt row `previous_domain_overlap`.

为什么更强: Increases chance the next attempt escapes the actual outage. Target: repeated same-domain failure chain rate down 60%.

信号: `fallback_domain_distance_histogram`, `same_domain_retry_failure_total`.

对应 F-* IDs: `F-UPSTREAM-FALLBACK-001`, `F-ROUTER-FALLBACK-002`, `F-ARCH-001`.

Effort: 8 小时

**A21 Risk-Weighted Channel Probe Scheduler [P0] [类型: 算法升级]**

基线-开源（代号引用）: Commercial-Pool-Ref has safe channel monitors with bounded runner, in-flight guard, SSRF protection, history, and rollups. Legacy-Ref has bulk tests and auto-disable/enable. Limitation: fixed-interval probing wastes capacity on stable channels and under-probes risky channels.

基线-官方（Vendor-X 代号）: Vendor-X1/Vendor-X2/Vendor-X3 availability and rate-limit behavior can shift quickly; probes must not amplify incidents or look abusive.

HUAKAI 升级:

原算法:

```python
every monitor.interval:
    run_probe(channel)
```

新算法:

```python
def next_probe_delay(channel):
    risk = (
        0.35 * recent_error_rate(channel) +
        0.20 * latency_slo_violation(channel) +
        0.15 * capacity_risk(channel) +
        0.15 * credential_expiry_risk(channel) +
        0.15 * state_uncertainty(channel)
    )
    base = interpolate(policy.max_interval, policy.min_interval, risk)
    return add_jitter(base, key=channel.id, pct=0.20)

def probe_loop(channel):
    if in_flight_guard.exists(channel.id):
        return skip("already_running")
    if probe_budget.exhausted(channel.provider):
        return skip("provider_probe_budget")
    result = run_ssrf_safe_probe(channel)
    update_probe_state(channel, result)
    schedule_after(channel, next_probe_delay(channel))
```

复杂度对比: Old `O(C)` fixed schedule. New priority queue scheduler `O(log C)` per probe. Space `O(C)` next-run heap.

数据结构变化: Probe priority heap, per-provider probe budget, probe risk vector, history rollup.

为什么更强: More probes where signal is needed, fewer where stable. Target: detect degraded channels 2x faster at same or lower probe volume.

信号: `probe_detection_latency_ms`, `probe_volume_by_risk_bucket`, `probe_skip_reason_total`.

对应 F-* IDs: `F-CH-MON-002`, `F-CH-MON-004`, `F-CH-002`, `F-REQ-CUSTOM-HOST-001`.

Effort: 12 小时

**A22 Account/Channel Health Hysteresis State Machine [P0] [类型: 状态机升级]**

基线-开源（代号引用）: Commercial-Pool-Ref records overloaded/rate-limited/temp states; Legacy-Ref auto-disable/enable uses configured checks; Billing-Engine-Ref auto-disable respects normalized error classes. Limitation: immediate state flips create flapping.

基线-官方（Vendor-X 代号）: Vendor-X1/Vendor-X2 rate-limit resets and Vendor-X3 dynamic quotas can produce transient failures. Vendor-X4 regional issues may recover gradually.

HUAKAI 升级:

原算法:

```python
if probe_failed:
    state = "disabled"
elif probe_ok:
    state = "enabled"
```

新算法（状态转换）:

```text
normal
  -- 1 severe auth/quota class --> needs_manual_recovery | quota_exhausted
  -- k transient failures/window --> degraded
degraded
  -- k2 transient failures/window --> cooling_down
  -- m successes/window --> normal
cooling_down
  -- cooldown_until reached + clean probe --> normal
  -- severe class --> needs_manual_recovery
needs_refresh
  -- refresh success --> normal
  -- refresh attempts exhausted --> needs_manual_recovery
disabled
  -- admin enable + clean probe --> normal
```

```python
def transition(state, signal):
    score = state.score.decay(half_life=10m).add(weight(signal.class_))
    if signal.class_ in SEVERE_TERMINAL:
        return terminal_state(signal.class_)
    if score >= policy.cooldown_threshold:
        return "cooling_down"
    if score >= policy.degraded_threshold:
        return "degraded"
    if state in {"degraded", "cooling_down"} and clean_success_streak(signal.account) >= policy.recover_streak:
        return "normal"
    return state
```

复杂度对比: Constant per signal. Space adds decayed score and streak counters per account/channel.

数据结构变化: Unified `account_state`, transition log, decayed failure score, success streak, state version.

为什么更强: Reduces flapping and exposes deterministic state authority. Target: false auto-disable down 40%; repeated disabled/enabled flips within 10m near zero.

信号: `state_transition_total`, `state_flap_total`, `manual_recovery_required_total`.

对应 F-* IDs: `F-ACCAPI-STATE-001`, `F-RATE-001`, `F-ACC-AUTODISABLE-001`, `F-ACC-AUTOENABLE-001`.

Effort: 12 小时

**A23 Client Identity Priority Detector [P0] [类型: 算法升级]**

基线-开源（代号引用）: Clean-Arch-Ref surfaces six session-affinity extraction sources. Billing-Engine-Ref has configurable request affinity by headers/body/context. Limitation: using the first non-empty key can choose an unstable or spoofable identity over a stronger one.

基线-官方（Vendor-X 代号）: Vendor-X1/Vendor-X2 client request metadata and Vendor-Meta sticky routing require stable identity without exposing raw user/session identifiers.

HUAKAI 升级:

原算法:

```python
session_key = header("X-Session-ID") or body.metadata.user_id or hash(first_messages)
```

新算法:

```python
SIGNALS = [
  Signal("auth_key_binding", weight=100, spoof=False, ttl="key_life"),
  Signal("vendor_session_metadata", weight=80, spoof=True, ttl="24h"),
  Signal("stable_cli_header", weight=70, spoof=True, ttl="6h"),
  Signal("conversation_id", weight=65, spoof=True, ttl="6h"),
  Signal("client_request_id", weight=40, spoof=True, ttl="1h"),
  Signal("message_prefix_hash", weight=25, spoof=False, ttl="30m"),
]

def detect_client_identity(req):
    candidates = []
    for s in SIGNALS:
        value = extract_signal(req, s)
        if value:
            confidence = s.weight - spoof_penalty(s, req) - churn_penalty(s, req)
            candidates.append((s.name, hmac_hash(value), confidence, s.ttl))
    if not candidates:
        return Identity("anonymous", hash(req.api_key_id), confidence=10, ttl="5m")
    return max(candidates, key=lambda x: x.confidence)
```

复杂度对比: Constant over fixed signal list. Space identity cache `O(active_identities)`.

数据结构变化: Identity detection config/version, hashed identity cache, route reason `identity_signal_class` and confidence.

为什么更强: Stable, auditable, and privacy-preserving sticky identity. Target: sticky miss due to detector churn down 50%; raw identity leakage 0.

信号: `identity_signal_selected_total`, `identity_confidence_histogram`, sticky cache churn.

对应 F-* IDs: `F-SESSION-001`, `F-ROUTE-AFFINITY-001`, `F-ACCAPI-BIND-001`.

Effort: 10 小时

**A24 Identity Cache Drift Detector [P1] [类型: 数据结构升级]**

基线-开源（代号引用）: Billing-Engine-Ref exposes affinity cache inspection/clear; Operator-Tool-Ref emphasizes attempt histories and operator-visible failures. Limitation: sticky caches can silently become stale or over-broad.

基线-官方（Vendor-X 代号）: Vendor-Meta sticky routing makes cache invalidation a support concern; Vendor-X1/Vendor-X2 project/key boundaries require identity not crossing entitlement.

HUAKAI 升级:

原算法:

```python
sticky_cache[session_key] = account_id
```

新算法:

```python
def cache_put(identity, binding_id, account_id, ttl):
    key = (identity.hash, binding_id)
    sticky_cache[key] = {
        "account_id": account_id,
        "identity_confidence": identity.confidence,
        "capability_version": account_capability_version(account_id),
        "expires_at": now() + ttl,
    }

def cache_get(identity, binding_id, req):
    entry = sticky_cache.get((identity.hash, binding_id))
    if not entry:
        return MISS("no_entry")
    if entry.capability_version != account_capability_version(entry.account_id):
        return MISS("capability_drift")
    if identity.confidence < policy.min_confidence:
        return MISS("low_confidence")
    return HIT(entry.account_id)
```

复杂度对比: Constant. Space adds binding dimension and snapshot version.

数据结构变化: Sticky cache key `(identity_hash, binding_id)`, capability version, confidence, invalidation reason stats.

为什么更强: Prevents session stickiness from crossing key binding or stale model capability. Target: cross-binding sticky hit = 0.

信号: `sticky_cache_miss_reason_total`, `cross_binding_sticky_hit_total == 0`, admin clear-by-binding.

对应 F-* IDs: `F-ROUTE-AFFINITY-002`, `F-ACCAPI-CAP-SNAP-001`, `F-SESSION-001`.

Effort: 6 小时

**A25 Adaptive Stream Buffer Controller [P0] [类型: 算法升级]**

基线-开源（代号引用）: HUAKAI released streaming spec uses bounded scanner buffer and typed oversize failure. Billing-Engine-Ref and Obs-Ref show body buffering/retention thresholds. Limitation: one static buffer cap either wastes memory or rejects legitimate large tool/response events.

基线-官方（Vendor-X 代号）: Vendor-X1 Responses and Vendor-X2 Messages streams can have varying event sizes, tool deltas, reasoning, and cache metadata. Vendor-X3/Vendor-X4 responses can be region/provider-shaped.

HUAKAI 升级:

原算法:

```python
scanner.max_buffer = route.max_buffer_bytes  # e.g. 1 MiB
for event in stream:
    parse(event)
```

新算法:

```python
def buffer_limit(route, provider, model, event_class):
    hist = event_size_sketch(provider, model, event_class)
    p99 = hist.quantile(0.99) if hist.ready else route.default_buffer
    limit = clamp(
        4 * p99,
        route.min_buffer_bytes,
        route.max_buffer_bytes,
    )
    if memory_pressure() > 0.8:
        limit = max(route.min_buffer_bytes, limit // 2)
    return limit

def process_event(event):
    cls = classify_event_prefix(event)
    limit = buffer_limit(route, provider, model, cls)
    if len(event) > limit:
        return terminal("RESPONSE_EVENT_TOO_LARGE", limit=limit)
    update_event_size_sketch(cls, len(event))
    return parse_and_forward(event)
```

复杂度对比: Old constant. New constant plus quantile sketch update `O(log k)` or `O(1)` depending sketch. Space `O(provider*model*event_class)`.

数据结构变化: Event-size sketches, memory pressure signal, per-route min/max buffer.

为什么更强: Keeps memory bounded while adapting to legitimate provider event sizes. Target: oversize false positives down 50%; peak stream memory down 20% under small-event workloads.

信号: `stream_buffer_limit_bytes`, `stream_event_oversize_total`, memory pressure vs buffer limit.

对应 F-* IDs: `F-GW-002`, `F-REQ-BODY-002`, `F-RETENTION-001`.

Effort: 10 小时

**A26 Expected-Value Drain Decision [P0] [类型: 算法升级]**

基线-开源（代号引用）: HUAKAI released streaming spec drains after client disconnect under time/bytes/cost budgets. Obs-Ref shows billing recovery exception behavior for body storage. Limitation: fixed drain budgets do not decide whether draining is worth the expected billing/forensic value for each stream.

基线-官方（Vendor-X 代号）: Vendor-X1/Vendor-X2 streams can report late usage; prompt caching/reasoning usage may arrive near terminal frames.

HUAKAI 升级:

原算法:

```python
while time < max_time and bytes < max_bytes and est_cost < max_cost:
    drain_next_event()
```

新算法（公式）:

```text
continue_drain if:
  E[value_remaining] > E[cost_to_drain] + risk_penalty

E[value_remaining] =
  P(terminal_usage_frame_remaining | stream_state, provider, elapsed, events_seen)
  * expected_settlement_error_if_stop
  + forensic_value_weight * incident_probability

E[cost_to_drain] =
  expected_tokens_remaining * unit_cost + network_cpu_cost + memory_pressure_penalty
```

```python
def should_continue_drain(state):
    if any_budget_exhausted(state):
        return False
    value = p_usage_frame_remaining(state) * settlement_error_if_stop(state)
    value += forensic_weight(state) * incident_probability(state.account)
    cost = expected_remaining_cost(state) + memory_pressure_penalty()
    return value > cost
```

复杂度对比: Constant per drained event. Space provider/model drain priors.

数据结构变化: Drain priors by provider/model/end_class; `drain_decision_reason`, `expected_value`, `expected_cost`.

为什么更强: Avoids paying to drain low-value streams while preserving billing correctness where late usage is likely. Target: drain bytes down 25% with no increase in ambiguous usage; ambiguous usage stays near zero.

信号: `drain_decision_total{reason}`, `ambiguous_usage_total`, `drain_cost_saved_estimate`, AT-GW-002-09/10/18.

对应 F-* IDs: `F-GW-002`, `F-OBS-001`, `F-BILL-SESSION-001`.

Effort: 10 小时

## 2. Priority Rollup

| Priority | A IDs | Why |
| --- | --- | --- |
| P0 | A01, A02, A04, A06, A07, A09, A11, A12, A13, A15, A17, A19, A21, A22, A23, A25, A26 | These protect the Account-to-API spine, money correctness, retry safety, account health, or streaming settlement. They should land before real paid public traffic. |
| P1 | A03, A05, A08, A10, A14, A16, A18, A20, A24 | These materially improve operability and efficiency, but can follow once the P0 invariants are stable. |
| P2 | None in this plan | Owner asked for algorithm upgrade, not backlog padding. Items not strong enough for P0/P1 were omitted instead of inflated. |

| Direction | Covered By |
| --- | --- |
| 1. 调度算法 | A01, A02, A03 |
| 2. Sticky session 迁移决策 | A04, A05 |
| 3. 凭据 lease + refresh storm | A06, A07, A08 |
| 4. 二阶段 quota reserve + settle | A09, A10 |
| 5. 多 attempt DAG 规划 | A11, A12 |
| 6. 错误归一化 + retry 决策表 | A13, A14 |
| 7. Versioned pricing snapshot | A15, A16 |
| 8. Capacity forecast + 补货推荐 | A17, A18 |
| 9. 跨 vendor capacity graph min-residual | A19, A20 |
| 10. Channel monitor probe + state 自动转换 | A21, A22 |
| 11. 客户端身份探测器 priority 决策 | A23, A24 |
| 12. Stream forwarder adaptive buffer + drain | A25, A26 |

Total estimated effort: **272 小时**.

## 3. Open Questions

1. **State transition authority**: For `F-ACCAPI-STATE-001`, should system transitions to `needs_manual_recovery` require operator confirmation for any provider class, or only for ambiguous/unknown classes?
2. **Billing attribution policy**: For multi-attempt requests, should HUAKAI default to `succeeded_on`, `dollar_weighted`, or `first_tried` attribution? Recommended default: `succeeded_on` for customer billing, `dollar_weighted` for internal account cost analytics.
3. **Model substitution policy**: A11 can include model-substitution edges, but this changes user-visible model semantics. Recommended default: disabled unless route policy explicitly allows safe substitution and response metadata flags it.
4. **Quantile reserve risk appetite**: A10 needs Owner policy for acceptable one-request overdraft. Recommended default: zero overdraft for subscription-only, bounded overdraft for trusted wallet/credit-line users.
5. **Body/drain privacy boundary**: A26 can improve billing recovery, but only if body/stream metadata retention policy is settled. Default prompt body logging should remain off.
6. **Capacity graph scope**: A19 can start inside one tenant/personal deployment, but SaaS Edition needs cross-tenant isolation. The graph must never optimize one tenant's demand using another tenant's private account capacity.

## 4. One-Line Summary

HUAKAI's algorithmic edge should be: **binding-first account selection, lease-visible credential use, attempt-DAG retry, immutable price/settlement math, forecasted capacity, fault-domain-aware spillover, hysteresis health, confidence-scored identity, and adaptive streaming settlement**.
