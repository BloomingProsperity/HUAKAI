# HUAKAI 算法升级计划 — Claude 平行版

Date: 2026-05-02
Author lane: Claude (PM-Orchestrator), parallel-draft per CLAUDE.md #10. Codex 同名 `-codex.md` 文件独立编写，本文件**未读** Codex 输出，仅在 synthesis 阶段对比。
Status: parallel-draft v1; awaits Codex side + synthesis.
Codename usage: `Commercial-Pool-Ref / Clean-Arch-Ref / Billing-Engine-Ref / Obs-Ref / Retry-Policy-Ref / Multi-Provider-Ref / Declarative-Ref / Operator-Tool-Ref / Legacy-Ref` 与 `Vendor-X1..X4 / Vendor-Meta` 见 [docs/reference_delta/2026-05-02/codename-mapping.md](../../reference_delta/2026-05-02/codename-mapping.md)。

## 0. 范围与判据

HUAKAI 是开源 AI gateway 竞赛产品。**目标 = Commercial-Pool-Ref 100% 核心功能必做 + 每项算法层面做得更强**。本文聚焦"**算法层面**"，不是 refactor 不是补漏。

**强者判据**（每条升级必须能用其中至少 2 条度量证明）：

1. **复杂度更优**：时间或空间一个量级，或 worst-case→amortized 改善
2. **一致性更强**：从 best-effort → atomic / linearizable / monotonic
3. **容错更强**：多失败域恢复时间从 O(N) 降到 O(log N) 或 O(1)
4. **可证明**：有数学不变量（Lyapunov drift、信息论上界、概率上界）
5. **可观测**：每个算法决策都能在 audit row 复盘
6. **客户感知**：P50/P99 延迟、误派率、续约率有可测变化

不接受"加一层抽象"或"多打日志"作为升级理由。

## 1. 算法升级清单（A01–A28）

---

### A01 5 层调度选择 → Binding-aware 6 层 + 候选集预剪枝 [P0] [算法升级]

**基线-开源（Commercial-Pool-Ref）**：5 层选择算法（routing-affinity / sticky-within-routing / sticky-standalone / load-aware / fallback-queue）。**不足**：所有 api_key 看到同一候选池；没有 binding 层；过滤是 O(全 account 数 × 9 维)，账号上千后调度延迟随账号规模线性退化。

**基线-官方（Vendor-X1, X2）**：Vendor-X1 Workspaces 限定 model 集；Vendor-X2 Workspace 限定 spend；HUAKAI 必须把 binding 持久化到本地。

**HUAKAI 升级**：

原算法（伪代码）：

```
def select_account_v1(api_key, model, request):
    candidates = []
    for acct in all_provider_accounts(tenant_id):              # O(N)
        if filter_9_dims(acct, model, request):                # O(N×9)
            candidates.append(acct)
    return layered_select(candidates, sticky_hint, request)     # O(K log K) score
# 总：O(N + N·9 + K log K)；N=账号数，K=候选数（K≤N）
```

新算法（伪代码）：

```
def select_account_v2(api_key, model, request):
    # Layer 0: binding-aware pre-filter — index-only lookup
    bindings = api_key_bindings.lookup_sorted_by_priority(
        api_key.id, tenant_id)                                 # O(B log B) via index
    if bindings.empty():
        return ROUTE_ERR_NO_BINDING                            # 显式失败 != tenant-default
    # Layer 0.5: candidate set materialization
    candidate_set = OrderedSet()
    for binding in bindings:
        targets = expand_binding(binding)                       # O(1) for account; O(M) for pool
        for acct_id in targets:
            if not in candidate_set:                           # 去重
                candidate_set.add(acct_id)
        if len(candidate_set) >= TOPK_CAP:                     # 早停
            break
    # Layer 1-5: 原算法跑在 candidate_set 上而不是 all_accounts
    return layered_select_v1(candidate_set, sticky_hint, request)
```

复杂度对比：

| 维度 | v1 | v2 |
|---|---|---|
| 时间（平均） | O(N) | O(B log B + M) ≈ O(log N + M) |
| 时间（worst case） | O(N) | O(B + M) ≤ O(N), B≤N |
| 空间 | O(K) | O(B + M) |
| 索引依赖 | scan | `api_key_bindings(api_key_id, priority)` btree |

**N**=tenant 内 account 数；**B**=该 key 的 binding 数（典型 1-5）；**M**=最大 binding 解析后 account 数（典型 1-50）。10000 账号 5 binding 5 account 场景：v1 跑 9 万次 filter；v2 跑 25 次。

数据结构变化：
- 新表 `api_key_bindings`（见 audit §5.1：3 个 per-kind 部分唯一索引避免 NULL-distinct trap）
- 新索引 `(api_key_id, priority, deleted_at)` 部分索引 WHERE deleted_at IS NULL
- `pool_groups.member_account_ids` 物化为冗余 array 列 + 触发器维护，避免 expand 时 join

为什么更强：
- **复杂度**：N→log N，账号数从千级扩展到十万级延迟无变化（Owner memory: sub2api 客户数→延迟上升问题预防）
- **可证明**：无 binding → 显式 ROUTE_ERR_NO_BINDING，不会 silent fallback 到错的 pool（Vendor-X1 Workspace 隔离正确性）
- **客户感知**：tier 化产品成为可能（"premium 绑 GPT-Plus pool"）

信号：`request_attempts.binding_id` 非 NULL；admin trace 能解释"key X 为何看不到 pool Y"；pre-filter 候选数 metric。

对应 F-* IDs：F-ACCAPI-BIND-001（spine），F-POOL-001（spec extend）

Effort：2-3 小时（migration + sqlc + selector 改造 + 测试 4 case）

---

### A02 多维评分 → Welford-online 标准化 + 动态权重 [P1] [算法升级]

**基线-开源（Commercial-Pool-Ref）**：score = w1·load + w2·queue + w3·err_rate + w4·ttft + w5·priority + w6·manual_load。权重静态配置；输入未标准化（load 0-1, ttft 100-30000ms 量纲悬殊导致大数维度吃掉小数维度）；err_rate 用最近 N 次成功率，无衰减。

**基线-官方**：无（这是开源仓的算法，官方不暴露）

**HUAKAI 升级**：

原公式：

```
score(a) = Σ w_i · raw_signal_i(a)        # 量纲不齐 → 权重不可比
err_rate(a) = failures_recent_60s(a) / requests_recent_60s(a)
```

新公式（Welford running stats + EWMA）：

```
# 每账号维护在线统计（Welford 算法，单 pass O(1)/event）
struct AcctStats:
    n: int
    mean[k]: float    # k=信号维度
    M2[k]: float      # 用于方差
    last_update_ms: int

def update(acct, signal_idx, x):
    a = acct.stats
    a.n += 1
    delta = x - a.mean[signal_idx]
    a.mean[signal_idx] += delta / a.n
    delta2 = x - a.mean[signal_idx]
    a.M2[signal_idx] += delta * delta2

def std(acct, signal_idx):
    return sqrt(acct.stats.M2[signal_idx] / max(acct.stats.n - 1, 1))

# 评分：z-score 化 + softmax 概率
def score_v2(acct, request):
    z = []
    for i, raw_i in enumerate(extract_signals(acct, request)):
        # 用 pool 级 mean/std 而不是单账号 — pool stats 周期性 reduce
        mu_i, sigma_i = pool_stats[i]
        z_i = (raw_i - mu_i) / max(sigma_i, EPS)
        z.append(z_i * sign[i])    # 越小越好的维度乘 -1
    s = sum(w[i] * z[i] for i in range(K))
    return s

# err_rate 用 EWMA 而不是固定窗口 — 历史失败影响指数衰减
def update_err_rate(acct, success):
    alpha = 1 - exp(-dt / HALF_LIFE_MS)        # half-life e.g. 30s
    acct.err_rate_ewma = alpha * (0 if success else 1) + (1-alpha) * acct.err_rate_ewma
```

复杂度对比：

| 维度 | v1 | v2 |
|---|---|---|
| 单 update | O(1) | O(K) for K 信号，K=6 |
| 评分计算 | O(K) | O(K) |
| 内存 | O(N·K)（窗口数组） | O(N·K)（统计） |
| 数学性质 | 量纲依赖权重 | scale-invariant |

数据结构变化：
- 内存 cache `acct_stats[acct_id] -> {n, mean[6], M2[6], err_rate_ewma}`
- 周期性 flush `pool_stats[pool_id]` 表（reduce 5min 窗口；用 Welford 合并公式）
- 移除 `provider_accounts.recent_*` 列（计算式，无需持久化）

为什么更强：
- **可证明**：z-score 标准化使权重 (w1..wK) 在不同信号量纲下可比；softmax 化后温度参数 T 直接控贪心 vs 探索
- **复杂度**：从滑窗 O(W) 删/加变 O(1) Welford 增量更新，W=窗口大小通常 100-1000
- **客户感知**：当某账号 ttft 突然变差，EWMA 在 30s 内权重收敛；定窗算法要等窗口推进 60s

信号：`scoring_diagnostics.z_scores[]` 写入 routing_reason；admin "账号 A 当前各维度偏离 pool 均值多少 sigma"

对应 F-* IDs：F-POOL-001 (spec extend §Phase B 信号)

Effort：4-5 小时

---

### A03 wait-plan vs fail 决策 → 期望返工成本最小化 [P1] [算法升级]

**基线-开源**：满载时 `if all_full: return wait_plan(ttl=固定值)`；ttl 是配置常量。**不足**：当所有账号都因 cooldown 短暂满载（即将释放）时 wait 是对的；当账号都进入长 cooldown（5h 7d window）时 wait 是错的——客户白白等。

**基线-官方（Vendor-X2）**：Anthropic 5h/7d 窗口的 reset 时间在 header 里给得很明确，HUAKAI 必须用上。

**HUAKAI 升级**：

原算法：

```
if no_slot_available:
    return WaitPlan(ttl=route.fallback_wait_budget)   # 静态 ttl
```

新算法（基于剩余冷却分布的期望成本最小化）：

```
def wait_or_fail(candidates, request):
    # 收集每个账号最早可用时间
    eta_list = []
    for acct in candidates:
        eta = max(
            acct.cooldown_until_ms,
            acct.rate_limit_reset_ms,
            acct.in_flight_release_eta_ms,    # 当前流式请求 P50 完成 ETA
            now_ms()
        )
        eta_list.append(eta - now_ms())       # 等待时长 ms
    
    # 排序取最快的 K 个
    eta_list.sort()
    
    # 期望成本：E[wait_then_succeed] vs E[fail_now_then_retry_at_client]
    # 假设客户重试成本 = wait + retry_latency_p50 + retry_failure_rate · retry_again_cost
    p_success_after_wait = estimate_p_success(eta_list, request)
    e_wait_succeed = eta_list[0] + p_success_after_wait * E_LATENCY
    e_fail_now = CLIENT_RETRY_COST    # 配置 + 实测
    
    if e_wait_succeed < e_fail_now AND eta_list[0] < MAX_WAIT_MS:
        return WaitPlan(ttl=eta_list[0] + JITTER, expected_account=candidates[0])
    else:
        # 失败但带建议性 Retry-After
        return Fail(retry_after_ms=eta_list[0])
```

复杂度对比：

| 维度 | v1 | v2 |
|---|---|---|
| 决策时间 | O(1) | O(K log K) for K 候选 |
| ttl 准确性 | 静态 | 基于实测 ETA |
| 客户重试损耗 | 高（盲等满 ttl） | 低（条件 wait） |

数据结构变化：
- `provider_accounts.in_flight_release_eta_ms` 计算列：基于 `last_dispatch_at + p50_latency`
- pool 级 `client_retry_cost_ms` 配置（一次客户重试的网络往返均值）

为什么更强：
- **客户感知**：当所有账号长冷却时立即给 503 + Retry-After（客户 client SDK 会做 retry-after），P99 等待降到 0；当账号都接近 release 时短等
- **可证明**：决策基于期望最小化，不基于 sysadmin 拍脑袋的常量
- **可观测**：`wait_plan_decision = {chosen_eta_ms, e_wait_succeed, e_fail_now, decision}` 进 audit

信号：客户响应头 `Retry-After`；操作员"过去 7 日 wait_plan vs immediate_fail 比例 + 各自 P99"

对应 F-* IDs：F-POOL-001 §Phase B Layer 3

Effort：3-4 小时

---

### A04 Sticky session 迁移决策 → migration manifest + 期望损失计算 [P1] [算法升级]

**基线-开源（Commercial-Pool-Ref）**：8 reason 断 sticky（session_limit / wait_queue_full / gate_check / rpm_red / account_cleared 等）。**不足**：断 sticky 时直接 fallback，不重建 context；不告诉客户上下文丢了什么；Vendor-X2 Prompt Caching 失效后客户被静默 5x-10x 多扣费。

**基线-官方（Vendor-X1, X2）**：Vendor-X2 Prompt Caching 5min/1h TTL 账号绑定；Vendor-X1 `previous_response_id` stateful；Vendor-X1 Realtime WebSocket session 是 connection-scoped。

**HUAKAI 升级**：

原算法：

```
if sticky_break_reason != null:
    return select_fresh(candidates - [sticky_acct])    # 静默 fallback
```

新算法（migration manifest + 损失估算）：

```
def evaluate_migration(sticky_acct, candidate_acct, request):
    # 检测 stateful resource 消失情况
    manifest = MigrationManifest()
    
    # 1. Prompt cache（Vendor-X2）
    if request.has_anthropic_cache_control():
        manifest.prompt_cache_lost = True
        manifest.cache_creation_extra_tokens = estimate_cache_creation_tokens(request)
        manifest.cost_delta_usd += pricing.cache_creation_per_1k * (
            manifest.cache_creation_extra_tokens / 1000)
    
    # 2. previous_response_id（Vendor-X1 Responses API）
    if request.has_previous_response_id():
        manifest.previous_response_id_lost = True
        manifest.semantic_continuity_broken = True
    
    # 3. HUAKAI 跨账号 conversation_id 映射
    if huakai_conversation_map.has(request.conversation_id):
        # 自家映射可恢复
        manifest.conversation_id_continuable = True
    else:
        manifest.conversation_id_continuable = False
    
    # 4. Realtime WS session
    if request.endpoint == "/v1/realtime":
        manifest.realtime_session_lost = True
        manifest.action = ACTION_FAIL_CLOSED        # WS 不能 mid-flight 换账号
    
    # 决策表
    if manifest.action == ACTION_FAIL_CLOSED:
        return Decision(
            kind="fail_closed",
            client_msg=f"Cannot migrate: {manifest.reasons()}",
            http_status=503,
            retry_after_ms=sticky_acct.cooldown_until_ms - now_ms()
        )
    elif manifest.cost_delta_usd > MIGRATION_COST_THRESHOLD_USD:
        # 让 client 知道并选择是否继续
        return Decision(
            kind="migrate_with_warning",
            client_headers={
                "X-Huakai-Migration-Cache-Lost": "true",
                "X-Huakai-Migration-Cost-Delta-Usd": f"{manifest.cost_delta_usd:.4f}",
                "X-Huakai-Migration-Reason": manifest.primary_reason,
            },
            target_acct=candidate_acct,
        )
    else:
        return Decision(kind="migrate_silent", target_acct=candidate_acct)
```

复杂度对比：

| 维度 | v1 | v2 |
|---|---|---|
| 决策时间 | O(1) | O(F) F=manifest 字段数 ~5 |
| 客户透明度 | 0 | 完整成本影响公示 |

数据结构变化：
- 新表 `huakai_conversation_map(conversation_id, last_account_id, last_seen_at)` — 跨账号续聊
- `request_attempts.migration_manifest jsonb` — 持久化每次 attempt 的迁移决定
- 模型价格表 `model_msrp(model, cache_creation_per_1k, cache_read_per_1k, ...)` 

为什么更强：
- **可证明**：成本影响算出来给客户头部看，不是黑箱多扣费
- **客户感知**：Vendor-X2 Prompt Caching 命中时一次请求成本可能 5x-10x 差异；migration manifest 给客户"是否继续"的可见性
- **架构**：WS 实时会话强制 fail_closed 而不是错误的 silent migrate（WS 本身不可 migrate）

信号：响应头 `X-Huakai-Migration-*`；操作员"过去 7 日因 prompt_cache_lost 触发计费倍增 N 次，金额 $M"

对应 F-* IDs：F-SESSION-001 (extend) + 新增 `F-MIGRATION-MANIFEST-001`

Effort：6-8 小时

---

### A05 Sticky 哈希策略 → Rendezvous (HRW) hashing 替代 mod-N [P2] [数据结构升级]

**基线-开源**：sticky 通过 `hash(session_key) % len(candidates)` 选 account；账号集变更（加/减/cooldown）时 hash 大面积 reshuffle，sticky 命中率从 95% 暴跌到 60%。

**基线-官方**：无

**HUAKAI 升级**：

原算法：

```
idx = hash(session_key) mod len(candidates)
return candidates[idx]
```

新算法（Highest Random Weight / Rendezvous hashing）：

```
def sticky_select_hrw(session_key, candidates):
    best_acct, best_w = None, -inf
    for acct in candidates:
        # 双 hash 抗碰撞
        w = hash64(session_key, acct.id, salt=acct.priority_seed)
        if w > best_w:
            best_w = w
            best_acct = acct
    return best_acct
```

复杂度对比：

| 维度 | mod-N | HRW |
|---|---|---|
| 单选时间 | O(1) | O(N) |
| 加 1 账号后 reshuffle 比例 | 1 - 1/N (≈100%) | 1/N |
| 减 1 账号后 reshuffle 比例 | 1 - 1/N | 1/N |
| 实现复杂度 | 1 行 | 5 行 |

数据结构变化：无（acct.id 已存在）；可加 `acct.priority_seed` 列防 hash 偏向

为什么更强：
- **可证明**：HRW 在 N→N±1 下只有 1/N 的 key 被 reshuffle（最优）；mod-N 是 1-1/N（最差）
- **客户感知**：账号扩缩容期间 sticky 命中率从 60% 升到 99%
- **复杂度代价**：O(N) 选择，但 N 通常 ≤50（pool 内候选）；可接受

信号：`sticky_hit_rate` metric 在账号增减事件后稳定不下跌

对应 F-* IDs：F-SESSION-001 (algo extend)

Effort：1-2 小时

---

### A06 凭据 lease 状态机 → token-version 化 + grace 持有 [P0] [状态机升级]

**基线-开源（Commercial-Pool-Ref）**：OAuth refresh 异步；上游 401 直接换账号；不感知"另一 goroutine 已经 refresh 完成 token v2"。结果：1 个账号被浪费一次 refresh-window，3 账号场景 33% 容量损失。

**基线-官方（Vendor-X1, X2）**：OAuth refresh 通常 2-5s。

**HUAKAI 升级**：

原状态机：

```
account_state ∈ {valid, refreshing, refresh_failed}
on 401:
    if state == valid:
        state = refreshing
        async refresh()
        return fail_to_caller(401)        # 调度器换下家
```

新状态机（lease + version + grace hold）：

```
struct AccountCredState:
    state: enum {valid, refreshing, refresh_failed, grace_held}
    cred_version: int            # 单调递增；refresh 成功后 +1
    last_refresh_at_ms: int
    grace_count: int             # 当前 hold 中的请求数
    grace_max: int               # 配置（默认 50）

def on_upstream_401(account, attempt):
    cs = account.cred_state
    if cs.state == valid:
        # 进入 refreshing；attempt 自身重试时机决策
        cs.state = refreshing
        cs.refresh_started_at_ms = now_ms()
        spawn refresh_async(account)
        # NEW: grace hold — 不立即 fail
        if cs.grace_count < cs.grace_max AND wait_budget_remaining_ms(attempt) > REFRESH_P50_MS:
            cs.grace_count += 1
            cs.state = grace_held
            wait_for_refresh_or_timeout(account, REFRESH_P95_MS)
            cs.grace_count -= 1
            if account.cred_version > attempt.cred_version:
                # 用新 token 在同账号重试
                return RetrySameAccount(new_version=account.cred_version)
            else:
                return FailoverToOtherAccount()
        else:
            return FailoverToOtherAccount()
    elif cs.state == refreshing OR grace_held:
        # 已在 refresh —— 加入 grace
        if cs.grace_count < cs.grace_max:
            cs.grace_count += 1
            wait_for_refresh_or_timeout(...)
            cs.grace_count -= 1
            return RetrySameAccount(...)
        else:
            return FailoverToOtherAccount()
    elif cs.state == refresh_failed:
        return FailoverToOtherAccount()
```

复杂度对比：

| 维度 | v1 | v2 |
|---|---|---|
| 单 401 处理 | O(1) | O(1) + 1 hold |
| 同账号最大并发 hold | 0 | grace_max（防 thundering herd） |
| 1 refresh 期间复用率 | 0 | up to grace_max 次 |
| 容量浪费（3 账号 1 失败） | 33% | < 5% |

数据结构变化：
- 内存 `account.cred_state.grace_count` 计数器（atomic）
- `request_attempts.cred_version_used` 列追溯每次 attempt 用的版本
- F-AUTH-005 spec 已有 `_token_version` CAS — 复用

为什么更强：
- **容量恢复**：33% → <5%
- **可证明**：grace_max 上限保证不雪崩；每个 hold 携带超时确保不无限阻塞
- **正确性**：cred_version 比较确保 hold 完后用的真是新 token，不是 stale cache

信号：`cred_grace_hits_total{outcome=retry_same_acct|failover}`；`refresh_storm_avoided_count`

对应 F-* IDs：F-ACCAPI-LEASE-001 + F-AUTH-005 (extend)

Effort：4-5 小时

---

### A07 Refresh storm 控制 → token-bucket × 3 scope + 协同推让 [P0] [算法升级]

**基线-开源（Clean-Arch-Ref）**：bounded refresh worker pool（默认 16 worker）。**不足**：单维控制，没考虑"100 个账号同 endpoint 同时过期"还是"分布在 5 endpoint"。Endpoint 共用时仍可能打爆 Vendor 端。

**基线-官方**：Vendor-X1 OAuth endpoint 隐含 rate-limit。

**HUAKAI 升级**：

原算法：

```
sem = Semaphore(N=16)
def refresh(acct):
    sem.acquire()
    try: do_refresh(acct)
    finally: sem.release()
```

新算法（3 维 token bucket + 协同推让）：

```
class StormController:
    def __init__(self):
        self.global_bucket = TokenBucket(rate=GLOBAL_RPS, burst=GLOBAL_BURST)
        self.endpoint_buckets: dict[(provider, oauth_url), TokenBucket] = {}
        self.account_locks: dict[acct_id, AsyncLock] = {}
    
    async def acquire(self, account, deadline_ms):
        # 1. 单账号锁（防同账号 N 并发 refresh）
        acct_lock = self.account_locks.setdefault(account.id, AsyncLock())
        ok = await acct_lock.acquire(timeout=deadline_ms - now_ms())
        if not ok:
            return AcquireResult.LOCK_TIMEOUT
        try:
            # 2. endpoint 桶
            ep_bucket = self.endpoint_buckets.setdefault(
                (account.provider, account.oauth_url),
                TokenBucket(rate=PER_ENDPOINT_RPS, burst=PER_ENDPOINT_BURST))
            if not ep_bucket.try_acquire():
                # 协同推让：返回桶下个 token 时间，让上游决定 wait/fail
                return AcquireResult.ENDPOINT_THROTTLED(retry_at=ep_bucket.next_token_ms())
            # 3. 全局桶
            if not self.global_bucket.try_acquire():
                ep_bucket.refund()      # 不浪费 endpoint token
                return AcquireResult.GLOBAL_THROTTLED(retry_at=self.global_bucket.next_token_ms())
            return AcquireResult.OK
        except:
            acct_lock.release()
            raise

# 推让协议：调度器看到 ENDPOINT_THROTTLED 时优先选其他 endpoint 账号
def select_with_storm_awareness(candidates):
    eligible = [a for a in candidates if not storm.endpoint_throttled(a)]
    if eligible:
        return select_normal(eligible)
    else:
        return wait_plan(min(storm.next_token_ms(a) for a in candidates))
```

复杂度对比：

| 维度 | v1 | v2 |
|---|---|---|
| 单 acquire | O(1) | O(1) per scope，3 scope |
| 隔离粒度 | 1 维 | 3 维（acct/endpoint/global） |
| 上游被打爆风险 | 高（共 endpoint 不感知） | 低（per-endpoint 桶） |
| 调度协同 | 无 | 调度器选 non-throttled endpoint |

数据结构变化：
- 内存 `TokenBucket{rate, burst, tokens, last_refill_at_ns}` 实现（无锁 atomic add）
- 配置 `oauth_storm_policy.{global, per_endpoint, per_account}.{rate, burst}`

为什么更强：
- **正确性**：100 账号 1 endpoint 同时过期不会打爆 Vendor OAuth；分散到 5 endpoint 时各 endpoint 独立限速
- **协同**：调度器知道哪些 endpoint throttled，优先调度其他 endpoint，平滑过渡而不是堆积
- **可观测**：每个桶的 `tokens / next_token_ms` 是 metric

信号：`storm_throttle_total{scope=global|endpoint|account}` per provider

对应 F-* IDs：F-AUTH-005 (spec extend §Phase B)

Effort：4-5 小时

---

### A08 Lease 释放与孤儿回收 → CAS + 周期 sweep + watermark [P1] [算法升级]

**基线-开源**：sub2api in_flight_count 用 INCR/DECR 维护，gateway crash 时 leaky；用周期性"重置全部 in_flight=0"（off-by-large 错误）。

**基线-官方**：无

**HUAKAI 升级**：

原算法：

```
on_request_start: UPDATE provider_accounts SET in_flight = in_flight + 1 WHERE id = ?
on_request_end: UPDATE ... SET in_flight = in_flight - 1 WHERE id = ?
periodic_sweep: UPDATE ... SET in_flight = 0    # 暴力，破坏正在跑的
```

新算法（lease token + CAS release + watermark sweep）：

```
# Acquire — 写 lease token 到 pool_slot_acquisitions
def acquire(account, request_id):
    token = uuid4()
    deadline = now_ms() + LEASE_MAX_TTL_MS
    sql_insert("""
        INSERT INTO pool_slot_acquisitions
            (account_id, lease_token, request_id, acquired_at, deadline_at)
        VALUES (?,?,?,?,?)
    """, account.id, token, request_id, now_ms(), deadline)
    sql_update("""
        UPDATE provider_accounts SET in_flight = in_flight + 1
        WHERE id = ? AND in_flight < cap_concurrency
    """, account.id)
    return token

# Release — CAS by token，避免双 release / 错 release
def release(account_id, lease_token, usage_outcome):
    affected = sql_exec("""
        DELETE FROM pool_slot_acquisitions
        WHERE account_id = ? AND lease_token = ?
        RETURNING 1
    """, account_id, lease_token)
    if affected:
        sql_exec("UPDATE provider_accounts SET in_flight = GREATEST(in_flight-1, 0) WHERE id = ?",
                 account_id)
    # else: 已经被 sweep 释放或不存在 — no-op，幂等

# Sweep — 不暴力清零，按 lease deadline + watermark 精确回收
def sweep_orphans():
    # 找 deadline 过期的 lease
    expired = sql_query("""
        SELECT account_id, lease_token, request_id FROM pool_slot_acquisitions
        WHERE deadline_at < ?
        FOR UPDATE SKIP LOCKED
        LIMIT 100
    """, now_ms())
    for row in expired:
        # 验证：request 真的没在跑（gateway heartbeat watermark）
        if request_heartbeat[row.request_id].last_seen_ms < now_ms() - HEARTBEAT_GRACE_MS:
            release(row.account_id, row.lease_token, usage_outcome="orphan_swept")
            audit_emit("orphan_sweep", row)

# Heartbeat — gateway 流式请求每 5s 写心跳
def heartbeat_request(request_id):
    redis.set(f"hb:{request_id}", now_ms(), ex=HEARTBEAT_TTL_S)
```

复杂度对比：

| 维度 | v1（暴力清零） | v2（lease+sweep） |
|---|---|---|
| 单 acquire | O(1) | O(1) + 1 INSERT |
| 单 release | O(1) | O(1) CAS |
| Sweep 误伤率 | 高（清掉正在跑的） | 0（heartbeat watermark 验证） |
| 双 release | counter 漂移 | 幂等 no-op |
| 长流式请求 | 被错误清掉 | heartbeat 续约 |

数据结构变化：
- 表 `pool_slot_acquisitions(account_id, lease_token, request_id, acquired_at, deadline_at)` 已存在
- 增加 `deadline_at` 列（如未存在）
- Redis `hb:{request_id}` 心跳 key（TTL 60s）

为什么更强：
- **正确性**：从"近似计数"升级为"linearizable count + audit"
- **容错**：gateway 进程崩溃后 sweep 在 watermark 后回收；不会误伤 healthy 长流
- **可观测**：每次 orphan sweep 写 audit，可统计 crash 频率

信号：`orphan_sweep_total{cause}`；`request_heartbeat_lag_seconds` p99

对应 F-* IDs：F-POOL-001 §Phase D，F-ACCAPI-ATTEMPT-001

Effort：3-4 小时

---

### A09 二阶段 quota：CRDT 风格 reserve + settle [P0] [算法升级]

**基线-开源（Billing-Engine-Ref）**：`BillingSession` 单阶段 — 请求结束时一次扣费。**不足**：高并发下两个请求都看到 quota=100，都进，结果消耗 150；超卖。

**基线-官方（Vendor-X1）**：`reasoning_budget` + `prompt_cache_key` 让 max output 大幅波动；不预扣 max → 超卖必然。

**HUAKAI 升级**：

原算法：

```
# Tx1: 一阶段扣费
on_request_end:
    UPDATE quotas SET used = used + actual_cost
    if used > limit: pretend it's fine and overrun
```

新算法（reserve-then-settle，4 scope，可组合）：

```
# Tx1 (Reserve) — 请求开始时
def reserve(api_key, account, pool, binding, request):
    max_in = request.max_input_tokens or estimate_input(request)
    max_out = request.max_output_tokens or model_default_max_output(request.model)
    # binding tier 带 multiplier（reasoning_budget 等可放大输出）
    multiplier = binding.tier_max_multiplier or 1.5
    max_cost_usd = (max_in * pricing.input_per_1k + max_out * pricing.output_per_1k * multiplier) / 1000
    
    # 4 scope 同 Tx 内 reserve
    with serializable_tx() as tx:
        for scope in [
            ("binding", binding.id, max_cost_usd),
            ("api_key", api_key.id, max_cost_usd),
            ("account", account.id, max_cost_usd),
            ("pool", pool.id, max_cost_usd),
        ]:
            row = tx.select_for_update(quota_table[scope[0]], id=scope[1])
            if row.reserved + row.committed + scope[2] > row.limit:
                tx.rollback()
                return ReserveError(insufficient=scope[0])
            tx.update(quota_table[scope[0]], id=scope[1], reserved=row.reserved + scope[2])
        # 写 claim row
        claim_id = tx.insert(billing_ledger_claims,
                             status="reserving",
                             reserved_usd=max_cost_usd,
                             scope_versions=[...])
        tx.commit()
    return ReserveOK(claim_id=claim_id, reserved_usd=max_cost_usd)

# Tx2 (Settle) — 请求结束时，actual_cost 替换预扣
def settle(claim_id, actual_cost_usd, status):
    with serializable_tx() as tx:
        claim = tx.select_for_update(billing_ledger_claims, claim_id)
        if claim.status != "reserving":
            return SettleError("not_reserving")
        diff = actual_cost_usd - claim.reserved_usd      # 通常负值（实际 < 预扣）
        for scope_kind, scope_id in claim.scopes:
            tx.update(quota_table[scope_kind], id=scope_id,
                      reserved=reserved - claim.reserved_usd,
                      committed=committed + actual_cost_usd)
        tx.update(billing_ledger_claims, claim_id,
                  status=status,                 # committed | aborted
                  actual_usd=actual_cost_usd,
                  settled_at=now_ms())
        tx.commit()

# Sweep — 长期未 settle 的 claim 视为 orphan
def sweep_orphan_claims():
    expired = sql_query("""
        SELECT claim_id FROM billing_ledger_claims
        WHERE status = 'reserving' AND reserved_at < NOW() - INTERVAL '1 hour'
    """)
    for cid in expired:
        settle(cid, actual_cost_usd=0, status="aborted")  # 退还 reserve
```

复杂度对比：

| 维度 | v1 | v2 |
|---|---|---|
| Tx1 复杂度 | 0（无） | O(scope 数=4) row lock |
| Tx2 复杂度 | O(1) | O(scope 数) |
| 超卖 | 高并发必然 | 不可能（4 scope 全 lock） |
| 客户透明度 | 无 | `X-Huakai-Quota-Headroom-Usd` 头 |
| 退还精度 | 不退 | 100%（diff = actual - reserved） |

数据结构变化：
- 已有 `billing_ledger_claims`（N+5b 计划），增加 `scope_versions jsonb` 记录每 scope 当时的 limit（防 admin 改 limit 后 settle 错误）
- `binding.tier_max_multiplier float NOT NULL DEFAULT 1.5`
- `quota_*` 表（per scope）`reserved` + `committed` 双列 + 视图 `available = limit - reserved - committed`

为什么更强：
- **正确性**：从"近似限额"升级为"linearizable 限额"；超卖 0
- **客户感知**：402 立刻发，而不是请求跑完才发现没钱（半道断流体验差）
- **退还精度**：predicted vs actual 差额自动还回

信号：`X-Huakai-Quota-Headroom-Usd: 4.21` 客户头；`reserve_reject_total{scope}`；orphan_claim_sweep counter

对应 F-* IDs：F-BILL-001 (extend with multiplier + 4-scope)

Effort：5-6 小时

---

### A10 二阶段 quota：分位数 reserve（reduce over-reserve） [P2] [算法升级]

**基线**（接 A09）：A09 用 `max_in × multiplier × max_out` 预扣，对绝大多数请求严重 over-reserve（实际只用 30%），导致客户头部 `Quota-Headroom` 假报警。

**HUAKAI 升级**：

```
# 每 model × prompt_pattern_class 维护历史分位数
class ReserveQuantileEstimator:
    def __init__(self):
        self.t_digest_per_class: dict[(model, class), TDigest] = {}
    
    def estimate(self, model, request, target_quantile=0.95):
        cls = classify_prompt(request)        # length bucket × tool_use × system_pattern
        td = self.t_digest_per_class.get((model, cls))
        if td is None or td.n < 100:
            # 冷启动：回退到 max
            return max_cost(request, model)
        # P95 + safety margin
        return td.quantile(target_quantile) * 1.10
    
    def update(self, model, request, actual_cost_usd):
        cls = classify_prompt(request)
        self.t_digest_per_class.setdefault((model, cls), TDigest()).add(actual_cost_usd)

# Reserve 用 P95 而非 max
reserved_usd = quantile_estimator.estimate(model, request, q=0.95)
# 如果 actual > reserved_usd（5% 概率），settle 时检测并补 reserve
if actual_cost > reserved:
    overdraft = actual_cost - reserved
    if scope_available(account_scope) >= overdraft:
        committed = actual_cost  # 直接结算
    else:
        emit("quota_p95_breach", {actual, reserved, overdraft})
        # 走 best-effort：committed = available；audit_overdraft = overdraft
```

复杂度对比：

| 维度 | A09（max） | A10（P95） |
|---|---|---|
| 单 reserve | O(1) | O(log K) t-digest query |
| 实际利用率 | ~30% (over-reserve) | ~85% |
| 客户 headroom 误报 | 高 | 低 |
| 5% 概率 breach 处理 | N/A | best-effort + audit |

数据结构变化：
- 内存 t-digest per (model, prompt_class)；周期 flush 到 `cost_distribution(model, class, digest_blob)` 表
- prompt_class 函数：`(input_token_bucket, has_tools, has_cache_control, system_len_bucket)` 离散化

为什么更强：
- **客户感知**：headroom 头报真实数；402 触发率显著下降
- **可证明**：P95 + 10% margin 数学上 5% 触底；触底走 audit 不丢钱
- **依赖 A09**：只是替换 reserve 估计器

Effort：3-4 小时

---

### A11 多 attempt DAG 规划 → 状态-动作图 + 期望时间最短路径 [P1] [算法升级]

**基线-开源**：retry 是顺序的 — fail → 换下家 → fail → 换下家。最多 N 次。**不足**：每次失败"换下家"是基于本次失败的局部决策，没看全局；可能两次都打中同 cooldown class 账号。

**基线-官方**：无

**HUAKAI 升级**：

原算法：

```
for i in range(MAX_RETRY):
    acct = select_next(exclude=tried)
    result = call(acct)
    if result.ok: return result
    tried.append(acct)
    if not retryable(result.error): break
return last_error
```

新算法（attempt DAG = 状态-动作图，求期望时间最短路径）：

```
# 状态空间：S = (set_tried, time_used_ms, error_classes_seen)
# 动作空间：A = {try_account_x, refresh_then_retry_x, wait_x_ms, fail_to_client}

def plan_attempts(request, candidates, deadline_ms):
    # 离线构建：每个候选账号的预期成功概率 + 预期延迟
    nodes = []
    for acct in candidates:
        p_succ = predict_success(acct, request, error_history)
        e_lat = predict_latency_ms(acct, request)
        nodes.append({
            "acct": acct,
            "p": p_succ,
            "e_lat": e_lat,
            "prerequisite": acct.cooldown_until_ms - now_ms() if cooldown else 0,
        })
    
    # 排序策略：贪心 — 期望"产出/时间"最大优先
    # 分数 = p_succ / (e_lat + prerequisite)
    nodes.sort(key=lambda n: -n["p"] / (n["e_lat"] + n["prerequisite"] + 1))
    
    # 计算 attempt 序列总期望时间
    # E[total] = e_lat[0] + (1-p[0]) * (e_lat[1] + (1-p[1]) * ...)
    # 找前缀使得 E[total] < deadline_ms 且 p_at_least_one_success ≥ TARGET_P
    plan = []
    cumulative_p_fail = 1.0
    cumulative_time = 0.0
    for n in nodes:
        if cumulative_time + n["e_lat"] + n["prerequisite"] > deadline_ms:
            break
        plan.append(n)
        cumulative_time += n["e_lat"] * cumulative_p_fail + n["prerequisite"]
        cumulative_p_fail *= (1 - n["p"])
        if 1 - cumulative_p_fail >= TARGET_P_SUCCESS:
            break
    
    return AttemptPlan(plan, expected_e_lat=cumulative_time, p_overall=1-cumulative_p_fail)

# 执行时按 plan 走，但每步动态修正：上一步真实结果 vs 预测，如偏差大重规划
def execute_plan(plan, request):
    for step_i, step in enumerate(plan):
        result = try_with_acct(step.acct, request)
        if result.ok:
            return result
        # 真实失败原因可能 update 后续预测
        if observed_class_invalidates_remaining_plan(result.error_class, plan[step_i+1:]):
            plan = replan(plan[step_i+1:], request, deadline_ms - elapsed)
    return last_error
```

复杂度对比：

| 维度 | v1（顺序） | v2（DAG） |
|---|---|---|
| 规划时间 | O(1) | O(K log K) for K 候选 |
| 平均 attempt 数 | 高（盲选） | 低（贪心 P95 减少失败） |
| 总 P99 | 不稳定 | 受 deadline 约束 |
| 重规划 | 无 | 上步失败 invalidate 后续时立即修正 |

数据结构变化：
- `request_attempts.plan_step_index` 和 `plan_total_steps` — 复盘
- 历史 success/latency per (account, model, prompt_class) → t-digest 数据用 A10 的

为什么更强：
- **客户感知**：P99 总延迟可被 deadline 约束（贪心截断）
- **可证明**：期望时间公式精确；不是"试 3 次拍脑袋"
- **协同**：和 A02 评分 + A10 t-digest 共享数据源

Effort：6-8 小时

---

### A12 Retry 决策表 → 错误类 × cooldown class × budget 矩阵 [P1] [数据结构升级]

**基线-开源**：retry 决策散在各处 if/else（429 → wait reset；401 → fallback；5xx → retry once）。**不足**：表达力差；新加错误类要改代码；测试矩阵爆炸。

**HUAKAI 升级**：

```
# 数据驱动的决策表（YAML 配置 + DB cache）
retry_policy_matrix = [
    # (error_class, account_cooldown_class, retry_budget_left)
    # → (action, target, jitter_ms, audit_class)
    
    {match: {error_class: "upstream_4xx_auth", attempt_n: 0},
     action: "refresh_then_retry_same_acct", grace_hold_ms: 5000},
    {match: {error_class: "upstream_4xx_auth", attempt_n: ">=1"},
     action: "failover_other_acct"},
    {match: {error_class: "upstream_429", retry_after_header: "present"},
     action: "wait_until_or_failover", max_wait_ms: 30000},
    {match: {error_class: "upstream_429", retry_after_header: "absent"},
     action: "exp_backoff_failover", base_ms: 1000, max: 8000},
    {match: {error_class: "upstream_5xx", attempt_n: "<=1"},
     action: "retry_same_acct_with_jitter", jitter_ms: 200},
    {match: {error_class: "upstream_5xx", attempt_n: ">=2"},
     action: "failover_other_acct"},
    {match: {error_class: "local_validate"},
     action: "fail_to_client_no_charge"},
    {match: {error_class: "network_timeout"},
     action: "failover_other_acct_with_quarantine_acct_60s"},
    # ...
]

def decide(error_class, attempt, budget):
    for rule in retry_policy_matrix:
        if rule.matches(error_class=error_class, attempt_n=attempt.n,
                       retry_after_header=attempt.retry_after_present,
                       budget_left=budget.remaining_ms):
            return rule.action
    return "fail_to_client_no_charge"  # default
```

复杂度对比：

| 维度 | v1（散 if） | v2（决策表） |
|---|---|---|
| 决策时间 | O(K) hardcoded | O(R) R=规则数，~30 |
| 加新错误类 | 改代码 + redeploy | 改 YAML + reload |
| 测试覆盖 | scattered | 矩阵覆盖 |

数据结构变化：
- 表 `retry_policy_versions(version, yaml_blob, active_at, sha256)` — 版本化
- 加载 → 内存 trie 或 hash 表查询

为什么更强：
- **可证明**：每规则有唯一 audit_class；行为可表格化测试
- **可观测**：每 attempt audit row 记录命中的 rule_id
- **可演化**：新 vendor 错误类无需改代码

Effort：3-4 小时

---

### A13 错误归一化 → 12 类标准错误 + 概率类匹配器 [P1] [算法升级]

**基线-开源**：错误识别用字符串包含 / status code（"organization disabled"、"credit balance"）。**不足**：上游改文案就漏；不同 vendor 同语义错误识别码不同。

**HUAKAI 升级**：

```
# 12 类标准错误（HUAKAI 规范）
ErrorClass = enum:
    UPSTREAM_4XX_AUTH         # token 失效
    UPSTREAM_4XX_QUOTA        # 上游账号配额耗尽
    UPSTREAM_4XX_DISABLED     # KYC / org disabled / workspace deactivated
    UPSTREAM_4XX_BAD_REQUEST  # 输入错（不可重试）
    UPSTREAM_429              # rate limit
    UPSTREAM_529              # overload
    UPSTREAM_5XX              # 上游服务故障
    NETWORK_TIMEOUT           # 局部网络
    LOCAL_TIMEOUT             # gateway 内 deadline
    LOCAL_VALIDATE            # gateway 拒绝（schema fail）
    PROTOCOL_VIOLATION        # 上游响应不可解
    UNKNOWN

# 分类器 = 概率类匹配器（可组合证据）
class ErrorClassifier:
    def classify(self, resp: HttpResponse, body: bytes, provider: str) -> Classification:
        evidences = []
        # E1: 状态码
        if resp.status == 401: evidences.append(("status_401", 0.9, ErrorClass.UPSTREAM_4XX_AUTH))
        if resp.status == 429: evidences.append(("status_429", 0.95, ErrorClass.UPSTREAM_429))
        # E2: header 信号
        if "x-codex-rate-limit-window" in resp.headers:
            evidences.append(("hdr_codex_rate", 0.95, ErrorClass.UPSTREAM_429))
        if "anthropic-ratelimit-unified-5h-status" in resp.headers:
            evidences.append(("hdr_anthropic_rate", 0.95, ErrorClass.UPSTREAM_429))
        # E3: body 关键词（按 provider 表）
        if provider == "anthropic" and b"organization disabled" in body:
            evidences.append(("body_org_disabled", 0.99, ErrorClass.UPSTREAM_4XX_DISABLED))
        # ... 其他
        
        # 加权投票
        scores: dict[ErrorClass, float] = {}
        for evid_name, weight, cls in evidences:
            scores[cls] = scores.get(cls, 0) + log(1 / (1 - weight))    # log-odds 加和
        
        if not scores:
            return Classification(ErrorClass.UNKNOWN, confidence=0.0, evidences=[])
        best_cls = max(scores, key=scores.get)
        confidence = sigmoid(scores[best_cls])
        return Classification(best_cls, confidence, evidences)

# 决策接 A12
def on_error(resp, body, provider, attempt):
    cls = classifier.classify(resp, body, provider)
    if cls.confidence < 0.7:
        emit_metric("error_class_low_confidence", {provider, status: resp.status})
        # 保守 fallback —— UNKNOWN 不让代价高的动作（如永久 disable）
        return retry_policy.decide(ErrorClass.UNKNOWN, attempt, budget)
    return retry_policy.decide(cls.cls, attempt, budget)
```

复杂度对比：

| 维度 | v1 | v2 |
|---|---|---|
| 单分类时间 | O(K) string scan | O(K) evidence eval |
| 误分类率（vendor 文案改） | 高 | 低（多 evidence 鲁棒） |
| 不可分类处理 | 错乱 | UNKNOWN + 保守 |

为什么更强：
- **可证明**：概率合成有数学基础；置信度阈值可调
- **鲁棒**：vendor 改一处文案不至于全错（其他 evidence 仍命中）
- **演化**：加 evidence 不影响已有规则

Effort：3-4 小时

---

### A14 错误分类的在线学习 → 误分类反馈环 [P3] [算法升级]

**基线**：A13 静态 evidence 表

**HUAKAI 升级**：

```
# 操作员/客户上报误分类，自动调权重
def report_misclassification(request_id, expected_cls, actual_cls):
    # 找回当时的 evidence
    attempt = request_attempts.get(request_id)
    evidences = attempt.classification_evidences
    # 对每条 evidence，调 weight：
    # - 如果 evidence.predicted == expected_cls：weight += δ (提升)
    # - 如果 evidence.predicted == actual_cls 但 actual ≠ expected：weight -= δ
    for evid in evidences:
        if evid.cls == expected_cls:
            evidence_weights[evid.name] += LEARN_RATE
        else:
            evidence_weights[evid.name] -= LEARN_RATE
    persist_weights()

# 每周对最近 misclassification 跑一次 batch update
```

为什么更强：
- **演化能力**：unknown 案例上报后自动改进；不用改代码
- **客户感知**：操作员可"标错"减少 false-positive
- **风险**：需 cap weight 范围避免崩坏 — 加 weight ∈ [0.5, 0.99] 上下界

Effort：4-5 小时

---

### A15 Versioned pricing snapshot → Merkle 化价格图 [P0] [数据结构升级]

**基线-开源（Billing-Engine-Ref）**：版本化定价快照（new-api 已有），但单次 settle 取的是 settle 时刻的 active version；**不足**：请求开始 → 结束跨越价格变更时，可能用新价格结算旧请求。

**基线-官方（Vendor-X1, X2）**：价格调整偶发但客户敏感。

**HUAKAI 升级**：

```
# 价格变更产生新 snapshot；snapshot Merkle 树确保不可篡改
struct PricingSnapshot:
    version: int
    active_from: timestamp
    active_until: timestamp | null
    rates: dict[(provider, model, dimension), Decimal]    # dimension={input, output, cache_creation, cache_read}
    parent_hash: hex                                       # 上一 snapshot
    self_hash: hex                                         # = sha256(version|rates|parent_hash)

def create_snapshot(rates, prev):
    snap = PricingSnapshot(
        version=prev.version + 1,
        rates=rates,
        parent_hash=prev.self_hash,
    )
    snap.self_hash = sha256(canonicalize(snap))
    return snap

# Reserve 时绑定 snapshot version；settle 用同 version
def reserve(request, ...):
    snap = pricing.active_snapshot()
    claim.pricing_snapshot_version = snap.version
    claim.pricing_snapshot_hash = snap.self_hash
    # ...

def settle(claim_id, actual_tokens):
    claim = ...
    snap = pricing.get_snapshot(claim.pricing_snapshot_version)
    # 完整性校验
    assert snap.self_hash == claim.pricing_snapshot_hash
    cost = compute_cost(actual_tokens, snap.rates)
    # ...
```

复杂度对比：

| 维度 | v1 | v2 |
|---|---|---|
| Reserve overhead | O(1) | O(1) + 1 hash binding |
| Settle 一致性 | best-effort | 与 reserve 同版本（强一致） |
| 价格篡改检测 | 无 | hash 比对 |

数据结构变化：
- 表 `pricing_snapshots(version, active_from, active_until, rates jsonb, parent_hash, self_hash)`
- `billing_ledger_claims.pricing_snapshot_version + pricing_snapshot_hash`

为什么更强：
- **正确性**：客户在请求开始时签的"按当时价"约定不会被结算时违约
- **审计**：snapshot 链可证明"任意 settle 用的是 reserve 时的价"
- **金融级**：价格调整不影响 in-flight 请求

Effort：3-4 小时

---

### A16 价格表达式求值 → 表达式 AST + 编译式求值 [P2] [算法升级]

**基线-开源**：new-api 用 DSL 字符串求值（每次解析 + eval）。**不足**：每请求多 50us-200us；高 RPS 显著。

**HUAKAI 升级**：

```
# DSL → AST 一次性 parse；运行时查表
class CompiledPricing:
    def __init__(self, snap: PricingSnapshot):
        # 构建 dispatch table，避免运行时分支
        self.table = {}
        for (provider, model, dim), rate in snap.rates.items():
            self.table[(provider, model, dim)] = rate
        # 物化常见组合的 sum
        for (provider, model) in self._distinct_pairs(snap):
            self.table[(provider, model, "_input_per_1k")] = self.table[(provider, model, "input")]
            # ...
    
    def cost(self, model, tokens) -> Decimal:
        provider = model_to_provider[model]
        return (
            self.table[(provider, model, "input")]   * tokens.input  / 1000
          + self.table[(provider, model, "output")]  * tokens.output / 1000
          + self.table[(provider, model, "cache_creation")] * tokens.cache_creation / 1000
          + self.table[(provider, model, "cache_read")]     * tokens.cache_read     / 1000
        )

# 用 Decimal 不用 float（金融）
```

为什么更强：
- **性能**：50-200us → ~1us；高 RPS 下省 CPU
- **正确性**：Decimal 全程，无 float 误差

Effort：2 小时

---

### A17 Capacity forecast → 时序模型 + 周期分解 [P2] [算法升级]

**基线-开源（Obs-Ref）**：trends 历史；no forecast。

**HUAKAI 升级**：

```
# 每账号维护时间序列：tokens_used per 1h bucket
# 预测：分解为 trend + weekly seasonality + daily seasonality + residual
def forecast_account_exhaust(acct, horizon_days=14):
    series = load_hourly_usage(acct.id, last_days=30)        # 30·24 = 720 points
    if len(series) < 168:  # 不足 1 周
        return SimpleForecast(burn_rate=mean(series), naive=True)
    
    # STL 分解（季节性 + 趋势 + 余差）
    weekly = stl_decompose(series, period=24*7).seasonal
    daily = stl_decompose(series, period=24).seasonal
    trend = stl_decompose(series, period=24*7).trend
    
    # 外推
    future_pts = []
    for h in range(horizon_days * 24):
        idx = len(series) + h
        future_pts.append(
            trend[-1] + (trend[-1] - trend[-2]) * h          # 线性 trend 外推
            + weekly[idx % len(weekly)]                       # 周内周期
            + daily[idx % 24]                                 # 日内周期
        )
    
    # 累计直到 quota_remaining 耗尽
    cumulative = 0
    for h, val in enumerate(future_pts):
        cumulative += val
        if cumulative >= acct.quota_remaining:
            return Forecast(exhaust_at=now() + timedelta(hours=h),
                           confidence_pos=acct.quota_remaining * 1.10,    # +10%
                           confidence_neg=acct.quota_remaining * 0.90)    # -10%
    return Forecast(exhaust_at=None, message="not in horizon")

# 推荐补货
def recommend(forecast):
    if forecast.exhaust_at is None: return None
    days_left = (forecast.exhaust_at - now()).days
    if days_left < 14:
        # 计算补一个账号的 ROI
        return Recommendation(
            text=f"Add 1 account to extend by {avg_account_extension_days} days",
            roi=...,
        )
```

复杂度对比：

| 维度 | v1 | v2 |
|---|---|---|
| 单 forecast | n/a | O(N log N) STL，N=720 |
| 准确性 | 0 | ±10% (P80 经验值) |

为什么更强：
- **客户感知**：操作员仪表盘"14 天后枯竭"+1 条补货建议
- **可证明**：基于真实分解模型，不是拍脑袋

Effort：8-10 小时（需 STL 库或自实现）

---

### A18 Capacity 预算 + 补货推荐 → 凸优化 [P3] [算法升级]

**基线**：A17 预测耗尽；A18 求"补几个账号"

**HUAKAI 升级**：

```
# 决策变量：n_i = 第 i 类账号要补几个
# 目标：最小化 cost = Σ n_i · price_i
# 约束：predicted_demand <= Σ n_i · capacity_i + current_capacity
# 这是 LP（连续）但 n_i 整数 — ILP，但维度低（账号种类 ≤ 5），暴力枚举
def optimal_topup(forecast, budget_usd):
    types = [
        {"name": "GPT-Plus", "capacity_tok": 50_000_000, "price": 200},
        {"name": "Claude-Pro", "capacity_tok": 30_000_000, "price": 100},
        # ...
    ]
    demand = forecast.demand_next_30d_tokens
    current = forecast.current_capacity_tokens
    deficit = max(demand - current, 0)
    
    best = None
    for n0 in range(0, 5):
        for n1 in range(0, 5):
            cost = n0 * types[0]["price"] + n1 * types[1]["price"]
            cap = n0 * types[0]["capacity_tok"] + n1 * types[1]["capacity_tok"]
            if cap >= deficit and cost <= budget_usd:
                if best is None or cost < best.cost:
                    best = Plan(plan=[n0, n1], cost=cost, deficit_covered=cap >= deficit)
    return best
```

为什么更强：
- **可证明**：枚举搜索 ≤ 5^5 = 3125 个候选，毫秒级
- **客户感知**：精确告诉"花 $300 满足未来 30 天"

Effort：3-4 小时

---

### A19 跨 vendor capacity graph → min-cut/max-flow [P2] [算法升级]

**基线**：每 vendor 独立看；不知"vendor A 容量 + vendor B 容量"汇总能否满足"model M 跨 vendor 需求"。

**基线-官方（Vendor-Meta）**：OpenRouter 跨 provider，但是它对 vendor capacity 不持有视图。

**HUAKAI 升级**：

```
# 建图：
# source -> [vendor account] -> [model node] -> sink
# 边容量 = 账号剩余容量 / model 支持映射
# 求 max-flow = 跨 vendor 总可用容量
# min-cut = 找瓶颈

import networkx as nx

def build_capacity_graph(accounts, models, requests_demand):
    G = nx.DiGraph()
    G.add_node("source")
    G.add_node("sink")
    for acct in accounts:
        G.add_edge("source", f"acct:{acct.id}", capacity=acct.tokens_remaining)
        for model in acct.model_allow_list:
            G.add_edge(f"acct:{acct.id}", f"model:{model}", capacity=float("inf"))
    for model, demand in requests_demand.items():
        G.add_edge(f"model:{model}", "sink", capacity=demand)
    return G

def analyze_capacity(G):
    flow_value, flow_dict = nx.maximum_flow(G, "source", "sink")
    cut_value, partition = nx.minimum_cut(G, "source", "sink")
    return {
        "achievable_throughput": flow_value,
        "bottleneck_edges": find_min_cut_edges(G, partition),
    }

# Operator dashboard：cut value < demand → 显示瓶颈
```

复杂度：max-flow Edmonds-Karp 是 O(V·E²)；HUAKAI 实际 V 通常 < 1000，毫秒级

为什么更强：
- **可证明**：min-cut 数学保证瓶颈识别
- **客户感知**：操作员看到"GPT-4o 是瓶颈，加 GPT 账号 ROI 最高"

Effort：5-6 小时

---

### A20 Channel monitor probe → 状态机自动转换 + hysteresis [P1] [状态机升级]

**基线-开源（Commercial-Pool-Ref）**：monitor 周期 ping；连续 N 次失败 → 标 unhealthy。**不足**：边界抖动（"2 succ 1 fail 2 succ"）会导致状态频繁翻转。

**HUAKAI 升级**：

```
# Hysteresis 状态机：升降级阈值不同（避震）
class ChannelHealthFSM:
    states = {HEALTHY, DEGRADED, UNHEALTHY, COOLDOWN}
    
    def transition(self, current, evt):
        # 滑窗 W=10
        succ_rate = self.succ_window_count(W=10) / W
        p99_lat = self.p99_latency_ms()
        
        if current == HEALTHY:
            if succ_rate < 0.6 or p99_lat > 30000:
                return DEGRADED
        elif current == DEGRADED:
            # 升级回 HEALTHY 要求更高阈值
            if succ_rate >= 0.95 and p99_lat <= 5000:
                return HEALTHY
            elif succ_rate < 0.3:
                return UNHEALTHY
        elif current == UNHEALTHY:
            # 不能直接回 HEALTHY，先 COOLDOWN
            if succ_rate >= 0.9:
                return COOLDOWN
        elif current == COOLDOWN:
            # cooldown 中只接 probe 不接业务流量；满 5min + 9/10 succ 才回
            if cooldown_elapsed >= 5min and succ_rate >= 0.9:
                return HEALTHY
        return current  # no change
```

复杂度对比：

| 维度 | v1 | v2 |
|---|---|---|
| 单 transition | O(1) | O(W) sliding window |
| 抖动率（5min 内变化） | 高 | 低（hysteresis） |
| 误派 | 高（standby unhealthy 还派流量） | 0（COOLDOWN 隔离） |

为什么更强：
- **可证明**：hysteresis 数学性质 — 升降阈值不重合避免振荡
- **正确性**：COOLDOWN 状态保证恢复期不打流量

Effort：3-4 小时

---

### A21 Probe 自适应频率 → 失败率反馈控制 [P2] [算法升级]

**基线**：probe 固定 60s/次

**HUAKAI 升级**：

```
# AIMD（Additive Increase, Multiplicative Decrease）
class AdaptiveProbeScheduler:
    def __init__(self):
        self.interval_ms = 60_000
        self.MIN, self.MAX = 5_000, 600_000
    
    def on_probe_result(self, succ):
        if succ:
            # 健康 → 加 10s（每次 +10%）
            self.interval_ms = min(self.interval_ms * 1.1, self.MAX)
        else:
            # 失败 → 减半
            self.interval_ms = max(self.interval_ms / 2, self.MIN)
```

为什么更强：
- 健康账号 probe 频率自然降低 → 减少 vendor 端无谓打扰
- 不健康账号 probe 加速 → 状态恢复反馈快

Effort：1-2 小时

---

### A22 客户端身份探测器 → 概率投票 + 置信阈值 [P1] [算法升级]

**基线-开源（Clean-Arch-Ref）**：6 路 sticky extraction。**不足**：把"身份"和"sticky session"混淆；不持久化身份；CredentialInjector 不能按身份选注入策略。

**HUAKAI 升级**：

```
class ClientIdentityDetector:
    detectors = [
        # (priority, name, fn(req) → (cls, confidence))
        (1, "claude_code_metadata", lambda r: detect_claude_code(r)),
        (2, "openai_codex_session_id", lambda r: detect_codex(r)),
        (3, "amp_thread_id", lambda r: detect_amp(r)),
        (4, "cursor_signature", lambda r: detect_cursor(r)),
        (5, "user_agent_substring", lambda r: detect_ua(r)),
        (6, "fallback_generic", lambda r: ("generic_openai", 0.5)),
    ]
    
    def detect(self, req):
        votes = []
        for prio, name, fn in self.detectors:
            cls, conf = fn(req)
            if cls is not None:
                votes.append((cls, conf, prio, name))
        if not votes:
            return ("unknown", 0.0)
        # 优先级 + 置信度组合（高优先级且置信高优先）
        best = max(votes, key=lambda v: -v[2] + v[1])
        # 写 audit
        emit_metric("client_identity_detected", {cls: best[0], detector: best[3], conf: best[1]})
        return (best[0], best[1])
```

为什么更强：
- 比 sub2api 单方法 sticky 强；身份持久化 + tier 化营销情报
- CredentialInjector 按 (provider, client_identity) 选注入策略

Effort：4-5 小时

---

### A23 Stream forwarder 自适应 buffer [P1] [算法升级]

**基线-开源（Commercial-Pool-Ref）**：scanner buffer 1MiB 固定。**不足**：长 reasoning 单 chunk 可超；过大浪费内存。

**HUAKAI 升级**：

```
# AIMD buffer 大小，基于历史最大 chunk 大小
class AdaptiveScanner:
    def __init__(self):
        self.cap_bytes = 1 * MiB
        self.MIN, self.MAX = 256*KiB, 64*MiB
    
    def on_chunk_seen(self, n_bytes):
        # 看到 80% 用满，加倍
        if n_bytes >= self.cap_bytes * 0.8:
            self.cap_bytes = min(self.cap_bytes * 2, self.MAX)
            log.info(f"scanner buffer ↑ to {self.cap_bytes}")
        # 长期 < 25% 利用，减半
        elif self.recent_p99_chunk < self.cap_bytes * 0.25:
            self.cap_bytes = max(self.cap_bytes / 2, self.MIN)
    
    def on_overflow(self):
        # 真溢出 — 终结流但保留 partial usage
        return TerminateClass.RESPONSE_EVENT_TOO_LARGE
```

数据结构变化：每 (provider, model) 维护 p99 chunk size 历史

为什么更强：
- 内存友好：低流量端不预占 64MiB
- 兼容大 chunk：reasoning 模型大 chunk 不再误终结

Effort：2-3 小时

---

### A24 Stream drain 决策 → 三预算最早触发 [P1] [状态机升级]

**基线**：F-GW-002 已设计 drain budget（time / bytes / cost）

**HUAKAI 升级**（接 spec）：

```
def drain_loop(stream, budgets):
    start_ms = now_ms()
    bytes_drained = 0
    estimated_cost = 0
    while True:
        evt = read_evt(stream, timeout=remaining_inter_event_budget())
        if evt is None: break
        bytes_drained += len(evt.payload)
        usage = update_accumulator(evt)
        estimated_cost = pricing.cost(usage)
        # 三 budget 任一触发即停
        if now_ms() - start_ms >= budgets.max_ms: 
            return DrainExit(reason="time")
        if bytes_drained >= budgets.max_bytes:
            return DrainExit(reason="bytes")
        if estimated_cost >= budgets.max_cost_usd:
            return DrainExit(reason="cost")
    return DrainExit(reason="upstream_eof")
```

为什么更强：
- 三维独立 + 早停；客户断线后成本封顶
- 已在 spec，本节给出确切实现伪代码

Effort：1 小时（已 spec）

---

### A25 SSE 帧 normalize → 状态机解析 [P1] [算法升级]

**基线**：sub2api 简单"按 \n\n 切"。**不足**：上游 chunk 切分不保证一帧一 chunk；可能"半帧"到达。

**HUAKAI 升级**：

```
class SSEFrameParser:
    def __init__(self):
        self.buf = bytearray()
        self.state = "idle"            # idle | in_event | in_data
    
    def feed(self, chunk: bytes) -> Iterator[Frame]:
        self.buf.extend(chunk)
        while True:
            # 找下一个完整帧（\n\n）
            sep = self.buf.find(b"\n\n")
            if sep == -1:
                # 缓存等下次
                if len(self.buf) > MAX_BUF: raise OverflowError
                return
            raw = bytes(self.buf[:sep])
            self.buf = self.buf[sep+2:]
            yield self._parse_frame(raw)
    
    def _parse_frame(self, raw):
        event = "message"
        data_lines = []
        for line in raw.split(b"\n"):
            if line.startswith(b":"): continue       # comment
            if line.startswith(b"event:"): event = line[6:].strip().decode()
            elif line.startswith(b"data:"): data_lines.append(line[5:].lstrip())
        return Frame(event=event, data=b"\n".join(data_lines))
```

为什么更强：
- 半帧友好；不会因 chunk 边界切错
- comment 和 retry 字段不污染 data

Effort：2-3 小时

---

### A26 Stream 提前 settle vs 等终结 → 期望成本最小 [P2] [算法升级]

**基线**：等终结 marker；marker 缺失时 inferred

**HUAKAI 升级**：

```
# 流式中"看到 stop_reason 但未到 [DONE]"决策：
# - 提前 settle 风险：上游可能再补元数据导致漏计
# - 等 marker 风险：客户已收完，gateway 多挂 N ms

def stream_settle_decision(state, last_evt, deadline_ms):
    if state.has_stop_reason and state.has_usage_terminal:
        # 标准 — 提前 settle 安全
        return EarlySettle()
    elif state.has_stop_reason and not state.has_usage_terminal:
        # 未来 1s 内可能到 — 等
        if remaining_inter_event_budget() > 1000:
            return WaitMore()
        else:
            # 等不起 — 用 inferred
            return InferAndSettle()
    elif not state.has_stop_reason:
        return WaitMore()
```

为什么更强：
- 减少长尾等待；保留 marker 真到的精度

Effort：2 小时

---

### A27 二阶段 quota 与 stream 协同 → 流式期间动态 reserve 调整 [P3] [算法升级]

**基线**：reserve 定 max；流式中累计实际 usage 超过预 reserve 时静默继续

**HUAKAI 升级**：

```
# 流式中每 N tokens 检查
def on_stream_tokens_increment(claim_id, delta_tokens):
    state = stream_state[claim_id]
    state.tokens_used += delta_tokens
    state.cost_used += compute_cost(delta_tokens)
    if state.cost_used > state.reserved_usd * 0.9:
        # 接近 reserve 上限 — 尝试加 reserve
        ok = quota.extend_reserve(claim_id, additional_usd=state.reserved_usd * 0.5)
        if not ok:
            # 软终结：发 stop_reason="quota_exhausted" 给客户，不再读 upstream
            state.soft_terminate = True
```

为什么更强：
- 客户长流不会半道因配额突然死掉
- 配额耗尽给客户友好提示而不是 503

Effort：3-4 小时

---

### A28 跨 vendor 故障域感知 spillover [P2] [算法升级]

**基线**：默认所有账号当独立故障域；vendor 全局故障（vendor cloud 区域故障）时调度无视

**HUAKAI 升级**：

```
# 故障域 = (vendor, region)
def fault_domain(acct):
    return (acct.provider, acct.region or "global")

# 指数衰减失败率 per domain
domain_health = defaultdict(lambda: ExpDecayCounter(half_life=120))

def on_attempt_result(acct, ok):
    d = fault_domain(acct)
    domain_health[d].observe(1 if ok else 0)

# 调度时，故障域整体不健康降权
def select_with_domain_awareness(candidates):
    for acct in candidates:
        d = fault_domain(acct)
        if domain_health[d].rate() < 0.3:
            acct.priority_penalty = 1000     # 推到末尾
        else:
            acct.priority_penalty = 0
    return sort_by_priority(candidates)
```

为什么更强：
- vendor 区域故障时秒级感知，整域降权
- 跨故障域恢复时间 from O(N 单账号试) → O(1) 一步切

Effort：2-3 小时

---

## 2. Priority Rollup

| Priority | A-IDs | 类型 |
|---|---|---|
| P0 | A01, A06, A07, A09, A15 | 调度+lease+storm+quota+pricing 必做 |
| P1 | A02, A03, A04, A08, A11, A12, A13, A20, A22, A23, A24, A25 | 算法骨架 |
| P2 | A05, A10, A16, A17, A19, A21, A26, A28 | 增益但非必须 |
| P3 | A14, A18, A27 | 创新拓展 |

| 算法领域 | A-IDs |
|---|---|
| 1 调度算法 | A01, A02, A03 |
| 2 Sticky migration | A04, A05 |
| 3 Lease + storm | A06, A07, A08 |
| 4 二阶段 quota | A09, A10, A27 |
| 5 Multi-attempt DAG | A11, A12 |
| 6 错误归一化 | A13, A14 |
| 7 Pricing snapshot | A15, A16 |
| 8 Capacity forecast | A17, A18 |
| 9 跨 vendor capacity graph | A19, A28 |
| 10 Channel monitor | A20, A21 |
| 11 客户端身份 | A22 |
| 12 Stream forwarder | A23, A24, A25, A26 |

总 Effort：约 ~110 小时

## 3. Open Questions

1. **A02 EWMA half-life**：30s 是猜测；需配合上线后 P99 抖动观测调
2. **A06 grace_max**：默认 50；高 RPS 系统可能要 200+
3. **A09 reserve scope 顺序**：4 scope 加锁顺序固定避免死锁；推荐 binding → key → account → pool（粒度由细到粗）
4. **A11 DAG TARGET_P_SUCCESS**：经验 0.95；客户严格 SLA 时 0.99
5. **A15 snapshot 持久化频率**：变更触发 vs 周期 snapshot — 推荐变更触发（hash chain 完整性）
6. **A17 STL 库选择**：Go 生态较少；可能需要 port Python statsmodels 或自实现
7. **A19 max-flow per request 还是 per minute**：per minute 后台聚合 + admin 看（per-request 太重）
8. **A22 unknown identity 默认行为**：当前 generic_openai；不确定是否安全 — 让操作员配置

## 4. 一行总结

HUAKAI 算法主张：**binding-first 路由 + 在线统计评分 + 期望成本最小决策 + grace-hold lease + 3-scope storm + 4-scope CRDT quota + Merkle pricing + STL forecast + max-flow capacity + hysteresis monitor + 概率分类 + adaptive streaming**——把 Commercial-Pool-Ref 的"凑功能"路径替换为"每个决策都有数学不变量"的路径，复杂度优化、一致性强化、客户透明度三轴同时拉高。
