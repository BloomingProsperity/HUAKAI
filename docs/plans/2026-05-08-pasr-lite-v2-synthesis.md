# 2026-05-08 PASR-lite v2 — Synthesis (claude × codex)

| 字段 | 值 |
| ---- | ---- |
| Lane A | [pasr-lite-v2-claude.md](2026-05-08-pasr-lite-v2-claude.md) |
| Lane B | [pasr-lite-v2-codex.md](2026-05-08-pasr-lite-v2-codex.md) |
| Owner 已锁 | K=3 / 不引 LBTS / 保留 Track B sticky_bindings 作 cold-start hint / 用 HRW |
| Status | synthesis 完成, 待 Owner 拍板 → 立即开 A1 |

## 1. 双 lane 一致项（不再决策）

| 决策项 | 共识 |
| ----- | --- |
| 主算法 | PASR-lite (HRW K=3 段) |
| HRW score 函数 | `hash(seed || prefix || account_id)` 取 top-3, partial top-K (不全排序) |
| 段内 cache 状态记录 | **3-bit bitmap per prefix** (一致) |
| 段过期 | 30min 无命中老化 + (codex) 段表 100k LRU evict 复合触发 |
| K=3 失败时 fallback | **不扩 K**，直接 HRW 全 ring 排序接力 (codex 主张, claude 同意) |
| Track B 表保留作 cold-start hint | 一致 |
| Bitmap 更新只在 `cache_creation_input_tokens > 0` 触发 | 一致 |
| 不引外部参考源码 | 一致 |
| Thompson MAB 不在 v1 | 一致, v2 段内 tie-break 备选 |

## 2. 双 lane 分歧项（synthesis 选择）

### D2 段内 tie-break — codex win (RR > Thompson)
- claude: bitmap 优先 + Thompson sampling Beta(α,β) 在 cached set 内 tie-break
- codex: bitmap 优先 + 段内 round-robin tie-break, v1 不引 MAB
- **选 codex**: K=3 段只有 3 个 arm, MAB 收敛意义有限; RR 更简单且足以工作; Thompson 留 v2 复杂度盲打。

### D4 持久化拓扑 — codex win (in-memory 权威 + PG async warm-start)
- claude: in-memory only 起步, 持久化未明
- codex: **in-memory 主路径权威, Postgres async best-effort 1帧/30s 写, 重启时一次性 warm-start, 运行时不读 PG**
- **选 codex**: 关键拓扑创新点。Track B 现状是 PG 同步读 = 客户增长时延迟跳水(memory project_sub2api_scaling_bottleneck)。PASR-lite 必须把 hot path 完全脱离 DB; PG 仅 warm-start 工具。这是与 Track B 最关键的范式差异。

### D5 K=3 失败 fallback — codex win (HRW 全 ring 接力)
- claude: ring round-robin
- codex: **HRW 全 ring 排序接力(同算法延伸)**, 仅在段全 unhealthy 时触发(低频), 性能可接受
- **选 codex**: HRW 全 ring 是同质算法; ring round-robin 与主路径不一致。Cold path 性能差也无所谓。免费消除"段成员被全 cooldown"livelock 风险。

### D6 shadow mode 周期 — claude win (1 周 > 1 天)
- claude: shadow 双跑 1 周对比
- codex: feature flag 5%/25%/100% 三阶段, 双跑 1 天
- **选 claude**: 1 天太短不能覆盖 5min cache TTL × N 个生命周期完整观测 + 工作日 vs 周末流量差异。1 周可见 cache_hit_ratio 真稳态。灰度 5/25/100% 可保留作切换实施细节。
- **synthesis: 双跑观测 7 天 + 灰度 0% → 5% → 25% → 100% 切换**

### D7 rebalance 抖动缓解 — codex 增强
- claude: 5min 冷却避免二次 rebalance
- codex: **段表"软迁移"延迟 1h** (新成员加入段后 1h 内 bitmap 仍记旧成员的 cache 状态, 缓慢 fade)
- **选 codex 增强**: 软迁移期间双倍 bitmap 内存代价小但避免账号增减抖动期 cache miss 雪崩。

## 3. 最终设计（synthesis 后）

```
数据结构:
  AccountRing { accounts, seed64 }                 // 全局 HRW ring
  PrefixSegment { hash, members[3], bitmap u8,
                  lastReadAt, lastWriteAt }        // 段元数据
  segmentTable: map<prefix_hash → PrefixSegment>   // in-memory 主权威
  
持久化:
  PG 表 pasr_segments (prefix_hash, members, bitmap, last_read_at)
  写: async 1帧/30s flush  (best-effort, 不阻塞请求)
  读: 仅启动时 warm-start 一次, 之后不读

热路径 schedule(req): O(1)
  prefix = hash_prefix(req)
  seg = segmentTable[prefix]              // miss → 新建段 (HRW top-3)
  candidates = [i for i in 0..2 if members[i].healthy && load < 0.95]
  if empty: fallback → HRW 全 ring top-N 接力
  cached = [i in candidates if bitmap & (1<<i)]
  if cached not empty:
    chosen = round_robin(cached)
  else:
    chosen = round_robin(candidates)
  return members[chosen]

after_response:
  i = members.index(acc)
  if cache_creation > 0: bitmap |= (1<<i)
  if cache_read > 0: lastReadAt = now
  if vendor 5xx / abnormal: members[i].health -= step

冷路径 (5min ticker):
  expire segments where now - lastReadAt > 30min
  evict LRU when |segmentTable| > 100k
  
冷路径 (admin add/remove account):
  affected = HRW.affected_segments(account)
  for prefix in affected:
    new_top3 = HRW.top3(prefix, accounts')
    soft_migrate(seg, new_top3)   // 1h 内保旧 bitmap, 之后 reset
```

## 4. 8 atomic 拆解（合并 claude + codex）

```
A1: HRW + AccountRing 数据结构 + xxhash64 score 函数 (~250 LoC)
    复用 DR-009 A05a 接口形态
    单测: 一致性 / top-3 顺序稳定 / 1/N 段重分配性质

A2: PrefixSegment + bitmap 操作 + LRU evict (~250 LoC)

A3: PASRSelector 实现 pool.Selector 接口, in-memory 主路径 (~350 LoC)
    feature flag 与 DefaultSelector 共存
    fallback 全 ring HRW 接力路径

A4: cache 反馈循环 — anthropic_sse / openai_sse 在 message_stop 调
    segment.afterResponse(idx, usage) (~150 LoC)

A5: 老化 ticker + LRU evict goroutine (~150 LoC)
    30min 时间触发 + 段表上限 100k

A6: PG 异步持久化 (writeback flush 1帧/30s + warm-start on boot)
    (~300 LoC)
    新增 db query: pasr_segments_load_all / pasr_segments_upsert_batch

A7: rebalance handler (account add/remove) + 软迁移 1h (~250 LoC)
    HRW.affected_segments + soft_migrate

A8: metrics 透出 (per-segment cache_hit_ratio, first-pick vs failover,
    段过期/重分配率) + cutover atomic (feature flag + shadow 7d) (~200 LoC)
```

合计 ~1900 LoC, ~3-4 工程日静态实现 + 7 天 shadow 切换观测。

## 5. 风险（双 lane 合并去重）

| 风险 | 缓解 |
|------|-----|
| Bitmap 误判 (vendor cache 实际仍在但 bitmap 漏) | 段内 cache_read 实测命中后 bit 持续刷新, 漏判退化为段内一次 cache miss(可接受) |
| 段重分配 livelock (账号频繁增减) | 软迁移 1h + rebalance 5min 冷却 |
| In-memory 重启失忆 → cold start cache miss 风暴 | PG warm-start + Track B 旧 sticky_bindings 作 cold hint |
| HRW seed 暴露 → 对手反向猜命中 | seed 由 admin 启动期注入, 30 天可轮换 |
| segmentTable 内存爆表 | LRU 100k 段 evict + 段过期 30min |
| K=3 段全 unhealthy livelock | HRW 全 ring 接力 fallback 自动 ramp 候选集 |
| MAB v1 不上 → 段内选择 RR 不学 | v2 评估 Thompson; v1 RR 已优于"总试 ring[0]" |
| Shadow mode 7d 期间 cache hit 漂移 | 双跑期间 metric 看新选 vs 旧选差; ≥ +20% 才切 |

## 6. clean-room 边界

不读外部参考项目源码 (sub2api / one-api / new-api / litellm / portkey / CPA)。

引用合规:
- HRW Rendezvous Hashing — Thaler & Ravishankar 1996, 学术算法, 无 license 风险
- xxhash64 — public domain
- AWS S3 / Riak / Cassandra K-replica 文档 — 公开行业做法对照, 不读源
- HUAKAI DR-009 A05a HRW spec — 仓库内已有, 直接复用

## 7. 决策矩阵

| 项 | claude | codex | synthesis 选 |
|----|--------|-------|--------------|
| D1 段内 cache 记录 | bitmap | bitmap | **bitmap** (一致) |
| D2 段内 tie-break | Thompson | RR | **RR (codex)** |
| D3 HRW score | hash(seed‖prefix‖acc) | xxhash64 | **xxhash64 (codex 选具体哈希函数)** |
| D4 持久化 | in-memory | in-mem 主 + PG async | **codex (PG warm-start)** |
| D5 K=3 失败 fallback | ring RR | HRW 全 ring | **codex (HRW 接力)** |
| D6 shadow 周期 | 1 周 | 1 天 | **claude (7d) + codex 灰度** |
| D7 rebalance | 5min 冷却 | 软迁移 1h | **合并 (5min 冷却 + 1h 软迁移)** |
| D8 段过期 | 30min 时间 | 时间 + LRU 100k | **codex 双触发** |

## 8. 待 Owner 拍板

如 synthesis 整体 OK, 立即开 A1 (HRW + AccountRing 数据结构, ~250 LoC, 半天)。

如 Owner 想调整某项 (如 K 改 2, shadow 改 3 天, 或先做 D4 PG 持久化原子), surface 给我即可, 我重排 atomic 顺序。
