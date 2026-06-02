// HUAKAI · iKun

// Package subscriptionenforce 实现订阅运行时强制的路由侧 gate (R-SUB-WIRE-1)。
// GroupPolicyGate 按 routes 表把调用者订阅档 (user_group) 限制到其允许的 pool_group;
// 与 pool 选择链路其余 gate 同形 (实现 poolrouter.Gate), 由 selector 装配后在选号时生效。
package subscriptionenforce

import (
	"context"
	"strings"

	poolrouter "github.com/BloomingProsperity/HUAKAI/internal/pool/router"
)

// GroupRoutes 是一次 (租户, 档, model) 路由查询的结果。
type GroupRoutes struct {
	// Configured 表示该 (租户, 档) 至少有一条启用且目标池有效(同租户/启用/未软删)的路由,
	// 与是否命中本 model 无关。用于区分"未配置分组路由"与"配置了但本 model 未授权",
	// 二者的白名单语义不同(前者放行、后者拒)。
	Configured bool
	// Allowed 是命中本 model 的路由所指向的有效 pool_group_id 集合。
	Allowed map[int64]struct{}
}

// RoutesRepo 提供按 (租户, 档, model) 读取启用且目标池有效的路由的只读能力。
type RoutesRepo interface {
	// GroupRoutes 返回该 (租户, 档) 的路由配置态 (Configured) 与命中本 model 的允许
	// pool_group 集 (Allowed)。无任何有效路由时 Configured=false 且 Allowed 为空。
	GroupRoutes(ctx context.Context, tenantID int64, userGroup, model string) (GroupRoutes, error)
}

// FailOpenObserver 在 gate 因 repo 出错 fail-open 放行时被调用 (用于 metric/log 告警)。
type FailOpenObserver func(ctx context.Context, req poolrouter.SelectionRequest, err error)

// GroupPolicyGate 实现 poolrouter.Gate + poolrouter.SelectionGatePreparer, 在 pool 选择
// 时按订阅档限制可用 pool_group。
//
// 白名单语义 (Owner 2026-05-30 拍板): 已配置档位必须显式绑定可用池。
//   - user_group 空 / repo 未注入 → 放行 (无档 = 无限制 / 休眠安全)。
//   - 该 (租户,档) 无任何有效路由 (Configured=false) → 放行 (兼容未配置分组路由的老租户)。
//   - 有有效路由但本 model 未命中任何规则 (Allowed 空) → 拒 (白名单: 未授权该 model)。
//   - 有有效路由且候选池在允许集内 → 放行; 否则拒 (group_policy)。
//   - repo 出错 → fail-open 放行 + 触发 observer: 路由档位是 entitlement 而非钱/安全闸,
//     瞬时 DB 错不应拒掉付费用户的请求 (与配额闸 S3 的 fail-closed 区别对待)。
type GroupPolicyGate struct {
	repo       RoutesRepo
	onFailOpen FailOpenObserver
}

// Option 配置 GroupPolicyGate。
type Option func(*GroupPolicyGate)

// WithFailOpenObserver 注入 fail-open 观测钩子 (nil 时 gate 静默 fail-open)。
func WithFailOpenObserver(fn FailOpenObserver) Option {
	return func(g *GroupPolicyGate) { g.onFailOpen = fn }
}

// NewGroupPolicyGate 构造按 routes 限档的 gate。repo 为 nil 时 gate 恒放行。
func NewGroupPolicyGate(repo RoutesRepo, opts ...Option) GroupPolicyGate {
	g := GroupPolicyGate{repo: repo}
	for _, o := range opts {
		o(&g)
	}
	return g
}

// Allow 实现 poolrouter.Gate (直接逐候选调用路径; 生产链路经 PrepareForSelection 走预备
// gate, 每 Select 只查库一次)。
func (g GroupPolicyGate) Allow(ctx context.Context, _ *poolrouter.AccountSnapshot, req poolrouter.SelectionRequest) (bool, poolrouter.GateFailureReason, error) {
	allow, reason := g.verdict(ctx, req)
	return allow, reason, nil
}

// PrepareForSelection 实现 poolrouter.SelectionGatePreparer: 一次 Select 查库一次, 返回
// 固定裁决的 gate。决策只依赖 req (同一 Select 内全候选共享同一 PoolGroupID, 裁决恒定),
// 故预备阶段即可定下整池裁决, 逐候选 Allow 不再查库。
func (g GroupPolicyGate) PrepareForSelection(ctx context.Context, req poolrouter.SelectionRequest) poolrouter.Gate {
	allow, reason := g.verdict(ctx, req)
	return staticVerdictGate{allow: allow, reason: reason}
}

// verdict 查库并按白名单语义裁决。空档 / nil repo / repo 错均 fail-open 放行。
func (g GroupPolicyGate) verdict(ctx context.Context, req poolrouter.SelectionRequest) (bool, poolrouter.GateFailureReason) {
	if req.UserGroup == "" || g.repo == nil {
		return true, ""
	}
	routes, err := g.repo.GroupRoutes(ctx, req.TenantID, req.UserGroup, req.RequestedModel)
	if err != nil {
		// fail-open: 路由档位非钱非安全闸; 瞬时 DB 错放行优于拒付费用户。observer 用于告警。
		if g.onFailOpen != nil {
			g.onFailOpen(ctx, req, err)
		}
		return true, ""
	}
	return decideGroupRoutes(routes, req.PoolGroupID)
}

// decideGroupRoutes 白名单裁决核心: 未配置→放行; 配置了且命中候选池→放行; 其余 (含配置了
// 但本 model 未命中、或命中了但候选池不在允许集) → 拒。
func decideGroupRoutes(r GroupRoutes, poolGroupID int64) (bool, poolrouter.GateFailureReason) {
	if !r.Configured {
		return true, ""
	}
	if _, ok := r.Allowed[poolGroupID]; ok {
		return true, ""
	}
	return false, poolrouter.GateFailureGroupPolicy
}

// staticVerdictGate 是 PrepareForSelection 返回的预备 gate: 对本次 Select 的全部候选
// 返回同一个已定裁决 (账号参数不参与)。
type staticVerdictGate struct {
	allow  bool
	reason poolrouter.GateFailureReason
}

func (g staticVerdictGate) Allow(context.Context, *poolrouter.AccountSnapshot, poolrouter.SelectionRequest) (bool, poolrouter.GateFailureReason, error) {
	return g.allow, g.reason, nil
}

// ModelPatternMatches 判定 routes.model_pattern_match 是否命中具体 model。
// 支持: '*' 或空 = 全匹配; 'prefix*' = 前缀匹配; 否则精确相等。
func ModelPatternMatches(pattern, model string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(model, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == model
}
