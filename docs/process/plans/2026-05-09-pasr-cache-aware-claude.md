# 2026-05-09 PASR-lite cache-aware 调整 (Claude lane)

| 字段 | 值 |
| ---- | ---- |
| Owner directive | "5min/1h 缓存 + 账号并发限制 之前算法没考虑进去" → 选 A 套餐 (升级 PASR-lite, 不推倒) |
| Owner 拍板权 | "拍板你来决定" — CLAUDE.md #10 例外条款, 单 lane 决策 (codex lane 后台并行做 retro review 用, 不阻塞实施) |
| Lane | Claude (planner + executor 同一 session — Owner delegated) |
| 前置 | PASR-lite A1-A8 + main-wire M1-M7 + D/D2 vendor metric 已落地 (commit `4670b4e` HEAD) |

---

## 1. 问题陈述 (来自 Owner 关切)

PASR-lite 现有实施在两个维度跟 vendor 实际行为脱钩:

**维度 1 — 缓存 TTL 不对齐**
- 段表老化窗口 = 30min (codex synthesis D8 决策)
- Anthropic prompt cache default TTL = 5min (extended cache 才 1h)
- OpenAI prompt cache TTL = 5-10min
- 后果: 30min 内同 prefix 反复进来, PASR 坚持送 steward, 但 vendor cache 已掉, 全程 cold miss + 阻止其他账号 warm

**维度 2 — 选 ranking 不考虑并发 headroom**
- 现状: 段内 hasCache 优先 → tie-break LoadRate 最低 → 选成员
- LoadRate 已隐含 headroom (= 1 - current/max), 但只在 tie-break 用, 没参与主 score
- 后果: hasCache=true 但 LoadRate=0.95 的账号会被选, 而 hasCache=true + LoadRate=0.3 的同段成员排第二 — 浪费 headroom

**维度 3 — cache miss 无 demote 反馈**
- 现状: pasr_feedback.go 只在 cache_creation > 0 OR cache_read > 0 时更新段
- cache_creation=0 && cache_read=0 (即 miss) 是静默 no-op
- 后果: 段成员"标记 hasCache=true 后永远不会撤销", 即使 vendor cache 早掉、连续 N 次 miss, ranking 还在为它加分

---

## 2. Scope

### In-scope (A 套餐)
1. **A1**: SegmentTable aging 30min → 5min default; 加 `PrefixSegment.ExtendedCacheTTL` 字段, 非 0 用 1h; ticker 周期 5min → 1min (粒度跟新 aging 一致)
2. **A2**: pasr_selector.go candidates scoring 改 score-based: `score = localityBonus + headroomBonus`, headroom = (1 - LoadRate) * 0.3
3. **A3**: 段加 `MissCount [3]atomic.Uint32` 字段; pasr_feedback.go handle() 处理 miss path: cache_creation==0 && cache_read==0 → MissCount[idx]++, 达到 2 → 清 HasCache bit (demote)

### Out-of-scope (留 follow-up)
- ExtendedCacheTTL 设置入口 (谁判断该用 5min 还是 1h?) — 默认全部 5min, extended 标记从 ResolvedModel 流入是 follow-up; 本批先把字段 + aging 逻辑就位, 入口路径单独 atom
- LBTS 范式重写 (Owner 选 A 套餐, 拒推倒)
- MAB 子模块 (选项 4 的中层, 暂不做)
- 短 prompt miss 抑制 (短 system prompt 本来 vendor 不 cache, 不该 demote — 留 A3 follow-up)

---

## 3. Atom 分解 + 验收

| Atom | 范围 | 文件 | LoC est | 验证 | 估时 |
|---|---|---|---|---|---|
| **A1** | aging 默认 5min + ExtendedCacheTTL 字段 + ticker 1min + EvictExpired 用 effectiveMaxAge | prefix_segment.go + pasr_aging_worker.go | ~80 src + ~50 test | 单测: default 5min evict / extended=1h 时 30min 不 evict + 70min evict / ticker 周期 1min | 1.5h |
| **A2** | candidates score-based ranking (locality + headroom) | pasr_selector.go (Select 函数 §5-§6 重写) | ~50 src + ~60 test | 单测: 同 hasCache 成员中 LoadRate 低胜 / hasCache=false + headroom 高 vs hasCache=true + headroom 低 → locality 仍优先 / 全段 hasCache=false 时按 headroom 排 | 2h |
| **A3** | MissCount 字段 + handle() miss path + demote at N=2 | prefix_segment.go (MissCount + Demote 方法) + pasr_feedback.go (miss path) | ~70 src + ~70 test | 单测: 段成员 connection 1 次 miss → MissCount=1 / 2 次 miss → HasCache bit 清 + IncDemoted metric / cache_read 重置 MissCount | 2h |
| **A4 (sanity)** | 全 race test + lint + push | 无 src 改动 | - | go test -race ./... PASS / go vet clean | 0.5h |

**总: 3 atom + 1 sanity, ~6h, < 1 工作日**

依赖图:
- A1 ↔ A3 同文件 (prefix_segment.go), 串行 commit
- A2 不同文件, 可与 A1 / A3 并行写, 但同 session 单线串行
- 实施顺序: A1 → A2 → A3 → A4

---

## 4. Blast radius

- 影响 5 个文件: prefix_segment.go, pasr_aging_worker.go, pasr_selector.go, pasr_feedback.go, 各自 _test.go
- 不动 main.go wire / dispatcher / registry / handler — handler 不感知段表内部
- 默认 mode=default 时 PASR 整条不启用, 这批改动**对 default 流量零影响**
- shadow / canary / pasr-* mode 才走新 ranking + 新 aging — Owner SOP 路径上才看到差别

---

## 5. 风险 (≥4)

1. **5min aging 太激进, 段表频繁重建**: vendor cache 实际维持时间偶尔 > 5min (尾延迟客户), 段表过早 evict 会 cold-miss。 缓解: ExtendedCacheTTL 字段提供 1h 旁路; ticker 1min 粒度可在压测后调慢回 2min 不影响正确性。
2. **headroom 权重 0.3 调参没有数据支撑**: 选 0.3 是 claude lane 经验值; 太大破 locality, 太小不解决 Owner 关切。 缓解: 加 expvar metric 记录 ranking 决策因子拆分 (locality vs headroom 各占多少决定权), Owner SOP 期间观察后调整。
3. **MissCount 在并发下计数失真**: 多个并发请求同时 demote 同一成员可能多次清 bit (幂等无问题) 或在 demote 时新请求又 ++ count (race 但 atomic 保证最终一致)。 缓解: 用 atomic.Uint32 + 测试 -race。
4. **demote 后 ranking 没回路 promote**: 一旦 HasCache bit 清, 该成员要重新通过 cache_creation observation 才能再 set。 这是设计意图 (vendor 真重新 cache 才信得过), 但若该成员段内排序永远 LoadRate 最低, 会冷落它。 缓解: scheduleHRWFullRing 路径仍可能选到它 → cache_creation 重新触发 → bit 重 set。 自然恢复链路完整。
5. **ExtendedCacheTTL 入口缺失**: 本批仅加字段, 谁来 set 它没接通 — 默认全 5min, 没有 1h 可用。 接 ResolvedModel.CacheCapability 是 follow-up atom (估 1h 单独做)。 在落 production 前必须接通, 否则 extended cache 客户场景被误杀。 这是 Owner 上线前的硬决策点。

---

## 6. Decision points (我自己 delegate 拍板)

| ID | 选项 | 我选 | 理由 |
|---|---|---|---|
| D1 | aging 默认值 5min vs 4min | 5min | 跟 Anthropic default TTL 精确对齐, 不预留 buffer (ticker 1min 粒度足够减小 overshoot) |
| D2 | ticker 周期 1min vs 30s | 1min | 30s 太频繁, 每 30s 全表扫描 + LRU evict 占 CPU; 1min 平衡开销和 freshness |
| D3 | headroom 权重 | 0.3 | hasCache 是强信号 (vendor 已经 warm), 不应被 headroom 翻盘; 0.3 让 50% headroom 差距能盖过纯 locality tie, 但 hasCache=true vs hasCache=false 永远 locality 胜 |
| D4 | miss demote 阈值 N | 2 | 1 次 miss 太敏感 (单次抖动就 demote); 3 次太钝 (浪费机会); 2 次平衡 |
| D5 | ExtendedCacheTTL 入口 | 留 follow-up | 本批不接通, 仅加字段; production 上线前必加 |

---

## 7. Codex lane 角色 (后台跑)

CLAUDE.md #10 + memory `feedback_no_skipping_codex_lane` 要求 codex 平行参与, 但 Owner explicit "拍板你来决定" 是 #10 例外。 折中: codex lane 后台起草不阻塞我实施, 它的输出当 retro review — 我做完 A1-A3 后看 codex 草案, 若发现我漏了关键风险/atom 就追加; 若 codex 推了完全不同方案 (e.g. 推倒重写), surface 给 Owner 决定是否再走一轮。

---

## 8. 验收测试矩阵 (per atom 已含, 此处汇总)

| # | 检查 | 期望 |
|---|---|---|
| 1 | aging 默认值 | DefaultSegmentMaxAge == 5*time.Minute |
| 2 | aging extended | ExtendedCacheTTL=1h 时 30min 后 LastReadAt 不 evict |
| 3 | aging extended evict | ExtendedCacheTTL=1h 时 70min 后 evict |
| 4 | aging worker 周期 | DefaultAgingInterval == 1*time.Minute |
| 5 | ranking locality 强势 | hasCache=true 段员永远胜过 hasCache=false 同段员, 即使 LoadRate 高 |
| 6 | ranking headroom 决胜 | 同 hasCache 状态时, LoadRate 低胜 (headroom 高) |
| 7 | miss++ | cache_creation=0 && cache_read=0 → MissCount[idx]++ |
| 8 | miss demote | MissCount[idx] >= 2 → HasCache bit 清 |
| 9 | miss reset | cache_read > 0 → MissCount[idx] 归 0 |
| 10 | demote metric | demote 触发 IncDemoted (新 metric) |
| 11 | race-clean | go test -race ./internal/pool/... PASS |
| 12 | 全 backend | go test -race ./... PASS |

---

## 9. Rollout

- 本批合并到 claude/phase-1 后, 默认 mode=default 不启用 PASR, 上线零风险
- Owner 本机 SOP 跑到 shadow 阶段, /debug/vars 看新 metric (miss / demote / locality_vs_headroom 决策切片) 数据正常
- canary 5% → strict 6 阶段不变, 只是数据更接近 vendor 真实 cache 行为
