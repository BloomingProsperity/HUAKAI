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
