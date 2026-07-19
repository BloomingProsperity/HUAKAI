package audit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RefundPendingRecord struct {
	ClaimID       int64
	RequestID     string
	DeltaMicroUSD int64
	Status        string
}

type RefundPendingStore interface {
	EnsurePending(context.Context, MismatchRefundPayload) (RefundPendingRecord, error)
	MarkCompleted(context.Context, int64, time.Time) error
	MarkFailed(context.Context, int64) error
}

type PGXRefundPendingStore struct {
	pool *pgxpool.Pool
}

func NewPGXRefundPendingStore(pool *pgxpool.Pool) (*PGXRefundPendingStore, error) {
	if pool == nil {
		return nil, errors.New("audit: pgx pool required")
	}
	return &PGXRefundPendingStore{pool: pool}, nil
}

func (s *PGXRefundPendingStore) EnsurePending(ctx context.Context, payload MismatchRefundPayload) (RefundPendingRecord, error) {
	if s == nil || s.pool == nil {
		return RefundPendingRecord{}, ErrReceiptStorageRequired
	}
	if err := validateRefundPayload(payload); err != nil {
		return RefundPendingRecord{}, err
	}
	var rec RefundPendingRecord
	err := s.pool.QueryRow(ctx, `
INSERT INTO audit_refund_pending (
    claim_id, request_id, delta_micro_usd, status, created_at, tenant_id
) VALUES (
    $1, $2, $3, 'pending', COALESCE($4::timestamptz, now()), $5
)
ON CONFLICT (claim_id) DO UPDATE SET
    status = 'pending',
    completed_at = NULL
WHERE audit_refund_pending.tenant_id = EXCLUDED.tenant_id
  AND audit_refund_pending.request_id = EXCLUDED.request_id
  AND audit_refund_pending.delta_micro_usd = EXCLUDED.delta_micro_usd
RETURNING claim_id, request_id, delta_micro_usd, status`,
		payload.ClaimID,
		payload.RequestID,
		payload.DeltaMicroUSD,
		nullablePayloadTime(payload.CreatedAt),
		payload.TenantID,
	).Scan(&rec.ClaimID, &rec.RequestID, &rec.DeltaMicroUSD, &rec.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RefundPendingRecord{}, fmt.Errorf("%w: conflicting refund pending identity", ErrReceiptInvalidDerivedData)
		}
		return RefundPendingRecord{}, fmt.Errorf("audit: ensure refund pending: %w", err)
	}
	return rec, nil
}

func (s *PGXRefundPendingStore) MarkCompleted(ctx context.Context, claimID int64, completedAt time.Time) error {
	if s == nil || s.pool == nil {
		return ErrReceiptStorageRequired
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	tag, err := s.pool.Exec(ctx, `
UPDATE audit_refund_pending
SET status = 'completed', completed_at = $2
WHERE claim_id = $1`, claimID, completedAt.UTC())
	if err != nil {
		return fmt.Errorf("audit: mark refund completed: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("audit: mark refund completed: pending record missing")
	}
	return nil
}

func (s *PGXRefundPendingStore) MarkFailed(ctx context.Context, claimID int64) error {
	if s == nil || s.pool == nil {
		return ErrReceiptStorageRequired
	}
	_, err := s.pool.Exec(ctx, `
UPDATE audit_refund_pending
SET status = 'failed', completed_at = NULL
WHERE claim_id = $1 AND status <> 'completed'`, claimID)
	if err != nil {
		return fmt.Errorf("audit: mark refund failed: %w", err)
	}
	return nil
}

func nullablePayloadTime(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	ts, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	return ts.UTC()
}
