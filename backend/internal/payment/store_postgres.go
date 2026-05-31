package payment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) OpenRecharge(ctx context.Context, input OpenInput) (Order, error) {
	if s == nil || s.pool == nil {
		return Order{}, ErrStoreNotConfigured
	}
	input = normalizeOpenInput(input)
	if err := validateOpenInput(input, true); err != nil {
		return Order{}, err
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		order, err := s.openRechargeOnce(ctx, input)
		if isSerializationFailure(err) {
			lastErr = err
			continue
		}
		return order, err
	}
	return Order{}, lastErr
}

func (s *PostgresStore) openRechargeOnce(ctx context.Context, input OpenInput) (Order, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Order{}, fmt.Errorf("payment: begin create order: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockUser(ctx, tx, input.TenantID, input.UserID); err != nil {
		return Order{}, err
	}
	if err := enforcePendingLimit(ctx, tx, input); err != nil {
		return Order{}, err
	}
	if err := enforceDailyAmountLimit(ctx, tx, input); err != nil {
		return Order{}, err
	}

	order, err := insertOrder(ctx, tx, input)
	if isUniqueViolation(err) {
		return Order{}, ErrExternalTradeConflict
	}
	if err != nil {
		return Order{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		if isUniqueViolation(err) {
			return Order{}, ErrExternalTradeConflict
		}
		return Order{}, fmt.Errorf("payment: commit create order: %w", err)
	}
	return order, nil
}

func lockUser(ctx context.Context, tx pgx.Tx, tenantID, userID int64) error {
	var id int64
	var userStatus, tenantStatus string
	var userDeletedAt, tenantDeletedAt *time.Time
	err := tx.QueryRow(ctx, `
SELECT u.id, u.status, u.deleted_at, t.status, t.deleted_at
FROM users u
JOIN tenants t ON t.id = u.tenant_id
WHERE u.tenant_id=$1 AND u.id=$2
FOR UPDATE OF u
FOR SHARE OF t`, tenantID, userID).Scan(&id, &userStatus, &userDeletedAt, &tenantStatus, &tenantDeletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return fmt.Errorf("payment: lock user: %w", err)
	}
	if tenantStatus != "active" || tenantDeletedAt != nil || userStatus != "active" || userDeletedAt != nil {
		return ErrAccountInactive
	}
	return nil
}

func enforcePendingLimit(ctx context.Context, tx pgx.Tx, input OpenInput) error {
	var pending int
	if err := tx.QueryRow(ctx, `
SELECT count(*)
FROM recharge_orders
WHERE tenant_id=$1 AND user_id=$2 AND status='PENDING'`, input.TenantID, input.UserID).Scan(&pending); err != nil {
		return fmt.Errorf("payment: count pending orders: %w", err)
	}
	if pending >= input.MaxPendingPerUser {
		return ErrPendingLimit
	}
	return nil
}

func enforceDailyAmountLimit(ctx context.Context, tx pgx.Tx, input OpenInput) error {
	if !input.DailyAmountLimit.IsPositive() {
		return nil
	}
	dayStart := time.Date(input.Now.Year(), input.Now.Month(), input.Now.Day(), 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.Add(24 * time.Hour)
	var current decimal.Decimal
	if err := tx.QueryRow(ctx, `
SELECT COALESCE(SUM(credited_amount), 0)
FROM recharge_orders
WHERE tenant_id=$1
  AND user_id=$2
  AND created_at >= $3
  AND created_at < $4
  AND status IN ('PENDING','PAID','CREDITING','COMPLETED')`,
		input.TenantID, input.UserID, dayStart, dayEnd).Scan(&current); err != nil {
		return fmt.Errorf("payment: sum daily orders: %w", err)
	}
	if current.Add(input.Amount).GreaterThan(input.DailyAmountLimit) {
		return ErrDailyAmountLimit
	}
	return nil
}

func insertOrder(ctx context.Context, tx pgx.Tx, input OpenInput) (Order, error) {
	row := tx.QueryRow(ctx, `
INSERT INTO recharge_orders (
	tenant_id, user_id, external_trade_no, recharge_ref, status,
	requested_amount, credited_amount, currency_code, created_at, updated_at
) VALUES (
	$1, $2, $3, $4, 'PENDING',
	$5, $5, $6, $7, $7
)
RETURNING id, tenant_id, user_id, external_trade_no, recharge_ref, status,
	credited_amount, currency_code, created_at, updated_at`,
		input.TenantID, input.UserID, input.ExternalTradeNo, rechargeRef(input.TenantID, input.UserID, input.ExternalTradeNo),
		input.Amount, input.CurrencyCode, input.Now)
	order, err := scanOrder(row)
	if err != nil {
		return Order{}, fmt.Errorf("payment: insert order: %w", err)
	}
	return order, nil
}

type orderScanner interface {
	Scan(dest ...any) error
}

func scanOrder(row orderScanner) (Order, error) {
	var order Order
	var status string
	if err := row.Scan(
		&order.ID, &order.TenantID, &order.UserID, &order.ExternalTradeNo, &order.RechargeRef,
		&status, &order.CreditedAmount, &order.CurrencyCode, &order.CreatedAt, &order.UpdatedAt,
	); err != nil {
		return Order{}, err
	}
	order.Status = Status(status)
	order.CreatedAt = order.CreatedAt.UTC()
	order.UpdatedAt = order.UpdatedAt.UTC()
	return order, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isSerializationFailure(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40001"
}

var _ Store = (*PostgresStore)(nil)
