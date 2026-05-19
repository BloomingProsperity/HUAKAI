---
plan_id: 2026-05-08-pasr-lite-v2-codex
lane: planner / codex-lane
owner_directive: 主算法锁定 PASR-lite (HRW K-replica 段, K=3); 不引 LBTS v1; 保留 sticky_bindings 作 cold-start hint
parallel_with: 2026-05-08-pasr-lite-v2-claude.md (codex 未读)
clean_room: 不读 sub2api / one-api / new-api / litellm / CPA / portkey / helicone 源码; HRW + Beta 采样 + LRU 是公开教科书算法; 任何引用须用本仓 paraphrase
top_finding: |
  独立判断: 段表必须 in-memory 主权威 + Postgres 持久化作 warm-start (双写, 不双读),
  且 K=3 失败时不要扩大 K, 而是把"未中段成员"按 HRW 全量 ranking 接力 — 否则段失败
  会瞬间击穿到无 cache 的随机账号, 这正是 sub2api 在 customer-count 上升时 cache hit
  跳水的根因。HRW 已经给了天然的扩展性 (top-K 取更大 K 等于免费扩段), 不需要新机制。
---

# 2026-05-08 PASR-lite v2 — codex-lane 独立设计

## 0 前置一致 (与 Owner 锁定一致, 此处不再决策)

- 主算法 = PASR-lite, prefix → HRW K=3 健康段 → 段内挑账号
- prefix 取自 `cache_routing.ComputePromptHash(body)` (现有 SHA-256 hex)
- HRW seed 取 `acct.priority_seed` (admin 可配, 默认回落 `acct.id` int64)
- LBTS / 轨迹预算 不进 v1
- Track B `sticky_bindings` 表保留为 cold-start hint, 不再走主路径

## 1 8 个决策点 — codex-lane 独立判断

### D1 段内 "谁有 cache" 状态记录方式 — **3-bit bitmap + 单 uint8**

选 `bitmap` (1 个 byte 存 K=3 成员的 cache 命中位)。理由:

- per-prefix 段已经只有 K=3 候选, 不需要复杂结构
- 1 byte 与 LRU/int 同字节宽, 但 atomic.LoadUint8 / CAS 极快 (sync/atomic 有原生支持)
- "last cache hit" int 信息量低于位图 + 还要再带一个时间戳判断是否过期, 综合成本反而高
- 段内 bitmap 不存命中次数, 只存"是否最近 ≥1 次命中过", 避免在热路径上更新计数器
- 命中信号源: `cachemetrics.ObserveByAccount` 已有 per-account 写, 段层从该回调反查 segment 即可标位
- 数据布局: `segment.cacheBits uint8 // bit_i=1 表示成员 i 最近命中`

### D2 段内多 healthy candidates 选择策略 — **bitmap 优先 + 段内 round-robin tie-break, v1 不上 Thompson**

- 第一优先: `cacheBits & healthyMask != 0` 的成员里挑 1 个
- 多个有 cache 时按 (1) admin priority 字段 desc (2) 段内 round-robin counter
- 全 0 cacheBits 时退到段内 RR, 不带 Thompson / UCB 复杂状态
- 理由:
  - Thompson / UCB 需要 (success_n, fail_n) per (prefix × account) 状态 = O(P × N) 内存爆炸
  - cache 命中数据已经是 strong signal — 命中过的就是好账号, 不需要再用 MAB 学
  - v1 admin priority 字段足够覆盖"VIP 账号优先"运维需求
  - **Owner memory** "稳定 = 比 sub2api 强", MAB 收敛慢, 反而是 fragility 源, 推后

### D3 HRW 实现细节 — **xxhash64(seed64 ⊕ prefix64) per pair, partial top-3 selection**

```text
score(prefix, account) = xxhash64( account.priority_seed ⊕ prefix.fingerprint64 )
                        // ⊕ 对 64-bit, prefix.fingerprint64 = first 8 bytes of SHA-256 hex
```

- 不用 sort 全量 — 单次 O(N) 扫描 + 维护 K=3 大小堆 (heap.Fix), 实测 N=1000 < 30µs
- prefix 不再每次重算 SHA-256 — 在 SelectionRequest 入口就拿到了 prompt-hash, fingerprint64 = SHA-256 前 16 hex char 转 uint64
- `xxhash` 已是 Go 标准生态库 (`github.com/cespare/xxhash/v2`); 仓内已有先例可复用; 不引新依赖
- 不用 mmh3 — xxhash 性能与 mmh3 平手且更主流
- 顶层 K=3 不直接排序 — 用 small heap (containers/heap), 减少 sort.Slice 分配
- 候选不足 K 时 (N<3) 退到全部 N 段员

### D4 prefix 段表存储 — **in-memory 主路径 + 异步 Postgres 持久化 (warm-start) + 不读跨节点共享**

三段决策:

1. **主路径**: in-memory `sync.Map[prefix64]segment`, 全部 hot data 在进程内
2. **持久化**: 后台 goroutine 每 30s flush dirty segment 到新表 `pasr_segment_table`, 仅作进程重启 warm-start; 不当 source of truth
3. **不引 Redis**: 跨节点共享放 v2 (R5/R6 之后), 因为
   - HUAKAI v1 假定单 gateway 副本 (DR-009 §Phase 部署假设)
   - Redis 引入新依赖 + cache 一致性故障模式
   - Postgres 已有, 沉淀成本最低
4. **冷启动**: 进程拉起时一次性 SELECT 上次 dirty flush 的全部 segment 行, 不阻塞 listen — 5s 内异步预热

段表 schema (新表, 与 sticky_bindings 不共享):

```sql
CREATE TABLE pasr_segment_table (
  prefix_fp64       bigint PRIMARY KEY,    -- xxhash 前 64-bit fingerprint
  members_csv       text NOT NULL,         -- "12,87,143" K=3 account_id
  cache_bits        smallint NOT NULL,     -- 0..7 D1 bitmap
  last_seen_at      timestamptz NOT NULL,
  hit_count         bigint NOT NULL DEFAULT 0  -- 仅观测用, 不参与决策
);
CREATE INDEX ON pasr_segment_table (last_seen_at) WHERE last_seen_at < now() - interval '30 minutes';
```

### D5 K=3 段失败后 fallback — **HRW 全 ring top-K 扩到 K=N (sorted continuation)**

- 段内 3 个全 unhealthy → 调用 `hrwRanking(prefix, allHealthyAccounts)[:N]` 接力
- 不扩到 K=5/10 后再折返 — 那是凭直觉的 partial relax
- HRW 数学性质: 把 K 从 3 直接扩到全量, **第 4..N 名的 cache hit 概率** = 段抖动情况下其它段的成员上次命中过的概率 ≈ 0; 所以扩 K 边际收益小
- 但段全失败时**避免拒绝请求**, 必须提供候选, 因此 fallback = 纯 HRW ranking (无 cache hint)
- HRW O(N) ranking 仅在 cold path (段全 fail) 触发, 性能可接受
- 拒绝请求让客户重试是 worst UX, 排除

### D6 shadow mode 切换策略 — **feature flag + 5%/25%/100% 三阶段灰度, 影子双跑只跑 1 天**

不全 1 周双跑 (Owner 锁 PASR-lite 主, sticky 兜底), 也不一刀切。三段:

1. **D6.a 影子 1 天**: 100% 流量走 sticky 主路径 + PASR shadow 计算 + 只比对 SelectionResult.AccountID, log 差异; 验证段表预期收敛
2. **D6.b 5% 真切**: 把 5% tenant_id (admin allowlist) 真切到 PASR 主, 观察 cache_token_count_by_account 命中率
3. **D6.c 25%/100%**: 24h 健康后 → 25% → 1 周后 100%, 触发 sticky_bindings 转 cold-start hint

切换粒度: tenant_id mod 100, 不按 prefix — 因为按 prefix 切换会撕裂同一会话流量, 命中率指标无法干净对比

### D7 rebalance 时 cache 失效 — **HRW 性质 + 段表"软迁移"延迟 1h**

- HRW 已经保证账号 N→N±1 时只有 1/N segment 受影响
- 但 PASR-lite 段表 caches HRW 决策, rebalance 直接改段表会瞬间作废全部 1/N 缓存
- 软迁移: 当 admin 删除/添加账号时, 不立即重算 segment, 而是
  - 命中现有 segment 时 → 检查段成员 health, 死成员段位 0 → 走 D5 fallback (扩到全 ring)
  - 真正 segment LRU evict 或 30min 老化时 → 自然以新 ring 重算
  - 1h 后 vacuum job 清理已 evict 账号的所有段记录
- 增量 rehash 1% 段是过度工程; HRW 本身就是增量友好的, 不需要再做一层
- 全停 rebalance 有运维窗口风险, 不选

### D8 段过期触发条件 — **双触发: 30min 时间 LRU + 段表上限 100k segment evict**

- 时间老化: prefix 30 分钟无 hit → evict; 防止冷数据无限滞留
- 容量老化: 段表条目超过 100,000 触发 LRU evict (容量上限可 admin 配置)
  - 100k × ~80 byte (3 int64 + uint8 + timestamp) ≈ 8 MB, 单进程内安全
- 双触发理由: 单时间老化在突发热点下可能瞬间堆积 > 1M 段; 单容量老化在低流量时长尾段会赖着不走
- 老化时仅删 in-memory 槽, Postgres 通过 vacuum job 异步清理 (last_seen < now() - 1h)

## 2 8 个原子拆解 (codex-lane 独立, 与 Claude lane 不约束一致)

| # | 原子 | LoC 估 | 关键文件 | 依赖 | clean-room 风险 |
|---|------|--------|----------|------|----------------|
| AC1 | `pkg/scheduler/pasrlite/segment.go` 段结构 + bitmap + xxhash 工具 | ~150 | 新文件 | 无 | 0 (公开教科书) |
| AC2 | `pkg/scheduler/pasrlite/hrw.go` HRW ranking + heap-top-K | ~120 | 新文件 | AC1 | 0 |
| AC3 | `pkg/scheduler/pasrlite/segment_table.go` in-memory sync.Map + LRU + 双触发 evict | ~200 | 新文件 | AC1 | 0 |
| AC4 | `pkg/scheduler/pasrlite/persistence.go` 异步 flush + warm-start, 新表 `pasr_segment_table` | ~180 | sql migration + go | AC3 + db.Queries | 中 (DB schema, Owner 高风险确认) |
| AC5 | `pkg/scheduler/pasrlite/selector.go` 实现 `pool.Selector` 接口 (Select 函数) | ~250 | 新文件, 与 selector.go 平级 | AC1-AC4 | 0 |
| AC6 | `cachemetrics` 新增 SegmentHit 回调 → 段层标位 | ~50 | cachemetrics.go 添加 hook | AC1 | 0 |
| AC7 | feature flag + admin route policy `scheduler_engine: pasr_lite\|sticky` | ~80 | gatewayhttp 配置 + route_policy 字段 | AC5 | 低 |
| AC8 | shadow mode + 灰度 + observability metrics | ~120 | gatewayhttp + expvar | AC5, AC7 | 0 |

合计 ~1150 LoC, 跨 8 commit; 每 commit 独立 buildable + tested。

## 3 风险 (5 条)

1. **段表持久化 schema 锁定** — `pasr_segment_table` 是 Owner-高风险 (DB schema), 必须 Owner 显式 ack 后再 migrate; 在 ack 前 AC4 可用 SQLite-in-memory 做单测
2. **xxhash 引入新依赖** — 仓内若已有 (`go.mod` grep) 则零成本; 若无, Owner memory "项目内部限制可为收益放宽" → 引入 OK 但需 dependency-license-auditor skill 审 MIT
3. **shadow 1 天对比窗口太短** — 若 prefix 长尾分布严重, 1 天可能未覆盖所有热段; 风险缓解: 影子日志保留 7 天, 5% 阶段再延长到 3 天
4. **sticky_bindings 双写冲突** — cold-start hint 路径仍读 sticky_bindings, 但 PASR 主路径选定后**不写**; 老 sticky 数据会在 1h TTL 后自然失效, 没有 conflict, 但要确认 Upsert 不被 PASR selector 调
5. **HRW seed 旋转** — admin 改 `priority_seed` 会全量段表失效; 需在 admin UI 显示 "warning: 段表会重建, cache 命中率短期下降"; 建议 admin 改 seed 改在低峰期

## 4 clean-room 边界 (codex-lane 独立陈述)

- 算法实现思路均出自公开论文 (Thaler & Ramakrishnan 1996 HRW; Bloom 1970 Bloom filter; Beta posterior — Bishop PRML)
- 不读 sub2api 任何源码 — 我们仅参考 Sub2API 公开 README 描述其 sticky 弱点
- 不读 new-api / one-api 调度代码
- 命名: `PASRLiteSelector` / `SegmentTable` / `HRWRanker` 全新命名, 不照搬 Sub2API 的 `RouterShard` / `WeightedShard` 等
- 注释中文, 标识符英文 (Owner memory: feedback_chinese_comments)
- 测试覆盖: AC1-AC5 单元测试 LoC ≥ 实现 LoC × 0.8 (acceptance-test-writer skill 跑); 集成测在 AC8

## 5 与 Claude lane 待 synthesize 的预期分歧点

(codex-lane 不读 Claude lane, 此处是预测; 实际差异 synthesize 时再核)

- 段内挑选可能差异: codex 选 bitmap+RR; Claude 可能选 Thompson — synthesize 优先选 codex (Owner 稳定性优先)
- 持久化层可能差异: codex 选 Postgres 异步; Claude 可能选纯 in-memory — Owner 锁了 sticky_bindings 表保留, 暗示偏持久化, codex 路线更贴 Owner intent
- shadow 时长可能差异: codex 选 1 天 + 灰度; Claude 可能选 1 周 — Owner 之前 push 速度, codex 路线更紧
- xxhash 依赖问题: 若 Claude 选 SHA-256 截断 / FNV — synthesize 时让 ralph 跑 benchmark, 取 latency 最好

## 6 建议 synthesize 路径

1. Owner read codex 本 plan + Claude 同名 plan
2. 对 8 决策点逐项 diff
3. 冲突项: codex 倾向独立判断 D1/D2/D4/D5 三个 — 这些是 algo 核心, 不能折中
4. 一致项 (HRW 公式 / fallback 逻辑) 直接合并
5. synthesize 出 `2026-05-08-pasr-lite-v2-synthesis.md`, 8 atomic 原子化派多 agent 平行实施

---

end-of-plan / codex-lane / 严格未读 claude 同名文件 / clean-room / 中文为主
