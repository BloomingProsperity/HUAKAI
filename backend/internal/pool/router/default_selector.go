package router

import (
	"context"
	"errors"
	"math/rand"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

type SelectorOption func(*DefaultSelector)

type DefaultSelector struct {
	accounts AccountSource
	policies RoutingPolicySource
	sticky   StickyStore
	gates    GateChain
	slots    SlotManager
	claims   ClaimGate
	rand     *rand.Rand
	randMu   sync.Mutex // 保护 rand: math/rand.Rand 非并发安全, gateway 多 goroutine 同时调 Shuffle 会 race
	now      func() time.Time
}

func NewDefaultSelector(accounts AccountSource, opts ...SelectorOption) *DefaultSelector {
	s := &DefaultSelector{
		accounts: accounts,
		gates:    DefaultGateChain(),
		slots:    nilSlotManager{},
		rand:     rand.New(rand.NewSource(time.Now().UnixNano())),
		now:      time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func WithRoutingPolicySource(v RoutingPolicySource) SelectorOption {
	return func(s *DefaultSelector) { s.policies = v }
}

func WithStickyStore(v StickyStore) SelectorOption {
	return func(s *DefaultSelector) { s.sticky = v }
}

func WithGateChain(v GateChain) SelectorOption {
	return func(s *DefaultSelector) { s.gates = v }
}

func WithSlotManager(v SlotManager) SelectorOption {
	return func(s *DefaultSelector) { s.slots = v }
}

func WithClaimGate(v ClaimGate) SelectorOption {
	return func(s *DefaultSelector) { s.claims = v }
}

func WithNow(fn func() time.Time) SelectorOption {
	return func(s *DefaultSelector) {
		if fn == nil {
			return
		}
		s.now = fn
		if _, ok := s.gates.Health.(ProviderAccountHealthGate); ok {
			s.gates.Health = ProviderAccountHealthGate{Now: fn}
		}
		if _, ok := s.gates.Model.(modelRateLimitGate); ok {
			s.gates.Model = modelRateLimitGate{Now: fn}
		}
	}
}

func (s *DefaultSelector) Select(ctx context.Context, req SelectionRequest) (*SelectionResult, error) {
	reason := NewRoutingReasonBuilder(req)
	if s == nil || s.accounts == nil {
		return nil, ErrNoEligibleAccount
	}
	accounts, err := s.accounts.ListAccounts(ctx, req)
	if err != nil {
		return nil, err
	}
	policy, err := s.policy(ctx, req)
	if err != nil {
		return nil, err
	}
	eligible := s.filter(ctx, accounts, req, reason)
	if len(eligible) == 0 {
		if reason.onlyFailure(GateFailureHealth, len(accounts)) {
			return nil, ErrAllChannelsDegraded
		}
		return nil, ErrNoEligibleAccount
	}

	routeConstrained := hasModelRoute(policy, req.RequestedModel)
	routed := modelRoute(policy, req.RequestedModel, eligible)
	if len(routed) > 0 {
		if res, done, err := s.trySticky(ctx, req, routed, RoutingLayerStickyWithinRoute, reason); done || err != nil {
			return res, err
		}
		if res, done, err := s.tryLayer(ctx, req, routed, RoutingLayerRoutingAffinity, reason); done || err != nil {
			return res, err
		}
	} else if !routeConstrained {
		if res, done, err := s.trySticky(ctx, req, eligible, RoutingLayerStickyStandalone, reason); done || err != nil {
			return res, err
		}
	}

	freshCandidates := eligible
	if routeConstrained {
		freshCandidates = routed
	}
	fresh := s.rankFresh(freshCandidates, policy)
	if res, done, err := s.tryLayer(ctx, req, fresh, RoutingLayerFresh, reason); done || err != nil {
		// Track B 闭环最后一片: fresh 选定后写 sticky_bindings 让后续相同
		// prompt prefix 命中此账号. 用 type assertion 让 StickyStore 接口
		// 不被强制扩展（实测 stub 不实现 Upsert 也不破现有测试）。
		// 写失败 best-effort 不阻塞主流程（绑定丢一次就再次走 fresh）。
		if done && err == nil && res != nil && res.AccountID != 0 && req.SessionHash != "" {
			if writer, ok := s.sticky.(interface {
				Upsert(ctx context.Context, tenantID int64, sessionHash, model string, accountID int64) error
			}); ok {
				_ = writer.Upsert(ctx, req.TenantID, req.SessionHash, req.RequestedModel, res.AccountID)
			}
		}
		return res, err
	}
	if plan := fallbackPlan(fresh, policy); plan != nil {
		reason.Wait(plan)
		return &SelectionResult{WaitPlan: plan, RoutingReasonJSON: reason.JSON()}, nil
	}
	return nil, ErrNoEligibleAccount
}

func (s *DefaultSelector) policy(ctx context.Context, req SelectionRequest) (*RoutingPolicy, error) {
	if s.policies == nil {
		return nil, nil
	}
	return s.policies.GetRoutingPolicy(ctx, req)
}

func (s *DefaultSelector) filter(ctx context.Context, accounts []*AccountSnapshot, req SelectionRequest, reason *RoutingReasonBuilder) []*AccountSnapshot {
	out := make([]*AccountSnapshot, 0, len(accounts))
	for _, account := range accounts {
		if account == nil {
			continue
		}
		ok, why, err := s.gates.Allow(ctx, account, req)
		if err != nil || !ok {
			reason.GateFailure(account.ID, why)
			continue
		}
		out = append(out, account)
	}
	return out
}

func (s *DefaultSelector) trySticky(ctx context.Context, req SelectionRequest, candidates []*AccountSnapshot, layer RoutingLayer, reason *RoutingReasonBuilder) (*SelectionResult, bool, error) {
	if s.sticky == nil {
		return nil, false, nil
	}
	id, found, err := s.sticky.Lookup(ctx, req)
	if err != nil || !found {
		return nil, false, err
	}
	for _, candidate := range candidates {
		if candidate.ID == id {
			return s.tryLayer(ctx, req, []*AccountSnapshot{candidate}, layer, reason)
		}
	}
	return nil, false, nil
}

func (s *DefaultSelector) tryLayer(ctx context.Context, req SelectionRequest, candidates []*AccountSnapshot, layer RoutingLayer, reason *RoutingReasonBuilder) (*SelectionResult, bool, error) {
	reason.Layer(layer)
	for _, account := range candidates {
		ok, why, err := s.gates.Allow(ctx, account, req)
		if err != nil {
			return nil, true, err
		}
		if !ok {
			reason.GateFailure(account.ID, why)
			continue
		}
		acquired, err := s.slots.Acquire(ctx, account, req)
		if errors.Is(err, ErrNoSlotAvailable) || errors.Is(err, ErrSlotManagerUnavailable) {
			continue
		}
		if err != nil {
			return nil, true, err
		}
		if acquired == nil || acquired.AcquisitionToken == uuid.Nil {
			continue
		}
		// money path 一致性: caller 传了 ClaimID 但 claims writer 没注入 = 配置 bug,
		// 不能静默跳过 writeback (否则 settlement 找不到 acquisition_token 锚点)。
		if req.ClaimID != 0 {
			if s.claims == nil {
				_ = acquired.release(ctx)
				return nil, true, errors.New("default selector: ClaimID provided but claims writer not configured")
			}
			if err := s.claims.WriteAcquisition(ctx, req.TenantID, req.ClaimID, account.ID, acquired.AcquisitionToken); err != nil {
				_ = acquired.release(ctx)
				// ErrClaimRace 必须 bubble 给 caller (区分"无候选"与"被另请求抢占"
				// 两种语义), 不能映射成 false/nil 让 caller 误判 no-eligible。
				return nil, true, err
			}
		}
		reason.Account(account.ID)
		return &SelectionResult{AccountID: account.ID, AcquisitionToken: acquired.AcquisitionToken, RoutingReasonJSON: reason.JSON()}, true, nil
	}
	return nil, false, nil
}

func (s *DefaultSelector) rankFresh(accounts []*AccountSnapshot, policy *RoutingPolicy) []*AccountSnapshot {
	out := append([]*AccountSnapshot(nil), accounts...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		if a.LoadRate != b.LoadRate {
			return a.LoadRate < b.LoadRate
		}
		return a.LastUsedAt.Before(b.LastUsedAt)
	})
	k := topK(policy, out)
	if k > 1 {
		// math/rand.Rand 非并发安全, 多 goroutine 同时 Shuffle 会 race
		// (Go race detector 会报). 用 randMu 串行化 Shuffle 调用。
		s.randMu.Lock()
		s.rand.Shuffle(k, func(i, j int) { out[i], out[j] = out[j], out[i] })
		s.randMu.Unlock()
	}
	return out
}

func topK(policy *RoutingPolicy, accounts []*AccountSnapshot) int {
	if len(accounts) == 0 {
		return 0
	}
	if policy != nil && (policy.BroadTopK || policy.OperatorScoring) && policy.TopKDefault > 1 {
		return min(policy.TopKDefault, len(accounts))
	}
	top := accounts[0]
	k := 1
	for k < len(accounts) && accounts[k].Priority == top.Priority && accounts[k].LoadRate == top.LoadRate && accounts[k].LastUsedAt.Equal(top.LastUsedAt) {
		k++
	}
	return k
}

func modelRoute(policy *RoutingPolicy, model string, accounts []*AccountSnapshot) []*AccountSnapshot {
	if !hasModelRoute(policy, model) {
		return nil
	}
	allowed := map[int64]struct{}{}
	for _, id := range policy.ModelAccountIDs[model] {
		allowed[id] = struct{}{}
	}
	out := make([]*AccountSnapshot, 0, len(accounts))
	for _, account := range accounts {
		if _, ok := allowed[account.ID]; ok {
			out = append(out, account)
		}
	}
	return out
}

func hasModelRoute(policy *RoutingPolicy, model string) bool {
	return policy != nil && len(policy.ModelAccountIDs[model]) > 0
}

func fallbackPlan(candidates []*AccountSnapshot, policy *RoutingPolicy) *WaitPlan {
	if len(candidates) == 0 {
		return nil
	}
	account := candidates[0]
	timeout, waiting := account.WaitTimeoutMS, account.MaxWaiting
	if policy != nil {
		if policy.FallbackTimeoutMS > 0 {
			timeout = policy.FallbackTimeoutMS
		}
		if policy.FallbackMaxWaiting > 0 {
			waiting = policy.FallbackMaxWaiting
		}
	}
	if timeout <= 0 && waiting <= 0 {
		return nil
	}
	return &WaitPlan{AccountID: account.ID, MaxConcurrency: account.MaxConcurrency, TimeoutMS: timeout, MaxWaiting: waiting}
}
