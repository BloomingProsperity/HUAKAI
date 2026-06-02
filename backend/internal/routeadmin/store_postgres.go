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
	// 但写侧必须 fail-closed 不落脏数据(防御纵深 + 防 tenant B 内部 id 经 routes 列表泄给 A)。
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
