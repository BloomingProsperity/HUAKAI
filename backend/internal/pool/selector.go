package pool

import (
	"context"
	"errors"
	"math/rand"
	"sort"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNoEligibleAccount   = errors.New("pool has no eligible provider account")
	ErrAllChannelsDegraded = errors.New("pool has no eligible provider account: all_channels_degraded")
	ErrClaimRace           = errors.New("pool claim writeback race")
)

type AccountSnapshot struct {
	ID             int64
	TenantID       int64
	Priority       int
	LoadRate       float64
	LastUsedAt     time.Time
	MaxConcurrency int
	WaitTimeoutMS  int
	MaxWaiting     int
}

type RoutingPolicy struct {
	ModelAccountIDs      map[string][]int64
	TopKDefault          int
	BroadTopK            bool
	OperatorScoring      bool
	ScoringPolicyVersion string
	FallbackTimeoutMS    int
	FallbackMaxWaiting   int
}

type AccountSource interface {
	ListAccounts(ctx context.Context, req SelectionRequest) ([]*AccountSnapshot, error)
}

type RoutingPolicySource interface {
	GetRoutingPolicy(ctx context.Context, req SelectionRequest) (*RoutingPolicy, error)
}

type StickyStore interface {
	Lookup(ctx context.Context, req SelectionRequest) (accountID int64, found bool, err error)
}

type ClaimGate interface {
	WriteAcquisition(ctx context.Context, tenantID, claimID, accountID int64, token uuid.UUID) error
}

type SelectorOption func(*DefaultSelector)

type DefaultSelector struct {
	accounts AccountSource
	policies RoutingPolicySource
	sticky   StickyStore
	gates    GateChain
	slots    SlotManager
	claims   ClaimGate
	rand     *rand.Rand
}

func NewDefaultSelector(accounts AccountSource, opts ...SelectorOption) *DefaultSelector {
	s := &DefaultSelector{
		accounts: accounts,
		gates:    DefaultGateChain(),
		slots:    nilSlotManager{},
		rand:     rand.New(rand.NewSource(time.Now().UnixNano())),
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

	routed := modelRoute(policy, req.RequestedModel, eligible)
	if len(routed) > 0 {
		if res, done, err := s.trySticky(ctx, req, routed, RoutingLayerStickyWithinRoute, reason); done || err != nil {
			return res, err
		}
		if res, done, err := s.tryLayer(ctx, req, routed, RoutingLayerRoutingAffinity, reason); done || err != nil {
			return res, err
		}
	} else if policy == nil || len(policy.ModelAccountIDs[req.RequestedModel]) == 0 {
		if res, done, err := s.trySticky(ctx, req, eligible, RoutingLayerStickyStandalone, reason); done || err != nil {
			return res, err
		}
	}

	fresh := s.rankFresh(eligible, policy)
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
		if s.claims != nil && req.ClaimID != 0 {
			if err := s.claims.WriteAcquisition(ctx, req.TenantID, req.ClaimID, account.ID, acquired.AcquisitionToken); err != nil {
				_ = acquired.release(ctx)
				if errors.Is(err, ErrClaimRace) {
					return nil, false, nil
				}
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
		s.rand.Shuffle(k, func(i, j int) { out[i], out[j] = out[j], out[i] })
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
	if policy == nil || len(policy.ModelAccountIDs[model]) == 0 {
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
