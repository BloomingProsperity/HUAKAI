package tenancy

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

var ErrTenantInactive = errors.New("tenancy: tenant inactive")

// LockActiveForWrite 把“租户仍可产生新业务事实”锁定到当前事务结束。
// SHARE 会与租户生命周期的非键状态更新互斥；KEY SHARE 不具备这个性质。
// 调用方拿锁后可以继续写会话、预扣、续费或奖励，停用方会等待该事务收敛；
// 停用先提交时，本函数返回 ErrTenantInactive 且调用方不得产生副作用。
func LockActiveForWrite(ctx context.Context, database db.DBTX, tenantID int64) error {
	if database == nil || tenantID <= 0 {
		return ErrTenantInactive
	}
	var lockedID int64
	err := database.QueryRow(ctx, `
SELECT id
FROM tenants
WHERE id=$1 AND status='active' AND deleted_at IS NULL
FOR SHARE`, tenantID).Scan(&lockedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTenantInactive
	}
	if err != nil {
		return fmt.Errorf("tenancy: lock active tenant: %w", err)
	}
	return nil
}
