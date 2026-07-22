// Package hermesprincipal 管理 Hermes 调用完整网关链时使用的租户级内部服务主体。
package hermesprincipal

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotConfigured = errors.New("hermesprincipal: 存储未配置")
	ErrInvalidTenant = errors.New("hermesprincipal: 租户无效")
	ErrTenantMissing = errors.New("hermesprincipal: 租户不存在")
	ErrCorrupt       = errors.New("hermesprincipal: 服务主体映射损坏")
)

// Principal 是内部模型调用进入共享网关链所需的租户、用户和 Key 外键。
// UserID 与 APIKeyID 只承担账本和路由外键，不代表普通用户，也不存在可用明文 bearer。
type Principal struct {
	TenantID int64
	UserID   int64
	APIKeyID int64
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Get 读取并校验已存在的服务主体。映射存在但父行性质不匹配时显式报错，绝不静默重建。
func (s *Store) Get(ctx context.Context, tenantID int64) (Principal, error) {
	if s == nil || s.pool == nil {
		return Principal{}, ErrNotConfigured
	}
	if tenantID <= 0 {
		return Principal{}, ErrInvalidTenant
	}
	principal, err := load(ctx, s.pool, tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrTenantMissing
	}
	return principal, err
}

// Ensure 在租户第一次使用 Hermes 时幂等创建内部服务主体。
// 同一租户通过事务级 advisory lock 串行化，避免并发请求留下孤立 users/api_keys 行。
func (s *Store) Ensure(ctx context.Context, tenantID int64) (Principal, error) {
	if s == nil || s.pool == nil {
		return Principal{}, ErrNotConfigured
	}
	if tenantID <= 0 {
		return Principal{}, ErrInvalidTenant
	}
	// 租户级事务锁已经提供了唯一创建顺序。使用读已提交可确保等待锁的请求在前一事务
	// 提交后看见最新映射；可串行化快照可能在等待前固定，反而会误判为尚未创建。
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return Principal{}, fmt.Errorf("hermesprincipal: 开启事务: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	lockName := fmt.Sprintf("huakai.hermes.principal.%d", tenantID)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockName); err != nil {
		return Principal{}, fmt.Errorf("hermesprincipal: 获取租户锁: %w", err)
	}

	principal, err := load(ctx, tx, tenantID)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return Principal{}, fmt.Errorf("hermesprincipal: 提交已有映射: %w", err)
		}
		return principal, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, err
	}

	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM tenants
		WHERE id=$1 AND status='active' AND deleted_at IS NULL
	)`, tenantID).Scan(&exists); err != nil {
		return Principal{}, fmt.Errorf("hermesprincipal: 检查租户: %w", err)
	}
	if !exists {
		return Principal{}, ErrTenantMissing
	}

	if err := tx.QueryRow(ctx, `
INSERT INTO users (tenant_id, display_name, status, role, principal_kind)
VALUES ($1, 'Hermes 内部服务主体', 'active', 'user', 'service')
RETURNING id`, tenantID).Scan(&principal.UserID); err != nil {
		return Principal{}, fmt.Errorf("hermesprincipal: 创建服务用户: %w", err)
	}
	principal.TenantID = tenantID
	if err := tx.QueryRow(ctx, `
INSERT INTO api_keys (
    tenant_id, user_id, name, key_hash, key_prefix, status, purpose
) VALUES (
    $1::bigint, $2::bigint, 'Hermes 内部模型调用', 'disabled-internal-service-principal',
    'hk_hermes_' || $1::bigint::text, 'active', 'hermes'
)
RETURNING id`, tenantID, principal.UserID).Scan(&principal.APIKeyID); err != nil {
		return Principal{}, fmt.Errorf("hermesprincipal: 创建内部 Key 外键: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO hermes_service_principals (tenant_id, user_id, api_key_id)
VALUES ($1, $2, $3)`, principal.TenantID, principal.UserID, principal.APIKeyID); err != nil {
		return Principal{}, fmt.Errorf("hermesprincipal: 写入主体映射: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Principal{}, fmt.Errorf("hermesprincipal: 提交新映射: %w", err)
	}
	return principal, nil
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func load(ctx context.Context, q rowQuerier, tenantID int64) (Principal, error) {
	var principal Principal
	var principalKind, purpose, userStatus, keyStatus string
	var userDeleted, keyDeleted bool
	err := q.QueryRow(ctx, `
SELECT h.tenant_id, h.user_id, h.api_key_id,
       u.principal_kind, u.status, u.deleted_at IS NOT NULL,
       k.purpose, k.status, k.deleted_at IS NOT NULL
FROM hermes_service_principals h
JOIN tenants t
  ON t.id=h.tenant_id AND t.status='active' AND t.deleted_at IS NULL
JOIN users u
  ON u.tenant_id=h.tenant_id AND u.id=h.user_id
JOIN api_keys k
  ON k.tenant_id=h.tenant_id AND k.user_id=h.user_id AND k.id=h.api_key_id
WHERE h.tenant_id=$1`, tenantID).Scan(
		&principal.TenantID, &principal.UserID, &principal.APIKeyID,
		&principalKind, &userStatus, &userDeleted,
		&purpose, &keyStatus, &keyDeleted,
	)
	if err != nil {
		return Principal{}, err
	}
	if principal.TenantID <= 0 || principal.UserID <= 0 || principal.APIKeyID <= 0 ||
		principalKind != "service" || purpose != "hermes" ||
		userStatus != "active" || keyStatus != "active" || userDeleted || keyDeleted {
		return Principal{}, ErrCorrupt
	}
	return principal, nil
}
