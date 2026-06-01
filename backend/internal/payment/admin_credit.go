package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

const (
	adminPaymentProvider      = "manual_admin"
	adminAuditRechargeSuccess = "RECHARGE_SUCCESS"
	adminAuditAdjustment      = "MANUAL_BALANCE_ADJUSTMENT"
)

type AdminBalanceAdjustmentInput struct {
	TenantID        int64
	UserID          int64
	Amount          decimal.Decimal
	CurrencyCode    string
	ActorID         string
	Reason          string
	RequestID       string
	ExternalTradeNo string
	Now             time.Time
}

type AdminBalanceAdjustmentResult struct {
	TenantID        int64
	UserID          int64
	NewBalance      decimal.Decimal
	CurrencyCode    string
	RechargeOrderID int64
}

type AdminBalanceStore interface {
	ApplyAdminBalanceAdjustment(context.Context, AdminBalanceAdjustmentInput) (AdminBalanceAdjustmentResult, error)
}

func (s *Service) AdminAdjustBalance(ctx context.Context, input AdminBalanceAdjustmentInput) (AdminBalanceAdjustmentResult, error) {
	if s == nil || s.store == nil {
		return AdminBalanceAdjustmentResult{}, ErrStoreNotConfigured
	}
	input = normalizeAdminBalanceAdjustmentInput(input)
	store, ok := s.store.(AdminBalanceStore)
	if !ok || store == nil {
		return AdminBalanceAdjustmentResult{}, ErrStoreNotConfigured
	}
	if err := validateAdminBalanceAdjustmentInput(input); err != nil {
		return AdminBalanceAdjustmentResult{}, err
	}
	return store.ApplyAdminBalanceAdjustment(ctx, input)
}

func (s *PostgresStore) ApplyAdminBalanceAdjustment(ctx context.Context, input AdminBalanceAdjustmentInput) (AdminBalanceAdjustmentResult, error) {
	if s == nil || s.pool == nil {
		return AdminBalanceAdjustmentResult{}, ErrStoreNotConfigured
	}
	input = normalizeAdminBalanceAdjustmentInput(input)
	if err := validateAdminBalanceAdjustmentInput(input); err != nil {
		return AdminBalanceAdjustmentResult{}, err
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		result, err := s.applyAdminBalanceAdjustmentOnce(ctx, input)
		if isSerializationFailure(err) {
			lastErr = err
			continue
		}
		return result, err
	}
	return AdminBalanceAdjustmentResult{}, lastErr
}

func (s *PostgresStore) applyAdminBalanceAdjustmentOnce(ctx context.Context, input AdminBalanceAdjustmentInput) (AdminBalanceAdjustmentResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return AdminBalanceAdjustmentResult{}, fmt.Errorf("payment: begin admin balance adjustment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockAdminAdjustmentKey(ctx, tx, input); err != nil {
		return AdminBalanceAdjustmentResult{}, err
	}
	if result, ok, err := getExistingAdminBalanceAdjustment(ctx, tx, input); ok || err != nil {
		if err != nil {
			return AdminBalanceAdjustmentResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return AdminBalanceAdjustmentResult{}, fmt.Errorf("payment: commit admin balance adjustment replay: %w", err)
		}
		return result, nil
	}

	if err := lockUser(ctx, tx, input.TenantID, input.UserID); err != nil {
		return AdminBalanceAdjustmentResult{}, err
	}

	var rechargeOrderID int64
	if input.Amount.IsPositive() {
		order, err := insertAdminRechargeOrder(ctx, tx, input)
		if isUniqueViolation(err) {
			return AdminBalanceAdjustmentResult{}, ErrExternalTradeConflict
		}
		if err != nil {
			return AdminBalanceAdjustmentResult{}, err
		}
		if err := insertBalanceRechargedEvent(ctx, tx, order); err != nil {
			return AdminBalanceAdjustmentResult{}, err
		}
		rechargeOrderID = order.ID
	}

	balance, err := adjustUserBalanceTx(ctx, tx, input)
	if err != nil {
		return AdminBalanceAdjustmentResult{}, err
	}
	if err := insertAdminBalanceAudit(ctx, tx, input, rechargeOrderID, balance); err != nil {
		return AdminBalanceAdjustmentResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		if isUniqueViolation(err) {
			return AdminBalanceAdjustmentResult{}, ErrExternalTradeConflict
		}
		return AdminBalanceAdjustmentResult{}, fmt.Errorf("payment: commit admin balance adjustment: %w", err)
	}
	return AdminBalanceAdjustmentResult{
		TenantID:        input.TenantID,
		UserID:          input.UserID,
		NewBalance:      balance,
		CurrencyCode:    input.CurrencyCode,
		RechargeOrderID: rechargeOrderID,
	}, nil
}

func normalizeAdminBalanceAdjustmentInput(input AdminBalanceAdjustmentInput) AdminBalanceAdjustmentInput {
	input.CurrencyCode = strings.ToUpper(strings.TrimSpace(input.CurrencyCode))
	if input.CurrencyCode == "" {
		input.CurrencyCode = "USD"
	}
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.Reason = strings.TrimSpace(input.Reason)
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.ExternalTradeNo = strings.TrimSpace(input.ExternalTradeNo)
	if input.Now.IsZero() {
		input.Now = time.Now().UTC()
	} else {
		input.Now = input.Now.UTC()
	}
	return input
}

func validateAdminBalanceAdjustmentInput(input AdminBalanceAdjustmentInput) error {
	if input.TenantID <= 0 || input.UserID <= 0 || input.Amount.IsZero() || input.ActorID == "" ||
		input.Reason == "" || input.ExternalTradeNo == "" {
		return ErrInvalidInput
	}
	if len(input.ExternalTradeNo) > 128 {
		return ErrInvalidInput
	}
	if input.CurrencyCode != "USD" || !fitsSignedMoneyColumn(input.Amount) {
		return ErrInvalidInput
	}
	return nil
}

func fitsSignedMoneyColumn(value decimal.Decimal) bool {
	if !value.Equal(value.Truncate(8)) {
		return false
	}
	return value.Abs().LessThan(moneyNumericUpperBound)
}

func adjustUserBalanceTx(ctx context.Context, tx pgx.Tx, input AdminBalanceAdjustmentInput) (decimal.Decimal, error) {
	var inserted decimal.Decimal
	if input.Amount.IsPositive() {
		inserted = input.Amount
	}
	var balance decimal.Decimal
	if err := tx.QueryRow(ctx, `
INSERT INTO user_balances (tenant_id, user_id, balance, held, version, updated_at)
VALUES ($1, $2, $3, 0, 1, $5)
ON CONFLICT (tenant_id, user_id) DO UPDATE
SET balance = GREATEST(user_balances.held, user_balances.balance + $4),
    version = user_balances.version + 1,
    updated_at = $5
RETURNING balance`,
		input.TenantID, input.UserID, inserted, input.Amount, input.Now,
	).Scan(&balance); err != nil {
		return decimal.Decimal{}, fmt.Errorf("payment: admin adjust user balance: %w", err)
	}
	return balance, nil
}

func lockAdminAdjustmentKey(ctx context.Context, tx pgx.Tx, input AdminBalanceAdjustmentInput) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`,
		fmt.Sprintf("payment-admin:%d", input.TenantID), input.ExternalTradeNo,
	); err != nil {
		return fmt.Errorf("payment: lock admin adjustment key: %w", err)
	}
	return nil
}

func getExistingAdminBalanceAdjustment(ctx context.Context, tx pgx.Tx, input AdminBalanceAdjustmentInput) (AdminBalanceAdjustmentResult, bool, error) {
	var (
		userID          int64
		rechargeOrderID pgtype.Int8
		amount          decimal.Decimal
		currencyCode    string
	)
	err := tx.QueryRow(ctx, `
SELECT user_id, recharge_order_id, paid_amount, currency_code
FROM payment_audit_log
WHERE tenant_id=$1
  AND provider=$2
  AND external_trade_no=$3
  AND outcome=$4
ORDER BY id
LIMIT 1`,
		input.TenantID, adminPaymentProvider, input.ExternalTradeNo, AuditOutcomeAccepted,
	).Scan(&userID, &rechargeOrderID, &amount, &currencyCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminBalanceAdjustmentResult{}, false, nil
	}
	if err != nil {
		return AdminBalanceAdjustmentResult{}, false, fmt.Errorf("payment: lookup admin adjustment replay: %w", err)
	}
	if userID != input.UserID || !amount.Equal(input.Amount) || strings.TrimSpace(currencyCode) != input.CurrencyCode {
		return AdminBalanceAdjustmentResult{}, true, ErrExternalTradeConflict
	}
	balance, err := readUserBalanceTx(ctx, tx, input.TenantID, input.UserID)
	if err != nil {
		return AdminBalanceAdjustmentResult{}, true, err
	}
	var orderID int64
	if rechargeOrderID.Valid {
		orderID = rechargeOrderID.Int64
	}
	return AdminBalanceAdjustmentResult{
		TenantID:        input.TenantID,
		UserID:          input.UserID,
		NewBalance:      balance,
		CurrencyCode:    input.CurrencyCode,
		RechargeOrderID: orderID,
	}, true, nil
}

func readUserBalanceTx(ctx context.Context, tx pgx.Tx, tenantID, userID int64) (decimal.Decimal, error) {
	var balance decimal.Decimal
	if err := tx.QueryRow(ctx, `
SELECT COALESCE((
    SELECT balance
    FROM user_balances
    WHERE tenant_id=$1 AND user_id=$2
), 0)`,
		tenantID, userID,
	).Scan(&balance); err != nil {
		return decimal.Decimal{}, fmt.Errorf("payment: read admin adjustment balance: %w", err)
	}
	return balance, nil
}

func insertAdminRechargeOrder(ctx context.Context, tx pgx.Tx, input AdminBalanceAdjustmentInput) (Order, error) {
	metadata, err := adminBalanceAuditMetadata(input, decimal.Decimal{}, adminAuditRechargeSuccess)
	if err != nil {
		return Order{}, err
	}
	row := tx.QueryRow(ctx, `
INSERT INTO recharge_orders (
	tenant_id, user_id, external_trade_no, recharge_ref, status,
	requested_amount, credited_amount, currency_code, provider, metadata,
	created_at, updated_at, paid_at, completed_at
) VALUES (
	$1, $2, $3, $4, 'COMPLETED',
	$5, $5, $6, $7, $8,
	$9, $9, $9, $9
)
RETURNING id, tenant_id, user_id, external_trade_no, recharge_ref, status,
	credited_amount, currency_code, provider, created_at, updated_at`,
		input.TenantID, input.UserID, input.ExternalTradeNo, rechargeRef(input.TenantID, input.UserID, input.ExternalTradeNo),
		input.Amount, input.CurrencyCode, adminPaymentProvider, string(metadata), input.Now,
	)
	order, err := scanOrder(row)
	if err != nil {
		return Order{}, fmt.Errorf("payment: insert admin recharge order: %w", err)
	}
	return order, nil
}

func insertAdminBalanceAudit(ctx context.Context, tx pgx.Tx, input AdminBalanceAdjustmentInput, rechargeOrderID int64, balance decimal.Decimal) error {
	code := adminAuditAdjustment
	if input.Amount.IsPositive() {
		code = adminAuditRechargeSuccess
	}
	metadata, err := adminBalanceAuditMetadata(input, balance, code)
	if err != nil {
		return err
	}
	var orderID *int64
	if rechargeOrderID > 0 {
		orderID = &rechargeOrderID
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO payment_audit_log (
	tenant_id, recharge_order_id, user_id, provider, external_trade_no,
	provider_event_id, outcome, reason, paid_amount, expected_amount,
	currency_code, metadata, created_at
) VALUES (
	$1, $2, $3, $4, $5,
	$6, $7, $8, $9, $9,
	$10, $11, $12
)`,
		input.TenantID, orderID, input.UserID, adminPaymentProvider, input.ExternalTradeNo,
		code, AuditOutcomeAccepted, AuditReasonCompleted, input.Amount,
		input.CurrencyCode, string(metadata), input.Now,
	); err != nil {
		return fmt.Errorf("payment: insert admin balance audit: %w", err)
	}
	return nil
}

func adminBalanceAuditMetadata(input AdminBalanceAdjustmentInput, balance decimal.Decimal, code string) ([]byte, error) {
	payload := map[string]string{
		"audit_code":    code,
		"actor_id":      input.ActorID,
		"delta_amount":  input.Amount.StringFixed(8),
		"currency_code": input.CurrencyCode,
	}
	if input.Reason != "" {
		payload["admin_reason"] = input.Reason
	}
	if input.RequestID != "" {
		payload["request_id"] = input.RequestID
	}
	if !balance.IsZero() {
		payload["new_balance"] = balance.StringFixed(8)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("payment: marshal admin balance audit metadata: %w", err)
	}
	return raw, nil
}

var _ AdminBalanceStore = (*PostgresStore)(nil)
