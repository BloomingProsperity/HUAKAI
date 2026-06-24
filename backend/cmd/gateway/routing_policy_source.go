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
//   - 返回值始终非 nil,但其它字段全留零值——与"policy==nil"行为等价(topK/hasModelRoute/
//     fallbackPlan 三处对 nil 与零值策略走同分支),只令加权分支可达,不改任何既有行为。
package main

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/pool"
)

// bindingRoutingPolicySource 是无状态的请求级路由策略源:据本次请求命中 binding 透传进
// SelectionRequest 的 selection_mode,返回对应 RoutingPolicy。不查库、不持状态——
// selection_mode 已由 dispatch 端从 registry 解析的 BindingMetadata 透传到 req,这里只做映射。
type bindingRoutingPolicySource struct{}

// newBindingRoutingPolicySource 构造生产 RoutingPolicySource。
func newBindingRoutingPolicySource() pool.RoutingPolicySource {
	return bindingRoutingPolicySource{}
}

// GetRoutingPolicy 据 req.SelectionMode 返回选号策略。
//   - "priority_weighted" → 加权分支(按账号 static_weight)。
//   - 其它(""/"strict_priority"/未知)→ strict_priority 等价,走均匀 Shuffle(默认保持)。
//
// 始终返回非 nil policy 让 selector 的 policy() 拿到确定结果;非加权时其余字段零值,
// 与 policy==nil 行为完全等价(见文件头不变量)。
func (bindingRoutingPolicySource) GetRoutingPolicy(_ context.Context, req pool.SelectionRequest) (*pool.RoutingPolicy, error) {
	mode := pool.SelectionModeStrictPriority
	if pool.SelectionMode(req.SelectionMode) == pool.SelectionModePriorityWeighted {
		mode = pool.SelectionModePriorityWeighted
	}
	return &pool.RoutingPolicy{SelectionMode: mode}, nil
}
