package payment

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

func (s *PostgresStore) FulfillCallback(ctx context.Context, cb VerifiedCallback) (CallbackResult, error) {
	if s == nil || s.pool == nil {
		return CallbackResult{HTTPStatus: 500}, ErrStoreNotConfigured
	}
	cb = normalizeVerifiedCallback(cb)
	if err := validateVerifiedCallback(cb); err != nil {
		return CallbackResult{HTTPStatus: 400}, err
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		result, err := s.fulfillCallbackOnce(ctx, cb)
		if isSerializationFailure(err) {
			lastErr = err
			continue
		}
		return result, err
	}
	return CallbackResult{HTTPStatus: 500}, lastErr
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

func (s *PostgresStore) fulfillCallbackOnce(ctx context.Context, cb VerifiedCallback) (CallbackResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return CallbackResult{HTTPStatus: 500}, fmt.Errorf("payment: begin callback fulfillment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	order, err := getOrderByExternalTradeForUpdate(ctx, tx, cb.TenantID, cb.ExternalTradeNo)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := insertMissingOrderPaymentAudit(ctx, tx, cb); err != nil {
			return CallbackResult{HTTPStatus: 500}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return CallbackResult{HTTPStatus: 500}, fmt.Errorf("payment: commit missing order audit: %w", err)
		}
		return CallbackResult{HTTPStatus: 200, AuditReason: AuditReasonOrderNotFound}, ErrOrderNotFound
	}
	if err != nil {
		return CallbackResult{HTTPStatus: 500}, err
	}
	result := CallbackResult{HTTPStatus: 200, OrderID: order.ID, UserID: order.UserID}
	if order.Provider != cb.Provider {
		if err := insertPaymentAudit(ctx, tx, order, cb, AuditOutcomeRejected, AuditReasonProviderMismatch); err != nil {
			return CallbackResult{HTTPStatus: 500}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return CallbackResult{HTTPStatus: 500}, fmt.Errorf("payment: commit provider mismatch audit: %w", err)
		}
		result.AuditReason = AuditReasonProviderMismatch
		return result, ErrPaymentProviderMismatch
	}
	if order.CurrencyCode != cb.CurrencyCode || !order.CreditedAmount.Equal(cb.PaidAmount) {
		if err := insertPaymentAudit(ctx, tx, order, cb, AuditOutcomeRejected, AuditReasonAmountMismatch); err != nil {
			return CallbackResult{HTTPStatus: 500}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return CallbackResult{HTTPStatus: 500}, fmt.Errorf("payment: commit amount mismatch audit: %w", err)
		}
		result.AuditReason = AuditReasonAmountMismatch
		return result, ErrPaymentAmountMismatch
	}

	tag, err := tx.Exec(ctx, `
UPDATE recharge_orders
SET status='PAID', paid_at=$3, updated_at=$3
WHERE tenant_id=$1 AND id=$2 AND status='PENDING'`,
		order.TenantID, order.ID, cb.Timestamp)
	if err != nil {
		return CallbackResult{HTTPStatus: 500}, fmt.Errorf("payment: mark callback paid: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if order.Status == StatusCompleted {
			if err := insertPaymentAudit(ctx, tx, order, cb, AuditOutcomeReplay, AuditReasonReplay); err != nil {
				return CallbackResult{HTTPStatus: 500}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return CallbackResult{HTTPStatus: 500}, fmt.Errorf("payment: commit replay audit: %w", err)
			}
			result.Idempotent = true
			result.Completed = true
			result.AuditReason = AuditReasonReplay
			return result, nil
		}
		if err := insertPaymentAudit(ctx, tx, order, cb, AuditOutcomeRejected, AuditReasonStateMismatch); err != nil {
			return CallbackResult{HTTPStatus: 500}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return CallbackResult{HTTPStatus: 500}, fmt.Errorf("payment: commit state mismatch audit: %w", err)
		}
		result.AuditReason = AuditReasonStateMismatch
		return result, ErrOrderStateConflict
	}

	balance, err := creditUserBalanceTx(ctx, tx, order.TenantID, order.UserID, order.CreditedAmount, cb.Timestamp)
	if err != nil {
		return CallbackResult{HTTPStatus: 500}, err
	}
	if err := insertBalanceRechargedEvent(ctx, tx, order); err != nil {
		return CallbackResult{HTTPStatus: 500}, err
	}
	if _, err := tx.Exec(ctx, `
UPDATE recharge_orders
SET status='COMPLETED', completed_at=$3, updated_at=$3
WHERE tenant_id=$1 AND id=$2 AND status='PAID'`,
		order.TenantID, order.ID, cb.Timestamp); err != nil {
		return CallbackResult{HTTPStatus: 500}, fmt.Errorf("payment: mark callback completed: %w", err)
	}
	if err := insertPaymentAudit(ctx, tx, order, cb, AuditOutcomeAccepted, AuditReasonCompleted); err != nil {
		return CallbackResult{HTTPStatus: 500}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CallbackResult{HTTPStatus: 500}, fmt.Errorf("payment: commit callback fulfillment: %w", err)
	}
	result.NewBalance = balance
	result.Completed = true
	result.AuditReason = AuditReasonCompleted
	return result, nil
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
	requested_amount, credited_amount, currency_code, provider, created_at, updated_at
) VALUES (
	$1, $2, $3, $4, 'PENDING',
	$5, $5, $6, $7, $8, $8
)
RETURNING id, tenant_id, user_id, external_trade_no, recharge_ref, status,
	credited_amount, currency_code, provider, created_at, updated_at`,
		input.TenantID, input.UserID, input.ExternalTradeNo, rechargeRef(input.TenantID, input.UserID, input.ExternalTradeNo),
		input.Amount, input.CurrencyCode, input.Provider, input.Now)
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
		&status, &order.CreditedAmount, &order.CurrencyCode, &order.Provider, &order.CreatedAt, &order.UpdatedAt,
	); err != nil {
		return Order{}, err
	}
	order.Status = Status(status)
	order.Provider = normalizeProvider(order.Provider)
	order.CreatedAt = order.CreatedAt.UTC()
	order.UpdatedAt = order.UpdatedAt.UTC()
	return order, nil
}

func normalizeVerifiedCallback(cb VerifiedCallback) VerifiedCallback {
	cb.Provider = normalizeProvider(cb.Provider)
	cb.ExternalTradeNo = strings.TrimSpace(cb.ExternalTradeNo)
	cb.ProviderEventID = strings.TrimSpace(cb.ProviderEventID)
	cb.CurrencyCode = strings.ToUpper(strings.TrimSpace(cb.CurrencyCode))
	if cb.Timestamp.IsZero() {
		cb.Timestamp = time.Now().UTC()
	} else {
		cb.Timestamp = cb.Timestamp.UTC()
	}
	return cb
}

func validateVerifiedCallback(cb VerifiedCallback) error {
	if cb.TenantID <= 0 || cb.Provider == "" || cb.ExternalTradeNo == "" || cb.ProviderEventID == "" ||
		cb.CurrencyCode != "USD" || !cb.PaidAmount.IsPositive() || !fitsMoneyColumn(cb.PaidAmount) {
		return ErrInvalidInput
	}
	return nil
}

func getOrderByExternalTradeForUpdate(ctx context.Context, tx pgx.Tx, tenantID int64, externalTradeNo string) (Order, error) {
	return scanOrder(tx.QueryRow(ctx, `
SELECT id, tenant_id, user_id, external_trade_no, recharge_ref, status,
	credited_amount, currency_code, provider, created_at, updated_at
FROM recharge_orders
WHERE tenant_id=$1 AND external_trade_no=$2
FOR UPDATE`, tenantID, externalTradeNo))
}

func insertBalanceRechargedEvent(ctx context.Context, tx pgx.Tx, order Order) error {
	if _, err := tx.Exec(ctx, `
INSERT INTO billing_events (
	tenant_id, event_type, actual_cost, actual_cost_signed,
	stream_state, delivered_token_count, fingerprint, recharge_order_id
) VALUES ($1, 'balance_recharged', $2, $2, 2, 0, $3, $4)`,
		order.TenantID, order.CreditedAmount, order.RechargeRef, order.ID,
	); err != nil {
		return fmt.Errorf("payment: insert balance recharge billing event: %w", err)
	}
	return nil
}

func insertPaymentAudit(ctx context.Context, tx pgx.Tx, order Order, cb VerifiedCallback, outcome, reason string) error {
	if _, err := tx.Exec(ctx, `
INSERT INTO payment_audit_log (
	tenant_id, recharge_order_id, user_id, provider, external_trade_no,
	provider_event_id, outcome, reason, paid_amount, expected_amount,
	currency_code, metadata, created_at
) VALUES (
	$1, $2, $3, $4, $5,
	$6, $7, $8, $9, $10,
	$11, '{}'::jsonb, $12
)`,
		order.TenantID, order.ID, order.UserID, cb.Provider, cb.ExternalTradeNo,
		cb.ProviderEventID, outcome, reason, cb.PaidAmount, order.CreditedAmount,
		cb.CurrencyCode, cb.Timestamp,
	); err != nil {
		return fmt.Errorf("payment: insert payment audit: %w", err)
	}
	return nil
}

func insertMissingOrderPaymentAudit(ctx context.Context, tx pgx.Tx, cb VerifiedCallback) error {
	if _, err := tx.Exec(ctx, `
INSERT INTO payment_audit_log (
	tenant_id, provider, external_trade_no, provider_event_id,
	outcome, reason, paid_amount, currency_code, metadata, created_at
) VALUES (
	$1, $2, $3, $4,
	$5, $6, $7, $8, '{}'::jsonb, $9
)`,
		cb.TenantID, cb.Provider, cb.ExternalTradeNo, cb.ProviderEventID,
		AuditOutcomeRejected, AuditReasonOrderNotFound, cb.PaidAmount, cb.CurrencyCode, cb.Timestamp,
	); err != nil {
		return fmt.Errorf("payment: insert missing order audit: %w", err)
	}
	return nil
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
