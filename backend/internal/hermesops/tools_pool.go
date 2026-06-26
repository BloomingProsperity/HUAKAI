package hermesops

import (
	"context"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// PoolListDeps 是 pool_list 工具的只读依赖:List 包装 dbbilling.Queries.ListPools(按租户 SELECT-only,
// SQL 已含 deleted_at IS NULL 只返活跃池)。nil 时工具按依赖检查 fail-closed。
type PoolListDeps struct {
	List func(ctx context.Context, params dbbilling.ListPoolsParams) ([]dbbilling.PoolGroup, error)
}

// poolListLimit 是单次列出 pool group 的上限(租户池数量少,够用且防超大读)。
const poolListLimit = 200

// PoolListSpec 构建只读 pool_list 工具:列出本租户的路由 pool group 及其配置——补全
// model_resolve_diagnose 开的"模型→池→账号"路由拓扑里"池本身有哪些、怎么配的"一环。租户 scope 取自
// 已鉴权 req.TenantID(ListPools 按 tenant_id 过滤)。PoolGroup 全是结构化配置字段、无自由文本/PII
// (Name 是运营自取的池标签),poolShape 仍显式列举投影(不 echo 整个 struct,防未来新增字段自动外泄)。
func PoolListSpec(deps PoolListDeps) ToolSpec {
	return ToolSpec{
		Name:         ToolPoolList,
		Category:     CategoryDiagnostic,
		Description:  "List the tenant's routing pool groups and their config: name, routing policy version, top-k/capability defaults, sticky/fallback max-waiting + timeouts, forced-route rate limit, enabled state. Tenant-wide complement to model_resolve_diagnose's model->pool bindings. READ ONLY.",
		ReadOnly:     true,
		RequiredRole: RoleTenantOperator,
		InputSchema:  map[string]string{},
		Run: func(ctx context.Context, req ToolRequest) (ToolResult, error) {
			if deps.List == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			rows, err := deps.List(ctx, dbbilling.ListPoolsParams{TenantID: req.TenantID, LimitCount: poolListLimit})
			if err != nil {
				return ToolResult{}, err
			}
			items := make([]map[string]any, 0, len(rows))
			enabledCount := 0
			for _, p := range rows {
				if p.Enabled {
					enabledCount++
				}
				items = append(items, poolShape(p))
			}
			return ToolResult{Summary: map[string]any{
				"pool_count":    len(rows),
				"enabled_count": enabledCount,
				"items":         items,
			}}, nil
		},
	}
}

// poolShape 把一条 PoolGroup 投影成路由配置诊断字段。显式列举(safe-by-construction):全是结构化
// 枚举/数值/布尔/时间戳,无自由文本/PII;TenantID 不回投(调用方已知自己租户),DeletedAt 不投
// (ListPools 已过滤,恒为空)。
func poolShape(p dbbilling.PoolGroup) map[string]any {
	return map[string]any{
		"id":                               p.ID,
		"name":                             p.Name,
		"routing_policy_version":           p.RoutingPolicyVersion,
		"top_k_default":                    p.TopKDefault,
		"capability_default":               p.CapabilityDefault,
		"allow_tenant_operator_force":      p.AllowTenantOperatorForce,
		"allow_last_resort":                p.AllowLastResort,
		"sticky_wait_max_waiting":          p.StickyWaitMaxWaiting,
		"fallback_wait_max_waiting":        p.FallbackWaitMaxWaiting,
		"sticky_wait_timeout_ms":           p.StickyWaitTimeoutMs,
		"fallback_wait_timeout_ms":         p.FallbackWaitTimeoutMs,
		"forced_route_rate_limit_per_hour": p.ForcedRouteRateLimitPerHour,
		"enabled":                          p.Enabled,
		"created_at":                       tsAny(p.CreatedAt),
		"updated_at":                       tsAny(p.UpdatedAt),
	}
}
