# 2026-05-08 PASR-lite Main-Wire Synthesis (Critic Lane)

## 1. 元信息

| 字段 | 值 |
| --- | --- |
| 日期 | 2026-05-08 |
| Lane | synthesis (fresh context, 第三方 critic, opus) |
| 关联 | `docs/plans/2026-05-08-pasr-mainwire-claude.md` (545 行) + `docs/plans/2026-05-08-pasr-mainwire-codex.md` (489 行) |
| Owner directive | 选项 3 = "暂停 A6/A7, 先做主线集成 + shadow 比对验证" |
| upstream 已 green | A1-A5 + A8 |
| upstream 暂停 | A6 (PG warm-start), A7 (rebalance handler) |
| 风险等级 | shadow=MEDIUM；canary/full=**HIGH**（codex M3 揭示的 slot acquisition gap 必须先补） |
| 决策点合并后总数 | 6 (claude 4 + codex 6, 去重 + critic 新增 2 = 6, 见 §5) |
| 风险登记总数 | 14 (claude 10 + codex 10, 去重融合 + critic 4 盲点 = 14, 见 §6) |

---

## 2. 共识区 (CONSENSUS)

两 lane 在以下设计选择上高度一致, 这些可视为既定基线, 执行时不再讨论：

1. **`deps.selector` 改为 `pool.Selector` 接口**, 不在 handler 层暴露 PASR 分支 — 两 lane 一致 (claude.md:62, codex.md:39-82)。理由: `chat_completions_handler.go:36` 已经接 `pool.Selector`, 是天然杠杆点。
2. **新增 `SelectorDispatcher` concrete 实现 `pool.Selector`**, 负责 mode dispatch + shadow async + canary sampling 全部逻辑 — 两 lane 一致 (claude.md:165, codex.md:80-82)。
3. **feature flag 用 typed config 走 `internal/config`, 不在 main.go 散读 `os.Getenv`** — 两 lane 一致 (claude.md:201, codex.md:92-94)。
4. **shadow PASR 必须 `Claims=nil`, 是硬不变量** — 两 lane 一致 (claude.md:230, codex.md:155-170)。codex 进一步加了 dispatcher 强制 `shadowReq.ClaimID=0` (codex.md:325-326), claude 默认依赖 PASRSelector 内部 `if p.claims != nil` 守门 (pasr_selector.go:249) — 两层保险更稳, 见分歧 §3.3。
5. **shadow goroutine panic 必须 recover, 不传染主路径** — 两 lane 一致 (claude.md:233, codex.md:410)。
6. **canary 用 deterministic hash bucket (FNV-1a + SessionHash 优先), 不用 random** — 两 lane 一致 (claude.md:167, codex.md:351-355)。
7. **rollback 第一版 = ENV + 重启, 不实现 SIGHUP 热切** — 两 lane 一致 (claude.md:213, codex.md:466-468)。
8. **PASRAgingWorker 跟 signal ctx 退出 + `defer Stop()` 兜底** — 两 lane 一致 (claude.md:66, codex.md:215-216)。
9. **段表 (SegmentTable) 是进程级共享, `RegisterPASRCacheFeedback` 启动期注册一次** — 两 lane 一致 (claude.md:69, codex.md:393-396)。
10. **不做 A6/A7, 不改 schema, 不引外部参考源码** — 两 lane 一致 (claude.md:50, codex.md:31-33)。

> **共识强度**: 10 条核心架构决策完全一致。problem space 约束 (Selector 接口已就位、PASR-lite 已落、Owner 路线明确) 把方案空间收紧, 因此分歧主要在 **安全门粒度** 和 **ring 数据来源** 上, 不在顶层架构。

---

## 3. 分歧区 (DIVERGENCE)

### 3.1 PASR actual 是否要补 SlotManager? (**最重要分歧**)

| Lane | 立场 |
| --- | --- |
| claude | **没看到这个问题**。整篇 plan 把 PASR 当 drop-in selector, M5 wiring 直接构造 `pasrSelector := pool.NewPASRSelector(...)` 给 canary/full 用 (claude.md:64), 没有 slot acquisition 步骤。 |
| codex | **核心安全门, M3 atom 专门补**。codex.md:147-170 明确指出当前 `PASRSelector.acquireAndReturn` 只写 claim 不动 `provider_accounts.in_flight_count` 和 `pool_slot_acquisitions`, 直接拿 PASR 当 actual selector 会破并发限制 (codex.md:155 + R-PASR-MW-001)。 |

**Critic 判断 (HIGH confidence)**: codex 完全正确, claude **致命漏洞**。证据:
- `selector.go:204-222` 显示 DefaultSelector `tryLayer` 顺序: `slots.Acquire(ctx, account, req)` → `claims.WriteAcquisition`, slot 是 atom 一部分。
- `pasr_selector.go:244-258` `acquireAndReturn` 完全跳过 slots.Acquire, 直接 `uuid.New() + claims.WriteAcquisition`。
- `db_slot_manager.go:74-103` 显示 slot acquire = 增 `in_flight_count` + 插 `pool_slot_acquisitions` 行 — 这两个写入是 cap_concurrency 强制和 settle 配对的根。
- 跳过 slot 后果: PASR 走 actual 路径 → account 实际并发突破 cap_concurrency, settler 找不到 acquisition row, billing/release 链断裂。

**Synthesis 推荐**: 采用 codex M3 作为强制门槛 — **canary 任何百分比上线前必须先合 M3**。M3 不是"可选优化", 是**安全门**。

### 3.2 AccountRing 从哪儿来? 启动期全局 vs 请求级 vs 5min ticker

| Lane | 方案 |
| --- | --- |
| claude | **新增 RingProvider 模块 + 5min ticker 重建 + 新 SQL `ListAllEligibleAccountsForRingBuild`**。per (tenant, pool_group) 分组, atomic.Pointer hot-swap (claude.md:301-358)。M2 LoC ~220, 含 sqlc + migration 评审。Owner D3 拍板。 |
| codex | **每请求从 `AccountSource.ListAccounts(ctx, req)` 现成 snapshot 构造 ring** (codex.md:378-387)。不加新 SQL, 不加 ticker, 不加 RingProvider。复用已有 `ListEligibleAccountsByPoolGroup`。 |

**Critic 判断 (HIGH confidence)**: codex 方案更优。证据:
1. `db_account_source.go:33-66` 显示 `DBAccountSource.ListAccounts` 已经按 (TenantID, PoolGroupID) 拉到 eligible accounts — PASRSelector 的 hot path **第一步就调它** (`pasr_selector.go:83`)。再多一次 ticker 重建就是冗余 round-trip。
2. claude 的 RingProvider 引入 4 项新风险 (R2 DB 抖动, R9 跨租户泄漏, R10 新 SQL 走漏审, "保留旧快照"策略复杂), 全部源于"全局 ring"自带的设计税。
3. codex 用 request-scoped ring 天然避开租户隔离风险 (因为 ListAccounts 已经过滤过)。
4. claude 的"per (tenant, pool_group) ring map"实质上是把 ListAccounts 的结果用另一份缓存复刻一遍, 但 ring 唯一用途是 HRW 排序 — 这步本身 O(N log K) 用 ring.Accounts 即可, 不需要 5min 缓存。
5. claude.md:357 自己也承认"两条路都得加 SQL", codex 路径不加 SQL, 直接复用既有, 是真"省"。

**Synthesis 推荐**: **采纳 codex 方案**。每请求 build ring。性能成本: HRW K=3 计算 O(N) 仅在 SegmentTable cold-miss 时跑一次 (LookupOrCreate 内已经做), 段命中后是 O(K=3) — 不构成 hot path 瓶颈。**例外**: 如果 ListAccounts 在某些 pool 返回 N>500 账号, ring 构造可能引入 µs 级开销, 这是 follow-up 优化, 不是 main-wire blocker。

> claude RingProvider 模块**整体废弃**。M2 atom 替换为 codex M4 "PASRSelector 接受 request-scoped ring"。

### 3.3 Shadow PASR 是独立实例 vs 同实例 + 选择性绕开 Claims

| Lane | 方案 |
| --- | --- |
| claude | **独立两个 PASRSelector 实例**, shadow 那个 `Claims=nil` (claude.md:381 + D1 推荐 A)。共享同一 SegmentTable。 |
| codex | **同样独立实例**, 进一步要求 shadow 实例 `Slots=nil` + dispatcher 强制 `shadowReq.ClaimID = 0` (codex.md:325-326, 168)。 |

**Critic 判断 (HIGH confidence)**: 两 lane 方案本质一致, 但 codex 比 claude 多两层防御纵深:
- **第一层 (两 lane 一致)**: `Claims=nil` → `acquireAndReturn` 内部短路 (pasr_selector.go:249)
- **第二层 (仅 codex)**: `Slots=nil` → 等 M3 落地后, slot acquire 也短路
- **第三层 (仅 codex)**: dispatcher 拷贝 req 把 `ClaimID=0` 抹掉 → 即使有人误把 actual 实例传成 shadow, 也写不进 billing_claims (因为 `req.ClaimID == 0` 时 acquireAndReturn 不写)

**Synthesis 推荐**: **采纳 codex 三层防御**。多写两行代码换"误用爆炸半径=0", 划算。在 M8/M3 单测里加 `panicClaimGate` 注入, 证明 shadow 实例传过去也不会触发 (codex.md:170 已点)。

### 3.4 canary 期 PASR 失败的 fallback 语义

| Lane | 方案 |
| --- | --- |
| claude | **未明确**。R5/R6 提到 rollback 但没区分"PASR 已 mutate state"和"未 mutate"。claude.md:165 M3 描述只说 5 mode 各 case, 没有 fallback 规则。 |
| codex | **明确分两段** (codex.md:367-374): PASR 在**未 acquire slot、未 write claim** 前返回错误 → fallback default; 已 mutate → fail closed + release。`pasr-strict` 禁 fallback, `pasr-primary` 允许。 |

**Critic 判断 (HIGH confidence)**: codex 正确且必要。如果允许"已写 claim"后 fallback default, DefaultSelector 会再写一份 claim → ErrClaimRace (`selector.go:217`) 或双 settle, 是 R-PASR-MW-004 的核心。

**Synthesis 推荐**: **采纳 codex 规则**。dispatcher 内 canary 路径需要 PASRSelector 暴露**有 mutation 标志**给调用方 — 当前 `Select` 接口只返 `(*SelectionResult, error)`, 错误时无法区分是"未 mutate 可 fallback"还是"已 mutate 必 fail"。**这暗示 PASRSelector 需要细化错误类型**（新 sentinel error 比如 `ErrPASRPreMutationFail` vs `ErrPASRPostMutationFail`）, 是 M3 任务的一部分。

### 3.5 mode 命名 + 数量

| Lane | 方案 |
| --- | --- |
| claude | 5 mode: `default / shadow / canary_5 / canary_25 / pasr` (claude.md:182-188)。shadow 百分比固化 100%。 |
| codex | 5 mode: `default / shadow / canary / pasr-primary / pasr-strict`, **shadow_percent 是独立 ENV** (codex.md:97-99)。canary 也是独立 percent。 |

**Critic 判断 (MEDIUM confidence)**: codex 设计更灵活 — shadow 百分比可调让 ops 在 5%→25%→100% shadow 期间渐进观察延迟影响 (R6 / R-PASR-MW-005), 不必每次切百分比都改 mode 名。canary_5 / canary_25 把百分比烧进 mode 名是反模式 (Owner 后面想加 canary_50 又得改代码)。`pasr-strict` (禁 fallback) 是验收终态有用 — 比 claude 的"full"语义更明确。

**Synthesis 推荐**: **采纳 codex mode 命名**, 但保留 claude 的渐进 SOP (5%→25%→100%)。最终 mode = `default / shadow / canary / pasr-primary / pasr-strict` (5 个), 加两个独立 percent ENV。

### 3.6 metrics 命名空间

| Lane | 方案 |
| --- | --- |
| claude | 顶层 expvar key 散落: `pasr_shadow_match_total`, `pasr_canary_pasr_path_total`, `pasr_ring_rebuild_total`... (claude.md:240-252) |
| codex | 集中在 `pasr_dispatch` map: `shadow_sampled_total`, `shadow_match_total`, `canary_pasr_used_total`... (codex.md:230-242) |

**Critic 判断 (LOW-MEDIUM confidence, 偏 codex)**: 现有 PASR-lite A8 已经把指标放在 `expvar.NewMap("pasr")` (`pasr_metrics.go:42`), 用子 key (`pasr_segment_count` 等)。如果 dispatcher 新增指标也走 `pasr_dispatch` 子 map, 与现有 `pasr` map **并列**而非嵌套, 一致性最好。两个分散 expvar 顶层 key (claude 路线) 不利 dashboard 聚合。

**Synthesis 推荐**: 新增 `expvar.NewMap("pasr_dispatch")` (codex 风格), 不污染 `pasr` map。

### 3.7 shadow timeout

| Lane | 方案 |
| --- | --- |
| claude | shadow goroutine 用 `context.WithTimeout(context.Background(), 3*time.Second)` (claude.md:277), 不复用 req.Context |
| codex | shadow 用 `context.WithTimeout(ctx, 50ms~100ms)`, 5%/25% 不同 (codex.md:341-343), 但建议复用原 ctx |

**Critic 判断 (MEDIUM confidence)**: 两 lane 都对一半:
- 复用 req.Context 的问题: 主响应已 return → ctx canceled → shadow 提前退出, 拿不到完整对比 — claude 担心是对的。
- 3s 太长: shadow 只是想看 PASR Select 选谁, 50ms 内 PASRSelector hot path 应该能完成 (`pasr_selector.go:81-178`是纯内存 + ListAccounts DB 查), 3s 是放任 pasr 慢逻辑泄漏。
- 50ms 太紧: ListAccounts 实际是 DB 查询, P99 可能 100ms+; 50ms 会把 shadow 大量记成 timeout error 污染对比指标。

**Synthesis 推荐**: shadow **派生独立 ctx (Background-based) + timeout 500ms** — 比 codex 严, 比 claude 松, 避免 goroutine 泄漏 (R1 / R-PASR-MW-005) 和过早 cancel 两难。

### 3.8 atom 数量 + 拆分粒度

| Lane | 拆分 | atom 数 | 总 LoC |
| --- | --- | --- | --- |
| claude | M1-M9, 含独立 RingProvider atom (M2, ~220 LoC) + 独立 metrics atom (M4) + admin debug endpoint (M9) | 9 | ~1275 |
| codex | M1-M7, 把 metrics 合在 M2 内, ring 合在 M4 (request-scoped, 不独立 atom) | 7 | ~830-1530 (含测试范围模糊) |

**Critic 判断**: codex M3 (slot parity) 是 claude 漏掉的关键 atom, 不可省。claude M9 (`/debug/pasr` admin endpoint) 是 nice-to-have 但不阻塞 main-wire — 移到 follow-up。claude M2 RingProvider 整体废弃 (见 §3.2)。最终 atom 序列见 §7。

---

## 4. 盲点区 (BLINDSPOTS)

两 lane 都漏了的关键问题。这些是 critic 价值核心。

### B1. `pasr_dispatch` 指标启动顺序 vs 并发请求争抢

**事实**: `pasr_metrics.go:36-52` 用 `sync.Once + expvar.NewMap("pasr")` 懒初始化 — 这意味着第一次调用任何 `Inc*` 时才注册 map。

**盲点**: claude.md M4 / codex.md M2 都说"新增 expvar metrics", 但**没有任何一个 lane 提到 dispatcher 启动期需要 eager initialize 新 map** (`expvar.NewMap("pasr_dispatch")`)。否则 shadow 模式下第一批请求还没到 → `/debug/vars` 拿不到 `pasr_dispatch` 子树 → ops dashboard 启动期空 panel。

**严重度**: MINOR (功能不破, ops 体验差)。**Synthesis 要求**: 新 `pasr_dispatch_metrics.go` 必须 `func init()` 或 main.go 启动期 eager 注册, 不走 sync.Once 懒初始化路径。

### B2. shadow 期段表"被 shadow PASR 学习"是否合规?

**事实**: `pasr_selector.go:108` 的 `LookupOrCreate` 是 shadow PASR 调度时**仍会创建段** (PASRSelector 不知道自己是不是 shadow 实例); `pasr_feedback.go` 的 cache observer 是**全局单例**, 不区分 shadow/actual 来源 — 所有 cache observation 都会回流到段表。

**盲点**: 两 lane 都假设 shadow 段表更新无害 (codex.md D-PASR-MW-003 提到但只说"是验证 cache locality 必要"+ "默认允许"), **没人想清楚**:
- shadow 模式下, 段表 LookupOrCreate 创建的段, **谁去 cache 它**? 真实流量走 default → vendor cache 是建立在 default 选的账号上 → cache observer 回流 → 找段 + idx → idx 可能找不到 (因为 default 选的账号可能不在 PASR HRW Top3)。结果: 段创建了但 bitmap 永远是 0。下次 shadow 又看这段又是 cold-miss。**段表沦为内存浪费**。
- 反过来 canary 5% 命中 PASR 时, 真有 PASR-account 走 vendor → 5% 流量喂段表 → 段 bitmap 学习速度只有 actual 模式的 5% → "shadow + canary 看似数据完整, 实则段表学习速度 = canary 百分比"。
- shadow 100% (codex 允许) 时, 段表会被 100% sample 学到 — 但 cache observer 看到的 vendor cache 命中是建立在 **default 选的账号**上, **不是 PASR 选的账号**, 所以 PASR 学到的 bitmap 实际上是"default 账号的 cache 状态" 不是 "PASR 账号的"。这种数据是误导的。

**严重度**: **MAJOR**。这影响 shadow 验证的核心问题"PASR 比 default 是否更优"的答案信号 — shadow 期段表数据是污染的。

**Synthesis 要求**:
- D-NEW-1 (新决策点): shadow 模式段表是否完全跳过 LookupOrCreate? 选项 A: shadow PASR 用只读 `Lookup` (不创建); 选项 B: 接受段表有污染但仍学; 选项 C: shadow PASR 完全不接 cache feedback (shadow + actual 双段表)。**Critic 推荐 A**: shadow 期 PASR 只读, 不污染段表; 段表学习等到 canary 才打开。
- 这条决策直接影响 D-PASR-MW-003 的回答 — Owner 拍板时必须知道这个污染才能选。

### B3. cache feedback observer 是全局单例, 双 PASRSelector 实例如何共享?

**事实**: `pasr_feedback.go:84` `RegisterPASRCacheFeedback(segments)` 是全局 `cachemetrics.RegisterCacheObserver` 注册, **每次调用都追加 observer 到 cachemetrics 单例**。

**盲点**: codex.md:393-396 说"`CacheFeedback` 进程级 observer 一次注册"。claude.md:69 也说"启动一次注册即可"。但 **shadow + canary mode 下两个 PASRSelector 实例共享同一 SegmentTable, 因此只需注册一次** — OK。**问题**: 如果未来加 SIGHUP 热切 (D2), reload 时再次调用 `RegisterPASRCacheFeedback` 会**重复注册** observer → 一个 cache event 触发两次 MarkRead/MarkCacheSeen → 段表 LRU 顺序乱、metrics 双计。

**严重度**: MEDIUM (本 plan SIGHUP 不在范围, 但 D2 follow-up atom 会撞这块墙)。

**Synthesis 要求**: M5 main wire commit message 必须明确 "RegisterPASRCacheFeedback 只能在 main 启动期调用一次, SIGHUP 热切 atom 必须先实现 cache observer **替换/反注册**接口 (cachemetrics 当前**不支持反注册**, 需要 atom 在 follow-up 里加)"。这块要写进 D2 决策点, Owner 才知道 SIGHUP 不止是 ENV 重读, 还要改 cachemetrics API。

### B4. SegmentTable 在 main goroutine + aging worker + cache observer + 多 PASR 实例并发场景下的延迟成本

**事实**: `prefix_segment.go:125-141` SegmentTable 用 sync.RWMutex; `LookupOrCreate` (热路径) 在新建段时升级写锁 (line 218); `MarkRead` 也升级写锁 (line 273); `EvictExpired` 全局写锁 (line 286)。

**盲点**: 两 lane 都没分析:
- shadow 100% + canary 5%: hot path 上 dispatcher 在 default + shadow PASR + (5%) canary PASR 三条线**同时**调 `LookupOrCreate / Lookup` → 写锁竞争。
- aging worker 5min ticker `EvictExpired` 拿全局写锁 (`prefix_segment.go:286`) → 段表大时 (100k 上限) 扫一次可能持锁 ms 级 → 这段时间所有 hot path 阻塞。
- claude.md R7 提"段表 OOM"但没提锁等待; codex.md R-PASR-MW-007 也只提内存。

**严重度**: MEDIUM-HIGH (canary 25%+ 时可能 P99 抖动)。

**Synthesis 要求**:
- 在 M2/M3 集成测试加 race + 100k 段 + 100 qps shadow 并发场景, 测 LookupOrCreate P99
- Owner 应知道这是观测项, **EvictExpired 持锁优化** (分段 / chunked evict) 是 follow-up atom — 写进 §10 Owner action items

### B5. Owner 没 AWS 凭据 (project memory: project_no_aws_credentials), shadow 比对怎么验证 Bedrock 路径?

**事实**: 项目 memory 明确 Owner 没 AWS API access; mock E2E 是最深测试。HUAKAI gateway 主路径之一是 Bedrock (anthropic via /v1/messages → bedrock_invoke adapter)。

**盲点**: 两 lane 都没提"shadow 比对在缺真上游时怎么得到有意义信号"。
- claude.md:511 提"下一个 plan 是性能基线对比" — 但没说这个性能对比怎么做。
- codex.md:482-488 success criteria 只说"PASR runtime 生命周期完整 + rollback 不需迁移", 没说"shadow 数据怎么形成 Owner 决策"。

**严重度**: MEDIUM (本 plan 范围内 shadow 起码能跑, 数据收集到 expvar; 但 Owner 看 shadow_match_ratio 想做 canary 决策时, 如果 Bedrock 路径数据来自 mock 上游, 比对就是假信号)。

**Synthesis 要求**: §10 Owner action items 写一条 "shadow 阶段需要决定: (a) Bedrock 是否用 mock 上游跑 shadow, (b) 是否先在 OpenAI / 其他 vendor (有 Owner 凭据) 上跑 shadow 7 天再考虑 Bedrock"。

---

## 5. 决策点合并 (DECISIONS — 给 Owner ≤6 个)

合并去重 + critic 新增, 共 **6 个决策点**, 按拍板优先级排序:

### D1 (TOP-1, HIGH-RISK). canary/full 上线前是否必补 PASR slot acquisition parity (codex M3)?
- 选项 A: **必补** (推荐)。M3 是 actual canary 硬安全门。
- 选项 B: 跳过, 直接 canary。**Critic 反对, 这是 R-PASR-MW-001 + R-PASR-MW-004 双高风险点。**
- **Critic 推荐 A**, 理由: `pasr_selector.go:249` 当前不写 slot, 直接上 canary 会破并发上限和 settle 配对。

### D2 (TOP-2, MEDIUM-RISK). shadow 模式 PASR 是否更新段表?
- 选项 A: shadow PASR 只读, 用 `SegmentTable.Lookup` 不 LookupOrCreate (推荐)。避开盲点 B2 段表污染。
- 选项 B: shadow PASR 写段表, 接受 100% shadow 期数据有污染 (codex 默认)。
- 选项 C: shadow + actual 双段表 (canary 时 actual 段表预热)。**复杂度高, 不推荐**。
- **Critic 推荐 A**, 理由: 段表数据污染会让 "shadow_match_ratio" 信号失真; A 简单, 段表学习推迟到 canary 5% 才开始。

### D3 (TOP-3). AccountRing 来源
- 选项 A: 每请求从 `AccountSource.ListAccounts` snapshot 构造 ring (codex M4, 推荐)。
- 选项 B: 启动期 + 5min ticker 重建 RingProvider + 新 SQL (claude M2)。**Critic 反对, 引入 4 项新风险且 ListAccounts 已经过滤好。**
- **Critic 推荐 A**, 理由见 §3.2。

### D4. canary 失败 fallback 语义
- 选项 A: PASR 未 mutate 前失败 → fallback default; 已 mutate → fail closed + release (codex 推荐)。
- 选项 B: PASR 任何错误都不 fallback (`pasr-strict` 形态)。
- **Critic 推荐 A 作为 `canary` + `pasr-primary` 默认; B 作为 `pasr-strict` 终态验收**。需要 PASRSelector 暴露细化错误类型 (`ErrPASRPreMutationFail` vs `ErrPASRPostMutationFail`)。

### D5. shadow timeout 和 ctx 派生
- 选项 A: shadow 用独立 Background-based ctx + 500ms timeout (Critic 推荐)。
- 选项 B: 复用 req.Context (codex)。**Critic 反对**, 主响应 return 后 ctx canceled, shadow 数据丢失。
- 选项 C: 独立 ctx + 3s timeout (claude)。**Critic 反对**, 给 PASR 慢逻辑泄漏空间。
- **Critic 推荐 A**。

### D6. mode 命名
- 选项 A: `default / shadow / canary / pasr-primary / pasr-strict` + 独立 percent ENV (codex 推荐)。
- 选项 B: `default / shadow / canary_5 / canary_25 / pasr` (claude)。**Critic 反对**, 把百分比烧进 mode 名不灵活。
- **Critic 推荐 A**。

> Owner 不需要再回答 claude.md 的 D2 (SIGHUP 热切) — 两 lane 一致暂不做, 写成 follow-up 即可, 不算决策点。Owner 也不需要回答 claude.md 的 D4 (`ErrNoEligibleAccount` metric) — 两 lane 共识"记 + warmup 静默", 直接照做。

---

## 6. 风险登记合并 (RISKS — 14 条)

合并去重 + critic 新增 4 条 (B1-B5 转化的)。按严重度排:

| ID | 风险 | 触发条件 | 影响 | 缓解 | 来源 |
| --- | --- | --- | --- | --- | --- |
| R1 | PASR actual 绕过 slot acquisition | M3 未做就开 canary | 破 cap_concurrency, settle 链断 | M3 强制门, D1 拍板 | codex |
| R2 | shadow 误写 claim/slot | shadow 实例 misconfigure | 双写 claim, billing 噪音 | 三层防御 (Claims=nil + Slots=nil + dispatcher 抹 ClaimID) | 两 lane |
| R3 | canary fallback 双写 | PASR 已 mutate 后又 fallback default | 双 slot, claim race | fallback 仅在 pre-mutation 错误时允许 | codex |
| R4 | shadow 段表数据污染 | shadow PASR 学到 default 选的账号 cache | shadow_match_ratio 信号失真 | D2 选项 A: shadow 只读段表 | **critic 新增 (B2)** |
| R5 | SegmentTable 写锁竞争 P99 抖动 | EvictExpired 持全局写锁 + 100k 段 | hot path P99 ms 级阻塞 | 集成测试覆盖 + follow-up chunked evict atom | **critic 新增 (B4)** |
| R6 | Bedrock shadow 数据假信号 | Owner 无 AWS 凭据, mock 上游 | shadow 决策依据失真 | 先在有凭据 vendor 跑 shadow 7d | **critic 新增 (B5)** |
| R7 | shadow 增加请求延迟 | 100% shadow 同步占资源 | P99 上升 | sampled rollout + 500ms timeout + 1024 buffer chan | 两 lane |
| R8 | shadow goroutine 泄漏 | shadow Select 卡住 | OOM / 句柄耗尽 | ctx timeout + buffer chan + drop 计数 | claude R1 |
| R9 | 配置拼错 | env value typo | 误启 PASR 或假信号 | typed config fail-fast (不 fail-soft) | codex R-006 |
| R10 | SegmentTable OOM | shadow 100% + 高 prefix cardinality | 内存压力 GC 抖动 | 100k cap + 30min aging + segment_count 监控 | 两 lane |
| R11 | diff 指标误读 | Default 与 PASR 设计目标不同 | Owner 误判 PASR 劣化 | diff 带 reason/model/pool, 联合 cache hit + latency 评估 | codex R-008 |
| R12 | panic 影响请求 | shadow PASR bug | 500 / 进程崩 | dispatcher recover, shadow panic 不污染 default | 两 lane |
| R13 | 回滚速度 | restart-only, 滚动 3-5min | 真 incident 慢 | 接受现状, follow-up 看是否做 SIGHUP | 两 lane |
| R14 | cache observer 反注册缺失 | 未来 SIGHUP reload 重复注册 observer | 段状态双计 | 写进 D2 follow-up atom 范围 | **critic 新增 (B3)** |

---

## 7. 最终 atom 序列 (Mn)

基于 synthesis, 合并后的 atom 序列 (替换两 lane 各自拆分):

| Atom | 范围 | LoC | 依赖 | 验收 |
| --- | --- | --- | --- | --- |
| **M1** | typed config (`PoolSelectorConfig`) + ENV parse + 单测 | ~140 | 无 | 5 mode + percent + salt 全 fail-fast 解析; default mode 等价现状 |
| **M2** | `pasr_dispatch_metrics.go` 新增 `expvar.NewMap("pasr_dispatch")`, eager init (盲点 B1) + Inc helpers | ~120 | M1 | `/debug/vars` 启动期就有 `pasr_dispatch` 子树 |
| **M3** | **PASRSelector slot parity** (codex M3, **强制门**): `PASRSelectorConfig.Slots SlotManager` 字段 + actual 路径 `Slots.Acquire` → `Claims.WriteAcquisition` 顺序 + 错误细化 (`ErrPASRPreMutationFail` vs `ErrPASRPostMutationFail`) + claim race 时 release slot | ~180 | M2 | actual 一次成功 = exactly once slot insert + claim write; claim 失败时 in_flight_count 还原 |
| **M4** | `SelectorDispatcher` 实现 `pool.Selector`, 5 mode dispatch + sampleBucket + shadow async (Background ctx + 500ms timeout) + canary fallback 规则 (D4) + panic recover; **shadow 用 SegmentTable.Lookup 只读** (D2 选项 A) | ~280 | M3 | 5 mode 单测 + shadow 不污染段表 + canary fallback 仅 pre-mutation |
| **M5** | request-scoped AccountRing (codex M4): PASRSelector `Select` 内从 ListAccounts 结果直接 build ring, 弃用 RingProvider 全局缓存 | ~80 | M3 | per-tenant ring 无跨租户泄漏; account 删除后 segment 成员自动失效 |
| **M6** | main.go wiring: `deps.selector` 改 `pool.Selector`; default mode 不构造 PASR; shadow/canary/pasr-* 构造 SegmentTable + agingWorker + RegisterPASRCacheFeedback + dispatcher; shutdown 链 | ~150 | M4 + M5 | 5 mode boot smoke; default mode 字节级等价 |
| **M7** | 集成测试 + smoke: `selector_dispatcher_integration_test.go` 表驱 (T1-T22 见 §8); 默认 + shadow 两档 smoke | ~450 | M6 | 全 green + race detector |

**总 atom 数: 7** (claude 9 → codex 7 → synthesis 7, 砍 RingProvider, 加 slot parity, 合并 metrics)

**总 LoC 估**: ~1400 (实现 ~950, 测试 ~450)

**总时间估**: 2.8 atom-day (slot parity 是新工作量, 但去掉 RingProvider 抵消)

执行顺序硬约束:
- M1 → M2 (config 先于 metrics, metrics 引用 config 字段)
- M3 必须先于 M4 (dispatcher 依赖错误类型分类)
- M5 必须先于 M6 (main.go 构造 PASR 时需要 ring builder)
- M7 最后, 跨 atom 集成测试

---

## 8. Verification Matrix

合并两 lane 测试矩阵 + critic 新增覆盖盲点:

| 测试 ID | 场景 | 类型 | 断言 | 来源 |
| --- | --- | --- | --- | --- |
| T1 | default boot 等价 | smoke | 现有 selector_test 全 green | claude T2 + codex T-017 |
| T2 | config 默认 / 非法 / 越界 | unit | typed fail-fast | codex T-001/002/003 |
| T3 | dispatcher default 不调用 PASR | unit | counter 验证 | codex T-004 |
| T4 | dispatcher shadow sampled match/diff | unit | match=同账号; diff 带 reason | claude T3/T4 + codex T-007 |
| T5 | dispatcher shadow drop (queue 满) | unit | shadow_drop_total > 0 | claude T5 |
| T6 | shadow 不写 claim (panic claim gate) | unit | 注入 panic claim gate 不触发 | codex T-012 |
| T7 | shadow 不写 slot (M3 后) | unit | 注入 panic slot manager 不触发 | **critic 新增 (B2/D2)** |
| T8 | **shadow 不污染段表 (D2 A)** | unit | shadow 100 req 后 segments 不增 | **critic 新增 (B2)** |
| T9 | shadow timeout 500ms | unit | 注入慢 PASR Select → shadow 超时不阻塞主路径 | **critic 新增 (D5)** |
| T10 | shadow panic recover | unit | recover; default 结果未损; shadow_panic_total++ | codex T-008 |
| T11 | canary deterministic 同 SessionHash | unit | 100 次同 hash 落同侧 | claude T7 + codex T-009 |
| T12 | canary 5% 分布 | unit | 1 万 sample ∈ [4.5%, 5.5%] | claude T8 + codex T-009 |
| T13 | canary PASR pre-mutation 失败 fallback default | unit | fallback_used++; default 结果返 | codex T-011 + D4 |
| T14 | canary PASR post-mutation 失败 fail closed + release | unit | slot release; 不调 default | **critic 新增 (D4)** |
| T15 | M3 PASR slot+claim parity 成功 | integration | exactly once slot row + in_flight++ + claim write | codex T-013 |
| T16 | M3 PASR claim failure release slot | integration | 失败时 in_flight 还原 | codex T-014 |
| T17 | request-scoped ring 租户隔离 | unit | tenant A 请求绝不选 B 账号 | claude T10 + codex T-015 |
| T18 | empty ring → ErrNoEligibleAccount | unit | 返错; canary 可 fallback | codex T-016 |
| T19 | aging worker shutdown | smoke | 收 SIGINT 5s 内退 | claude T12 |
| T20 | pasr-strict 不 fallback | unit | post-mutation 失败 fail closed; pre-mutation 失败也 fail | **critic 新增 (D4)** |
| T21 | race detector | optional | go test -race ./internal/pool 全 green | codex T-020 |
| T22 | **SegmentTable 100k + 100qps 并发延迟** | benchmark | LookupOrCreate P99 < 1ms | **critic 新增 (B4)** |

22 测试。M7 atom 全部覆盖。

---

## 9. Rollback contract

| 阶段 | 进入条件 | 退出/降级条件 | 持续时长 | rollback SLA |
| --- | --- | --- | --- | --- |
| `default` | 初始 | n/a | n/a | n/a (基线) |
| `shadow 5%` | M1-M7 全 green + Owner 批 D2 | shadow_panic_total > 0; shadow_diff_ratio > 50% | 7 天 | ENV + 重启 ≤ 5min (rolling) |
| `shadow 25%` | shadow 5% 7d 无 anomaly | 同上 + p95 latency 上升 > 10% | 3 天 | ≤ 5min |
| `shadow 100%` | shadow 25% 3d 无 anomaly + diff 解释清楚 | 同上 + segment_count 接近 100k 上限 | 7 天 | ≤ 5min |
| `canary 5%` | shadow 100% 7d ok + **D1 (M3 slot parity) 落地** + Owner 批 | no_capacity 显著上升 / 5xx > 0.1% / settle 失败 | 24 小时 | **关键: 已 mutate 的请求不可 fallback, 必须 fail closed + release; 操作 = 改 mode=default + 滚动重启** |
| `canary 25%` | canary 5% 24h ok | 同上 | 48 小时 | ≤ 5min |
| `pasr-primary` | canary 25% ok + 完整 rollback drill | 任意 high signal | 持续 | ≤ 5min |
| `pasr-strict` | pasr-primary 7d ok | n/a (验收终态) | 验收期 | ≤ 5min |
| `rollback` 任意 → default | mode env 改回 + 滚动重启 | n/a | 5 pod ≈ 3min, 50 pod ≈ 5-8min | DefaultSelector 始终在进程内, 无 migration 需求 |

> **关键不变量**: rollback 不依赖 SegmentTable 内存清空 (重启清空); 不依赖 cache observer 反注册 (重启清空); 不依赖 DB schema (无 migration)。

---

## 10. Owner action items

执行前 Owner 必须做的事:

1. **拍板 D1**: M3 slot parity 是 canary 硬门 — 同意 (Critic 推荐) / 反对。
2. **拍板 D2**: shadow 段表只读 (Critic 推荐 A) / 双段表 (C) / 接受污染 (B)。
3. **拍板 D3**: ring 来源 — request-scoped (Critic 推荐 A) / 全局 ticker (B)。
4. **拍板 D4**: canary fallback 语义 — pre-mutation only (Critic 推荐 A) / 永不 fallback (B 仅作为 strict 终态)。
5. **拍板 D5**: shadow timeout 500ms 独立 ctx (Critic 推荐 A)。
6. **拍板 D6**: mode 命名 — `pasr-primary / pasr-strict` (Critic 推荐 A)。
7. **环境准备**: shadow 阶段验证依赖 vendor 上游真实数据。Owner 没 AWS 凭据 (memory: project_no_aws_credentials), 决定: (a) shadow 是否在 OpenAI / 其他有凭据 vendor 跑 7 天再考虑 Bedrock; (b) Bedrock 路径是否用 mock 上游接受信号偏差。
8. **节奏拍板**: shadow 7d → canary 5% 24h → canary 25% 48h → primary → strict (Critic 推荐, 比 claude 24h/24h 更保守)。
9. **follow-up atom roster** (本 plan 不做, 但 Owner 应知道):
   - SIGHUP 热切 (含 cache observer 反注册 API 改造) — 风险 R14
   - SegmentTable EvictExpired 分段持锁优化 — 风险 R5
   - `/debug/pasr` admin endpoint (claude M9, 移到 follow-up)
   - PASR-lite 性能基线对比 plan (本 plan 后入口)

---

## TL;DR (200 字)

两 lane 在 10 项顶层架构上一致 (Selector 接口、Dispatcher、typed config、shadow Claims=nil、deterministic canary、restart-only rollback)。主要分歧 8 项, 4 项 critic 强 codex: **PASR 当前不写 slot, codex M3 是 canary 硬安全门** (claude 完全漏掉); **AccountRing 用 request-scoped 而非全局 ticker** (codex 省 SQL 省风险); **canary fallback 必须区分 pre/post mutation**; **mode 命名 pasr-primary/pasr-strict 优于 canary_5/25**。Critic 新增 5 个盲点: shadow 段表数据污染 (B2 → D2 决策点)、SegmentTable 写锁 P99 抖动 (B4)、Bedrock 无凭据 shadow 假信号 (B5)、cache observer 反注册缺失影响未来 SIGHUP (B3)、metrics eager init (B1)。最终 7 atom (砍 RingProvider, 加 slot parity, 合并 metrics) ~1400 LoC, 2.8 atom-day, 6 决策点, 14 风险, 22 测试。
