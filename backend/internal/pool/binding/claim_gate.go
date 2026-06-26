// Phase C.2 生产适配器:基于 DB 的 pool.ClaimGate。
//
// 把 selector 的 claim 回写接缝桥接到 sqlc 生成的 WriteAcquisitionToken 查询
//(Pattern B,依据 F-POOL-001 §6 + F-OBS-001 §Tx1)。
// 设计上按租户作用域 —— Phase B.5 P1 对 Settler.Abort 的修复让我们认识到:
// 任何只以 claim_id 为键的 UPDATE 都是多租户的自伤地雷。

package binding

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// DBClaimGate 把 (provider_account_id, acquisition_token) 这一对写到由
// (id, tenant_id) 标识的、处于 reserving 状态的 claim 行上。若 WHERE 子句匹配到
// 零行,则返回 ErrClaimRace —— 意味着该 claim 已不在 'reserving' 状态、已被并发
// selector 写过,或租户作用域拒绝了此次写入。
type DBClaimGate struct {
	q *dbbilling.Queries
}

// NewDBClaimGate 从一个 sqlc.Queries 句柄构造该适配器。
func NewDBClaimGate(q *dbbilling.Queries) *DBClaimGate {
	return &DBClaimGate{q: q}
}

// WriteAcquisition 实现 pool.ClaimGate。
func (g *DBClaimGate) WriteAcquisition(ctx context.Context, tenantID, claimID, accountID int64, token uuid.UUID) error {
	if g == nil || g.q == nil {
		return errors.New("pool: DBClaimGate not configured")
	}
	rows, err := g.q.WriteAcquisitionToken(ctx, dbbilling.WriteAcquisitionTokenParams{
		ID:                claimID,
		ProviderAccountID: &accountID,
		AcquisitionToken:  token,
		TenantID:          tenantID,
	})
	if err != nil {
		return fmt.Errorf("pool: write acquisition token: %w", err)
	}
	if rows == 0 {
		return ErrClaimRace
	}
	return nil
}

var _ ClaimGate = (*DBClaimGate)(nil)
