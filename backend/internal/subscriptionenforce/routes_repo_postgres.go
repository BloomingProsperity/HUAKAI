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

// GroupRoutes 查该租户该用户组下启用且未软删的路由，并用 LEFT JOIN 单独判断目标池是否
// 同租户、启用且未软删。路由是否配置与目标是否仍有效必须分开：否则池被停用或软删后，
// 已配置白名单会被误判为“从未配置”并放行到其它池。
//
// match_priority 真裁决(slice B, Owner Q3): SELECT 取回 match_priority, 命中本 model 的路由
// 交给 highestPriorityAllowed 只保留最高优先档(最小值)的 pool_group, 并列同档取并集。优先档
// 计算放应用层纯函数。Configured 按“有任一启用路由”判；命中当前 model 但目标失效时
// InvalidMatchingTarget=true，由 gate 返回稳定的配置不可用错误。
func (r *PostgresRoutesRepo) GroupRoutes(ctx context.Context, tenantID int64, userGroup, model string) (GroupRoutes, error) {
	if r == nil || r.pool == nil {
		return GroupRoutes{}, fmt.Errorf("subscriptionenforce: routes repo not configured")
	}
	rows, err := r.pool.Query(ctx, `
SELECT r.pool_group_id, r.model_pattern_match, r.match_priority, pg.id IS NOT NULL AS target_valid
FROM routes r
LEFT JOIN pool_groups pg
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
		var targetValid bool
		if err := rows.Scan(&poolGroupID, &pattern, &priority, &targetValid); err != nil {
			return GroupRoutes{}, fmt.Errorf("subscriptionenforce: scan route: %w", err)
		}
		// 路由本身启用即视为已配置；目标失效只能拒绝，不能退化为未配置直通。
		out.Configured = true
		if ModelPatternMatches(pattern, model) {
			if !targetValid {
				out.InvalidMatchingTarget = true
				continue
			}
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
