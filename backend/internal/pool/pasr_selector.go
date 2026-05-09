// pasr_selector.go — PASR-lite A3: 实现 pool.Selector 接口的自有调度算法。
//
// PASR-lite 调度逻辑:
//  1. req.SessionHash 作为 prefix key (上游已 hash, 复用 Track B prompt_hash)
//  2. SegmentTable.LookupOrCreate(prefix, ring) → 取或建 K=3 段
//  3. 段成员中过滤健康+不超载的 candidates
//  4. 段全 unhealthy → HRW 全 ring 排序接力 (codex synthesis D5)
//  5. candidates 内: 优先 HasCache bit 已 set 的成员 (synthesis D2)
//  6. tie-break: LoadRate 最低 (synthesis 同质算法延伸)
//  7. ClaimGate.WriteAcquisition 写出 acquisition_token
//
// 与 DefaultSelector 共存: 不替换, caller 根据 feature flag 选用哪个
// Selector 实例化。production cutover 见 A8 atomic。
//
// hot path 性能 (per req): O(1) SegmentTable 查表 + O(K=3) 段内过滤 +
// O(1) ClaimGate 写 = ~10 µs (vs DefaultSelector 多次 DB 查 + sticky
// flat hash, ~ms 级)。
package pool

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// pasrPostMutationReleaseTimeout 给 post-mutation 失败时的 slot release 留独立
// 短 ctx (2s)。 D4 不变量要求"已 mutate slot 必须 release", 不能因为上游 ctx
// 已 cancel/timeout 而连带丢 release; 用 context.WithoutCancel 派生干净 ctx。
const pasrPostMutationReleaseTimeout = 2 * time.Second

// ErrPASRPreMutationFail 表示 PASRSelector 在写 slot/claim **之前** 就失败,
// 上游 dispatcher (M4) 可安全 fallback 到 DefaultSelector — 没有半成品状态需要清理。
// 触发场景: SlotManager.Acquire 返非 nil 错误 (ErrNoSlotAvailable / DB 抖动 / 其他)。
var ErrPASRPreMutationFail = errors.New("pasr: pre-mutation failure (safe to fallback)")

// ErrPASRPostMutationFail 表示 PASRSelector 已 acquire slot **后** 才失败
// (claim 写入失败 / claim race / DB 异常)。 上游 dispatcher 必须 fail closed,
// 已 acquire 的 slot 已由本函数 release, **不可** 再走 default 否则双 claim race。
var ErrPASRPostMutationFail = errors.New("pasr: post-mutation failure (must fail closed)")

// PASRSelector 实现 Selector 接口, 用 HRW K=3 段 + cache locality 优先。
type PASRSelector struct {
	// accounts 提供当前活账号快照 (LoadRate / Priority / ...)。
	accounts AccountSource

	// claims 写 acquisition_token 到 billing_claims 表。
	claims ClaimGate

	// slots 实管账号并发上限 (provider_accounts.in_flight_count + pool_slot_acquisitions)。
	// shadow 实例显式注入 nil → acquireAndReturn 跳过 slot/claim, 仅生成 token (synthesis D2)。
	// canary / pasr-primary / pasr-strict 必须注入真实 SlotManager — 否则破 cap_concurrency。
	slots SlotManager

	// ring 当前 HRW 账号集 (atomic-replaceable; rebalance 时换指针)。
	// Phase A3 用 ringProvider 间接获取以支持 hot-swap。
	ringProvider func() *AccountRing

	// segments in-memory 段表 (codex synthesis D4 主权威)。
	segments *SegmentTable

	// loadCap 段成员被剔出 candidates 的 LoadRate 上限。 0.95 默认。
	loadCap float64

	// readOnlySegments 当为 true 时, Select 用 SegmentTable.Lookup 不创建段
	// (D2 决策: shadow 实例段表只读, 不污染 actual 段 bitmap 学习数据)。
	// 段未命中 → 直接走 HRW 全 ring 接力, 等价于 cold-miss 路径。
	readOnlySegments bool

	// ringSeed 用于 RingProvider 未注入时的 request-scoped ring 构造 (synthesis D3)。
	// 0 时用默认 0xCAFEBABE — 与现有测试保持一致。
	ringSeed uint64
}

// defaultPASRRingSeed 是 RingSeed 字段未指定时的默认 HRW 种子, 与 atom A1
// 测试矩阵 (newPASRTestRig) 一致, 保证向后兼容。 Owner 30 天轮换 seed 时由
// main.go 在构造 PASRSelectorConfig 时显式注入。
const defaultPASRRingSeed uint64 = 0xCAFEBABE

// PASRSelectorConfig 构造期参数。
type PASRSelectorConfig struct {
	Accounts         AccountSource
	Claims           ClaimGate
	Slots            SlotManager         // M3: nil → 兼容路径 (不持 slot 不写 claim, shadow / 老测试)
	RingProvider     func() *AccountRing // M5: 可选; nil 时 Select 用 request-scoped ring (synthesis D3)
	Segments         *SegmentTable
	LoadCap          float64 // 0 用 0.95
	ReadOnlySegments bool    // M4 (D2): true 时 Select 走 Lookup-only 段表路径, 不污染段
	RingSeed         uint64  // M5: request-scoped ring 的 HRW seed; 0 时用默认 0xCAFEBABE
}

// NewPASRSelector 构造实例。 M5 起 RingProvider 不再 mandatory — nil 时
// Select 走 request-scoped ring (synthesis D3 选项 A: per-request 从 ListAccounts
// snapshots build ring, 避免全局 ticker / 新 SQL / 跨租户泄漏)。
func NewPASRSelector(cfg PASRSelectorConfig) (*PASRSelector, error) {
	if cfg.Accounts == nil {
		return nil, errors.New("pasr: AccountSource 必填")
	}
	if cfg.Segments == nil {
		return nil, errors.New("pasr: SegmentTable 必填")
	}
	cap := cfg.LoadCap
	if cap <= 0 {
		cap = 0.95
	}
	seed := cfg.RingSeed
	if seed == 0 {
		seed = defaultPASRRingSeed
	}
	return &PASRSelector{
		accounts:         cfg.Accounts,
		claims:           cfg.Claims,
		slots:            cfg.Slots, // 可为 nil — 兼容 shadow + 老测试路径
		ringProvider:     cfg.RingProvider,
		segments:         cfg.Segments,
		loadCap:          cap,
		readOnlySegments: cfg.ReadOnlySegments,
		ringSeed:         seed,
	}, nil
}

// Select 实现 Selector 接口主入口。
func (p *PASRSelector) Select(ctx context.Context, req SelectionRequest) (*SelectionResult, error) {
	// 1. 拉账号快照 → snapshots[accID] = *AccountSnapshot 用于 health/load 判
	accs, err := p.accounts.ListAccounts(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("pasr: ListAccounts: %w", err)
	}
	snapshots := make(map[int64]*AccountSnapshot, len(accs))
	for _, a := range accs {
		if a == nil {
			continue
		}
		snapshots[a.ID] = a
	}
	if len(snapshots) == 0 {
		return nil, ErrNoEligibleAccount
	}

	// 2. 取/建段 - prefix key 用 req.SessionHash; 空 hash 直降级到 HRW 全 ring
	// M5: RingProvider 注入则用注入路径 (向后兼容老 atom 测试 + 显式 hot-swap);
	// 否则用 request-scoped ring (synthesis D3): 直接从 ListAccounts snapshots
	// 派生 — per (tenant, pool_group) 已经在 ListAccounts 上游过滤好,
	// 天然避开跨租户泄漏 + 不需要全局 ticker / 新 SQL。
	var ring *AccountRing
	if p.ringProvider != nil {
		ring = p.ringProvider()
	} else {
		ring = BuildAccountRingFromSnapshots(accs, p.ringSeed)
	}
	if ring == nil || len(ring.Accounts) == 0 {
		return nil, ErrNoEligibleAccount
	}
	prefixKey := []byte(req.SessionHash)
	if len(prefixKey) == 0 {
		// 客户端没给 prompt prefix hash → PASR 退化, 全 ring 选首个 healthy
		return p.scheduleNoSegment(ctx, req, ring, snapshots)
	}
	// M4 (D2): readOnlySegments=true (shadow 实例) 用 Lookup 不创建; 段未命中
	// 直接走 HRW 全 ring 接力, 不污染段表 — 让 actual 路径独占段学习数据。
	var seg *PrefixSegment
	if p.readOnlySegments {
		seg = p.segments.Lookup(prefixKey)
		if seg == nil {
			return p.scheduleHRWFullRing(ctx, req, ring, snapshots, [PASRSegmentSize]int64{})
		}
	} else {
		seg = p.segments.LookupOrCreate(prefixKey, ring)
	}

	// 3. 段内过滤 healthy + 未超载
	type candidate struct {
		idx       int
		accountID int64
		snapshot  *AccountSnapshot
		hasCache  bool
	}
	var candidates []candidate
	for i, accID := range seg.Members {
		if accID == 0 {
			continue
		}
		if _, excluded := req.ExcludedAccounts[accID]; excluded {
			continue
		}
		snap, ok := snapshots[accID]
		if !ok {
			continue // 段成员账号已不存在 (rebalance 滞后)
		}
		if snap.LoadRate >= p.loadCap {
			continue
		}
		candidates = append(candidates, candidate{
			idx:       i,
			accountID: accID,
			snapshot:  snap,
			hasCache:  seg.HasCache(i),
		})
	}

	// 4. 段全 unhealthy → HRW 全 ring 接力 (codex D5)
	if len(candidates) == 0 {
		return p.scheduleHRWFullRing(ctx, req, ring, snapshots, seg.Members)
	}

	// 5. 优先 HasCache 段员 (synthesis D2: bitmap 优先)
	pool := candidates
	cached := candidates[:0]
	for _, c := range candidates {
		if c.hasCache {
			cached = append(cached, c)
		}
	}
	if len(cached) > 0 {
		pool = cached
	}

	// 6. tie-break: LoadRate 最低胜 (synthesis 同质 RR 替代 — 用 load 排序)
	chosen := pool[0]
	for _, c := range pool[1:] {
		if c.snapshot.LoadRate < chosen.snapshot.LoadRate {
			chosen = c
		} else if c.snapshot.LoadRate == chosen.snapshot.LoadRate &&
			c.snapshot.LastUsedAt.Before(chosen.snapshot.LastUsedAt) {
			// load 相等时取最久未用 — 接近 round-robin 行为
			chosen = c
		}
	}

	// metrics: first-pick (idx 0) vs failover (idx 1/2)
	if chosen.idx == 0 {
		IncFirstPick()
	} else {
		IncFailover()
	}

	// 7. claim 写入 + 返
	return p.acquireAndReturn(ctx, req, chosen.snapshot)
}

// scheduleNoSegment: req.SessionHash 为空时, 直接全 ring 走 HRW 排序选
// 首个 healthy 账号 (相当于无 prefix 的 PASR 退化形态)。
func (p *PASRSelector) scheduleNoSegment(
	ctx context.Context, req SelectionRequest,
	ring *AccountRing, snapshots map[int64]*AccountSnapshot,
) (*SelectionResult, error) {
	// 用 req.RequestedModel 作为弱 prefix, 至少在 model 维度有 cache locality
	prefixKey := []byte(req.RequestedModel)
	if len(prefixKey) == 0 {
		// 完全无 hint, 用空 prefix - 所有请求会路由到 HRW 同一首选
		// 这不是好情况但兜底, caller 应保证 SessionHash 或 RequestedModel 非空
		prefixKey = []byte("__pasr_noprefix__")
	}
	return p.scheduleHRWFullRing(ctx, req, ring, snapshots, [PASRSegmentSize]int64{})
}

// scheduleHRWFullRing: 段全 unhealthy 时 (synthesis D5), 直接对全 ring
// 跑 HRW 排序, 选首个 healthy 账号。已尝试段成员 (excludedSegmentMembers)
// 跳过避免再选。
//
// 性能: O(N) 排序 (cold path), N 通常 < 1000, ~50 µs 一次, 可接受。
func (p *PASRSelector) scheduleHRWFullRing(
	ctx context.Context, req SelectionRequest,
	ring *AccountRing, snapshots map[int64]*AccountSnapshot,
	excludedSegmentMembers [PASRSegmentSize]int64,
) (*SelectionResult, error) {
	IncFullRingFallback()
	prefixKey := []byte(req.SessionHash)
	if len(prefixKey) == 0 {
		prefixKey = []byte(req.RequestedModel)
	}
	if len(prefixKey) == 0 {
		prefixKey = []byte("__pasr_noprefix__")
	}

	// 段成员已尝试集
	tried := make(map[int64]bool, PASRSegmentSize)
	for _, m := range excludedSegmentMembers {
		if m != 0 {
			tried[m] = true
		}
	}

	// HRW 全 ring 排序: TopK(prefix, len(accounts))
	sorted := ring.TopK(prefixKey, len(ring.Accounts))
	for _, accID := range sorted {
		if tried[accID] {
			continue
		}
		if _, excluded := req.ExcludedAccounts[accID]; excluded {
			continue
		}
		snap, ok := snapshots[accID]
		if !ok {
			continue
		}
		if snap.LoadRate >= p.loadCap {
			continue
		}
		return p.acquireAndReturn(ctx, req, snap)
	}
	return nil, ErrNoEligibleAccount
}

// acquireAndReturn 走 PASR actual 写入路径: SlotManager.Acquire (pre-mutation) →
// ClaimGate.WriteAcquisition (post-mutation) → 返 SelectionResult。
//
// 错误分类 (synthesis §3.4 + D4):
//   - p.slots == nil 或返 ErrSlotManagerUnavailable → 兼容路径, 不持 slot;
//     ClaimID != 0 时仍会调 Claims.WriteAcquisition (老 atom 测试矩阵依赖此行为),
//     ClaimID == 0 时整体短路返 fresh uuid。 触发场景: shadow 实例 / 老测试 /
//     dispatcher canary 之外的模式
//   - SlotManager.Acquire 返其他错误 (ErrNoSlotAvailable / DB 抖动 / 其他) →
//     ErrPASRPreMutationFail, dispatcher 可安全 fallback default
//   - Slot 已 acquire, ClaimGate.WriteAcquisition 失败 → release slot +
//     ErrPASRPostMutationFail, dispatcher 必须 fail closed (再走 default 会双
//     claim race)
//
// 不变量: 函数返回 (nil, ErrPASR*) 时, in_flight_count / billing_claims 表都已
// 还原到入函数前的状态; 返回 (*SelectionResult, nil) 时刚好写了 1 行 slot
// acquisition + 1 行 claim acquisition (若 ClaimGate 注入)。
func (p *PASRSelector) acquireAndReturn(
	ctx context.Context, req SelectionRequest, snapshot *AccountSnapshot,
) (*SelectionResult, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("%w: nil snapshot", ErrPASRPreMutationFail)
	}
	accountID := snapshot.ID

	// HIGH-1 fix: ClaimID=0 → 不调 Slots.Acquire 直接走 token-only。
	// 真 SlotManager 已 acquire 但没 ClaimGate 写 acquisition 时, SelectionResult
	// 没暴露 ReleaseFunc, caller 无法 release → slot 泄漏到 sweeper。
	// shadow 实例 / dispatcher 已设 ClaimID=0 表示"不持 slot"路径。
	if req.ClaimID == 0 {
		return p.tokenOnlyResult(ctx, req, accountID)
	}

	// HIGH-1 fix: 真 SlotManager 注入但 Claims=nil 是 misconfigure —
	// acquireAndReturn 后无法写 acquisition, slot 同样会泄漏。 启动期就该挡住,
	// 这里 fail-fast 避免悄悄漏 slot。
	if p.slots != nil && p.claims == nil {
		return nil, fmt.Errorf("%w: slots configured without claim gate (account=%d)",
			ErrPASRPreMutationFail, accountID)
	}

	// pre-mutation: 尝试 acquire slot — nil SlotManager 或 ErrSlotManagerUnavailable
	// 都视为兼容路径, 不持 slot, 用 fresh uuid 返回 (shadow / 老 atom 测试)。
	var token uuid.UUID
	var release ReleaseFunc
	if p.slots != nil {
		acq, err := p.slots.Acquire(ctx, snapshot, req)
		if err != nil {
			if errors.Is(err, ErrSlotManagerUnavailable) {
				return p.tokenOnlyResult(ctx, req, accountID)
			}
			// MEDIUM-2 fix: 用 %w 而非 %v 包装根因, errors.Is 链保持 ErrNoSlotAvailable
			// 与 ErrClaimRace 等 sentinel 可被上游 dispatcher 进一步判别。
			return nil, fmt.Errorf("%w: slot acquire account=%d: %w",
				ErrPASRPreMutationFail, accountID, err)
		}
		if acq == nil {
			return nil, fmt.Errorf("%w: slot acquire returned nil for account=%d",
				ErrPASRPreMutationFail, accountID)
		}
		// MEDIUM-1 fix (round 2): token=Nil 但 acq.Release != nil 表示 SlotManager
		// 已 mutate 但状态损坏 — 走 release 路径不可静默泄漏。 release 失败时同 HIGH-2,
		// 错误链含 "slot release failed: %w", dispatcher / ops 才能定位泄漏事件。
		if acq.AcquisitionToken == uuid.Nil {
			if acq.Release != nil {
				cleanupCtx, cancel := context.WithTimeout(
					context.WithoutCancel(ctx), pasrPostMutationReleaseTimeout)
				if relErr := acq.Release(cleanupCtx); relErr != nil {
					cancel()
					return nil, fmt.Errorf("%w: slot acquire returned empty token for account=%d; slot release failed: %w",
						ErrPASRPostMutationFail, accountID, relErr)
				}
				cancel()
			}
			return nil, fmt.Errorf("%w: slot acquire returned empty token for account=%d",
				ErrPASRPostMutationFail, accountID)
		}
		token = acq.AcquisitionToken
		release = acq.Release
	} else {
		// 显式 nil: shadow 实例不持 slot 直接返 token (D2 段表只读路径配套)。
		return p.tokenOnlyResult(ctx, req, accountID)
	}

	// post-mutation: 写 claim — 失败必须 release slot 并标 PostMutationFail。
	if err := p.claims.WriteAcquisition(ctx, req.TenantID, req.ClaimID, accountID, token); err != nil {
		// HIGH-2 fix: release 用 WithoutCancel 派生独立 ctx + 2s timeout, 防止
		// 上游 ctx (req timeout / handler cancel) 把 release 也带走 — D4 要求
		// "post-mutation 失败必须 release" 是硬不变量, 不能 best-effort。
		// release 错误也包入返回链, dispatcher / ops 能定位"slot 没真还原"事件。
		cleanupCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), pasrPostMutationReleaseTimeout)
		defer cancel()
		if release != nil {
			if relErr := release(cleanupCtx); relErr != nil {
				return nil, fmt.Errorf("%w: claim write account=%d: %w; slot release failed: %w",
					ErrPASRPostMutationFail, accountID, err, relErr)
			}
		}
		return nil, fmt.Errorf("%w: claim write account=%d: %w",
			ErrPASRPostMutationFail, accountID, err)
	}

	return &SelectionResult{
		AccountID:        accountID,
		AcquisitionToken: token,
	}, nil
}

// tokenOnlyResult 兼容路径: 不持 slot, 直接返 fresh uuid。 触发场景:
//  1. PASRSelector.Slots == nil (shadow 实例 + dispatcher 显式不要 slot 写)
//  2. SlotManager.Acquire 返 ErrSlotManagerUnavailable (兼容老 atom 测试)
//  3. req.ClaimID == 0 (HIGH-1 fix: 真 Slots 注入但 caller 不要 claim, 避开
//     slot 泄漏 — caller 拿不到 ReleaseFunc)
//
// 注意: ClaimID != 0 时本函数仍会调 Claims.WriteAcquisition 写 acquisition,
// 与"不持 slot"语义并存; 老 atom 测试矩阵依赖此行为, 不要改。
// dispatcher actual / canary 模式下绝不应触发本函数 — dispatcher 必须保证
// req.ClaimID != 0 + Slots/Claims 都注入。
func (p *PASRSelector) tokenOnlyResult(
	ctx context.Context, req SelectionRequest, accountID int64,
) (*SelectionResult, error) {
	token := uuid.New()
	if p.claims != nil && req.ClaimID != 0 {
		if err := p.claims.WriteAcquisition(ctx, req.TenantID, req.ClaimID, accountID, token); err != nil {
			return nil, fmt.Errorf("pasr: WriteAcquisition: %w", err)
		}
	}
	return &SelectionResult{
		AccountID:        accountID,
		AcquisitionToken: token,
	}, nil
}

// 编译期断言: PASRSelector 实现 Selector 接口
var _ Selector = (*PASRSelector)(nil)
