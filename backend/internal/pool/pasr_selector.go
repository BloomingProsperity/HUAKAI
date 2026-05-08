// pasr_selector.go — PASR-lite A3: 实现 pool.Selector 接口的自有调度算法。
//
// PASR-lite 调度逻辑:
//   1. req.SessionHash 作为 prefix key (上游已 hash, 复用 Track B prompt_hash)
//   2. SegmentTable.LookupOrCreate(prefix, ring) → 取或建 K=3 段
//   3. 段成员中过滤健康+不超载的 candidates
//   4. 段全 unhealthy → HRW 全 ring 排序接力 (codex synthesis D5)
//   5. candidates 内: 优先 HasCache bit 已 set 的成员 (synthesis D2)
//   6. tie-break: LoadRate 最低 (synthesis 同质算法延伸)
//   7. ClaimGate.WriteAcquisition 写出 acquisition_token
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

	"github.com/google/uuid"
)

// PASRSelector 实现 Selector 接口, 用 HRW K=3 段 + cache locality 优先。
type PASRSelector struct {
	// accounts 提供当前活账号快照 (LoadRate / Priority / ...)。
	accounts AccountSource

	// claims 写 acquisition_token 到 billing_claims 表。
	claims ClaimGate

	// ring 当前 HRW 账号集 (atomic-replaceable; rebalance 时换指针)。
	// Phase A3 用 ringProvider 间接获取以支持 hot-swap。
	ringProvider func() *AccountRing

	// segments in-memory 段表 (codex synthesis D4 主权威)。
	segments *SegmentTable

	// loadCap 段成员被剔出 candidates 的 LoadRate 上限。 0.95 默认。
	loadCap float64
}

// PASRSelectorConfig 构造期参数。
type PASRSelectorConfig struct {
	Accounts     AccountSource
	Claims       ClaimGate
	RingProvider func() *AccountRing // hot-swap 支持; nil 时调用方自行预填
	Segments     *SegmentTable
	LoadCap      float64 // 0 用 0.95
}

// NewPASRSelector 构造实例。
func NewPASRSelector(cfg PASRSelectorConfig) (*PASRSelector, error) {
	if cfg.Accounts == nil {
		return nil, errors.New("pasr: AccountSource 必填")
	}
	if cfg.Segments == nil {
		return nil, errors.New("pasr: SegmentTable 必填")
	}
	if cfg.RingProvider == nil {
		return nil, errors.New("pasr: RingProvider 必填")
	}
	cap := cfg.LoadCap
	if cap <= 0 {
		cap = 0.95
	}
	return &PASRSelector{
		accounts:     cfg.Accounts,
		claims:       cfg.Claims,
		ringProvider: cfg.RingProvider,
		segments:     cfg.Segments,
		loadCap:      cap,
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
	ring := p.ringProvider()
	if ring == nil || len(ring.Accounts) == 0 {
		return nil, ErrNoEligibleAccount
	}
	prefixKey := []byte(req.SessionHash)
	if len(prefixKey) == 0 {
		// 客户端没给 prompt prefix hash → PASR 退化, 全 ring 选首个 healthy
		return p.scheduleNoSegment(ctx, req, ring, snapshots)
	}
	seg := p.segments.LookupOrCreate(prefixKey, ring)

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
	return p.acquireAndReturn(ctx, req, chosen.accountID)
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
		return p.acquireAndReturn(ctx, req, accID)
	}
	return nil, ErrNoEligibleAccount
}

// acquireAndReturn 写 claim acquisition 并返 SelectionResult。
func (p *PASRSelector) acquireAndReturn(
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
