# Sprint C Day 1 — Red Tests Spec

**日期**: 2026-05-09
**前置 plan**: `docs/plans/2026-05-09-next-pivot-synthesis.md`
**目的**: Day 1 写"先红后绿"测试。Day 2-4 让它们绿。本文件**不写代码**，只给 executor 照写的 spec。
**范围**: 6 个测试覆盖综合 plan §测试策略+验收标准的 6 项验收。
**纪律**: 全部测试只触 HUAKAI 内部 `cachemetrics`/`pool`/`proto` 包；不读 `~/refs/`。

## 共享 fixture 约定

- **TenantID/AccountID 取值**: tenant=`9001`, account=`8001`（皆非 0，避开 short-circuit）
- **PrefixHash 取值**: `"prefix-day1-red"`（≤32B，segmentKey 走 raw 模式）
- **HRW ring**: 用 `pool.NewAccountRing` 构造，注入 3 个 account ID（含 8001），保证 `LookupOrCreate` 把 8001 选进 Members
- **Now 注入**: 测试用 `func() time.Time { return time.Unix(1700000000, 0) }`（固定时刻，便于断言 LastReadAt）
- **Observer reset**: 每个测试前调 `cachemetrics.ResetObservers()`（如不存在则 Day 2 顺手加 export_test helper）

---

## 测试 1: OpenAI SSE cache_tokens 触发 PASR

**File**: `backend/internal/proto/openai_sse_pasr_test.go`（新建）
**Function**: `TestOpenAISSE_CacheReadTokens_TriggersPASRObserver`
**Setup**:
- 构造 `SegmentTable` + `AccountRing(8001, 8002, 8003)`
- `LookupOrCreate(9001, []byte("prefix-day1-red"), ring)` 预创建 segment
- `RegisterPASRCacheFeedback(segments)` 注册 observer
- 构造 `OpenAIUpstreamState{TenantID: 9001, AccountID: 8001, PrefixHash: "prefix-day1-red", AccumulatedUsage: CanonicalUsage{CacheReadInputTokens: 137}}`
**Action**:
- 调 `finalizeOpenAIState(state, false)` (`backend/internal/proto/openai_sse.go:395`)
**Assertion**:
- `seg := segments.Lookup(9001, []byte("prefix-day1-red"))` 非 nil
- `seg.LastReadAt.Load() == fixedNow.UnixNano()`（MarkRead 已调）
- `seg.MissCount[idx].Load() == 0`（cache_read 路径 ResetMissCount 已调；idx = `seg.IndexOf(8001)`）
- `IncCacheHitObs` 计数 +1（用 `pasr_metrics_test.go` 的 expvar snapshot 模式读）
- `seg.HasCache(idx) == false`（**Decision 1 红测**：当前实现只在 CacheCreation>0 时 set bit，本测试故意期望 **绿后** Day 2 把 read-hit 也 set bit；Day 1 红 → 这条 assert 在当前 code 下就会失败，正是想要的红状态）
**失败诊断**: 若 `seg == nil`，segment 在 finalize 前被 evict（检查 fixed-now wiring）；若 LastReadAt 未变，说明 `pasr_feedback.handle` `obs.CacheRead > 0` 分支未走，去看 `cachemetrics.go:228` short-circuit 是否吃掉事件。

---

## 测试 2: Gemini SSE cachedContentTokenCount 触发 PASR

**File**: `backend/internal/proto/gemini_sse_pasr_test.go`（新建）
**Function**: `TestGeminiSSE_CachedContentTokens_TriggersPASRObserver`
**Setup**:
- 同测试 1 构造 SegmentTable + ring + observer
- 构造 `GeminiUpstreamState{TenantID: 9001, AccountID: 8001, PrefixHash: "prefix-day1-red", CachedContentTokens: 256, AccumulatedUsage: CanonicalUsage{InputTokens: 1000, OutputTokens: 50}}`
**Action**:
- 调 Gemini finalize 路径（`backend/internal/proto/gemini_sse.go:295` 附近 finalize；如函数私有则模拟 SSE end 事件流走 `ProviderEventToCanonicalEvents` 直到 stream end）
**Assertion**:
- `seg.LastReadAt.Load() == fixedNow.UnixNano()`
- `seg.MissCount[idx].Load() == 0`
- `cachemetrics.SnapshotByAccount(8001)` 中 `read >= 256`（Day 2 wiring 后必须把 CachedContentTokens 喂给 `ObserveByAccountWithPrefix`）
- `IncCacheHitObs` 计数 +1
**失败诊断**: 当前 `gemini_sse.go:319-325` 只把 CachedContentTokenCount 存进 state，**完全没调** `cachemetrics.ObserveByAccountWithPrefix` → 三个断言都会红。Day 2 fix = 在 `finalizeGeminiState` 终态（line 314 前）插入 `ObserveByAccountWithPrefix(0, int64(state.CachedContentTokens), state.TenantID, state.AccountID, state.PrefixHash)`。

---

## 测试 3: 0/0 demote 不再死路径

**File**: `backend/internal/cachemetrics/cachemetrics_pasr_observer_test.go`（新建）
**Function**: `TestObserveByAccountWithPrefix_ZeroZero_NotifiesObserverForMissDemote`
**Setup**:
- 构造 SegmentTable + ring；`LookupOrCreate(9001, []byte("prefix-day1-red"), ring)` 拿 seg
- `idx := seg.IndexOf(8001)`；先 `seg.MarkCacheSeen(idx)` 模拟段成员有 hasCache bit
- `RegisterPASRCacheFeedback(segments)`
**Action**:
- 连续调两次 `cachemetrics.ObserveByAccountWithPrefix(0, 0, 9001, 8001, "prefix-day1-red")`
**Assertion**:
- 第 1 次后: `seg.MissCount[idx].Load() == 1`，`seg.HasCache(idx) == true`（未达 threshold）
- 第 2 次后: `seg.HasCache(idx) == false`（PASRDemoteThreshold=2 触发 Demote）
- `seg.MissCount[idx].Load() == 0`（Demote 内置 reset）
- `IncMissObs` 计数 +2，`IncDemote` 计数 +1
**失败诊断**: 当前 `cachemetrics.go:228-230` 的 `if cacheCreation == 0 && cacheRead == 0 { return }` 把 0/0 调用 short-circuit → `notifyObservers` 永不触发 → MissCount 永远 0。Day 2 fix = 移除此 short-circuit（或改成只在 prefixHash 空 + obs 全空时跳）。

---

## 测试 4: 空 tenant/account/prefix no-op + 负数计数

**File**: `backend/internal/cachemetrics/cachemetrics_pasr_observer_test.go`
**Function**: `TestObserveByAccountWithPrefix_DegenerateInputs_NoSegmentMutation_NoPanic`
**Setup**:
- SegmentTable + ring + 预创建 seg(9001, "prefix-day1-red")
- `RegisterPASRCacheFeedback(segments)`
- 记录初态: `before := seg.HasCacheBitmap.Load(); beforeRead := seg.LastReadAt.Load(); beforeMiss := seg.MissCount[0].Load()`
**Action**: 用 sub-test 跑 6 个退化输入
- `t.Run("empty_prefix", ...)`: `ObserveByAccountWithPrefix(10, 0, 9001, 8001, "")`
- `t.Run("zero_account", ...)`: `ObserveByAccountWithPrefix(10, 0, 9001, 0, "prefix-day1-red")`
- `t.Run("zero_tenant", ...)`: `ObserveByAccountWithPrefix(10, 0, 0, 8001, "prefix-day1-red")`
- `t.Run("negative_creation", ...)`: `ObserveByAccountWithPrefix(-1, 5, 9001, 8001, "prefix-day1-red")`
- `t.Run("negative_read", ...)`: `ObserveByAccountWithPrefix(5, -1, 9001, 8001, "prefix-day1-red")`
- `t.Run("all_zero_no_account", ...)`: `ObserveByAccountWithPrefix(0, 0, 0, 0, "")` —— 必须不 panic
**Assertion (每个 sub-test)**:
- `seg.HasCacheBitmap.Load() == before`
- `seg.LastReadAt.Load() == beforeRead`
- `seg.MissCount[0].Load() == beforeMiss`
- `recover() == nil`（无 panic）
**失败诊断**: 若 negative_creation 改了状态，说明 `cachemetrics.go:231-233` 的 negative guard 被绕过；若 zero_tenant 改了 segment，`pasr_feedback.go:66-68` 的 TenantID==0 short-circuit 失效；若 all_zero_no_account panic，observer 链 recover 没罩住。

---

## 测试 5: 跨 tenant 隔离

**File**: `backend/internal/pool/pasr_feedback_tenant_isolation_test.go`（新建；放 pool 包以直接访问 SegmentTable 内部）
**Function**: `TestPASRFeedback_SamePrefixDifferentTenants_NoCrossContamination`
**Setup**:
- SegmentTable + ring(8001, 8002, 8003)
- `segA := segments.LookupOrCreate(9001, []byte("prefix-day1-red"), ring)`
- `segB := segments.LookupOrCreate(9002, []byte("prefix-day1-red"), ring)`
- 验前置: `segA != segB`，且二者各有 8001 在 Members
- `RegisterPASRCacheFeedback(segments)`
**Action**:
- `cachemetrics.ObserveByAccountWithPrefix(50, 0, 9001, 8001, "prefix-day1-red")` —— 仅触 tenant A
**Assertion**:
- `idxA := segA.IndexOf(8001); segA.HasCache(idxA) == true` (creation 路径已 mark)
- `idxB := segB.IndexOf(8001); segB.HasCache(idxB) == false` (tenant B 段无变)
- `segB.LastReadAt.Load() == segB.CreatedAt`（tenant B 未被 touch）
- `segA.MissCount[idxA].Load() == 0`（creation 已 reset）
**失败诊断**: 若 segB.HasCache 为 true，说明 segmentKey 编码漏 tenant 维度（去查 `prefix_segment.go:468` segmentKey 编码）；若 segA、segB 引用是同一对象，`LookupOrCreate` 复用了 key — 应 fail 在前置 `segA != segB`。

---

## 测试 6: 多次 read-hit + 后续 miss demote 完整序列（综合验收 #1+#3）

**File**: `backend/internal/pool/pasr_feedback_lifecycle_test.go`（新建）
**Function**: `TestPASRFeedback_ReadHitThenMissSequence_DemotesAfterThreshold`
**Setup**:
- SegmentTable + ring + seg(9001, "prefix-day1-red")
- `idx := seg.IndexOf(8001)`
- `RegisterPASRCacheFeedback(segments)`
**Action (按序，每步独立断言)**:
1. `ObserveByAccountWithPrefix(0, 100, 9001, 8001, "prefix-day1-red")` (read-hit)
2. `ObserveByAccountWithPrefix(0, 0, 9001, 8001, "prefix-day1-red")` (miss #1)
3. `ObserveByAccountWithPrefix(0, 200, 9001, 8001, "prefix-day1-red")` (read-hit reset)
4. `ObserveByAccountWithPrefix(0, 0, 9001, 8001, "prefix-day1-red")` (miss #1 again)
5. `ObserveByAccountWithPrefix(0, 0, 9001, 8001, "prefix-day1-red")` (miss #2 → demote)
**Assertion**:
- 步骤 1 后: `seg.LastReadAt` 已更新；`seg.MissCount[idx] == 0`；`seg.HasCache(idx) == true`（**Decision 1 期望**：read-hit 即 set bit；Day 2 改完才会绿）
- 步骤 2 后: `MissCount[idx] == 1`；`HasCache(idx) == true`
- 步骤 3 后: `MissCount[idx] == 0`（reset）；`HasCache(idx) == true`
- 步骤 4 后: `MissCount[idx] == 1`
- 步骤 5 后: `HasCache(idx) == false`；`MissCount[idx] == 0`（Demote 内置 reset）
- 全过程无 panic；`IncDemote` 累计 +1
**失败诊断**: 步骤 1 红 → Day 2 read-hit→MarkCacheSeen wiring 还没接；步骤 2/4 红 → 0/0 short-circuit 还在；步骤 5 红 → Demote 阈值或位操作错。

---

## Day 2 executor 实现优先序（写完红测试后看的提示）

1. **C-3 优先**（`cachemetrics.go:228-230` 0/0 short-circuit 移除）→ 测试 3、4、6 的 miss 链路立即过半
2. **C-1**（`pasr_feedback.go:80-92` 加 read-hit MarkCacheSeen 分支，在 CacheRead>0 且 CacheCreation==0 时也 set bit）→ 测试 1、6 的 HasCache 断言绿
3. **C-2**（`gemini_sse.go` finalize 终态调 `ObserveByAccountWithPrefix`）→ 测试 2 绿
4. tenant 隔离（测试 5）已由现有 `segmentKey` 编码保证；只需确认无回归

## 需要 Day 2 顺手加的 test helper（不在本 spec scope 但 executor 会撞到）

- `cachemetrics/export_test.go`: 加 `ResetObservers()` 让测试间互不污染（综合 plan §失败模式 "Global observer test 间互相污染"）
- `pool/pasr_metrics_test.go` 已有 metrics snapshot 模式可复用

## Tail block (per AGENTS.md template)

Source files read: `docs/plans/2026-05-09-next-pivot-synthesis.md`, `backend/internal/cachemetrics/cachemetrics.go`, `backend/internal/pool/pasr_feedback.go`, `backend/internal/pool/prefix_segment.go`, `backend/internal/proto/openai_sse.go` (lines 380-460), `backend/internal/proto/gemini_sse.go` (lines 1-120, 300-360) — HUAKAI internal only, exempt per CLAUDE.md #12.
Lane: spec writer (Day 1 red-tests)
Agent: Claude opus-4-7 [1m] (general-purpose subagent aff118341d3933477)
UTC timestamp: 2026-05-09T08:55Z
