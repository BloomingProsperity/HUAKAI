// HUAKAI · iKun

package paymenthttp

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

// PostgresRefundRequestRecorder persists user refund requests and admin decisions.
type PostgresRefundRequestRecorder struct {
	pool   *pgxpool.Pool
	refund refundRequestMoneyService
	now    func() time.Time
}

// NewPostgresRefundRequestRecorder constructs the production refund-request recorder.
func NewPostgresRefundRequestRecorder(pool *pgxpool.Pool, refund refundRequestMoneyService) *PostgresRefundRequestRecorder {
	return &PostgresRefundRequestRecorder{
		pool:   pool,
		refund: refund,
		now:    func() time.Time { return time.Now().UTC() },
	}
}

var _ RefundRequestRecorder = (*PostgresRefundRequestRecorder)(nil)

func (s *PostgresRefundRequestRecorder) CreateRefundRequest(ctx context.Context, in RefundRequestInput) (RefundRequest, error) {
	if s == nil || s.pool == nil {
		return RefundRequest{}, ErrRefundRequestUnavailable
	}
	if in.TenantID <= 0 || in.UserID <= 0 || in.OrderID <= 0 {
		return RefundRequest{}, ErrRefundRequestInvalidInput
	}
	now := in.Now
	if now.IsZero() {
		now = s.now()
	}
	row := s.pool.QueryRow(ctx, `
INSERT INTO payment_refund_requests (tenant_id, order_id, user_id, reason, status, created_at)
VALUES ($1, $2, $3, $4, 'pending', $5)
ON CONFLICT (tenant_id, order_id) DO UPDATE
SET order_id = payment_refund_requests.order_id
RETURNING id, tenant_id, order_id, user_id, COALESCE(reason, ''), status, created_at, decided_at, decided_by`,
		in.TenantID, in.OrderID, in.UserID, nullableRefundRequestText(in.Reason), now.UTC())
	req, err := scanRefundRequest(row)
	if err != nil {
		return RefundRequest{}, fmt.Errorf("paymenthttp: create refund request: %w", err)
	}
	return req, nil
}

func (s *PostgresRefundRequestRecorder) ListPendingRefundRequests(ctx context.Context, tenantID int64) ([]RefundRequest, error) {
	if s == nil || s.pool == nil {
		return nil, ErrRefundRequestUnavailable
	}
	if tenantID <= 0 {
		return nil, ErrRefundRequestInvalidInput
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, tenant_id, order_id, user_id, COALESCE(reason, ''), status, created_at, decided_at, decided_by
FROM payment_refund_requests
WHERE tenant_id=$1 AND status='pending'
ORDER BY created_at ASC, id ASC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("paymenthttp: list refund requests: %w", err)
	}
	defer rows.Close()
	var out []RefundRequest
	for rows.Next() {
		req, err := scanRefundRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("paymenthttp: scan refund request: %w", err)
		}
		out = append(out, req)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("paymenthttp: iterate refund requests: %w", err)
	}
	return out, nil
}

func (s *PostgresRefundRequestRecorder) ApproveRefundRequest(ctx context.Context, tenantID, requestID, adminActorID int64) (RefundRequest, error) {
	if s == nil || s.pool == nil || s.refund == nil {
		return RefundRequest{}, ErrRefundRequestUnavailable
	}
	if tenantID <= 0 || requestID <= 0 || adminActorID <= 0 {
		return RefundRequest{}, ErrRefundRequestInvalidInput
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return RefundRequest{}, fmt.Errorf("paymenthttp: begin approve refund request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	req, err := getRefundRequestForUpdateTx(ctx, tx, tenantID, requestID)
	if err != nil {
		return RefundRequest{}, err
	}
	if req.Status != RefundRequestPending {
		if err := tx.Commit(ctx); err != nil {
			return RefundRequest{}, fmt.Errorf("paymenthttp: commit approved replay: %w", err)
		}
		return req, nil
	}
	order, err := s.refund.GetOrder(ctx, tenantID, req.OrderID)
	if err != nil {
		return RefundRequest{}, err
	}
	if _, err := s.refund.RefundOrder(ctx, payment.RefundOrderInput{
		TenantID:       tenantID,
		OrderID:        req.OrderID,
		AmountCents:    order.AmountCents,
		IdempotencyKey: refundRequestIdempotencyKey(req.ID),
		Reason:         req.Reason,
		ActorKind:      payment.ActorKindAdmin,
		ActorID:        adminActorID,
	}); err != nil {
		return RefundRequest{}, err
	}
	decidedAt := s.now()
	req, err = updateRefundRequestDecisionTx(ctx, tx, tenantID, requestID, RefundRequestApproved, req.Reason, decidedAt, adminActorID)
	if err != nil {
		return RefundRequest{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RefundRequest{}, fmt.Errorf("paymenthttp: commit approve refund request: %w", err)
	}
	return req, nil
}

func (s *PostgresRefundRequestRecorder) RejectRefundRequest(ctx context.Context, tenantID, requestID int64, reason string, adminActorID int64) (RefundRequest, error) {
	if s == nil || s.pool == nil {
		return RefundRequest{}, ErrRefundRequestUnavailable
	}
	if tenantID <= 0 || requestID <= 0 || adminActorID <= 0 {
		return RefundRequest{}, ErrRefundRequestInvalidInput
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return RefundRequest{}, fmt.Errorf("paymenthttp: begin reject refund request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	req, err := getRefundRequestForUpdateTx(ctx, tx, tenantID, requestID)
	if err != nil {
		return RefundRequest{}, err
	}
	if req.Status != RefundRequestPending {
		if err := tx.Commit(ctx); err != nil {
			return RefundRequest{}, fmt.Errorf("paymenthttp: commit reject replay: %w", err)
		}
		return req, nil
	}
	// Closes the visible split-transaction approval window: if money already moved,
	// a still-pending request must not be relabeled rejected. Full approve atomicity
	// needs a future RefundOrder external-transaction refactor.
	alreadyRefunded, err := refundRequestHasRefundFactTx(ctx, tx, req)
	if err != nil {
		return RefundRequest{}, err
	}
	if alreadyRefunded {
		return RefundRequest{}, ErrRefundRequestAlreadyResolved
	}
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		req.Reason = trimmed
	}
	req, err = updateRefundRequestDecisionTx(ctx, tx, tenantID, requestID, RefundRequestRejected, req.Reason, s.now(), adminActorID)
	if err != nil {
		return RefundRequest{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RefundRequest{}, fmt.Errorf("paymenthttp: commit reject refund request: %w", err)
	}
	return req, nil
}

func refundRequestHasRefundFactTx(ctx context.Context, tx pgx.Tx, req RefundRequest) (bool, error) {
	key := refundRequestIdempotencyKey(req.ID)
	var exists bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
	SELECT 1
	FROM payment_refunds
	WHERE tenant_id=$1
	  AND (idempotency_key=$2 OR order_id=$3)
) OR EXISTS (
	SELECT 1
	FROM payment_orders
	WHERE tenant_id=$1
	  AND id=$3
	  AND status='refunded'
)`, req.TenantID, key, req.OrderID).Scan(&exists); err != nil {
		return false, fmt.Errorf("paymenthttp: check refund request refund fact: %w", err)
	}
	return exists, nil
}

func getRefundRequestForUpdateTx(ctx context.Context, tx pgx.Tx, tenantID, requestID int64) (RefundRequest, error) {
	req, err := scanRefundRequest(tx.QueryRow(ctx, `
SELECT id, tenant_id, order_id, user_id, COALESCE(reason, ''), status, created_at, decided_at, decided_by
FROM payment_refund_requests
WHERE tenant_id=$1 AND id=$2
FOR UPDATE`, tenantID, requestID))
	if err == pgx.ErrNoRows {
		return RefundRequest{}, ErrRefundRequestNotFound
	}
	if err != nil {
		return RefundRequest{}, fmt.Errorf("paymenthttp: get refund request: %w", err)
	}
	return req, nil
}

func updateRefundRequestDecisionTx(ctx context.Context, tx pgx.Tx, tenantID, requestID int64, status RefundRequestStatus, reason string, decidedAt time.Time, adminActorID int64) (RefundRequest, error) {
	req, err := scanRefundRequest(tx.QueryRow(ctx, `
UPDATE payment_refund_requests
SET status=$3, reason=$4, decided_at=$5, decided_by=$6
WHERE tenant_id=$1 AND id=$2
RETURNING id, tenant_id, order_id, user_id, COALESCE(reason, ''), status, created_at, decided_at, decided_by`,
		tenantID, requestID, status, nullableRefundRequestText(reason), decidedAt.UTC(), adminActorID))
	if err != nil {
		return RefundRequest{}, fmt.Errorf("paymenthttp: update refund request decision: %w", err)
	}
	return req, nil
}

func scanRefundRequest(row interface{ Scan(...any) error }) (RefundRequest, error) {
	var req RefundRequest
	var status string
	var decidedAt sql.NullTime
	var decidedBy sql.NullInt64
	if err := row.Scan(&req.ID, &req.TenantID, &req.OrderID, &req.UserID, &req.Reason, &status, &req.CreatedAt, &decidedAt, &decidedBy); err != nil {
		return RefundRequest{}, err
	}
	req.Status = RefundRequestStatus(status)
	req.CreatedAt = req.CreatedAt.UTC()
	if decidedAt.Valid {
		t := decidedAt.Time.UTC()
		req.DecidedAt = &t
	}
	if decidedBy.Valid {
		req.DecidedBy = decidedBy.Int64
	}
	return req, nil
}

func nullableRefundRequestText(s string) any {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil
	}
	return trimmed
}
