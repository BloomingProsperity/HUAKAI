// HUAKAI · iKun

package payment

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// RefundOrder 在一个事务内记录订单退款、负向 billing event、订单状态和审计。
func (s *PostgresStore) RefundOrder(ctx context.Context, rec refundRecord) (RefundResult, error) {
	if s == nil || s.pool == nil {
		return RefundResult{}, ErrStoreNotConfigured
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return RefundResult{}, fmt.Errorf("payment: begin refund: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	res, err := s.refundOrderTx(ctx, tx, rec)
	if err != nil {
		return RefundResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RefundResult{}, fmt.Errorf("payment: commit refund: %w", err)
	}
	return res, nil
}

func (s *PostgresStore) refundOrderTx(ctx context.Context, tx pgx.Tx, rec refundRecord) (RefundResult, error) {
	key := strings.TrimSpace(rec.IdempotencyKey)
	if key == "" {
		return RefundResult{}, ErrInvalidInput
	}
	order, err := getOrderForUpdateTx(ctx, tx, rec.TenantID, rec.OrderID)
	if err != nil {
		return RefundResult{}, err
	}
	if existing, err := getPaymentRefundByKeyTx(ctx, tx, rec.TenantID, key); err == nil {
		if existing.OrderID != rec.OrderID {
			return RefundResult{}, ErrIdempotencyConflict
		}
		balance, err := userBalanceTx(ctx, tx, rec.TenantID, existing.UserID)
		if err != nil {
			return RefundResult{}, err
		}
		return RefundResult{Order: order, Refund: existing, BalanceCents: balance, Idempotent: true}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return RefundResult{}, err
	}

	credit, err := validateRefundableTopupOrderTx(ctx, tx, order)
	if err != nil {
		return RefundResult{}, err
	}
	if rec.AmountCents <= 0 || rec.AmountCents > credit.AmountCents {
		return RefundResult{}, ErrInvalidAmount
	}
	if err := debitAvailableRefundBalanceTx(ctx, tx, order.TenantID, order.UserID, rec.AmountCents, rec.Now); err != nil {
		return RefundResult{}, err
	}
	refund, err := insertPaymentRefundTx(ctx, tx, order, rec, key)
	if err != nil {
		if isUniqueViolation(err) {
			return RefundResult{}, ErrIdempotencyConflict
		}
		return RefundResult{}, err
	}
	billingID, err := insertPaymentRefundBillingEventTx(ctx, tx, refund)
	if err != nil {
		return RefundResult{}, err
	}
	refund.BillingEventID = billingID
	row := tx.QueryRow(ctx, `
UPDATE payment_orders SET status='refunded', updated_at=$3
WHERE tenant_id=$1 AND id=$2
RETURNING`+orderSelectColumns, rec.TenantID, rec.OrderID, rec.Now)
	order, err = scanOrder(row)
	if err != nil {
		return RefundResult{}, fmt.Errorf("payment: mark order refunded: %w", err)
	}
	if err := insertAuditTx(ctx, tx, auditInsert{
		TenantID:    rec.TenantID,
		OrderID:     order.ID,
		EventType:   AuditOrderRefunded,
		ActorKind:   actorKindOrDefault(rec.ActorKind),
		ActorID:     rec.ActorID,
		ReasonClass: rec.Reason,
		RequestID:   rec.RequestID,
		Payload:     map[string]any{"amount_cents": refund.AmountCents, "refund_id": refund.ID, "billing_event_id": billingID},
		Now:         rec.Now,
	}); err != nil {
		return RefundResult{}, err
	}
	balance, err := userBalanceTx(ctx, tx, rec.TenantID, order.UserID)
	if err != nil {
		return RefundResult{}, err
	}
	return RefundResult{Order: order, Refund: refund, BalanceCents: balance}, nil
}

func debitAvailableRefundBalanceTx(ctx context.Context, tx pgx.Tx, tenantID, userID, amountCents int64, now time.Time) error {
	if amountCents <= 0 {
		return ErrInvalidAmount
	}
	amount := decimalFromCents(amountCents)
	tag, err := tx.Exec(ctx, `
UPDATE user_balances
SET balance = balance - $3,
    version = version + 1,
    updated_at = $4
WHERE tenant_id=$1
  AND user_id=$2
  AND balance - held >= $3`, tenantID, userID, amount, now)
	if err != nil {
		return fmt.Errorf("payment: debit refund balance: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRefundExceedsAvailable
	}
	return nil
}

func validateRefundableTopupOrderTx(ctx context.Context, tx pgx.Tx, order Order) (CreditRecord, error) {
	if order.OrderKind != OrderKindTopup {
		return CreditRecord{}, ErrRefundUnsupportedKind
	}
	if order.Status != StatusCompleted {
		return CreditRecord{}, ErrOrderNotRefundable
	}
	credit, err := getCreditByOrderTx(ctx, tx, order.TenantID, order.ID)
	if errors.Is(err, ErrOrderNotFound) {
		return CreditRecord{}, ErrOrderNotRefundable
	}
	return credit, err
}

func getPaymentRefundByKeyTx(ctx context.Context, tx pgx.Tx, tenantID int64, key string) (RefundRecord, error) {
	var r RefundRecord
	var actorID, billingID sql.NullInt64
	err := tx.QueryRow(ctx, `
SELECT pr.id, pr.tenant_id, pr.order_id, pr.user_id, pr.amount_cents, pr.currency,
       pr.idempotency_key, COALESCE(pr.reason, ''), pr.actor_kind, pr.actor_id,
       pr.created_at, be.id
FROM payment_refunds pr
LEFT JOIN billing_events be
  ON be.tenant_id = pr.tenant_id
 AND be.payment_refund_id = pr.id
 AND be.event_type = 'payment_refunded'
WHERE pr.tenant_id=$1 AND pr.idempotency_key=$2
ORDER BY pr.id ASC
LIMIT 1`, tenantID, key).Scan(
		&r.ID, &r.TenantID, &r.OrderID, &r.UserID, &r.AmountCents, &r.CurrencyCode,
		&r.IdempotencyKey, &r.Reason, &r.ActorKind, &actorID, &r.CreatedAt, &billingID,
	)
	if err != nil {
		return RefundRecord{}, err
	}
	if actorID.Valid {
		r.ActorID = actorID.Int64
	}
	if billingID.Valid {
		r.BillingEventID = billingID.Int64
	}
	r.CurrencyCode = strings.TrimSpace(r.CurrencyCode)
	r.CreatedAt = r.CreatedAt.UTC()
	return r, nil
}

func insertPaymentRefundTx(ctx context.Context, tx pgx.Tx, order Order, rec refundRecord, key string) (RefundRecord, error) {
	refund := RefundRecord{
		TenantID:       rec.TenantID,
		OrderID:        order.ID,
		UserID:         order.UserID,
		AmountCents:    rec.AmountCents,
		CurrencyCode:   order.CurrencyCode,
		IdempotencyKey: key,
		Reason:         rec.Reason,
		ActorKind:      actorKindOrDefault(rec.ActorKind),
		ActorID:        rec.ActorID,
		ActorRef:       rec.ActorRef,
	}
	if err := tx.QueryRow(ctx, `
INSERT INTO payment_refunds (
	tenant_id, order_id, user_id, amount_cents, currency, idempotency_key,
	reason, actor_kind, actor_id, actor_ref, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING id, created_at`,
		refund.TenantID, refund.OrderID, refund.UserID, refund.AmountCents, refund.CurrencyCode,
		refund.IdempotencyKey, nullableText(refund.Reason), refund.ActorKind, nullableInt64(refund.ActorID), nullableText(refund.ActorRef), rec.Now,
	).Scan(&refund.ID, &refund.CreatedAt); err != nil {
		return RefundRecord{}, err
	}
	refund.CreatedAt = refund.CreatedAt.UTC()
	return refund, nil
}

func insertPaymentRefundBillingEventTx(ctx context.Context, tx pgx.Tx, refund RefundRecord) (int64, error) {
	signed := decimalFromCents(refund.AmountCents).Neg()
	fingerprint := fmt.Sprintf("payment-refund:t%d:o%d:r%d", refund.TenantID, refund.OrderID, refund.ID)
	var billingID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO billing_events (tenant_id, event_type, actual_cost, actual_cost_signed,
	stream_state, delivered_token_count, fingerprint, payment_refund_id)
VALUES ($1, 'payment_refunded', $2, $3, 2, 0, $4, $5)
RETURNING id`, refund.TenantID, decimal.Zero, signed, fingerprint, refund.ID).Scan(&billingID); err != nil {
		return 0, fmt.Errorf("payment: insert refund billing event: %w", err)
	}
	return billingID, nil
}
