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
	if err := lockPaymentRefundKey(ctx, tx, rec.TenantID, key); err != nil {
		return RefundResult{}, err
	}
	if stored, err := getPaymentRefundByKeyTx(ctx, tx, rec.TenantID, key); err == nil {
		existing := stored.RefundRecord
		if !paymentRefundMatchesRequest(existing, rec) {
			return RefundResult{}, ErrRefundIdempotencyConflict
		}
		if err := validateStoredPaymentRefund(stored); err != nil {
			return RefundResult{}, err
		}
		order, err := getOrderForUpdateTx(ctx, tx, rec.TenantID, existing.OrderID)
		if err != nil {
			return RefundResult{}, err
		}
		if order.TenantID != existing.TenantID || order.ID != existing.OrderID ||
			order.UserID != existing.UserID ||
			strings.TrimSpace(order.CurrencyCode) != strings.TrimSpace(existing.CurrencyCode) {
			return RefundResult{}, ErrRefundFactInvalid
		}
		_, total, status, remaining, err := loadPaymentRefundProgressTx(ctx, tx, order)
		if err != nil {
			return RefundResult{}, err
		}
		if order.Status != status || (existing.RequireExact && total < existing.RequestedAmountCents) {
			return RefundResult{}, ErrRefundFactInvalid
		}
		balance, err := userBalanceTx(ctx, tx, rec.TenantID, existing.UserID)
		if err != nil {
			return RefundResult{}, err
		}
		return RefundResult{
			Order: order, Refund: existing, BalanceCents: balance,
			CumulativeRefundedCents: total, RemainingRefundableCents: remaining, Idempotent: true,
		}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return RefundResult{}, err
	}
	order, err := getOrderForUpdateTx(ctx, tx, rec.TenantID, rec.OrderID)
	if err != nil {
		return RefundResult{}, err
	}

	credit, totalBefore, statusBefore, remainingBefore, err := loadPaymentRefundProgressTx(ctx, tx, order)
	if err != nil {
		return RefundResult{}, err
	}
	if order.Status != statusBefore {
		return RefundResult{}, ErrRefundFactInvalid
	}
	amount, alreadySatisfied, err := paymentRefundAmount(credit.AmountCents, totalBefore, rec.AmountCents, rec.RequireExact)
	if err != nil {
		return RefundResult{}, err
	}
	if alreadySatisfied {
		balance, balanceErr := userBalanceTx(ctx, tx, rec.TenantID, order.UserID)
		if balanceErr != nil {
			return RefundResult{}, balanceErr
		}
		return RefundResult{
			Order: order, BalanceCents: balance,
			CumulativeRefundedCents: totalBefore, RemainingRefundableCents: remainingBefore,
			Idempotent: true, AlreadySatisfied: true,
		}, nil
	}
	if err := debitAvailableRefundBalanceTx(ctx, tx, order.TenantID, order.UserID, amount, rec.Now); err != nil {
		return RefundResult{}, err
	}
	refund, err := insertPaymentRefundTx(ctx, tx, order, rec, key, amount)
	if err != nil {
		if isUniqueViolation(err) {
			return RefundResult{}, ErrRefundIdempotencyConflict
		}
		return RefundResult{}, err
	}
	billingID, err := insertPaymentRefundBillingEventTx(ctx, tx, refund)
	if err != nil {
		return RefundResult{}, err
	}
	refund.BillingEventID = billingID
	totalAfter := totalBefore + amount
	statusAfter, remainingAfter, err := paymentRefundProgress(credit.AmountCents, totalAfter)
	if err != nil {
		return RefundResult{}, err
	}
	row := tx.QueryRow(ctx, `
UPDATE payment_orders SET status=$3, updated_at=$4
WHERE tenant_id=$1 AND id=$2
RETURNING`+orderSelectColumns, rec.TenantID, rec.OrderID, statusAfter, rec.Now)
	order, err = scanOrder(row)
	if err != nil {
		return RefundResult{}, fmt.Errorf("payment: update cumulative refund status: %w", err)
	}
	if err := insertAuditTx(ctx, tx, auditInsert{
		TenantID:    rec.TenantID,
		OrderID:     order.ID,
		EventType:   AuditOrderRefunded,
		ActorKind:   actorKindOrDefault(rec.ActorKind),
		ActorID:     rec.ActorID,
		ReasonClass: rec.Reason,
		RequestID:   rec.RequestID,
		Payload: map[string]any{
			"amount_cents": refund.AmountCents, "requested_amount_cents": refund.RequestedAmountCents,
			"require_exact": refund.RequireExact, "cumulative_refunded_cents": totalAfter,
			"remaining_refundable_cents": remainingAfter, "refund_id": refund.ID, "billing_event_id": billingID,
		},
		Now: rec.Now,
	}); err != nil {
		return RefundResult{}, err
	}
	balance, err := userBalanceTx(ctx, tx, rec.TenantID, order.UserID)
	if err != nil {
		return RefundResult{}, err
	}
	return RefundResult{
		Order: order, Refund: refund, BalanceCents: balance,
		CumulativeRefundedCents: totalAfter, RemainingRefundableCents: remainingAfter,
	}, nil
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

func loadPaymentRefundProgressTx(ctx context.Context, tx pgx.Tx, order Order) (CreditRecord, int64, OrderStatus, int64, error) {
	if order.OrderKind != OrderKindTopup {
		return CreditRecord{}, 0, "", 0, ErrRefundUnsupportedKind
	}
	if order.Status != StatusCompleted && order.Status != StatusRefunded {
		return CreditRecord{}, 0, "", 0, ErrOrderNotRefundable
	}
	credit, err := getCreditByOrderTx(ctx, tx, order.TenantID, order.ID)
	if errors.Is(err, ErrOrderNotFound) {
		return CreditRecord{}, 0, "", 0, ErrOrderNotRefundable
	}
	if err != nil {
		return CreditRecord{}, 0, "", 0, err
	}
	var refunded int64
	if err := tx.QueryRow(ctx, `
SELECT COALESCE(SUM(amount_cents), 0)::bigint
FROM payment_refunds
WHERE tenant_id=$1 AND order_id=$2`, order.TenantID, order.ID).Scan(&refunded); err != nil {
		return CreditRecord{}, 0, "", 0, fmt.Errorf("payment: read cumulative refund: %w", err)
	}
	status, remaining, err := paymentRefundProgress(credit.AmountCents, refunded)
	if err != nil {
		return CreditRecord{}, 0, "", 0, err
	}
	return credit, refunded, status, remaining, nil
}

type storedPaymentRefund struct {
	RefundRecord
	BillingEventCount  int64
	BillingEventAmount sql.NullString
}

func getPaymentRefundByKeyTx(ctx context.Context, tx pgx.Tx, tenantID int64, key string) (storedPaymentRefund, error) {
	var r storedPaymentRefund
	var actorID, billingID sql.NullInt64
	err := tx.QueryRow(ctx, `
SELECT pr.id, pr.tenant_id, pr.order_id, pr.user_id, pr.amount_cents,
		       pr.requested_amount_cents, pr.require_exact, pr.currency,
		       pr.idempotency_key, COALESCE(pr.reason, ''), pr.actor_kind, pr.actor_id,
		       COALESCE(pr.actor_ref, ''),
		       pr.created_at, be.id, be.event_count, be.actual_cost_signed
	FROM payment_refunds pr
	LEFT JOIN LATERAL (
	    SELECT MIN(event.id) AS id,
	           COUNT(*)::bigint AS event_count,
	           MIN(event.actual_cost_signed)::text AS actual_cost_signed
	    FROM billing_events AS event
	    WHERE event.tenant_id = pr.tenant_id
	      AND event.payment_refund_id = pr.id
	      AND event.event_type = 'payment_refunded'
	) AS be ON TRUE
	WHERE pr.tenant_id=$1 AND pr.idempotency_key=$2
	`, tenantID, key).Scan(
		&r.ID, &r.TenantID, &r.OrderID, &r.UserID, &r.AmountCents,
		&r.RequestedAmountCents, &r.RequireExact, &r.CurrencyCode,
		&r.IdempotencyKey, &r.Reason, &r.ActorKind, &actorID, &r.ActorRef, &r.CreatedAt,
		&billingID, &r.BillingEventCount, &r.BillingEventAmount,
	)
	if err != nil {
		return storedPaymentRefund{}, err
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

func validateStoredPaymentRefund(stored storedPaymentRefund) error {
	refund := stored.RefundRecord
	if refund.ID <= 0 || refund.TenantID <= 0 || refund.OrderID <= 0 || refund.UserID <= 0 ||
		refund.AmountCents <= 0 || refund.RequestedAmountCents <= 0 ||
		(!refund.RequireExact && refund.RequestedAmountCents != refund.AmountCents) ||
		(refund.RequireExact && refund.RequestedAmountCents < refund.AmountCents) ||
		strings.TrimSpace(refund.CurrencyCode) == "" ||
		stored.BillingEventCount != 1 || refund.BillingEventID <= 0 || !stored.BillingEventAmount.Valid {
		return ErrRefundFactInvalid
	}
	signedAmount, err := decimal.NewFromString(stored.BillingEventAmount.String)
	if err != nil || !signedAmount.Equal(decimalFromCents(refund.AmountCents).Neg()) {
		return ErrRefundFactInvalid
	}
	return nil
}

func lockPaymentRefundKey(ctx context.Context, tx pgx.Tx, tenantID int64, key string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`,
		fmt.Sprintf("payment-refund:%d", tenantID), key); err != nil {
		return fmt.Errorf("payment: lock refund idempotency key: %w", err)
	}
	return nil
}

func paymentRefundMatchesRequest(existing RefundRecord, rec refundRecord) bool {
	return existing.OrderID == rec.OrderID &&
		existing.RequestedAmountCents == rec.AmountCents &&
		existing.RequireExact == rec.RequireExact &&
		strings.TrimSpace(existing.Reason) == strings.TrimSpace(rec.Reason) &&
		existing.ActorKind == actorKindOrDefault(rec.ActorKind) &&
		existing.ActorID == rec.ActorID &&
		strings.TrimSpace(existing.ActorRef) == strings.TrimSpace(rec.ActorRef)
}

func insertPaymentRefundTx(ctx context.Context, tx pgx.Tx, order Order, rec refundRecord, key string, amount int64) (RefundRecord, error) {
	refund := RefundRecord{
		TenantID:             rec.TenantID,
		OrderID:              order.ID,
		UserID:               order.UserID,
		AmountCents:          amount,
		RequestedAmountCents: rec.AmountCents,
		RequireExact:         rec.RequireExact,
		CurrencyCode:         order.CurrencyCode,
		IdempotencyKey:       key,
		Reason:               rec.Reason,
		ActorKind:            actorKindOrDefault(rec.ActorKind),
		ActorID:              rec.ActorID,
		ActorRef:             rec.ActorRef,
	}
	if err := tx.QueryRow(ctx, `
INSERT INTO payment_refunds (
	tenant_id, order_id, user_id, amount_cents, requested_amount_cents, require_exact,
	currency, idempotency_key, reason, actor_kind, actor_id, actor_ref, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING id, created_at`,
		refund.TenantID, refund.OrderID, refund.UserID, refund.AmountCents,
		refund.RequestedAmountCents, refund.RequireExact, refund.CurrencyCode,
		refund.IdempotencyKey, nullableText(refund.Reason), refund.ActorKind,
		nullableInt64(refund.ActorID), nullableText(refund.ActorRef), rec.Now,
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
