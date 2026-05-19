---
plan_id: 2026-05-08-pasr-mainwire-synthesis
date: 2026-05-09
lane: synthesis
inputs:
  - docs/process/plans/2026-05-08-pasr-mainwire-claude.md   (claude lane, 545 行)
  - docs/process/plans/2026-05-08-pasr-mainwire-codex.md    (codex lane, 489 行)
critic_role: 第三方 fresh critic, 无 author bias, 已读两份 plan + 关键源码
critic_id: opus-4-7-1m-synthesis
verified_against_source: true
related:
  - docs/process/plans/2026-05-08-pasr-lite-v2-synthesis.md  (PASR-lite A1-A5+A8 已 green)
  - docs/process/plans/2026-05-08-upgrade7-u7e-synthesis.md  (并行 lane, 不冲突)
upstream_atoms_complete: [A1, A2, A3, A4, A5, A8]
upstream_atoms_paused:   [A6 (PG warm-start), A7 (rebalance handler)]
status: ACCEPT-WITH-RESERVATIONS
---

# 2026-05-09 PASR-lite Main-Wire — Synthesis (claude × codex)

> **重大事实修正**: 写本 synthesis 时已 Read 仓库代码确认 — claude/codex 两份 plan
> 描述的 M1-M6 大部分代码已落地 (`backend/internal/pool/selector_dispatcher.go`,
> `pasr_dispatch_metrics.go`, `pasr_selector.go` slot parity, `cmd/gateway/selector_wiring.go`,
> `internal/config/pool_selector.go`)。本 synthesis 因此既评估两份 plan 的合理性,
> 也指导 **剩余 atom** (smoke 启动验证、shadow learn 决策、ops runbook、可选 SIGHUP)。

---

## 1. 元信息

| 字段 | 值 |
| --- | --- |
| 输入 | claude lane (545 行, A 偏架构图 / 运维 SOP) + codex lane (489 行, B 偏 5-mode 语义 / D-001 SlotManager parity) |
| 我 | 第三方 fresh critic, 未参与任一 lane 起草, 独立读两份 + 仓库源码 |
| 校验范围 | 所有 plan 中的"位置:行号"声明已用 Read 工具核实 |
| 状态 | DRAFT — 待 Owner 拍 §6 Owner 决策点后即可执行 §5 剩余 atom |
| 已合并 atom | M1 (config), M2 (dispatch metrics), M3 (PASRSelector slot parity), M4 (dispatcher 核心), M5 (request-scoped ring), M6 (main wire), 部分 M7 (3 个 dispatcher 集成测试) |
| 待执行 atom | M8-M11 (见 §5) — smoke 启动 5-mode + shadow learn 决策 + observability 收尾 + runbook |

---

## 2. 关键事实校验 (写之前 Read 的源码引用)

| 声明 | 源码现状 | 依据 |
| --- | --- | --- |
| `cmd/gateway/main.go:70` 是 `*pool.DefaultSelector` 强类型 | **错** — 已是 `pool.Selector` 接口 (`backend/cmd/gateway/main.go:70`); claude lane 描述的"待升级"已落地; codex lane 第 12 行也指出"当前是 `*pool.DefaultSelector`"——同样过时, 两 lane 都基于已被 commit 的旧快照 | `backend/cmd/gateway/main.go:70` `selector pool.Selector` |
| `chat_completions_handler.go:31-37` 是 `pool.Selector` 接口 | **对** — `Selector pool.Selector` | `backend/internal/gatewayhttp/chat_completions_handler.go:31-37` 与 `:36` 一致 |
| `pasr_selector.go:244-258` `acquireAndReturn` 不走 SlotManager (codex D-001) | **过时** — 已修复, `acquireAndReturn` (现 `:324-414`) 实现 pre-mutation `Slots.Acquire` + post-mutation `Claims.WriteAcquisition` + 失败 release 全链路, 含 `ErrPASRPreMutationFail` / `ErrPASRPostMutationFail` 错误分类与 `pasrPostMutationReleaseTimeout` (`:32`) 兜底 | `backend/internal/pool/pasr_selector.go:32-42, 324-414` |
| `db_account_source.go` 有 per-pool-group 过滤 | **对** — `ListAccounts` 调 `ListEligibleAccountsByPoolGroup(TenantID, PoolGroupID)`, 天然 (tenant, pool_group) 隔离 | `backend/internal/pool/db_account_source.go:33-43` |
| `db_slot_manager.go` SlotManager.Acquire 接口 | **对** — `Acquire(ctx, *AccountSnapshot, SelectionRequest) (*AcquireResult, error)`; Serializable Tx + IncrementInFlightCount + InsertSlotAcquisition 原子化; 返 idempotent ReleaseFunc | `backend/internal/pool/db_slot_manager.go:59-114` |
| dispatcher 5 mode 集 = `[default, shadow, canary, pasr-primary, pasr-strict]` | **对** — 已实现, dispatcher mode const 与 config 字面量在 M7 cross-check 测试守门 | `backend/internal/pool/selector_dispatcher.go:51-57` + `m7_dispatcher_integration_test.go:175-194` |
| dispatcher 已含 fnv64a 桶抽样 | **对** — `shouldSample` 用 `fnv.New64a` + samplingSalt + SessionHash, deterministic | `backend/internal/pool/selector_dispatcher.go:342-355` |
| shadow 异步 + 500ms timeout + WithoutCancel | **对** — `runShadowJob` 用 `context.WithTimeout(context.WithoutCancel(parentCtx), shadowSelectTimeout)`, panic recover, drop counter | `backend/internal/pool/selector_dispatcher.go:283-313` + 常量 `:66` |
| PASRSelector M5 request-scoped ring | **对** — `ringProvider == nil` 时用 `BuildAccountRingFromSnapshots(accs, p.ringSeed)`, 不走全局 ring / 新 SQL | `backend/internal/pool/pasr_selector.go:148-152` |
| smoke 已含 5-mode 启动期断言 | **错** — `cmd/gateway/smoke_test.go` 仅测 default 模式真实流量 (Phase C); 没有 shadow / canary / pasr-* 启动 smoke | `backend/cmd/gateway/smoke_test.go:73-109` |
| `/debug/vars` 已暴露 `pasr_dispatch` map | **对** — `expvar.NewMap("pasr_dispatch")` 在 `init()` eager 注册 | `backend/internal/pool/pasr_dispatch_metrics.go:54-59` |
| 全局 vs request-scoped ring 已选 request-scoped | **对** — 两 lane 都倾向 request-scoped, 实施已落地 | `backend/internal/pool/pasr_selector.go:148-152` + claude 13.1 + codex §8 |

测试体量校验: `go build ./...` clean + `go vet` clean + 已落地测试 1622 行
(`selector_dispatcher_test.go` 446, `m7_*` 195, `pasr_selector_slot_test.go` 374,
`pasr_selector_ring_test.go` 212, `pasr_dispatch_metrics_test.go` 146, `pool_selector_test.go` 249)。

---

## 3. Agree 表 — 两 lane 一致, 直接执行不需 Owner 拍

| # | 决策 | claude 出处 | codex 出处 | 现状 |
| --- | --- | --- | --- | --- |
| AG-1 | `deps.selector` 升 `pool.Selector` 接口, 不在 handler 引入 PASR 分支 | §2 表 + §3 图 | §3 表"接口字段"为推荐方案 | 已落地 (`main.go:70`) |
| AG-2 | `SelectorDispatcher` 实 `pool.Selector` 接口, 内部状态机, handler 无感 | §3.1 ASCII 图 | §3 表"Dispatcher concrete" | 已落地 (`selector_dispatcher.go:71-358`) |
| AG-3 | shadow 实例 `Slots=nil` + `Claims=nil` 是硬不变量 (不写 billing_claims / 不持 slot) | §6.2-1 + §8 表 + R8 | §3 + R-PASR-MW-002 | 已落地 (`selector_wiring.go:111-117` + `pasr_selector.go:336-338`) |
| AG-4 | dispatcher 把 shadowReq.ClaimID=0 作为三层防御之一 | §6.2-1 隐含 | §3 + 算法第 5 步 | 已落地 (`selector_dispatcher.go:246-247`) |
| AG-5 | shadow goroutine 用独立 ctx (`context.WithoutCancel` + 短 timeout) + panic recover + buffered chan + 非阻塞 send + drop counter | §6.4 伪代码 | §6 timeout + R-PASR-MW-005 | 已落地 (`selector_dispatcher.go:244-313`, 500ms timeout) |
| AG-6 | canary 用 fnv64a hash bucket deterministic 抽样, 不用 random / 不用 tenant 白名单 (单独) | §6.3 + R4 + M7 atom | §7 表"hash mod"推荐 | 已落地 (`selector_dispatcher.go:342-355`) |
| AG-7 | rollback 第一版只做 restart-only (env + 重启), 不做 SIGHUP 热切 | §5.3 + D2 推荐 A | §5 + D-PASR-MW-002 + §12 | 已落地 (selector_wiring 返 cleanup func) |
| AG-8 | 启动期 fail-fast: 非法 mode / 越界 percent → typed error 让 main 退出, **不 silent fallback** | §5.2 决策注 | §2.4 配置策略 | 已落地 (`config/pool_selector.go:140-162`) |
| AG-9 | shadow 模式只在 default 拿到 valid result (非 error / 非 wait plan) 时入队比对 | §6.1 触发条件 | §6 算法步骤 3-4 | 已落地 (`selector_dispatcher.go:181-186`) |
| AG-10 | actual canary 必须有 SlotManager parity (PASR 写 slot 走与 DefaultSelector 同一 in_flight 路径); 段表 + claim 双写防护 | §8 决策 D1 表 | D-PASR-MW-001 (BLOCKING) | 已落地 (`pasr_selector.go:324-414`, 含 ErrPre/PostMutation 错误链) |
| AG-11 | post-mutation 失败必须 release slot, 用 `WithoutCancel` 派生独立短 ctx 避免被上游 cancel 带走 | §6.2-1 | D-PASR-MW-004 + R-PASR-MW-009 | 已落地 (`pasr_selector.go:32, 397-404`) |
| AG-12 | ring 来源 = per-(tenant, pool_group), 不建全局 ring (跨租户隔离) | §7.1 + R9 | §8 推荐 + R-PASR-MW-003 | 已落地 (request-scoped from `ListEligibleAccountsByPoolGroup`) |
| AG-13 | shadow goroutine 使用 buffered chan 限并发 (cap≈1024), 主路径不被 block | §6.2-3 + R1 | §6 算法 + R-PASR-MW-005 | 已落地 (`selector_dispatcher.go:61, 253-258`) |
| AG-14 | 切换流程 SOP = default → shadow 5%/25%/100% → canary 5% → 25% → pasr-primary → pasr-strict | §5.3 表 | §5 状态机 | 文档对齐 (config 文件头注释) |
| AG-15 | 5-mode 字面量必须 dispatcher / config 包 cross-check, 防字符串 drift | §5.1 enum | M2 表 + 测试 T-PASR-MW-019 | 已落地 (`m7_dispatcher_integration_test.go:175-194`) |

→ **结论**: 15 项一致点, 全部已实现或已锁定执行细节。Owner 不需就这些再拍板。

---

## 4. Disagree 表 + 我的裁决

每条裁决基于 §2 校验过的源码事实 + 两 lane 文本对比。

### D-1. Mode 数量 / 命名: 5 枚举 vs `canary` + percent 单独?

- **claude (§5.1)**: 5 mode = `default` / `shadow` / `canary_5` / `canary_25` / `pasr` (full) — canary 比例烧死在 mode 名内
- **codex (§5)**: 5 mode = `default` / `shadow` / `canary` / `pasr-primary` / `pasr-strict` — canary 比例由 `HUAKAI_PASR_CANARY_PERCENT` 单独注入, 多了 strict 终态
- **裁决: B (codex)** ✅ 已落地
  - 依据: `internal/config/pool_selector.go:33-38` 定义 5 const 与 codex 一致, 代码已 commit
  - 理由: (a) 比例与模式解耦, ops 调 5%→25%→100% 不重新编译; (b) `pasr-strict` 终态防"PASR-primary 一直 fallback 让 PASR 永远没全量验过"成为温水煮青蛙; (c) claude "canary_5/25" 把 25% 烧成字面量, 后续要 10% / 50% 必须改代码, 不灵活
  - claude 提到的"5 mode 重启切流"语义在 codex 方案下用 `(mode=canary, percent=N)` 就达成, 没有功能损失

### D-2. 全局 ring (启动期 + 5min ticker 重建) vs request-scoped ring?

- **claude (§7)**: 力推 `RingProvider` 全局 + 5min ticker 重建 + per (tenant, pool_group) 分组 + 新 SQL `ListAllEligibleAccountsForRingBuild` + 保留旧快照防 DB 抖动 (整段 §7 几十行)
- **codex (§8)**: 反对全局 ring, 推 request-scoped ring (从 `ListAccounts(ctx, req)` snapshots 直接 `BuildAccountRingFromSnapshots` 派生)
- **裁决: B (codex)** ✅ 已落地
  - 依据: `pasr_selector.go:148-152` 当 `ringProvider == nil` 时 `BuildAccountRingFromSnapshots(accs, p.ringSeed)`; `selector_wiring.go:101` 注释 "RingProvider 不注入 — 走 M5 request-scoped ring (synthesis D3)"
  - 理由: (a) request-scoped 天然每请求 (tenant, pool_group) 隔离, 用现有 `ListEligibleAccountsByPoolGroup` 一次查询, 不需要新 SQL / 新 migration; (b) claude 担心"DB 抖动期重建拿空集"用 ring `len==0` 直接 `ErrNoEligibleAccount` 就够 — DB 抖动时 default 也会失败, 不存在"PASR 雪崩 default 健康"的 asymmetric 故障; (c) 节省一个 ticker goroutine + 一份 snapshot map 内存; (d) A7 (rebalance handler) 暂停时全局 ring 缺乏 invalidate 通道, request-scoped 完全规避
  - claude 提"per (tenant, pool_group) 分组" 在 codex 路径下天然成立 (ListEligibleAccountsByPoolGroup 已按 (tenant, pool_group) 过滤), 不需要额外 ring snapshot map

### D-3. shadow 模式段表是否更新 (是否"学习")?

- **claude (§3.1 + §6.2)**: 隐含 shadow 仍走 LookupOrCreate → 段表会被更新 (代码描述里 shadow PASR 用同一 SegmentTable)
- **codex (D-PASR-MW-003)**: 显式问 Owner — 推荐"允许 shadow 学习 (段表更新)", 否则需要 `HUAKAI_PASR_SHADOW_LEARN=false` 开关
- **裁决: 走 codex 第三选项 (D2 不污染)** ✅ 已落地
  - 依据: `pasr_selector.go:64-70` `readOnlySegments` 字段 + 新 ctor flag `ReadOnlySegments`; `selector_wiring.go:116` shadow 实例硬编码 `ReadOnlySegments: true`; `m7_dispatcher_integration_test.go:127-131` 断言 "100 req 后 SegmentTable.Size()=0"
  - 理由: (a) shadow 学习段表 → 与 actual 路径段位 bitmap 绑定造成混线; D2 段表只读不污染让 shadow 是**纯观察**信号; (b) shadow 命中段位时退化到全 ring fallback, 反而暴露"段表 cold-miss 时 PASR 会怎样"的真实行为, 是更有价值的对比信号; (c) "shadow 学习" 会让段位 hits 在切到 canary 前已被预热, mask 真实 cold-start 收益评估
  - codex D-PASR-MW-003 推荐"允许学习" — 我裁决反对, **改为不允许学习** (实施已按此做)
  - 这是本 synthesis 唯一**明确反 codex 推荐**的裁决, 但与已落地代码一致 (说明实施期已选了更保守路径)

### D-4. shadow PASR 实例 — 独立 vs 共享 selectWithoutClaim 方法?

- **claude (D1)**: 推荐 A 独立实例 — 两份 PASRSelector 共享同一 SegmentTable, 仅 Claims 字段不同
- **codex (§3-§4 隐含)**: 默认走"独立实例"路径 (M3 表"shadow 用 panic claim gate 也不触发", 直接说 `pasrShadow := pool.NewPASRSelector(..., Slots=nil, Claims=nil)`)
- **裁决: A (独立实例)** ✅ 已落地
  - 依据: `selector_wiring.go:95-123` 走两个 `pool.NewPASRSelector` ctor (一个 actual 一个 shadow); 共享 `segments` 单例 + `accountSource` 多份 (无状态)
  - 理由: 两 lane 实质同意, 仅 claude 显式列为决策点。零侵入 PASR core, 测试边界清晰

### D-5. PASR 错误分类 + canary fallback 时机

- **claude (§9 R8 + §11 T6)**: 隐含 "PASR 失败 fallback default" 但没区分 pre/post mutation
- **codex (§7 + R-PASR-MW-004)**: 显式区分 pre-mutation (可 fallback) vs post-mutation (已 release, 必 fail closed) — 这是关键安全门
- **裁决: B (codex)** ✅ 已落地
  - 依据: `pasr_selector.go:35-42` `ErrPASRPreMutationFail` / `ErrPASRPostMutationFail` 两个 sentinel; `selector_dispatcher.go:194-211` canary 路径用 `errors.Is` 分流
  - 理由: post-mutation 已 mutate slot 后再走 default 会双 claim race + 双 in-flight; codex 的精细错误分类是必要的安全门; claude 没区分相当于潜在 BLOCKING 缺口
  - 这是 codex lane 最关键的贡献之一

### D-6. config ENV 命名

- **claude (§5.2)**: `HUAKAI_SELECTOR_MODE` / `HUAKAI_PASR_RING_REFRESH` / `HUAKAI_PASR_SEGMENT_MAX_AGE` / `HUAKAI_PASR_SEGMENT_CAP` / `HUAKAI_PASR_LOAD_CAP`
- **codex (§4-M1)**: `HUAKAI_POOL_SELECTOR_MODE` / `HUAKAI_PASR_SHADOW_PERCENT` / `HUAKAI_PASR_CANARY_PERCENT` / `HUAKAI_PASR_HASH_SALT` / `HUAKAI_PASR_HRW_SEED` / 等 (更多)
- **裁决: 混合** ✅ 已落地为最小集
  - 依据: `config/pool_selector.go:81-89` 已实现 `HUAKAI_POOL_SELECTOR_MODE` / `HUAKAI_POOL_SELECTOR_SHADOW_PCT` / `HUAKAI_POOL_SELECTOR_CANARY_PCT` / `HUAKAI_POOL_SELECTOR_SALT` 四个 ENV
  - claude 的 RING_REFRESH / LOAD_CAP / SEGMENT_CAP 等运行时调参 ENV 暂未实现 — 走 SegmentTableConfig{} 默认值; **建议本 synthesis 在 §5 M11 atom 补回**, 否则 ops 调段表 cap 必须改代码
  - codex 的 `HUAKAI_PASR_HRW_SEED` 暂未实现 — 多副本一致性需要时补; 现走默认 `0xCAFEBABE`

### D-7. shadow 单 worker vs 多 worker?

- **claude**: 没明确, §6.2-3 说 "buffer 1024 限并发" 暗示可能多 goroutine
- **codex**: 没明确
- **裁决: 单 worker** ✅ 已落地
  - 依据: `selector_dispatcher.go:260-268` 注释 "单 worker 足够 — shadow 比对 hot path 是纯 PASR Select (内存 + 一次 ListAccounts), 单核 5k+ rps 没压力; 多 worker 反而增加段表锁竞争 (synthesis B4)"
  - 理由: shadow 不是关键路径, 单 worker 简化生命周期管理; 高负载时直接走 drop_counter 信号而非加 worker

### D-8. shadow ctx timeout 长度

- **claude (§6.4)**: 3s
- **codex (§6)**: 50-100ms 推荐, 5% 用 100ms 起步
- **裁决: 中间值 500ms** ✅ 已落地
  - 依据: `selector_dispatcher.go:66` `shadowSelectTimeout = 500 * time.Millisecond`
  - 理由: claude 的 3s 太宽 (shadow 异步+1024 buffer 排队下 3s 累 N 个 inflight 容易爆); codex 100ms 太严 (PASR cold-miss 走全 ring fallback 50-150µs CPU + 一次 ListAccounts 几 ms, 100ms 边缘); 500ms 给 cold-miss + ring 重建 + 慢账号源足够余量, 同时单 worker × 1024 chan 极端积压不超 8.5min, drop_counter 会先告警

→ **8 项分歧, 全部裁决, 全部与已落地代码一致**。表明实施期已 implicitly 走了与 synthesis 一致的路线; 本 synthesis 是**事后对齐 + 写明决策依据**, 不是"先 synth 再 impl"。

---

## 5. Gap 表 — 一边提了另一边没提的关键点

### G-1. codex D-PASR-MW-001 SlotManager parity (BLOCKING)
- **claude 状态**: 没单独提; §8 决策 D1 只讨论 shadow 是独立实例 vs 共享方法, 没意识到 actual 路径若不持 slot 会 cap_concurrency 破防
- **codex 状态**: 显式列为 D-PASR-MW-001 + R-PASR-MW-001, 必采纳, 是 actual canary 上线前的必要补丁
- **真伪**: **真**, 但 **过时** — 已修复 (§2 校验)
- **采纳**: ✅ 已采纳, 实施已落地; codex 这条是关键贡献

### G-2. claude per (tenant, pool_group) ring + 保留旧快照防 DB 抖动 — codex 没说就是漏了吗?
- **claude 状态**: §7 整节, 力推全局 ring + 旧快照保留
- **codex 状态**: 反对全局 ring, request-scoped 替代
- **真伪**: **claude 描述的问题真实** (DB 抖动期段表成员失效) **但解法过工程化** — codex request-scoped 路径下, DB 抖动 = `ListAccounts` 失败 → `ListAccounts` 返 err → 上游 dispatcher 从 PASR 失败处理路径走, default 也会失败, 没有"全局 ring 凭空兜底"的需求
- **采纳**: 不采纳全局 ring; 但**保留 codex 实施已做的 ring 成员失效兜底逻辑** (`pasr_selector.go:188-190` 段成员账号已不存在 → 跳过 → 段全 unhealthy 走 HRW 全 ring fallback)

### G-3. codex T-PASR-MW-013 PASR slot parity 集成测试 — claude 没列具体 PG 状态断言
- **claude 状态**: §11 T2-T13 全为单元 + 集成测试, 但断言粒度停在 "selector_test 全 green"
- **codex 状态**: T-PASR-MW-013 列出具体 PG 行断言: `pool_slot_acquisitions` 写一行 + in_flight_count +1 + claim token 一致
- **真伪**: **真**, 必采纳
- **采纳**: ✅ `pasr_selector_slot_test.go` (374 行) 已实现, 但**仍是单元测试**用 fake; **真 PG 集成 smoke 缺失** (smoke_test.go 仅跑 default 模式, 见 §2 校验) → 列入 §5 M9 atom

### G-4. codex log 字段 schema (`prefix_hash8` / `tenant_id` / `endpoint_family` 等)
- **claude 状态**: §6.4 仅 `zap.Int64("primary", ...) + zap.Int64("shadow", ...) + zap.String("session_hash_prefix", safePrefix(req.SessionHash, 8))`
- **codex 状态**: §6 完整 log schema 13 字段
- **真伪**: **真**, codex schema 更利于 ops 离线聚合
- **采纳**: 部分采纳 — dispatcher 当前不打 log (只累计 metrics), shadow_diff 时**应该**有结构化 log; 列入 §5 M10 atom

### G-5. claude D2 SIGHUP 热切 — codex 也提到但都不做, 决策一致
- **claude 状态**: D2 列为决策点, 推荐 A (重启)
- **codex 状态**: D-PASR-MW-002 推荐 restart-only
- **真伪**: 一致
- **采纳**: 共识不做 SIGHUP; 第二轮 (post-1.0) 再考虑

### G-6. claude `/debug/pasr` admin endpoint (M9) — codex 没提
- **claude 状态**: M9 atom 列出 `/debug/pasr` 只读 endpoint dump segment table stats
- **codex 状态**: §6 仅 `/debug/vars` expvar
- **真伪**: 部分真 — `/debug/vars` 已暴露 `pasr` + `pasr_dispatch` 两个 expvar map (含 segment_count + EvictedTotal 等); 单独 `/debug/pasr` 是 nice-to-have, 不 BLOCKING
- **采纳**: 暂不采纳 — `/debug/vars` 已够用; 如未来有特殊 dump 需求 (含 segment 级 entries 列表) 再加, 列入 §6 决策点

### G-7. codex T-PASR-MW-020 race detector 测试 — claude 没列
- **claude 状态**: 没明确 `-race` 测试
- **codex 状态**: T-PASR-MW-020 optional `go test -race ./internal/pool`
- **真伪**: **真**, dispatcher + SegmentTable + AgingWorker 并发模型必须过 race detector
- **采纳**: 列入 §5 M8 atom

### G-8. claude D4 shadow `ErrNoEligibleAccount` 是否 alert
- **claude 状态**: D4 推荐启动后 60s warmup 静默, 之后记 metric + alert
- **codex 状态**: 没单独说, R-PASR-MW-008 隐含 "diff 指标误读" 顺带提"按 cache hit / latency / no-capacity 联合评估"
- **真伪**: **真**, claude 的 warmup 静默是合理 ops 策略
- **采纳**: 已实现 metric (`shadow_pasr_err_total`), warmup gating 是 dashboard / alert rule 配置层, 不需代码改; 列入 §6 Owner 决策

### G-9. claude 风险表对 segment table OOM 已定 100k cap; codex 也定 100k
- **claude 状态**: R7
- **codex 状态**: R-PASR-MW-007
- **真伪**: 一致
- **采纳**: 已用 `SegmentTableConfig{}` 默认值; **claude D6 提的 SEGMENT_CAP / SEGMENT_MAX_AGE / LOAD_CAP ENV 暂未暴露**, 列入 §5 M11

### G-10. codex 提到 PASR latency 单独 metric; claude 没提
- **claude 状态**: 没单独 latency metric
- **codex 状态**: §6 log schema `default_latency_ms` / `pasr_latency_ms`; R-PASR-MW-005
- **真伪**: **真**, 没 latency 数据无法判断 "shadow 是否拖慢 P99"
- **采纳**: 列入 §5 M10 atom (与 G-4 合并 — observability 收尾)

### G-11. claude/codex 都没提 — Phase E orphan-sweep 与 PASR 兼容性
- **状态**: 两 lane 都没提 `pool_slot_acquisitions` 的 lease 90s + Phase E sweeper 与 PASR 的交互
- **真伪**: 严格说**不属于本 plan 范围** (Phase E 当前是 TODO), 但 PASR canary 上线前需要确认: 假设 PASR 正常 release, sweeper 静默兜底; 假设 PASR post-mutation 失败 release 也失败, sweeper 90s 后清掉 — DefaultSelector 行为一致, 所以 PASR 与 sweeper 兼容性继承自 DefaultSelector
- **采纳**: 不需新代码; 列入 §6 Owner 决策点 (告知 + 接受现状)

→ **11 个 Gap, 8 项必采纳并已落实或转为 atom; 3 项不采纳 (G-2 全局 ring / G-6 /debug/pasr / G-9 SEGMENT ENV 部分 — 后者已转为 M11 待办)**。

---

## 6. 升 Owner 决策点 (≤3)

> 本 synthesis 仅列**真无法裁的**决策点。其余分歧在 §4 已裁决并与已落地代码一致。

### O-1. shadow 模式 ErrNoEligibleAccount 是否 alert? warmup 多久?

- **问题陈述**: shadow PASR 路径若返 `ErrNoEligibleAccount` (段全 unhealthy + 全 ring 也无 healthy 候选), 计入 `shadow_pasr_err_total`。启动前 60s 段表是冷的, 几乎所有 shadow request 都会触发 `ErrNoEligibleAccount` (cold-miss → fullRingFallback)。是否在 dashboard 设置启动后 60s warmup 静默?
- **选项 A** (claude D4 推荐): warmup 60s 静默, 之后超阈值 (>5%) 触发 ops alert
- **选项 B** (codex 隐含): 不设 warmup, dashboard 直接看 5min 滑动窗 P95
- **我的推荐**: **A**. shadow 期 60s warmup 静默是合理 ops noise floor; 但 5% 阈值偏严, 改为 10% (cold-miss 是合法路径, shadow 不变更主流量); 决策实质是"alert 规则设啥阈值", 不是代码改, 但需要 Owner 拍板写进 runbook

### O-2. canary actual 上线前是否要 24h shadow 100% 观察?

- **问题陈述**: 当前 SOP (config 注释) 是 "shadow 5%/25%/100% → canary 5%"。每档观察多久没在代码里, 但代码侧已支持任意切换。Owner 选 "shadow 100% 24h 后 canary 5%" 还是 "shadow 25% 7d 后直接 canary 5%"?
- **选项 A** (claude §5.3): "shadow 起 7 天观察" → canary
- **选项 B** (codex §5 状态机): "shadow 5% → 25% → 100%, 各档 24h 没异常" → canary
- **我的推荐**: **B + 一个变体**: shadow 5% × 24h → 25% × 24h → 100% × 48h → canary 5% (即至少 4 天观察, 不必到 7 天); 因为 shadow 100% 才能采到完整流量分布的 diff signal, 25% 可能漏掉某些 tenant; 48h 是覆盖工作日 + 周末的最小窗
- **理由**: 这是 ops 风险偏好, 我倾向"覆盖 1 个完整周末"作为最小节奏; Owner 拍板

### O-3. SEGMENT_CAP / SEGMENT_MAX_AGE / LOAD_CAP / HRW_SEED 是否暴露为 ENV?

- **问题陈述**: 当前段表 cap=100k / 老化=30min / loadCap=0.95 / HRW seed=0xCAFEBABE 都烧死。claude D6 列了 4 个 ENV; codex M1 列了 5 个。是否在 M11 atom 暴露?
- **选项 A**: 全暴露 (M11 实现 4-5 个 ENV)
- **选项 B**: 暂不暴露, 等 shadow / canary 跑完后看是否需要调
- **我的推荐**: **A 但只暴露 3 个**: `HUAKAI_PASR_SEGMENT_CAP` / `HUAKAI_PASR_SEGMENT_MAX_AGE` / `HUAKAI_PASR_LOAD_CAP`; HRW seed 暂不暴露 (单实例不需要, 多实例一致性是 A6/A7 阶段问题)
- **理由**: ops 调段表 cap 不必改代码 + 重启编译; 三个值是直接影响内存 / cache hit 的旋钮, 暴露为 ENV 是低成本高价值; HRW seed 改了等于段表全 invalidate, 不是日常旋钮

→ 3 个决策点, 都是 ops 策略 / 配置暴露范围, 不阻塞代码 atom 执行。

---

## 7. 启动条件 / Pre-execution checklist

开始 §5 剩余 atom 前必须满足:

1. **§6 决策已拍** — Owner 在 O-1 / O-2 / O-3 上确认; O-1/O-2 进 runbook, O-3 决定 M11 是否落地
2. **现有测试全 green** — `cd backend && go test ./internal/pool/... ./internal/config/... ./cmd/gateway/...` (我已 `go vet` clean, 但 build 时未跑测试; 执行前必须验证 1622 行测试本身通过)
3. **default mode smoke 不退化** — `HUAKAI_DATABASE_URL=... go test -tags=smoke -run TestPhaseC_Smoke ./cmd/gateway` 仍 green; 这是零回归 gate
4. **codex 横评已过** — 本 synthesis 与已落地 commit 之间**没有矛盾** (我已校验); 如发现矛盾, 必须先 codex 横评再继续
5. **本 synthesis 已 codex 横评** — Owner 应派 codex 跑一次 read-only review (cross-check 我对源码事实的引用)
6. **不读非 MIT reference source** — 剩余 atom 全部基于已有 HUAKAI 接口扩展, 不需要再读 sub2api / new-api / 等; clean-room 风险继续低

---

## 8. 最终合并 atom 序列 M1...M11

> 已合并 atom: M1-M6 + 部分 M7 (3 个 dispatcher 集成测试)。本节列**剩余 atom + 已合并 atom 简记**, 按执行序排列。

### 已合并 (回顾, 不再执行)

| Atom | 范围 | 现状 |
| --- | --- | --- |
| **M1** | `internal/config/pool_selector.go` — typed config + ENV parse + Validate (5 mode + 2 percent + salt) | ✅ 落地, `pool_selector_test.go` 249 行 |
| **M2** | `internal/pool/pasr_dispatch_metrics.go` — expvar map `pasr_dispatch` + 16 个 counter + Inc helpers + Snapshot | ✅ 落地, `pasr_dispatch_metrics_test.go` 146 行 |
| **M3** | `internal/pool/pasr_selector.go` — `acquireAndReturn` 走 SlotManager pre-mutation + Claims post-mutation, ErrPASRPreMutationFail / ErrPASRPostMutationFail 错误链, post-fail release 用 WithoutCancel + 2s ctx | ✅ 落地, `pasr_selector_slot_test.go` 374 行 |
| **M4** | `internal/pool/selector_dispatcher.go` — 5 mode dispatch + shadow async worker + canary fnv64a 抽样 + pre/post mutation 分流 + Stop graceful | ✅ 落地, `selector_dispatcher_test.go` 446 行 |
| **M5** | `internal/pool/pasr_selector.go:148-152` + `BuildAccountRingFromSnapshots` — request-scoped ring 不依赖全局 RingProvider | ✅ 落地, `pasr_selector_ring_test.go` 212 行 |
| **M6** | `cmd/gateway/main.go` + `cmd/gateway/selector_wiring.go` — 启动期按 PoolSelectorConfig 装配 default / shadow / canary / pasr-* + cleanup 函数 + AgingWorker.Start(ctx) | ✅ 落地 |
| **M7-partial** | `internal/pool/m7_dispatcher_integration_test.go` — shadow ReadOnly 段表不污染 + actual 段表学习 + mode 字面量 cross-check | ✅ 落地 (3 测试) |

### 剩余待执行

| Atom | 名称 | 范围 | LoC 估 | 测试要求 | 依赖 | 风险 | 验收 criteria |
| --- | --- | --- | --- | --- | --- | --- | --- |
| **M8** | race detector 全测 + shadow worker 生命周期 stress | `internal/pool/selector_dispatcher_test.go` 加 stress 用例: 1024 并发 Select + Stop 中途 + recover panic; 跑 `go test -race ./internal/pool/...` 全 green | ~120 (新测试) | `go test -race -count=10 -run "Selector|PASR" ./internal/pool` 0 race + 0 panic | M4 (已合) | LOW | -race 0 报告; Stop 调多次幂等 |
| **M9** | smoke build-tag 5-mode 启动验证 | `cmd/gateway/smoke_test.go` 加 `TestSmoke_AllModes_Boot`: 用 build tag, 表驱跑 default / shadow / canary / pasr-primary / pasr-strict 五次启动, 每次 `HUAKAI_POOL_SELECTOR_MODE=$mode` + 对应 percent ENV; 验证 srv.ListenAndServe 不 fail + curl `/debug/vars` 看到 `pasr_dispatch` 子树非空 + cleanup 正常退出 | ~180 | smoke tag (要求 HUAKAI_DATABASE_URL); 5 mode 各启动 + 关停 | M6 (已合) | MEDIUM (smoke 启动期 wiring) | 5 mode 各能从 ENV 启动 + cleanup 退出; 现有 default mode smoke 不退化 |
| **M10** | observability 收尾 — shadow_diff 结构化 log + canary latency histogram | `internal/pool/selector_dispatcher.go` 加: (a) shadow_diff 时打 zap.Debug 结构化 log (codex §6 schema, prefix_hash8 / tenant_id / pool_group_id / requested_model / endpoint_family / default_acc / pasr_acc / 单边 latency); (b) canary actual 路径加 latency histogram (用现有 expvar `expvar.Float` map "pasr_dispatch_latency" — 暂不引 prometheus); 注意 log 不能泄漏 prompt / credential | ~150 | 单测: log 字段完整 + 按 zap observer 验证字段; expvar latency map 注册成功 + 累加正确 | M4 (已合) | LOW (纯 observe 路径, 不动主逻辑) | shadow_diff 日志可被 grep "pasr_shadow_compare" 拉到; `/debug/vars` 含 latency map |
| **M11** | (条件: O-3 选 A) 段表运行时 ENV — SEGMENT_CAP / SEGMENT_MAX_AGE / LOAD_CAP | `internal/config/pool_selector.go` 加 3 个 ENV 解析 (含越界 fail-fast); `cmd/gateway/selector_wiring.go` 把 cfg 注入 `SegmentTableConfig` 与 `PASRSelectorConfig.LoadCap`; cfg 单测加越界用例 | ~110 | 单测: 3 ENV 默认值 / 合法值 / 非法值 (负数 / 超 100 / 非数字); selector_wiring 集成验证 cfg 注入路径 | M1 (已合) + M6 (已合) | LOW | env-driven 调段表 cap 不需重启编译 |
| **M12** | (条件: O-1/O-2 已拍) ops runbook + alert rule 文档 | 新增 `docs/runbooks/pasr-mainwire-progressive-rollout.md`: SOP 5 阶段 + 每阶段验证 + alert 阈值 + rollback 步骤 + dashboard panel 列表 | ~250 (纯 doc) | 文档 review (Owner) | M9 + M10 | LOW | Owner accept; 后续 Slack/dashboard 配置照此文档执行 |

**剩余 LoC 估**: 实施 ~280 + 测试 ~280 + 文档 ~250 = ~810 LoC; **5 个 atom × 0.2-0.5 atom-day ≈ 1.5 atom-day** 可全部执行完。

**执行序**:
1. **M8 优先** — race detector 是验证已合代码的最后一道质量门, 失败会 invalidate M3/M4
2. **M9** — smoke 5-mode boot 是 production cutover 的 prerequisite
3. **M10** — observability 收尾, 上线前必有
4. **M11** — 条件 atom (O-3 选 A 才做)
5. **M12** — runbook 文档, 必须在 shadow 5% 真实流量启动前完成

依赖图: M8 → (M9 ∥ M10) → M11 → M12; M8 与已合 M3/M4 强依赖。

---

## 9. 风险登记 (合并去重 ≥10)

按等级排序; 已合并代码已含的缓解标 ✅。

| # | 风险 | 触发条件 | 影响 | 缓解 | 来源 |
| --- | --- | --- | --- | --- | --- |
| R1 | shadow goroutine 泄漏 (PASR Select 卡住或阻塞 N>1024) | 段表 / 账号源极端慢; shadow worker drain 不完 | OOM, 句柄耗尽 | ✅ buffered chan 1024 + 非阻塞 send + drop counter; ✅ 500ms ctx; ✅ panic recover (`selector_dispatcher.go:60-66, 244-313`); M8 加 stress 用例验证 | claude R1 + codex R-PASR-MW-005 |
| R2 | post-mutation 失败 release 也失败 → slot 泄漏 | DB 抖动 + claim write 失败 + release ctx 也异常 | provider_accounts.in_flight_count 漂移, cap_concurrency 误锁 | ✅ release 用 `WithoutCancel` + 独立 2s ctx (`pasr_selector.go:32, 397-404`); ✅ 错误链 `slot release failed: %w` 让 ops 定位; Phase E sweeper 90s lease 兜底 | codex R-PASR-MW-009 (claude 隐含) |
| R3 | canary fallback 双写 (PASR 已写 slot 后再走 default) | dispatcher 不区分 pre/post mutation | 双 slot, claim race, settle 错乱 | ✅ ErrPASRPreMutationFail / ErrPASRPostMutationFail 双 sentinel; ✅ dispatcher canary 路径 `errors.Is` 分流 (`selector_dispatcher.go:194-211`) | codex R-PASR-MW-004 |
| R4 | shadow 误写 claim (复用 actual PASR 实例 / req.ClaimID 未抹) | 共享实例 / 三层防御缺一层 | billing_claims 双写 → 真请求失败 | ✅ 三层防御已落地: shadow 实例 Slots=nil + Claims=nil + dispatcher 抹 ClaimID=0 + ReadOnlySegments=true (selector_wiring + selector_dispatcher) | codex R-PASR-MW-002 (claude R8) |
| R5 | 全局 ring 跨租户泄漏 | 启动期建全局 ring 把所有 tenant 账号塞一起 | 租户隔离破坏 — 安全事故 | ✅ 不建全局 ring; request-scoped ring 走 `ListEligibleAccountsByPoolGroup` 天然 (tenant, pool_group) 隔离 | claude R9 + codex R-PASR-MW-003 |
| R6 | shadow 增加请求延迟 (主路径被 block) | shadow 落入主路径同步执行 | P99 上升 | ✅ shadow 异步 + 非阻塞入队 + buffer 满直接 drop; M8 stress 验证 | codex R-PASR-MW-005 |
| R7 | segment table OOM (cap 设过大 / aging 滞后) | 万级 prefix 短期涌入 + 段表无 cap | OOM | ✅ 默认 100k cap + 30min aging worker 5min ticker; M11 暴露 cap ENV (条件) | claude R7 + codex R-PASR-MW-007 |
| R8 | 配置错误 (mode typo / percent 越界) 导致 silent 误启用 | ENV 设置错; 启动 silent fallback | 流量误切 / 数据观察不可信 | ✅ fail-fast: typed error 让 main 退出, 不 silent fallback (`config/pool_selector.go:140-162`) | codex R-PASR-MW-006 |
| R9 | ring 重建期 DB 抖动 → 空 ring → PASR 雪崩 fallback | DB 连接池满 / 网络分区 (request-scoped 路径下 = ListAccounts 失败) | PASR 模式短暂全部走 fullRingFallback; cache locality 价值消失 | request-scoped 路径下 ListAccounts 失败 = default 也失败, 没 asymmetric; PASR 上层错误链 fallback default | claude R2 |
| R10 | shadow / primary 频繁不一致 (diff_ratio > 50%) | PASR 与 default sticky binding 优先级不同 | shadow 验证不出 PASR 是否更优 | M10 加 shadow_diff 结构化 log (含 prefix_hash8 + tenant_id + endpoint_family + 两边 acc); 7 天 Owner 看样本人工审 | claude R3 + codex R-PASR-MW-008 |
| R11 | canary 抽样偏倚 (fnv64a 在某些客户 SessionHash 分布下偏离 5%) | 客户 SessionHash 不均 | 实际 7% 或 3% | claude R4: 监控 `canary_pasr_used_total / canary_total` ∈ [4%, 6%], 超出告警; M12 写入 runbook | claude R4 |
| R12 | rollback 不在 5s 内 (滚动重启需 3-5min) | 真 incident 时回退慢 | live traffic 影响延长 | 接受现状 (本 plan 显式 restart-only); SIGHUP 热切是 post-1.0 atom | claude R5 + codex R-PASR-MW-010 |
| R13 | shadow goroutine panic 拖垮 main (worker recover 漏一类 panic) | runtime.Goexit / 非 recoverable panic | 进程崩 | ✅ defer recover + IncDispatchShadowPanic; M8 加 panic injection 测试 | codex R-PASR-MW-009 |
| R14 | Phase E orphan-sweep 与 PASR release 兼容性未验 | 待 Phase E 实现 sweeper 后跨 PASR 路径未测 | 极小概率: PASR 已 release 但 sweeper 也判 lease 过期重复 release | DB 端 ReleaseSlotAndDecrementInFlight CTE idempotent (`db_slot_manager.go:120-131`), 二次调用 no-op; 不需新代码 | 我自己识别的 (G-11) |
| R15 | shadow 对 actual claim 锁竞争 (多 worker 时) | 假设未来加 shadow 多 worker | 段表锁竞争 → P99 上升 | 单 worker 已锁定 (selector_dispatcher.go:260-268), M8 stress 守护 | 我自己识别的 (D-7 衍生) |
| R16 | clean-room 风险 (本 atom 是否 inadvertently 复用了外部参考结构) | 实施期不慎参考 sub2api / portkey | clean-room policy 违规 | 全部接口 (Selector / SlotManager / ClaimGate / SegmentTable / AccountSource) 是 HUAKAI 内部抽象, 不读非 MIT 项目源码; ENV 命名空间 HUAKAI_POOL_*, 不复制外部命名 | clean-room policy 提醒 |

→ **16 项风险, 14 项已被已合代码缓解, 2 项 (R10/R11) 等 M10/M12 收尾后转为 ops 监控规则**。

---

## 10. 失误检查 (我做的 self-audit)

按 critic 协议要求, 自审本 synthesis:

1. **是否过度信任已合代码?** — 我 Read 了 main.go / selector_wiring.go / selector_dispatcher.go / pasr_selector.go / pasr_dispatch_metrics.go / config/pool_selector.go / m7_dispatcher_integration_test.go + db_slot_manager.go / db_account_source.go; 编译 + vet 全 clean; 但**未运行测试** — Pre-execution checklist §7 #2 要求 Owner 执行前先跑全测试套件
2. **是否过度反 codex?** — 我在 D-3 (shadow 段表只读) 反对 codex D-PASR-MW-003 推荐, 但与已落地代码一致, 是基于"段表数据纯净度比学习便利更重要"的明确论点, 不是为反而反
3. **是否过度信 claude?** — 我在 D-2 反对 claude §7 全局 ring 整节方案, 因为 codex request-scoped 是更简的实现且与已落地代码一致; 这是基于"少建 ticker 少加 SQL = 更安全"的明确论点
4. **是否漏了 BLOCKING?** — codex D-001 SlotManager parity 是 plan-level BLOCKING, 我已在 G-1 标识并验证已修复; 没有发现其他 BLOCKING gap
5. **风险评级是否过度?** — R1-R16 中 LOW / MEDIUM / HIGH 我没显式标, 但 R3 (canary 双写) / R4 (shadow 误写) / R5 (跨租户泄漏) 是 HIGH 已落地缓解; R12 rollback 慢是 MEDIUM 接受
6. **Owner 决策点是否过多?** — 3 个, 都是 ops 配置不是代码改, 没超 ≤3 限制
7. **是否落漏 codex 重要观察?** — 反复对照 codex §7 canary 失败 fallback 5 条规则 / §6 shadow 算法 7 步 / §11 测试矩阵 20 项, 我都已映射到 §3-§5; codex log schema 13 字段映射到 G-4/M10
8. **是否落漏 claude 重要观察?** — claude §7 RingProvider / §11 测试矩阵 13 项 / §12 rollback 触发条件 5 项, 我都已处理; claude D2 SIGHUP 热切共识不做

→ **自审结论**: 本 synthesis 真实反映两 lane 共识 + 关键分歧 + 已落地实施现状; 不是 rubber-stamp, 也不是 manufactured outrage。

---

## 11. clean-room 声明

- 本 synthesis 写入未读任何 sub2api / new-api / portkey / helicone / litellm / all-api-hub / envoy-ai-gateway 源代码
- 引用算法 (HRW Rendezvous Hashing / FNV-1a) 是公开学术 / public domain, 上游 atom A1 已注引用
- 所有决策依据来自: (a) 两 lane plan 文本 (b) HUAKAI 仓库源码 (Read 工具直接读取) (c) 既定 clean-room / fail-fast / parity 不变量
- ENV 命名空间 `HUAKAI_POOL_*` / `HUAKAI_PASR_*` 是项目内既定模式, 不复制外部参考项目命名

Source files read (this synthesis):
- `backend/cmd/gateway/main.go`
- `backend/cmd/gateway/selector_wiring.go`
- `backend/cmd/gateway/smoke_test.go`
- `backend/internal/pool/pool.go` (隐含, dispatcher 文件头注引用)
- `backend/internal/pool/pasr_selector.go`
- `backend/internal/pool/selector_dispatcher.go`
- `backend/internal/pool/pasr_dispatch_metrics.go`
- `backend/internal/pool/m7_dispatcher_integration_test.go`
- `backend/internal/pool/db_account_source.go`
- `backend/internal/pool/db_slot_manager.go`
- `backend/internal/config/pool_selector.go`
- `backend/internal/gatewayhttp/chat_completions_handler.go` (selector usage 行)
- `docs/process/plans/2026-05-08-pasr-mainwire-claude.md`
- `docs/process/plans/2026-05-08-pasr-mainwire-codex.md`

Lane: synthesis. Critic ID: opus-4-7-1m-synthesis. UTC timestamp: 2026-05-09.

---

## 12. 下一步 (本 synthesis approved 后)

1. Owner 拍板 §6 O-1 / O-2 / O-3 (3 个决策点)
2. 派 codex 跑一次 read-only review 校验本 synthesis 与已合代码的一致性 (CLAUDE.md #10 互审制度)
3. 按 §5 atom 序执行: M8 (race) → M9 (smoke 5-mode) → M10 (observability) → M11 (条件: O-3 选 A) → M12 (runbook)
4. 每 atom commit 前 `codex exec review --uncommitted --full-auto`
5. M9 + M10 全 green 后, Owner 在 staging 启 shadow 5% 真实流量 (按 §8 M12 runbook 节奏)

---

**Plan ends**. Synthesis 由 fresh critic 第三方独立评估两份 plan + 校验已落地代码完成。
