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

// AllowedPoolGroups 查该租户该用户组下启用且未软删的路由, 在 Go 侧按 model_pattern_match
// 过滤命中的, 收集其 pool_group_id。model_pattern 的通配匹配放在应用层 (而非 SQL LIKE),
// 以便与 GroupPolicyGate 的 ModelPatternMatches 语义单一来源、可单测。
func (r *PostgresRoutesRepo) AllowedPoolGroups(ctx context.Context, tenantID int64, userGroup, model string) (map[int64]struct{}, error) {
	if r == nil || r.pool == nil {
		return nil, fmt.Errorf("subscriptionenforce: routes repo not configured")
	}
	rows, err := r.pool.Query(ctx, `
SELECT pool_group_id, model_pattern_match
FROM routes
WHERE tenant_id = $1
  AND user_group_match = $2
  AND enabled = true
  AND deleted_at IS NULL`,
		tenantID, userGroup)
	if err != nil {
		return nil, fmt.Errorf("subscriptionenforce: query routes: %w", err)
	}
	defer rows.Close()
	allowed := make(map[int64]struct{})
	for rows.Next() {
		var poolGroupID int64
		var pattern string
		if err := rows.Scan(&poolGroupID, &pattern); err != nil {
			return nil, fmt.Errorf("subscriptionenforce: scan route: %w", err)
		}
		if ModelPatternMatches(pattern, model) {
			allowed[poolGroupID] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("subscriptionenforce: iterate routes: %w", err)
	}
	return allowed, nil
}
