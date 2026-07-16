package adminsessionauth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// PostgresIdentityStore 从 users 与 tenants 的可信状态构造 session admin 身份。
type PostgresIdentityStore struct {
	queries *admindb.Queries
}

// NewPostgresIdentityStore 构造 session admin 专用身份仓储。
func NewPostgresIdentityStore(database db.DBTX) *PostgresIdentityStore {
	if database == nil {
		return &PostgresIdentityStore{}
	}
	return &PostgresIdentityStore{queries: admindb.New(database)}
}

// ResolveActiveAdminIdentity 精确校验 tenant/user/active/admin 后构造根或子树身份。
func (s *PostgresIdentityStore) ResolveActiveAdminIdentity(ctx context.Context, tenantID, userID int64) (admin.AdminIdentity, error) {
	if s == nil || s.queries == nil || tenantID <= 0 || userID <= 0 {
		return admin.AdminIdentity{}, admin.ErrAdminUnauthorized
	}
	row, err := s.queries.ResolveActiveSessionAdmin(ctx, admindb.ResolveActiveSessionAdminParams{
		UserID: userID, TenantID: tenantID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return admin.AdminIdentity{}, admin.ErrAdminUnauthorized
	}
	if err != nil {
		return admin.AdminIdentity{}, fmt.Errorf("%w: resolve session admin: %v", admin.ErrAdminBackend, err)
	}
	claims := admin.IdentityClaims{
		UserID: row.UserID, Source: admin.AdminSourceSession,
		Role: admin.RolePlatformAdmin,
	}
	if row.ParentTenantID != nil {
		claims.Role = admin.RoleTenantOperator
		claims.ScopeTenantID = row.TenantID
	}
	return admin.NewAdminIdentity(ctx, claims, s.loadTenantScope)
}

func (s *PostgresIdentityStore) loadTenantScope(ctx context.Context, rootTenantID int64) ([]admin.TenantScopeNode, error) {
	rows, err := s.queries.ListActiveTenantScope(ctx, rootTenantID)
	if err != nil {
		return nil, err
	}
	nodes := make([]admin.TenantScopeNode, 0, len(rows))
	for _, row := range rows {
		nodes = append(nodes, admin.TenantScopeNode{
			TenantID: row.ID, Depth: row.Depth, CycleDetected: row.CycleDetected,
			ScopeRootIsChild: row.ScopeRootIsChild,
		})
	}
	return nodes, nil
}
