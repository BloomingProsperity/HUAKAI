# 2026-05-08 PASR-lite v2 — HUAKAI 自有调度算法 (Claude lane)

| 字段 | 值 |
| ---- | ---- |
| Owner directive | "用我们自己的东西" + "我是说核心调度算法框架以及逻辑" + "可以K3" |
| Owner critique | trie 4 致命缺陷 → 改 consistent hash ring + K-replica 段 |
| 已锁定 | K=3 (行业 sweet spot, AWS S3/Riak 默认) |
| 复用 spec | DR-009 A05a HRW Rendezvous Hashing (2026-05-02 已锁) |
| Lane | Claude planner v2，已合 Owner 反馈，待 codex 平行 + synthesis |

## 1. 算法核心（最终版）

### 1.1 一句话
**每个 prompt prefix 不指向单 steward，指向一个 K=3 段（HRW 选出 top-3 健康账号），段内按"谁有 cache"+ MAB 排序选一个**。

### 1.2 数据结构
```go
// AccountRing 维护全局账号集 + HRW seed
type AccountRing struct {
    accounts []ringEntry  // 当前所有 healthy provider_account
    seed     uint64       // HRW 哈希种子（admin 可换密钥防对手猜命中）
}

// PrefixSegment 每个 prefix 对应的 K=3 段 + 段内 cache 状态
type PrefixSegment struct {
    PrefixHash      [16]byte         // hash(system + tools + first-msg)
    Members         [3]int64         // HRW top-3 account_id
    HasCacheBitmap  uint8            // 3 bits: bit_i=1 表示 Members[i] 见过 prefix 写 cache
    LastCacheReadAt time.Time        // 任一 member 最近 cache_read 时间(老化锚)
    AttemptStats    [3]armStats      // MAB 段内子排序: success/failure 计数
}

type armStats struct {
    successes uint32   // cache_read_input_tokens > 0 计 1
    failures  uint32   // 5xx / cache miss / latency > P99 计 1
    lastUsed  int64    // unix nanos
}
```

### 1.3 选 account 算法（每请求 O(1) hot path）
```
schedule(req):
    prefix = hash_prefix(req.system, req.tools, req.messages[0])
    segment = ring.lookup(prefix)              // O(1) hash 查表; HRW 已固定 K=3 成员
    
    candidates := []
    for i in 0..2:
        acc = segment.Members[i]
        if acc.health < 0.3: continue          // 故障跳过
        if acc.load_ratio > 0.95: continue      // 满载跳过
        candidates.append(i)
    
    if candidates is empty:
        return ring.fallback(req)              // 整段不可用,降级到 ring 全 round-robin
    
    // 优先级: (有 cache) > (MAB Thompson sample) 
    cached = candidates.filter(i where segment.HasCacheBitmap & (1<<i) != 0)
    if cached not empty:
        // 在已 cache 段员里 MAB sample
        chosen = thompson_sample(cached, segment.AttemptStats)
    else:
        // 无 cache 段员,全段 MAB sample
        chosen = thompson_sample(candidates, segment.AttemptStats)
    
    return segment.Members[chosen]

after_response(req, acc, response):
    idx = segment.Members.index(acc)
    if response.cache_creation_input_tokens > 0:
        segment.HasCacheBitmap |= (1 << idx)   // 标记: 这个段员现在有 cache
    if response.cache_read_input_tokens > 0:
        segment.AttemptStats[idx].successes++
        segment.LastCacheReadAt = now
    if response.error or no cache hit and was expected:
        segment.AttemptStats[idx].failures++
```

### 1.4 老化（每 5min ticker）
```
for each prefix in segment_table:
    if now - segment.LastCacheReadAt > 30min:    # 30min 无命中 → 段过期
        evict(prefix)
    decay AttemptStats by half-life=24h          # MAB 老观测衰减
```

### 1.5 ring rebalance（账号增减时）
```
on account.add(new_acc):
    # HRW 性质: 仅约 1/N 段需要重分配
    affected = compute_affected_prefixes(new_acc)
    for prefix in affected:
        new_top3 = HRW.top3(prefix.hash, all_accounts)
        # 如果 new_acc 进入段, 它 HasCacheBitmap=0, AttemptStats=0
        # 已被替换出去的旧成员的 cache 自然失效（无被引用就老化掉）

on account.remove(dead_acc):
    affected = prefixes_with_member(dead_acc)
    for prefix in affected:
        new_top3 = HRW.top3(prefix.hash, accounts \ {dead_acc})
        # 缩段时 cache bitmap 平移 bits
```

## 2. 与 baseline 对比（Owner critique 全部闭合）

| 缺陷 (trie) | PASR-lite v2 修复 |
|---|---|
| 热点 prefix 全打单 steward | K=3 段自动 33:33:33 分散 |
| O(depth) 慢 | O(1) hash 查表 |
| Steward 死 → 子树全死 | 1 死 → 段内还剩 2，爆炸半径 1/3 |
| 5min 老化 cache miss thrash | LastCacheReadAt 锚位每命中刷新，30min 真闲才老化 |
| 单点 cache 过度集中 | 段内 3 个账号都积累 cache，vendor 限流单账号无感 |

## 3. 决定点（Owner 已答）

| Q | A |
|---|---|
| K=2 vs K=3 | **K=3 锁** |
| Track B `sticky_bindings` 表怎么处置 | 保留作 cold-start hint，运行时不再权威 |
| LBTS（codex lane）在 v1 引不引 | **不引**，v2 评估 |

## 4. 实施 atomic 拆解（8 个原子 < 500 LoC each）

```
A1: ringHRW 数据结构 + HRW.Top3(prefix, accounts) 实现 (~250 LoC)
    - 复用 DR-009 A05a 已有 HRW spec 接口
    - 单测: 一致性 (新增账号仅 1/N 段被重分配) + top3 顺序稳定

A2: PrefixSegment + HasCacheBitmap + AttemptStats (~200 LoC)
    - bitmap 操作 + decay tick

A3: PASRSelector 实现 pool.Selector 接口 (~350 LoC)
    - schedule() 主流程, fallback path
    - feature flag 默认 false 与现 selector 共存

A4: cache 反馈循环 — 接 cachemetrics.ObserveByAccount 旁路 (~150 LoC)
    - 在 anthropic_sse / openai_sse 的 message_stop / [DONE] 回调里
      调 segment.afterResponse(idx, usage)

A5: 5min 老化 ticker goroutine (~100 LoC)
    - segment_table cleanup + AttemptStats decay
    - 关停时 graceful drain

A6: ring rebalance (account add/remove handler) (~250 LoC)
    - admin add/remove account → 扫 segment_table → 局部更新

A7: metrics 透出 expvar/prometheus (~150 LoC)
    - per-segment cache_hit_ratio
    - 段内 first-pick vs failover 比
    - 段过期次数 / 段重分配次数

A8: cutover atomic — feature flag 翻 default=true + 验证 (~50 LoC + 集成测)
    - shadow mode 跑 1 周 (新选择 + 旧选择都跑, 对比命中率), 然后切
```

## 5. 与现有代码集成

| 现有 | 改动 | 影响 |
|------|------|-----|
| `pool/selector.go` | 新增 `PASRSelector` 实现 `Selector` 接口；admin 配置 feature flag 切换 | 现有 `RoundRobinSelector` 不动 |
| `pool/sticky_store.go` | 不动；运行时不再被 PASR 主路径调用，但 cold-start hint 路径仍读 | Track B 保留 |
| `pool/db_sticky_store.go` | 不动 | 同上 |
| `cache_routing/prompt_hash.go` | 复用 `ComputePromptHash` 作为 PASR prefix hash | 0 改动 |
| `cachemetrics/cachemetrics.go` | A4 在 ObserveByAccount 旁观察 segment | 透传 |
| `binding/binding.go` | account add/remove hooks 触 ring.rebalance | 新 hook |

## 6. 风险

| 风险 | 缓解 |
|------|-----|
| HasCacheBitmap 误判 (vendor 缓存其实仍在但 bitmap 漏) | 段内 cache_read 实测命中后 bit 持续刷新；漏判最坏退化为段内一次 cache miss(可接受) |
| 段重分配抖动 (账号频繁 add/remove) | rebalance 加冷却(5min 内不二次 rebalance 同 prefix) |
| MAB 冷启动每段从 0 学 | thompson_sample 退化为均匀采样, 数 1-2 次后即稳 |
| ring seed 暴露 → 对手反向猜命中 | seed 由 admin 启动期注 + 每 30 天可轮换 |
| cache_creation 信号假阳 (vendor 偶发误标) | 用 K=3 段冗余天然吸收, 单错不致命 |

## 7. clean-room 边界

本算法设计完全 HUAKAI 自有，不读外部参考项目源码。HRW Rendezvous Hashing 是 1996 年公开学术算法，引用 Thaler & Ravishankar 1996 paper（业界共识，无 license 风险）。

允许读: AWS S3 / Riak / Cassandra 公开文档关于 K-replica 副本管理 (业界做法对照)；HUAKAI 内部 DR-009 A05a 已有 HRW spec。
不读: sub2api / new-api / one-api / portkey / litellm / CPA 任何源码。

## 8. 估时

- A1-A2 数据结构 + 单测：4-6 hours
- A3 selector 实现 + 集成测：4-6 hours
- A4-A5 反馈循环 + 老化：3-4 hours
- A6 rebalance：3 hours
- A7 metrics：2 hours
- A8 cutover 含 shadow mode 1 周观测：取决于真流量; 静态部分 < 1 day

合计 v1 全栈 PASR-lite ~3-4 工程日 (静态实现 + 单测) + 1 周 shadow mode 切换观测。

## 9. 等待

- codex 平行写 codex lane PASR-lite v2 plan (Agent 代理派发)
- 双 lane 对比 → synthesis
- Owner 拍板 synthesis → 立即开 A1
