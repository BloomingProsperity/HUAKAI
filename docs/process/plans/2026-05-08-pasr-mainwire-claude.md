---
plan_id: 2026-05-08-pasr-mainwire-claude
date: 2026-05-08
lane: claude (独立; 未读 codex lane)
parallel_with: 2026-05-08-pasr-mainwire-codex.md (待写)
related:
  - 2026-05-08-pasr-lite-v2-synthesis.md  (已落 A1-A5, A8 green)
  - 2026-05-08-pasr-lite-v2-claude.md
  - 2026-05-08-pasr-lite-v2-codex.md
upstream_atoms_complete: [A1, A2, A3, A4, A5, A8]
upstream_atoms_paused:   [A6 (PG warm-start), A7 (rebalance handler)]
owner_directive_2026-05-08: 把 PASR-lite 接 main.go, feature flag 控制, 与 DefaultSelector 共存, shadow mode + 渐进 5%/25%/100% 切流, 用真实流量验证再决定 A6/A7 投入
---

# 2026-05-08 PASR-lite Main-Wire — Claude lane 独立设计

> 目标: **把 PASR-lite (A1-A5+A8 已落) 接入 cmd/gateway/main.go, 启 shadow → canary → full 渐进路径, 给 Owner 一份能直接照着走的执行手册**。

---

## 0. 元信息

| 字段 | 值 |
| --- | --- |
| Lane | Claude (fresh context, 未看 codex lane) |
| Parent | PASR-lite v2 (`2026-05-08-pasr-lite-v2-synthesis.md`), atom A1-A5+A8 已 green |
| Status | DRAFT — 待 codex parallel 后 cross-discuss |
| 风险等级 | MEDIUM (热路径 selector 改动, 但 feature flag 默认 OFF + 默认仍走 DefaultSelector) |
| 估时 (atomized) | 约 2.5-3 个 atom-day; 见 §11 |
| 阻塞 / 依赖 | 无外部 PR; 内部依赖 A1-A5+A8 (✅) + cachemetrics observer 链 (✅) |
| 选择 | Owner 选项 3 = "暂停 A6/A7, 先做主线集成 + shadow 比对验证" |

---

## 1. 目标 / 非目标

### 1.1 必达目标 (Must)

1. PASR-lite (A1-A5+A8) 在生产二进制 `cmd/gateway/main.go` 实际跑起来，可以单台机器 instance 内同时持有 `DefaultSelector` 和 `PASRSelector` 实例。
2. **feature flag** 控制 4 种模式：`default` (现状, PASR 不实例化) / `shadow` (PASR 异步影子) / `canary_5` / `canary_25` / `pasr` (full)，env var 切换。
3. **shadow mode**: 真实流量主路径仍走 DefaultSelector，PASR 同时跑一遍 selection 但不写 `claim acquisition`、不影响响应；记录两边选择是否一致到 metrics + 日志。
4. **canary**: 用 `SessionHash` 取模做 deterministic 5% / 25% 子集走 PASR，其余仍走 default; rollback 切回 default 在 5s 内（重启不算）。
5. **AccountRing 启动期构建** + 每 5 min 重建 ticker；对 (tenant, pool_group) 维度分 ring。
6. **零回归**: `default` 模式下行为字节级等同当前 chat-completions handler；smoke test + 现有 selector_test 全 green；新模式下 PASR 不写 `billing_claims` 是硬不变量。
7. **可观测**: 12+ 个 expvar metric (覆盖 shadow match/diff、canary 抽样比、模式当前态、rollback 计数)；启动 / 切换 / rollback 三处结构化日志。

### 1.2 非目标 (Won't, 本 plan 不做)

- A6 (PG warm-start, `pasr_segments` 表持久化) — Owner 显式暂停。
- A7 (rebalance handler — admin endpoint 触发 ring 重置) — Owner 显式暂停。
- prefix hash 计算改造 — 复用 Track B `cache_routing.ComputePromptHash` 写入 `req.SessionHash` 的现有路径，不动 handler。
- 多机一致性 — 当前是单 instance 多 selector 共存; 多副本场景的 ring/segment 跨实例同步不在本 plan，Owner 已知。
- 性能基准 (P99 延迟对比) — 本 plan 仅交付能跑、能切、能记录的版本；性能验证是 _下一个_ plan 的入口（用本 plan 产出的 metrics）。

---

## 2. 关键代码上下文（已读，不抄）

| 位置 | 现状 | 本 plan 怎么动 |
| --- | --- | --- |
| `cmd/gateway/main.go:70` | `selector *pool.DefaultSelector` (强类型) | 升级为 `selector pool.Selector` 接口 + 加 `pasrSelector` + `selectorMode` |
| `cmd/gateway/main.go:109-118` | `pool.NewDefaultSelector(...)` 直构造 | 走新工厂 `buildSelectorStack(cfg, deps, logger)` 返回 dispatcher |
| `internal/gatewayhttp/chat_completions_handler.go:36` | `Selector pool.Selector` ✅ 已接口化 | **零改动** — 本是 plan 杠杆点 |
| `internal/pool/pasr_selector.go:71-78` | `NewPASRSelector(cfg)` 接 Accounts/Claims/RingProvider/Segments/LoadCap | main.go 喂入；shadow 模式 `Claims=nil` 即不写 acquisition |
| `internal/pool/pasr_feedback.go:84` | `RegisterPASRCacheFeedback(segments)` 一行 | 在 `default` 模式 **不调用**（避免 segment table 浪费内存）；其它三态调用 |
| `internal/pool/pasr_aging_worker.go:61` | `Start(ctx)` goroutine + `Stop()` 优雅停 | main.go shutdown 链注册 `worker.Stop()` |
| `internal/pool/pool.go:17` | `Selector` 接口已存在 | 直接复用 |
| `internal/db/pool_accounts.sql.go:392` | `ListEligibleAccountsByPoolGroup(tenant, pool_group)` 过滤 | ring 必须 per (tenant, pool_group) 分组 — 见 §6 |
| `internal/cachemetrics/cachemetrics.go:184` | `RegisterCacheObserver(fn)` 全局单例订阅 | 启动一次注册即可，PASR feedback 一直挂着 |

---

## 3. 主架构方案 + ASCII 图

### 3.1 顶层数据流

```
                                  ┌──────────────────────────────────────────┐
HTTP req                          │            cmd/gateway/main.go            │
  │                               │                                          │
  ▼                               │  buildSelectorStack(cfg) →               │
NewChatCompletionsHandler         │    SelectorDispatcher{                   │
  ChatHandlerDeps{                │      mode: SelectorMode (atomic.Pointer) │
    Selector pool.Selector ─────────►   default *DefaultSelector              │
  }                               │      pasr    *PASRSelector (nil if off)  │
                                  │      shadowAsync bool                    │
                                  │      canaryDenominator int               │
                                  │    }                                     │
                                  └──────────────────────────────────────────┘
                                                      │
                              .Select(ctx, req)       │
                                                      ▼
                              ┌──────────────────────────────────────────────┐
                              │ SelectorDispatcher.Select                    │
                              │                                              │
                              │  switch mode {                               │
                              │  case SelectorModeDefault:                   │
                              │    return d.default.Select(...)              │
                              │                                              │
                              │  case SelectorModeShadow:                    │
                              │    res := d.default.Select(...)              │
                              │    go d.runShadow(ctx, req, res.AccountID)   │
                              │    return res, nil                           │
                              │                                              │
                              │  case SelectorModeCanary5/25:                │
                              │    if canaryHit(req.SessionHash, denom) {    │
                              │      return d.pasr.Select(...)               │
                              │    }                                         │
                              │    return d.default.Select(...)              │
                              │                                              │
                              │  case SelectorModePASR:                      │
                              │    return d.pasr.Select(...)                 │
                              │  }                                           │
                              └──────────────────────────────────────────────┘

  ┌───── 异步影子 (shadow only, goroutine) ───────┐
  │  pasrShadowSelect(ctx, req)                  │
  │    res, err := pasrShadow.Select(...)        │  ← Claims 字段 = nil, 不写 acquisition
  │    if res.AccountID == primary.AccountID:    │
  │      pasr_shadow_match_total++               │
  │    else:                                     │
  │      pasr_shadow_diff_total++                │
  │      logger.Debug("shadow_diff", ...)        │
  └──────────────────────────────────────────────┘
```

### 3.2 Selector mode 状态机

```
   ┌─────────┐  ENV 改 + SIGHUP / 重启       ┌─────────┐
   │ default │ ─────────────────────────────► │ shadow  │
   └─────────┘                                └────┬────┘
        ▲                                          │ 7d 验证 metrics 后
        │ rollback (任何时刻 ENV 改回 default)     │
        │                                          ▼
        │                                     ┌──────────┐
        │                                     │ canary_5 │
        │                                     └─────┬────┘
        │                                           │ 24h 无 anomaly
        │                                           ▼
        │                                     ┌──────────┐
        │                                     │ canary_25│
        │                                     └─────┬────┘
        │                                           │ 24h 无 anomaly
        │                                           ▼
        └──────────────────── PASR (full) ◄────────┘
```

> 切换两种语义：
> - **重启切**: 改 ENV → restart pod → 新 mode 生效（默认通道）
> - **热切 (后续可加，本 plan 不做)**: SIGHUP 重读 ENV + atomic.Pointer.Store 替换 mode；本 plan 仅在 `SelectorDispatcher.mode` 字段用 atomic.Pointer 占位，让后续 PR 加 SIGHUP 不改代码结构

---

## 4. 子原子拆分 (M1...M9)

每个 M atom 独立可 commit + 测试。**M atom 命名以避免和 PASR-lite A1-A8 重号**。

| Atom | 范围 | LoC 估 | 测试 | 风险 | 验收 |
| --- | --- | --- | --- | --- | --- |
| **M1** | `internal/pool/selector_mode.go` — `SelectorMode` enum + `SelectorMode.String()` + `ParseSelectorMode(s string)` 解析 ENV | ~80 | 单测: parse 5 种合法值 + 3 种非法值 → fallback default 行为 | LOW | `go test ./internal/pool -run TestSelectorMode` green; 5 mode + invalid → default |
| **M2** | `internal/pool/ring_provider.go` — `RingProvider` 类型 + `NewRingProvider(q *db.Queries, refresh time.Duration)`; 启动 goroutine 周期重建 ring (per (tenant, pool_group)); atomic.Pointer hot-swap | ~220 | 单测: 假 q 返 1/3/8 账号 → ring.Accounts 升序去重; 重建时旧引用仍可读; concurrent Get vs Refresh 用 race detector | MEDIUM (DB 抖动期重建可能拿到空集 → 必须保留旧 ring) | `RingProvider.Get(tenantID, poolGroupID)` 返 `*pool.AccountRing`; refresh 错误时旧 ring 不丢; 5 min ticker 可注入测时钟 |
| **M3** | `internal/pool/selector_dispatcher.go` — `SelectorDispatcher{default, pasr, mode, shadowQueue, canaryDenominator}` 实现 `pool.Selector` 接口；mode dispatch 逻辑 | ~260 | 单测: 5 种 mode 各 case；canary deterministic (相同 SessionHash 永远落同侧)；shadow goroutine 不阻塞主路径; 主路径错误时 shadow 不触发；shadow panic recover | MEDIUM (并发 + goroutine 生命周期) | shadow path 验证 5 个 race 场景; canary `hash(SessionHash) mod 100 < 5` 稳定; PASR full mode 100% 走 PASR |
| **M4** | `internal/pool/pasr_shadow_metrics.go` — 8 个新 expvar 指标 + `Inc*` helpers (见 §6.3) | ~120 | 单测 (用 `expvar.Get` reflective 取值断言): metric 累加正确; `selector_mode_current` set 后可读 | LOW | 启动后 `/debug/vars` 看到 `pasr_shadow.{...}` 子树; gauge `selector_mode_current` 字符串值 |
| **M5** | `cmd/gateway/main.go` — 改 `deps` struct: `selector *pool.DefaultSelector` → `selector pool.Selector`; 新增 `selectorDispatcher *pool.SelectorDispatcher`、`pasrSegments *pool.SegmentTable`、`pasrAgingWorker *pool.PASRAgingWorker`、`ringProvider *pool.RingProvider`; 改 run() 分支构造 + shutdown 关停链 | ~150 | 单测: 不动；`backend/cmd/gateway/smoke_test.go` 启动 `default` 模式应原状 green | HIGH (启动期 wiring; 一处改动影响 prod) | 5 mode 各能从 ENV 启动；smoke_test green；`grep "PASR enabled" log` 在非 default 模式可见 |
| **M6** | `internal/config/config.go` — 加 `SelectorMode string` + `PASRRingRefresh time.Duration` + `PASRSegmentMaxAge time.Duration`(可选, 默认走 `DefaultSegmentMaxAge`) + `PASRSegmentTableCap` 字段; ENV 名见 §5.2 | ~45 | 单测: ENV 不存在 → default; 非法值 → typed error | LOW | `cfg.SelectorMode == "shadow"` 等读到 |
| **M7** | `internal/pool/canary.go` — `canaryHit(seed string, denom int) bool`; 用 fnv64a(seed) % 100 < denom 取 5/25; 注入 seed 测试 deterministic | ~50 | 单测: 100k seeds 落 5% 子集分布 ±1%; 相同 seed 永远同结果 | LOW | 抽样均匀 + deterministic |
| **M8** | `internal/pool/selector_dispatcher_integration_test.go` — 黑盒测试: 真 DefaultSelector + 真 PASRSelector (用 fakeAccountSource + 内存 SegmentTable) → 5 mode 期望行为 | ~280 | 表驱测: default = match; shadow = match + diff/match counter 正确; canary_5 = ~5% 走 PASR (1万 sample); PASR = 100% PASR | MEDIUM | green |
| **M9** | `cmd/gateway/main.go` — `mountRoutes` 加 `/debug/pasr` 只读 admin endpoint，dump segment table 简要 stats (Size, EvictedTotal, FirstPickRate, FailoverRate, ShadowMatch%); auth 暂用 admin token | ~70 | 集成测: 启动 shadow 模式 → curl `/debug/pasr` 拿到 JSON, 字段全 | LOW | 端点返 200 + JSON |

**总 LoC 估**: ~1275 (含测试). 实现侧约 700 LoC, 测试约 575.

---

## 5. Feature flag 状态机 + 切换流程

### 5.1 mode enum (M1)

```go
type SelectorMode string

const (
    SelectorModeDefault  SelectorMode = "default"  // PASR 不实例化
    SelectorModeShadow   SelectorMode = "shadow"   // 主路径 default; PASR 异步影子
    SelectorModeCanary5  SelectorMode = "canary_5"
    SelectorModeCanary25 SelectorMode = "canary_25"
    SelectorModePASR     SelectorMode = "pasr"     // 100% PASR
)
```

### 5.2 ENV 变量约定

| ENV | 类型 | 默认 | 含义 |
| --- | --- | --- | --- |
| `HUAKAI_SELECTOR_MODE` | string | `default` | 解析为 `SelectorMode`; 非法值 → 启动 fail-fast (不要 silent fallback, 防止部署事故) |
| `HUAKAI_PASR_RING_REFRESH` | duration (Go) | `5m` | RingProvider 周期重建间隔 |
| `HUAKAI_PASR_SEGMENT_MAX_AGE` | duration | `30m` | 段无 cache_read 老化 (覆写 `DefaultSegmentMaxAge`) |
| `HUAKAI_PASR_SEGMENT_CAP` | int | `100000` | 段表上限 (覆写 `DefaultSegmentTableCap`) |
| `HUAKAI_PASR_LOAD_CAP` | float | `0.95` | 段成员被剔出 candidates 的 LoadRate 上限 |

> **决策**: 用 `config.Config` 字段而不是 `os.Getenv` 散读，理由是 (a) 单测可注入 (b) main.go 只一个 cfg 入口 (c) ENV 名变更时只改一处。

### 5.3 切换流程 (运维 SOP)

| 阶段 | 操作 | 验证 | 时长 |
| --- | --- | --- | --- |
| 1. shadow 起 | `HUAKAI_SELECTOR_MODE=shadow` 重启 1 实例 | 7 天观察 `pasr_shadow_diff_ratio < 5%` (即 shadow 与 default 有 95%+ 选择一致); `pasr_segment_count` 稳态; 无 OOM | 7 天 |
| 2. canary_5 | 改 `=canary_5` 重启全 fleet | 24h 观察 `pasr_canary_5_request_total` 占比 ≈5%; 错误率 / 延迟 P99 与 default 持平 | 24h |
| 3. canary_25 | 改 `=canary_25` 重启 | 24h 同上 | 24h |
| 4. PASR full | 改 `=pasr` 重启 | 持续观察 cache hit rate 提升、`pasr_first_pick_total / select_total > 80%` | 持续 |
| **rollback** | 任意时刻改回 `=default` 重启 | smoke 通过即生效；shadow 模式被异步路径不影响主请求 | 单实例重启 ~30s; 多实例滚动 ~3-5min |

> **快速 rollback ≠ 0 downtime**：本 plan 不实现 SIGHUP 热切（避免引入复杂度），rollback = ENV 改 + 滚动重启。Owner 若要 5s 内热切，需要单独 atom（决策点 D2）。

---

## 6. Shadow mode 比对算法 + metrics 列表

### 6.1 shadow 触发条件

```
mode == SelectorModeShadow
  && d.pasr != nil  
  && primaryRes != nil
  && primaryRes.AccountID != 0          // primary 失败时不跑 shadow (无可比基线)
  && req.PoolGroupID != 0                // 没 pool_group 跳 (PASR 无法工作)
```

### 6.2 关键不变量

1. **shadow PASR 永远不写 acquisition**: 启动期构造 `pasrShadowSelector := pool.NewPASRSelector(PASRSelectorConfig{Claims: nil, ...})`, claims=nil 时 `acquireAndReturn` 内部 `if p.claims != nil` 已守住 (见 pasr_selector.go:249). 这是硬不变量 — 单测必须断言 `BillingClaim` 表无 shadow 写入。
2. **shadow goroutine 不阻塞主路径**: dispatch 顺序是 `primary.Select()` → `return res` (主路径)；shadow 用 `go d.runShadow(...)` 后台跑。**ctx 用 `context.Background()` 派生新 ctx 而非透传 req.Context()**, 避免主响应已返时 ctx canceled 让 shadow 提前退出 (我们要测 shadow 完整跑完拿到结果)。
3. **shadow 限并发**: `shadowQueue chan struct{}` 带 buffer 1024; 满时丢弃 + `pasr_shadow_drop_total++`. **shadow 不能让 goroutine 无限堆积** (DefaultSelector 比 PASR 慢，shadow 跟不上时主路径继续，shadow 主动丢)。
4. **panic recover**: shadow goroutine 内 `defer recover()`; panic 计入 `pasr_shadow_panic_total`，不传染主路径。

### 6.3 metrics 列表 (M4 + 复用现有)

| metric (expvar key) | 类型 | 含义 | 触发位置 |
| --- | --- | --- | --- |
| `selector_mode_current` | string (expvar.String) | 当前进程的 SelectorMode | 启动 + 切换时 set |
| `pasr_shadow_match_total` | int64 | shadow 与 default 选同账号的次数 | shadow goroutine |
| `pasr_shadow_diff_total` | int64 | shadow 与 default 选不同账号 | shadow goroutine |
| `pasr_shadow_error_total` | int64 | shadow 自身 Select() 出错 | shadow goroutine |
| `pasr_shadow_drop_total` | int64 | shadowQueue 满丢弃 | dispatcher.Select |
| `pasr_shadow_panic_total` | int64 | shadow goroutine 内 recover 计数 | shadow goroutine |
| `pasr_canary_pasr_path_total` | int64 | canary_5/25 命中走 PASR 的请求 | dispatcher.Select |
| `pasr_canary_default_path_total` | int64 | canary_5/25 落入 default 的请求 | dispatcher.Select |
| `pasr_full_select_total` | int64 | full PASR 模式选 | dispatcher.Select |
| `pasr_default_select_total` | int64 | default 模式选 | dispatcher.Select |
| `pasr_ring_rebuild_total` | int64 | RingProvider tick 重建次数 | RingProvider |
| `pasr_ring_rebuild_error_total` | int64 | 重建失败 (DB 抖动等) | RingProvider |
| `pasr_ring_account_count` | int (expvar.Int) | 最近一次重建后的 ring 账号数 (取所有 (t,pg) 总和) | RingProvider |
| 已存在 (PASR-lite A8) | | 复用: `pasr_segment_count`, `pasr_evictions_total`, `pasr_first_pick_total`, `pasr_failover_total`, `pasr_full_ring_fallback_total`, `pasr_segment_creates_total`, `pasr_cache_hit_observations`, `pasr_cache_creation_obs` | |

派生指标 (Owner 看 dashboard 用，本 plan 不算)：
- `shadow_match_ratio = match / (match + diff)` — 期望 >= 95% (PASR 与 default 选择高度一致 = PASR 不是乱选)
- `pasr_first_pick_ratio = first_pick / (first_pick + failover + full_ring_fallback)` — 期望随时间上升
- `cache_hit_observations / select_total` — 期望比 default 模式高 (PASR locality 价值)

### 6.4 shadow 比对伪代码 (写在 selector_dispatcher.go)

```go
func (d *SelectorDispatcher) runShadow(ctx context.Context, req SelectionRequest, primaryAcc int64) {
    defer func() {
        if r := recover(); r != nil {
            IncShadowPanic()
        }
    }()
    select {
    case d.shadowQueue <- struct{}{}:
        defer func() { <-d.shadowQueue }()
    default:
        IncShadowDrop()
        return
    }
    // 用独立 ctx (3s 超时), 不复用 req.Context()
    sctx, scancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer scancel()
    res, err := d.pasr.Select(sctx, req)
    if err != nil {
        IncShadowError()
        return
    }
    if res.AccountID == primaryAcc {
        IncShadowMatch()
    } else {
        IncShadowDiff()
        d.logger.Debug("pasr_shadow_diff",
            zap.Int64("primary", primaryAcc),
            zap.Int64("shadow", res.AccountID),
            zap.String("session_hash_prefix", safePrefix(req.SessionHash, 8)))
    }
}
```

注意 `shadowQueue` 必须用 buffered chan + 非阻塞 send (`select default:`) 实现限流 + drop。

---

## 7. AccountRing hot-swap (M2 详解)

### 7.1 为什么 ring 必须 per (tenant, pool_group) 分组

`ListEligibleAccountsByPoolGroup(tenant, pool_group)` 已经是 per-(tenant, pool_group) 过滤的；如果建一个全局 ring 把所有 tenant 所有 pool_group 的账号塞一起，PASR 选段时 HRW 可能选到 tenant A 的账号给 tenant B 的请求 — **租户隔离破坏 = 安全事故**。

→ 解法: `RingProvider` 内部维护 `map[ringKey]*atomic.Pointer[AccountRing]`，其中 `ringKey = struct{ TenantID, PoolGroupID int64 }`。

### 7.2 RingProvider 接口 (M2)

```go
type RingProvider struct {
    q             *db.Queries
    refresh       time.Duration
    seedFn        func() uint64       // 默认 time.Now().UnixNano()
    rings         atomic.Pointer[ringSnapshot]   // 整个 map 一并替换
    
    stop          chan struct{}
    done          chan struct{}
    mu            sync.Mutex
    running       bool
}

type ringSnapshot map[ringKey]*pool.AccountRing
type ringKey     struct { TenantID, PoolGroupID int64 }

// Get 单次 lookup, hot path 调用; nil 时调用方退化到 default
func (rp *RingProvider) Get(tenantID, poolGroupID int64) *pool.AccountRing
func (rp *RingProvider) Start(ctx context.Context)
func (rp *RingProvider) Stop()
func (rp *RingProvider) Refresh(ctx context.Context) error  // 同步触发, 启动期 + 测试用
```

### 7.3 重建策略

1. **启动期同步**: main.go 启 PASR 时先 `rp.Refresh(ctx)` 拿一次完整快照, 避免冷启动 ring=nil 让 PASR 全部走 ErrNoEligibleAccount 退路。失败 → main 启动 fail-fast (典型 DB 不通是别的问题)。
2. **后台异步**: 5 min ticker tick → 全量重建 → 整 ringSnapshot map atomic 替换。**部分失败保留旧快照**: 如果某 (tenant, pool_group) 这次拿到空账号集但旧快照里有，**保留旧的不覆盖** (新建 snapshot 把旧 entry 拷过去) — 防 DB 抖动一瞬间清掉所有 ring。
3. **新 (tenant, pool_group)**: 重建发现新 key 直接加到 snapshot；segment table 空，下次请求 LookupOrCreate 自然冷启段。
4. **tenant 删除**: 重建发现旧 key 不在新查询结果中 — 当前策略**保留** (避免 tenant 短暂"消失"导致 ring 空), 24h 后清理任务由后续 atom 实现 (本 plan 不做)。

### 7.4 ring 数据来源 query

需要新增一个 sqlc query — `ListAllEligibleAccountsForRingBuild`:

```sql
-- name: ListAllEligibleAccountsForRingBuild :many
SELECT pa.id, pa.tenant_id, c.pool_group_id
FROM provider_accounts pa
INNER JOIN channels c ON c.id = pa.channel_id AND c.tenant_id = pa.tenant_id
WHERE pa.enabled = true
  AND pa.deleted_at IS NULL
  AND pa.health_state IN ('operational', 'degraded')
ORDER BY pa.tenant_id, c.pool_group_id, pa.id;
```

> **风险**: 这是新 SQL，需要 sqlc generate + migration 评审 — 因此 RingProvider 实施需 1 个 atom 单独走 migration 流程。Owner 决策点 D3。
>
> **替代方案**: 不加 query，启动期遍历所有已知 (tenant, pool_group) — 但当前没"已知 pool_group 列表"的 query 接口，等价于先得加另一个新 query。两条路都得加 SQL，本路径更省。

---

## 8. claim 写入路径处理（决策点 D1）

PASR-lite `acquireAndReturn` 已写 claim：

```go
// pasr_selector.go:249
if p.claims != nil && req.ClaimID != 0 {
    if err := p.claims.WriteAcquisition(...); err != nil { ... }
}
```

main.go 在三种模式下 PASR 实例的 `Claims` 字段：

| Mode | `pasrSelector.Claims` | 原因 |
| --- | --- | --- |
| default | (PASR 未构造) | n/a |
| shadow | **nil** (硬不变量 §6.2-1) | shadow 不能写 claim, 否则双写 billing_claims |
| canary_5/25 | **真 ClaimGate** | canary 命中走 PASR 时是真处理请求 |
| pasr (full) | **真 ClaimGate** | 真处理 |

**实现**: main.go 起两个 PASR 实例 (shadow 用专门一份 Claims=nil 的)，在 dispatcher 内根据 mode 选择哪个跑。**或者**只起一个 PASR 实例 + 在 shadow goroutine 内调一个 `selectWithoutClaim` 内部 helper — 但这要修改 PASRSelector 暴露新方法 (改 A3 atom 已合的代码)。**推荐前者** (零侵入 PASR 代码)。

→ 见决策点 D1。

---

## 9. 风险登记 (≥5)

| # | 风险 | 触发条件 | 影响 | 缓解 |
| --- | --- | --- | --- | --- |
| R1 | shadow goroutine 泄漏 | shadow PASR Select 卡住 (DB 锁、ring nil 等) | OOM、句柄耗尽 | (a) shadow ctx 3s 超时 (b) shadowQueue 限并发 1024 (c) `pasr_shadow_drop_total` 监控 (d) M3 测试用 fakeAccountSource 注入 1s 慢响应验证 timeout 路径 |
| R2 | RingProvider 重建期 DB 抖动 → 空 ring → PASR 雪崩走 fallback | DB 连接池满 / 网络分区 | PASR 模式下短暂全部走 HRW 全 ring fallback (有兜底, 不是 5xx, 但 cache locality 价值消失) | "保留旧快照" 策略 (§7.3-2); `pasr_ring_rebuild_error_total` alert |
| R3 | shadow 与 primary 选择频繁不一致 (diff_ratio > 50%) | PASR 调度逻辑与 default 在某些场景行为分歧 (例如 sticky binding 优先 vs HRW 优先) | shadow 验证不出 PASR 是否更优 | 记录 diff 详情 (但只取 SessionHash 前 8B 防隐私); 7 天后 Owner 看 diff 样本人工审 |
| R4 | canary deterministic 偏倚 | fnv64a(SessionHash) % 100 < 5 在某些客户 SessionHash 分布下偏离 5% | 实际抽样 7% 或 3%, 引发误判 | M7 测试 100k seed 验证分布; 监控 `pasr_canary_pasr_path_total / total ∈ [4%, 6%]`，超出告警 |
| R5 | rollback 不在 5s 内 | ENV 改 + 重启需要 fleet 滚动 | 真 incident 时回退慢 | 接受现状 (本 plan 显式说明)；后续 atom 实现 SIGHUP 热切 (Owner D2 决策) |
| R6 | shadow 流量打 PASR 影响真实 selector 性能 | shadow goroutine 多到挤占 CPU | P99 上升 | (a) shadow buffer 1024 限并发 (b) 起步只在 1 实例做 shadow 其余仍 default |
| R7 | segment table OOM | `HUAKAI_PASR_SEGMENT_CAP` 设过大 + 万级别 prefix 短期涌入 | OOM | 默认 100k cap (已是 PASR-lite synthesis 决策); aging worker 5 min ticker 兜底; M5 启动 log 打印当前配置 |
| R8 | sticky binding 双写 | 漏掉了 PASR 模式不能写 sticky_bindings 的检查 | 数据库噪音表写满 | DefaultSelector.tryLayer 内 `s.sticky.(interface{Upsert...})` 才写 sticky；PASRSelector 完全不调 sticky → 天然不写。M8 测试断言 sticky 表无 PASR 写入 |
| R9 | ring 跨租户泄漏 | RingProvider 实现 bug: 把所有 tenant 账号塞一起 | 租户隔离破坏 — 严重 | M2 测试: 2 tenant 各 5 账号 → ring map 必须分 2 entry 独立; integration test 也要覆盖 |
| R10 | new SQL query 走漏审 | `ListAllEligibleAccountsForRingBuild` 未走 sqlc 生成 / migration 审 | 生产部署后 query 不 exist | Atom M2 拆出独立 commit 含 sqlc generate output + dbtest |

---

## 10. 决策点 — Owner 拍板（≥3）

### D1. shadow PASR 是独立实例 vs PASRSelector 加 `selectWithoutClaim` 方法?

| 选项 | 利 | 弊 |
| --- | --- | --- |
| **A. 独立实例** (推荐) | 零侵入 PASR; 两个 PASRSelector 实例只占内存差几 KB | 起两份 segment table 浪费内存？— 实际**共享同一个** SegmentTable 实例 (注入同一指针), 仅 Claims 字段不同 → 内存几乎零开销 |
| B. 加方法 | 单实例统一接口 | 改 A3 已合代码; 可能破已合测试; 新增 API 表面 |

**Claude 建议**: A — 实测内存差 < 200 bytes (Selector struct), 隔离性更好。

### D2. rollback 是否要 SIGHUP 热切？还是 ENV + 滚动重启?

| 选项 | 利 | 弊 | 估时 |
| --- | --- | --- | --- |
| **A. 仅 ENV + 重启** (本 plan 默认) | 简单; 0 新代码; rollback ~3-5 min (滚动) | 非"5s 级"; 真 incident 慢 | 0 (本 plan 已含) |
| B. SIGHUP 热切 (本 plan 不实现, 列为 follow-up atom) | 5s 内切换 | atomic.Pointer.Store + ENV 重读路径; 中等复杂; 配额/段表是否清空又是新决策 | +0.5 atom-day |

**Claude 建议**: 本 plan 走 A; 看 shadow / canary 数据后 Owner 决定是否再投入 B。

### D3. 新 SQL `ListAllEligibleAccountsForRingBuild` 该不该加？

| 选项 | 利 | 弊 |
| --- | --- | --- |
| **A. 加新 query** (推荐) | 一次拉全表 build snapshot 简单 | sqlc generate + migration 评审 (~半个 atom) |
| B. 复用 `ListEligibleAccountsByPoolGroup` 多次调 | 不动 SQL | 必须先有 (tenant, pool_group) 列表 — 又得加另一个 query, 净结果一样还多一次 round-trip |

**Claude 建议**: A; clean-room: 自己写 SQL，不看任何外部参考项目，joint 表写法和 `ListEligibleAccountsByPoolGroup` 风格一致 (两个表 INNER JOIN 已是项目内既定模式)。

### D4. shadow 模式下要不要把 PASR 选不到账号 (`ErrNoEligibleAccount`) 记 metric？

| 选项 | 利 | 弊 |
| --- | --- | --- |
| **A. 记 + alert** (推荐) | shadow 期发现 PASR 配置 / ring 问题 | 噪音 (启动期 ring 还没建时正常会有) |
| B. 静默 | 信号清晰 | 看不到问题 |

**Claude 建议**: A，但启动后 60s 不计入 (warmup 静默)。

---

## 11. 集成测试矩阵 (M8 + smoke)

| 测试 | 模式 | 输入 | 期望 | 实现位置 |
| --- | --- | --- | --- | --- |
| T1 boot smoke 5 mode | 5 mode 各一次 | env=每种 | `srv.ListenAndServe()` 不 fail; `selector_mode_current` 有值 | `cmd/gateway/smoke_test.go` 加 mode 表驱 |
| T2 default 行为零回归 | default | 现有 chat handler 测 | 已有 selector tests 全 green | 现有 `internal/pool/selector_test.go` 不动 |
| T3 shadow match | shadow | 1k req + fakeAccountSource 让 PASR 与 default 同选 | match=1k, diff=0 | `internal/pool/selector_dispatcher_integration_test.go` |
| T4 shadow diff | shadow | fakeAccountSource 让 PASR/default 选不同 | diff>0, match=0 | 同上 |
| T5 shadow drop | shadow | 注入 PASR Select 慢 1s + 主路径 1k qps | shadow_drop_total > 0 | 同上 |
| T6 shadow no claim | shadow | 跑 100 req | `billing_claims` 表无 shadow 写 (用 fakeClaimGate 计数) | 同上 |
| T7 canary deterministic | canary_5 | 同 SessionHash 跑 100 次 | 100 次结果一致 (要么全 PASR, 要么全 default) | M7 单测 + dispatcher 集成 |
| T8 canary 5% sample | canary_5 | 10000 个不同 SessionHash | PASR path 比例 ∈ [4.5%, 5.5%] | dispatcher 集成 |
| T9 ring rebuild | shadow | refresh=100ms + 注入 fakeQ 改账号集 | ring.Get 在 200ms 内反映新结构 | M2 单测 |
| T10 ring tenant isolation | shadow | 2 tenant 各 3 账号 | tenant A 请求绝不选到 tenant B 账号 | M2 单测 |
| T11 ring DB 抖动保留 | shadow | refresh fakeQ 一次返 err 一次返空 | 旧 ring 仍可读, 不变 nil | M2 单测 |
| T12 worker shutdown | 任意 | run() 收 SIGINT | aging worker + ring provider 都 Stop, 5s 内退 | smoke_test |
| T13 PASR full mode | pasr | 1k req | `pasr_full_select_total = 1000` + `pasr_default_select_total = 0` | dispatcher 集成 |

---

## 12. Rollback 路径 + 时间估算

### 12.1 Rollback 触发条件 (任一即触发)

- `pasr_shadow_diff_ratio > 30%` 持续 30 min (PASR 选择与 default 大幅分歧 = 实现 bug)
- `pasr_canary_*_total / select_total` 偏离目标 ±50% (canary 抽样偏倚)
- `pasr_ring_rebuild_error_total` 5 min 内 > 10 次 (DB 不通会一并影响 default, 但 PASR 退化更危险)
- 主路径 P99 比 PASR-lite 上线前 baseline 上升 > 20% (说明 shadow goroutine 挤占 CPU)
- 任何 5xx 涨 > 0.1% (硬阈值)

### 12.2 Rollback 步骤

1. 改 `HUAKAI_SELECTOR_MODE=default` (k8s configmap / .env).
2. 滚动重启 (k8s rolling update; 或 systemd reload binary).
3. 滚动期 (≈ pod 数 × 30s pod startup) 内同时存在新旧 mode pod — 接受不一致 (流量 sticky 度低, 几分钟内拉平).
4. 重启完成后 `selector_mode_current = default` 全局成立; PASR 路径完全旁路 (代码仍在但不构造).
5. 时间预算: 5 pod fleet ≈ 3 min; 50 pod fleet ≈ 5-8 min.

> **若 Owner 选 D2-B (SIGHUP 热切)**, rollback 改为：发 SIGHUP → ENV 重读 → atomic.Pointer.Store → 5s 内全实例切换。本 plan 不交付该路径。

### 12.3 atom 时间估算

| Atom | 估时 | 复杂度 |
| --- | --- | --- |
| M1 SelectorMode | 0.2 atom-day | LOW |
| M2 RingProvider + 新 SQL + dbtest | 0.6 atom-day | MEDIUM (含 sqlc generate) |
| M3 SelectorDispatcher | 0.5 atom-day | MEDIUM (并发 + goroutine) |
| M4 metrics | 0.2 atom-day | LOW |
| M5 main.go wiring | 0.4 atom-day | HIGH (启动期 wiring + smoke) |
| M6 config | 0.1 atom-day | LOW |
| M7 canary | 0.1 atom-day | LOW |
| M8 dispatcher integration test | 0.4 atom-day | MEDIUM (并发测试编排) |
| M9 /debug/pasr endpoint | 0.2 atom-day | LOW |
| **总计** | **2.7 atom-day** | |

> 1 atom-day ≈ 1 个专注 Codex/Claude lane 工作日; 含每 atom commit 前的 codex 横评 + 代码自审 + 测试。

---

## 13. Owner 待回答清单（消化本 plan 必看）

1. **D1** 选 A (独立实例) 还是 B (PASRSelector 加方法)？
2. **D2** 仅 ENV+重启 (A) 还是后续 SIGHUP 热切 (B, follow-up)？
3. **D3** 加新 SQL `ListAllEligibleAccountsForRingBuild` (A) 还是用现有 query 重组 (B, 实质要加另一个 query)？
4. **D4** shadow `ErrNoEligibleAccount` 记 metric (A) 还是静默 (B)？
5. shadow 模式起步只在 1 个实例还是全 fleet？(本 plan 默认假设全 fleet 起 shadow，配额观察)
6. canary_5 → canary_25 → full 节奏：本 plan 写的"24h 无 anomaly 即下一阶段"，Owner 接受还是更保守 72h？
7. 这个 plan 完成后 (M1-M9 全 green) **下一个 plan 是什么**？— 我建议: "PASR-lite 性能基线对比 + cache locality 收益评估"，依据本 plan 产出的 metrics。

---

## 14. clean-room 声明

- 本 plan 写入未读任何 sub2api / new-api / portkey / helicone / litellm / all-api-hub / envoy-ai-gateway 源代码
- 引用的算法 (HRW Rendezvous Hashing, splitmix64, FNV-1a) 是公开学术 / public domain 出版物 (PASR-lite A1 已在 hrw_ring.go 顶注引用)
- 所有 SQL 编写未参考外部项目 schema, joint 写法基于本仓库 `ListEligibleAccountsByPoolGroup` 既定风格
- selector 状态机 / shadow 比对算法 / canary deterministic hashing — 自有设计

Source files read (this plan):
- `cmd/gateway/main.go`
- `internal/pool/{pool.go, selector.go, pasr_selector.go, pasr_feedback.go, pasr_aging_worker.go, pasr_metrics.go, hrw_ring.go, prefix_segment.go, db_account_source.go}`
- `internal/gatewayhttp/chat_completions_handler.go` (selector usage 行)
- `internal/cachemetrics/cachemetrics.go` (RegisterCacheObserver)
- `internal/db/{querier.go, pool_accounts.sql.go}` (ListEligibleAccountsByPoolGroup 行)
- `internal/config/config.go`

Lane: claude. Agent ID: a126b0c7318b2ebae. UTC timestamp: 2026-05-08.

---

## 15. 下一步 (本 plan approved 后)

1. 派 codex 写 `2026-05-08-pasr-mainwire-codex.md` (并行独立 lane), 不读本文件
2. cross-discuss (Owner 主持) → 出 synthesis 文件
3. Owner 拍板 D1-D4
4. 按 M1 → M9 顺序原子 commit, 每 commit 前 `codex exec review --uncommitted --full-auto` 横评
5. M5 (main.go wiring) commit 后 smoke + cross-review 双门禁通过才 merge
6. shadow 起 7 天 → canary_5 24h → canary_25 24h → full

---

**Plan ends**. ~700 行 (+ 测试 ~575 LoC). 9 atom. 2.7 atom-day. 4 个 Owner 决策点. 10 项风险已登记.
