// HUAKAI · iKun

package panelauth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresRoleStore 从 users 表读 role(租户内, 未软删)。只读。
type PostgresRoleStore struct {
	pool *pgxpool.Pool
}

// NewPostgresRoleStore 构造基于连接池的 role 只读仓储。
func NewPostgresRoleStore(pool *pgxpool.Pool) *PostgresRoleStore {
	return &PostgresRoleStore{pool: pool}
}

// UserRole 返回 (tenant,user) 的 users.role; 无此未软删行 → ErrUserNotFound。
// tenant 谓词避免跨租户读到他租户用户的角色(串租户越权防线)。
func (s *PostgresRoleStore) UserRole(ctx context.Context, tenantID, userID int64) (string, error) {
	if s == nil || s.pool == nil {
		return "", ErrStoreNotConfigured
	}
	var role string
	err := s.pool.QueryRow(ctx, `
SELECT role FROM users
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		userID, tenantID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUserNotFound
	}
	if err != nil {
		return "", err
	}
	return role, nil
}

// ActiveUserRole 同时要求用户与所属租户均为 active 且未软删。用户封禁、租户停用
// 或任一方软删除后,该用户即刻失去 admin 权力(每请求生效,不等 session 过期)。
// admin 权力面与 /v1/auth/me 面板归属都走它,避免两条入口出现状态口径分叉。
func (s *PostgresRoleStore) ActiveUserRole(ctx context.Context, tenantID, userID int64) (string, error) {
	if s == nil || s.pool == nil {
		return "", ErrStoreNotConfigured
	}
	var role string
	err := s.pool.QueryRow(ctx, `
SELECT u.role
FROM users u
JOIN tenants t ON t.id = u.tenant_id
WHERE u.id = $1
  AND u.tenant_id = $2
  AND u.deleted_at IS NULL
  AND u.status = 'active'
  AND t.deleted_at IS NULL
  AND t.status = 'active'`,
		userID, tenantID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUserNotFound
	}
	if err != nil {
		return "", err
	}
	return role, nil
}
