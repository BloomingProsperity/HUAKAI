package payment

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) ExpireStalePendingOrders(ctx context.Context, now time.Time, limit int) (int, error) {
	if s == nil || s.pool == nil {
		return 0, ErrStoreNotConfigured
	}
	if limit <= 0 {
		return 0, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("payment: begin expire stale pending orders: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
UPDATE payment_orders
SET status='expired', updated_at=$1
WHERE id IN (
	SELECT id
	FROM payment_orders
	WHERE status='pending'
	  AND expires_at IS NOT NULL
	  AND expires_at < $1
	ORDER BY id
	LIMIT $2
	FOR UPDATE SKIP LOCKED
)
RETURNING tenant_id, id`, now, limit)
	if err != nil {
		return 0, fmt.Errorf("payment: expire stale pending orders: %w", err)
	}

	expired := make([]auditInsert, 0, limit)
	for rows.Next() {
		var ev auditInsert
		if err := rows.Scan(&ev.TenantID, &ev.OrderID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("payment: scan expired order: %w", err)
		}
		ev.EventType = AuditOrderExpired
		ev.ActorKind = ActorKindSystem
		ev.Now = now
		expired = append(expired, ev)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("payment: iterate expired orders: %w", err)
	}

	for _, ev := range expired {
		if err := insertAuditTx(ctx, tx, ev); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("payment: commit expire stale pending orders: %w", err)
	}
	return len(expired), nil
}
