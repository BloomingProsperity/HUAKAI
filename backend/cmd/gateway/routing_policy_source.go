// routing_policy_source.go — 生产 RoutingPolicySource 装配点(路由加权激活闭环)。
//
// 闭环背景:pool/router 的 weightedReservoirIndex(按账号 static_weight 加权选号)早已实现,
// 但被两处断点架空——其一是 default selector 从不注入 RoutingPolicySource,故 policy() 恒 nil,
// SelectionMode 恒空,永走均匀 Shuffle。本文件提供生产注入实现点亮该分支。
//
// 关键不变量(默认 strict_priority 不翻转,opt-in 激活非全局翻转):
//   - binding 未设 / 设 'strict_priority' → req.SelectionMode 为空或 "strict_priority"
//     → 返回 SelectionMode 非 priority_weighted 的 policy → selector 仍走 Shuffle,
//     与接线前逐一字节一致。
//   - 仅 binding 显式 'priority_weighted' → 返回 SelectionMode=priority_weighted 的 policy
//     → selector 走 weightedReservoirIndex 加权选号。
//   - 返回值始终非 nil;SelectionMode 只受 binding 控制,fallback wait 配置按池组短 TTL
//     补齐,避免 selector 热路径每轮都查库。
package main

import (
	"context"
	"sync"
	"time"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
)

const defaultRoutingPolicyCacheTTL = 45 * time.Second

type routingPolicyPoolGetter interface {
	GetPool(context.Context, dbbilling.GetPoolParams) (dbbilling.PoolGroup, error)
}

type routingPolicyCacheKey struct {
	tenantID    int64
	poolGroupID int64
}

type routingPolicyCacheEntry struct {
	fallbackTimeoutMS  int
	fallbackMaxWaiting int
	topKDefault        int
	expiresAt          time.Time
}

type routingPolicyInflight struct {
	done  chan struct{}
	entry routingPolicyCacheEntry
	err   error
}

// bindingRoutingPolicySource 是请求级路由策略源:据本次请求命中 binding 透传进
// SelectionRequest 的 selection_mode 返回选号模式,并用短 TTL 缓存补齐 fallback wait 配置。
// selection_mode 已由 dispatch 端从 registry 解析的 BindingMetadata 透传到 req,这里只做映射。
type bindingRoutingPolicySource struct {
	q routingPolicyPoolGetter

	mu       sync.Mutex
	cache    map[routingPolicyCacheKey]routingPolicyCacheEntry
	inflight map[routingPolicyCacheKey]*routingPolicyInflight
	cacheTTL time.Duration
	now      func() time.Time
}

// newBindingRoutingPolicySource 构造生产 RoutingPolicySource。
func newBindingRoutingPolicySource(q ...routingPolicyPoolGetter) pool.RoutingPolicySource {
	var queries routingPolicyPoolGetter
	if len(q) > 0 {
		queries = q[0]
	}
	return &bindingRoutingPolicySource{
		q:        queries,
		cacheTTL: defaultRoutingPolicyCacheTTL,
		now:      time.Now,
	}
}

// GetRoutingPolicy 据 req.SelectionMode 返回选号策略。
//   - "priority_weighted" → 加权分支(按账号 static_weight)。
//   - 其它(""/"strict_priority"/未知)→ strict_priority 等价,走均匀 Shuffle(默认保持)。
//
// 始终返回非 nil policy 让 selector 的 policy() 拿到确定结果;SelectionMode 与 fallback
// wait 配置彼此独立,默认 strict 不会被 fallback 缓存翻成加权。
func (s *bindingRoutingPolicySource) GetRoutingPolicy(ctx context.Context, req pool.SelectionRequest) (*pool.RoutingPolicy, error) {
	mode := pool.SelectionModeStrictPriority
	if pool.SelectionMode(req.SelectionMode) == pool.SelectionModePriorityWeighted {
		mode = pool.SelectionModePriorityWeighted
	}
	policy := &pool.RoutingPolicy{SelectionMode: mode}
	if mode == pool.SelectionModePriorityWeighted {
		policy.OperatorScoring = true
		policy.ScoringPolicyVersion = "adaptive-v1"
	}
	if s.q == nil || req.TenantID == 0 || req.PoolGroupID == 0 {
		return policy, nil
	}
	key := routingPolicyCacheKey{tenantID: req.TenantID, poolGroupID: req.PoolGroupID}
	entry, err := s.fallbackPolicy(ctx, key, dbbilling.GetPoolParams{
		TenantID: req.TenantID,
		ID:       req.PoolGroupID,
	})
	if err != nil {
		return nil, err
	}
	applyRoutingPolicyFallback(policy, entry)
	return policy, nil
}

func (s *bindingRoutingPolicySource) fallbackPolicy(ctx context.Context, key routingPolicyCacheKey, params dbbilling.GetPoolParams) (routingPolicyCacheEntry, error) {
	s.mu.Lock()
	now := s.currentTime()
	if s.cache == nil {
		s.cache = make(map[routingPolicyCacheKey]routingPolicyCacheEntry)
	}
	if entry, ok := s.cache[key]; ok && now.Before(entry.expiresAt) {
		s.mu.Unlock()
		return entry, nil
	}
	if s.inflight == nil {
		s.inflight = make(map[routingPolicyCacheKey]*routingPolicyInflight)
	}
	if call, ok := s.inflight[key]; ok {
		done := call.done
		s.mu.Unlock()
		select {
		case <-done:
			return call.entry, call.err
		case <-ctx.Done():
			return routingPolicyCacheEntry{}, ctx.Err()
		}
	}
	call := &routingPolicyInflight{done: make(chan struct{})}
	s.inflight[key] = call
	s.mu.Unlock()

	poolGroup, err := s.q.GetPool(ctx, params)
	var entry routingPolicyCacheEntry
	if err == nil {
		entry = routingPolicyCacheEntry{
			fallbackMaxWaiting: int(poolGroup.FallbackWaitMaxWaiting),
			fallbackTimeoutMS:  int(poolGroup.FallbackWaitTimeoutMs),
			topKDefault:        int(poolGroup.TopKDefault),
			expiresAt:          s.currentTime().Add(s.effectiveCacheTTL()),
		}
	}

	s.mu.Lock()
	if err == nil {
		if s.cache == nil {
			s.cache = make(map[routingPolicyCacheKey]routingPolicyCacheEntry)
		}
		s.cache[key] = entry
	}
	call.entry = entry
	call.err = err
	delete(s.inflight, key)
	close(call.done)
	s.mu.Unlock()
	return entry, err
}

func (s *bindingRoutingPolicySource) currentTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *bindingRoutingPolicySource) effectiveCacheTTL() time.Duration {
	if s.cacheTTL > 0 {
		return s.cacheTTL
	}
	return defaultRoutingPolicyCacheTTL
}

func applyRoutingPolicyFallback(policy *pool.RoutingPolicy, entry routingPolicyCacheEntry) {
	policy.FallbackMaxWaiting = entry.fallbackMaxWaiting
	policy.FallbackTimeoutMS = entry.fallbackTimeoutMS
	policy.TopKDefault = entry.topKDefault
}
