// HUAKAI · iKun

package subscriptionenforce

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresRoutesRepo 从 routes 表读启用路由规则 (只读, 不写)。
type PostgresRoutesRepo struct {
	pool *pgxpool.Pool
}

// NewPostgresRoutesRepo 构造基于连接池的 routes 只读仓储。
func NewPostgresRoutesRepo(pool *pgxpool.Pool) *PostgresRoutesRepo {
	return &PostgresRoutesRepo{pool: pool}
}

// GroupRoutes 查该租户该用户组下、启用且未软删、且目标 pool_group 有效(同租户/启用/未软删)
// 的路由。JOIN pool_groups 校验目标池避免返回跨租户/已禁用/软删的 pool_group_id (F2)。
// Configured = 是否存在任一这样的有效路由(不论是否命中本 model), 用于白名单语义区分
// "未配置档"与"配置了但本 model 未授权"。model_pattern 的通配匹配放应用层 (而非 SQL LIKE),
// 与 GroupPolicyGate 的 ModelPatternMatches 语义单一来源、可单测。
//
// match_priority 真裁决(slice B, Owner Q3): SELECT 取回 match_priority, 命中本 model 的路由
// 交给 highestPriorityAllowed 只保留最高优先档(最小值)的 pool_group, 并列同档取并集。优先档
// 计算放应用层纯函数, 与匹配语义同处一包、免 DB 单测。Configured 仍按"有任一有效路由"判, 与优先档
// 无关。
func (r *PostgresRoutesRepo) GroupRoutes(ctx context.Context, tenantID int64, userGroup, model string) (GroupRoutes, error) {
	if r == nil || r.pool == nil {
		return GroupRoutes{}, fmt.Errorf("subscriptionenforce: routes repo not configured")
	}
	rows, err := r.pool.Query(ctx, `
SELECT r.pool_group_id, r.model_pattern_match, r.match_priority
FROM routes r
JOIN pool_groups pg
  ON pg.id = r.pool_group_id
 AND pg.tenant_id = r.tenant_id
 AND pg.enabled = true
 AND pg.deleted_at IS NULL
WHERE r.tenant_id = $1
  AND r.user_group_match = $2
  AND r.enabled = true
  AND r.deleted_at IS NULL`,
		tenantID, userGroup)
	if err != nil {
		return GroupRoutes{}, fmt.Errorf("subscriptionenforce: query routes: %w", err)
	}
	defer rows.Close()
	var out GroupRoutes
	var matched []matchedRoute
	for rows.Next() {
		var poolGroupID int64
		var pattern string
		var priority int
		if err := rows.Scan(&poolGroupID, &pattern, &priority); err != nil {
			return GroupRoutes{}, fmt.Errorf("subscriptionenforce: scan route: %w", err)
		}
		// 有一条启用且目标池有效的路由即视为该档"已配置分组路由"(与是否命中本 model、优先档无关)。
		out.Configured = true
		if ModelPatternMatches(pattern, model) {
			matched = append(matched, matchedRoute{poolGroupID: poolGroupID, priority: priority})
		}
	}
	if err := rows.Err(); err != nil {
		return GroupRoutes{}, fmt.Errorf("subscriptionenforce: iterate routes: %w", err)
	}
	// 命中集只保留最高优先档(最小 match_priority); 无命中 → 空集(非 nil)。
	out.Allowed = highestPriorityAllowed(matched)
	return out, nil
}
