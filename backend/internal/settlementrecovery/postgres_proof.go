package settlementrecovery

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresCommittedProof 用 pgxpool 实现 CommittedProof 三证检查。
//
// 三证(全部成立才返 true):
//   1. billing_ledger_claims 同 tenant + claim_id 行 status='committed'
//   2. usage_records 同 tenant + claim_id 至少 1 行(Tx2 settle 同事务写入)
//   3. billing_events 同 tenant + claim_id event_type='claim_committed' 至少 1 行
//
// 缺任一证均返 false,worker 继续视失败重试(多次后转 quarantined)。
type PostgresCommittedProof struct {
	pool *pgxpool.Pool
}

// NewPostgresCommittedProof 构造 PG-backed proof。pgxpool 由 wiring 传入。
func NewPostgresCommittedProof(pool *pgxpool.Pool) *PostgresCommittedProof {
	return &PostgresCommittedProof{pool: pool}
}

// IsCommitted 验三证。
//   - (true, nil)  三证齐全,worker 标 delivered
//   - (false, nil) 任一证缺,worker 继续视失败
//   - (false, err) DB 查询失败,worker 把 err 包到 replay_failure_reason
func (p *PostgresCommittedProof) IsCommitted(ctx context.Context, tenantID, claimID int64) (bool, error) {
	if p == nil || p.pool == nil {
		return false, errors.New("settlementrecovery: postgres proof pool not configured")
	}

	// 证 1:claim status=committed
	var status string
	err := p.pool.QueryRow(ctx,
		`SELECT status FROM billing_ledger_claims WHERE tenant_id=$1 AND id=$2`,
		tenantID, claimID,
	).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		// claim 行都没了 — settle 一定没成功(reserve 阶段失败),不视 committed
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("settlementrecovery: query claim status: %w", err)
	}
	if status != "committed" {
		return false, nil
	}

	// 证 2:usage_records 行存在
	var usageCount int
	if err := p.pool.QueryRow(ctx,
		`SELECT count(*) FROM usage_records WHERE tenant_id=$1 AND claim_id=$2`,
		tenantID, claimID,
	).Scan(&usageCount); err != nil {
		return false, fmt.Errorf("settlementrecovery: query usage_records: %w", err)
	}
	if usageCount == 0 {
		return false, nil
	}

	// 证 3:billing_events 有 claim_committed 事件
	var eventCount int
	if err := p.pool.QueryRow(ctx,
		`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND claim_id=$2 AND event_type='claim_committed'`,
		tenantID, claimID,
	).Scan(&eventCount); err != nil {
		return false, fmt.Errorf("settlementrecovery: query billing_events: %w", err)
	}
	return eventCount > 0, nil
}
