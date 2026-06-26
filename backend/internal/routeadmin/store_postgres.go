// HUAKAI · iKun

package routeadmin

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore 用裸 pgx 写 routes 表(镜像 internal/subscriptionenforce 的只读仓储 + voucher store 范式)。
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore 构造基于连接池的 routes 写仓储。
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// routeColumns 是 RETURNING / SELECT 的统一列序(与 scanRoute 一一对应)。
const routeColumns = `id, tenant_id, name, user_group_match, model_pattern_match, pool_group_id, match_priority, enabled, created_at, updated_at`

func (s *PostgresStore) Create(ctx context.Context, in CreateInput) (Route, error) {
	if s == nil || s.pool == nil {
		return Route{}, ErrStoreNotConfigured
	}
	// 目标 pool_group 必须属于同租户且未软删: 单列 FK(REFERENCES pool_groups(id)) 拦不住跨租户引用
	// —— tenant A 可建指向 tenant B pool_group 的路由。热路径 gate 的 JOIN 虽会运行时过滤掉这种目标,
	// 但写侧必须失败即关闭(fail-closed)不落脏数据(防御纵深 + 防 tenant B 内部 id 经 routes 列表泄给 A)。
	// 条件 INSERT ... SELECT ... WHERE EXISTS 原子完成: 归属不成立 → 插 0 行 → pgx.ErrNoRows → ErrPoolGroupNotFound。
	// MatchPriority 为 nil 时传 NULL, COALESCE 回落 DB 默认 100。
	row := s.pool.QueryRow(ctx, `
INSERT INTO routes (tenant_id, name, user_group_match, model_pattern_match, pool_group_id, match_priority)
SELECT $1, $2, $3, $4, $5, COALESCE($6::integer, 100)
WHERE EXISTS (
    SELECT 1 FROM pool_groups WHERE id = $5 AND tenant_id = $1 AND deleted_at IS NULL
)
RETURNING `+routeColumns,
		in.TenantID, in.Name, in.UserGroupMatch, in.ModelPatternMatch, in.PoolGroupID, in.MatchPriority)
	r, err := scanRoute(row)
	if err != nil {
		if isUniqueViolation(err) {
			return Route{}, ErrDuplicateName
		}
		if errors.Is(err, pgx.ErrNoRows) {
			// WHERE EXISTS 不成立: pool_group 不存在 / 非本租户 / 已软删。
			return Route{}, ErrPoolGroupNotFound
		}
		if isForeignKeyViolation(err) {
			return Route{}, ErrPoolGroupNotFound
		}
		return Route{}, fmt.Errorf("routeadmin: insert route: %w", err)
	}
	return r, nil
}

func (s *PostgresStore) Update(ctx context.Context, in UpdateInput) (Route, error) {
	if s == nil || s.pool == nil {
		return Route{}, ErrStoreNotConfigured
	}
	// 全替换可编辑字段。目标 pool_group 必须同租户且未软删(防跨租户引用, 与 Create 同的纵深防御):
	// WHERE 同时要求 (本租户该行未软删) 与 EXISTS(同租户 pool_group)。更新 0 行 → pgx.ErrNoRows,
	// 此时反查该行是否仍在以消歧: 行在 → pool_group 问题; 行不在 → 行本身不存在。
	// MatchPriority 为 nil 时传 NULL, COALESCE 回落 DB 默认 100(全替换语义); enabled/created_at 不动。
	row := s.pool.QueryRow(ctx, `
UPDATE routes
SET name = $3, user_group_match = $4, model_pattern_match = $5,
    pool_group_id = $6, match_priority = COALESCE($7::integer, 100), updated_at = now()
WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL
  AND EXISTS (
      SELECT 1 FROM pool_groups WHERE id = $6 AND tenant_id = $1 AND deleted_at IS NULL
  )
RETURNING `+routeColumns,
		in.TenantID, in.ID, in.Name, in.UserGroupMatch, in.ModelPatternMatch, in.PoolGroupID, in.MatchPriority)
	r, err := scanRoute(row)
	if err != nil {
		if isUniqueViolation(err) {
			return Route{}, ErrDuplicateName
		}
		if isForeignKeyViolation(err) {
			return Route{}, ErrPoolGroupNotFound
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return Route{}, s.disambiguateUpdateMiss(ctx, in.TenantID, in.ID)
		}
		return Route{}, fmt.Errorf("routeadmin: update route: %w", err)
	}
	return r, nil
}

// disambiguateUpdateMiss 在 Update 影响 0 行时判定真因: 该(租户,行)仍未软删 → 是 pool_group
// 不满足同租户/未删(ErrPoolGroupNotFound); 否则 → 行本身不存在/已软删(ErrRouteNotFound)。
// 只读, 仅错误路径触发; 并发软删该行的窄窗会落到 ErrRouteNotFound, 语义可接受。
func (s *PostgresStore) disambiguateUpdateMiss(ctx context.Context, tenantID, id int64) error {
	var exists int
	err := s.pool.QueryRow(ctx, `
SELECT 1 FROM routes WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL`, tenantID, id).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRouteNotFound
	}
	if err != nil {
		return fmt.Errorf("routeadmin: update route (disambiguate): %w", err)
	}
	return ErrPoolGroupNotFound
}

func (s *PostgresStore) List(ctx context.Context, tenantID int64) ([]Route, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	rows, err := s.pool.Query(ctx, `
SELECT `+routeColumns+`
FROM routes
WHERE tenant_id = $1 AND deleted_at IS NULL
ORDER BY match_priority, id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("routeadmin: list routes: %w", err)
	}
	defer rows.Close()
	var out []Route
	for rows.Next() {
		r, err := scanRoute(rows)
		if err != nil {
			return nil, fmt.Errorf("routeadmin: scan route: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("routeadmin: iterate routes: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) Get(ctx context.Context, tenantID, id int64) (Route, error) {
	if s == nil || s.pool == nil {
		return Route{}, ErrStoreNotConfigured
	}
	row := s.pool.QueryRow(ctx, `
SELECT `+routeColumns+`
FROM routes
WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL`, tenantID, id)
	r, err := scanRoute(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Route{}, ErrRouteNotFound
	}
	if err != nil {
		return Route{}, fmt.Errorf("routeadmin: get route: %w", err)
	}
	return r, nil
}

// SetEnabled 翻转 enabled 闸(独立窄动作): 仅未软删的本租户行可改, 不碰 pool_group/match_priority 等其它列。
// 不做 pool_group EXISTS 校验 —— 启用一条 pool_group 已软删的路由无害(热路径 gate 的 JOIN 仍运行时过滤掉),
// 该检查只属于改 pool_group 的 Update。RETURNING 0 行 → pgx.ErrNoRows → ErrRouteNotFound。
// 幂等: 把 enabled 设成当前值, UPDATE 仍命中该行(WHERE 不含 enabled 条件)并返回快照, 不报错。
//
// 路线图(后续扩展点): enabled 现为裸布尔, 只表达「运营手动启/停」。若将来落地健康检查自动停用(auto-disable),
// 须把它扩成小枚举(enabled / manual-disabled / auto-disabled)以区分运营手动停用与系统自动停用 —— 否则
// 自动重新启用会覆盖运营的手动停用(new-api/CLIProxyAPI 用多态 status 正是防此)。
// 详见 docs/process/plans/2026-06-18-routes-enable-disable.md。
func (s *PostgresStore) SetEnabled(ctx context.Context, tenantID, id int64, enabled bool) (Route, error) {
	if s == nil || s.pool == nil {
		return Route{}, ErrStoreNotConfigured
	}
	row := s.pool.QueryRow(ctx, `
UPDATE routes
SET enabled = $3, updated_at = now()
WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL
RETURNING `+routeColumns, tenantID, id, enabled)
	r, err := scanRoute(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Route{}, ErrRouteNotFound
	}
	if err != nil {
		return Route{}, fmt.Errorf("routeadmin: set route enabled: %w", err)
	}
	return r, nil
}

func (s *PostgresStore) SoftDelete(ctx context.Context, tenantID, id int64) (Route, error) {
	if s == nil || s.pool == nil {
		return Route{}, ErrStoreNotConfigured
	}
	// 仅未软删的行可删; RETURNING 0 行 → pgx.ErrNoRows → ErrRouteNotFound(幂等: 再删返 not found)。
	row := s.pool.QueryRow(ctx, `
UPDATE routes
SET deleted_at = now(), updated_at = now()
WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL
RETURNING `+routeColumns, tenantID, id)
	r, err := scanRoute(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Route{}, ErrRouteNotFound
	}
	if err != nil {
		return Route{}, fmt.Errorf("routeadmin: soft-delete route: %w", err)
	}
	return r, nil
}

// rowScanner 抽象 pgx.Row 与 pgx.Rows 的 Scan, 让 scanRoute 共用。
type rowScanner interface {
	Scan(dest ...any) error
}

func scanRoute(row rowScanner) (Route, error) {
	var r Route
	err := row.Scan(&r.ID, &r.TenantID, &r.Name, &r.UserGroupMatch, &r.ModelPatternMatch,
		&r.PoolGroupID, &r.MatchPriority, &r.Enabled, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
