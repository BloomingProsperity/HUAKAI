package payment

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

const (
	adminAdjustmentFingerprint = "admin_adjustment"
	adminPaymentProvider       = "manual_admin"
	adminAuditRechargeSuccess  = "RECHARGE_SUCCESS"
	adminAuditAdjustment       = "MANUAL_BALANCE_ADJUSTMENT"
)

type AdminBalanceAdjustmentInput struct {
	TenantID     int64
	UserID       int64
	Amount       decimal.Decimal
	CurrencyCode string
	ActorID      string
	// ActorRef 双身份归属串(AuditActor() 形态 "admin_token:<id>"/"admin_user:<id>"),
	// 落 text 列;ActorID 仍是旧 bigint 列的纯数字载体(session-admin 传 "0"→NULL)。
	ActorRef        string
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
	if err := validateAdminBalanceAdjustmentInput(input); err != nil {
		return AdminBalanceAdjustmentResult{}, err
	}
	store, ok := s.store.(AdminBalanceStore)
	if !ok || store == nil {
		return AdminBalanceAdjustmentResult{}, ErrStoreNotConfigured
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
	for attempt := 0; attempt < fulfillTxRetryAttempts; attempt++ {
		result, err := s.applyAdminBalanceAdjustmentOnce(ctx, input)
		if err == nil {
			return result, nil
		}
		if isPgRetryableTxConflict(err) || isUniqueViolation(err) {
			lastErr = err
			continue
		}
		return AdminBalanceAdjustmentResult{}, err
	}
	return AdminBalanceAdjustmentResult{}, fmt.Errorf("payment: admin adjustment exhausted retries: %w", lastErr)
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
	if existing, ok, err := getExistingAdminAdjustmentTx(ctx, tx, input); ok || err != nil {
		if err != nil {
			return AdminBalanceAdjustmentResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return AdminBalanceAdjustmentResult{}, fmt.Errorf("payment: commit admin adjustment replay: %w", err)
		}
		return existing, nil
	}
	if input.Amount.IsNegative() {
		return AdminBalanceAdjustmentResult{}, ErrAdminDebitNotSupported
	}
	if err := lockPaymentUserTx(ctx, tx, input.TenantID, input.UserID); err != nil {
		return AdminBalanceAdjustmentResult{}, err
	}
	amountCents, err := decimalAmountToCents(input.Amount)
	if err != nil {
		return AdminBalanceAdjustmentResult{}, err
	}
	// 双身份归属:actorID(旧 bigint 列,token 通道的数字 TokenID;session-admin 为 0→NULL)
	// + input.ActorRef(新 text 列,AuditActor() 串,两通道都可归属)。
	actorID := parseAdminActorID(input.ActorID)
	order, err := insertOrderTx(ctx, tx, createOrderRecord{
		TenantID:           input.TenantID,
		UserID:             input.UserID,
		OutTradeNo:         input.ExternalTradeNo,
		AmountCents:        amountCents,
		CurrencyCode:       input.CurrencyCode,
		ProviderKind:       ProviderManual,
		RequestFingerprint: adminAdjustmentFingerprint,
		CreatedByAdminID:   actorID,
		CreatedByActor:     input.ActorRef,
		RequestID:          input.RequestID,
		OrderKind:          OrderKindTopup,
		Now:                input.Now,
	})
	if errors.Is(err, ErrIdempotencyConflict) || isUniqueViolation(err) {
		return AdminBalanceAdjustmentResult{}, ErrExternalTradeConflict
	}
	if err != nil {
		return AdminBalanceAdjustmentResult{}, fmt.Errorf("payment: insert admin adjustment order: %w", err)
	}
	for _, ev := range []auditInsert{
		{TenantID: input.TenantID, OrderID: order.ID, EventType: AuditOrderCreated, ActorKind: ActorKindAdmin, ActorID: actorID, ActorRef: input.ActorRef, ReasonClass: input.Reason, RequestID: input.RequestID, Payload: map[string]any{"source": adminAdjustmentFingerprint, "amount_cents": amountCents}, Now: input.Now},
		{TenantID: input.TenantID, OrderID: order.ID, EventType: AuditPaidConfirmed, ActorKind: ActorKindAdmin, ActorID: actorID, ActorRef: input.ActorRef, ReasonClass: input.Reason, RequestID: input.RequestID, Now: input.Now},
		{TenantID: input.TenantID, OrderID: order.ID, EventType: AuditFulfillmentStarted, ActorKind: ActorKindAdmin, ActorID: actorID, ActorRef: input.ActorRef, ReasonClass: input.Reason, RequestID: input.RequestID, Now: input.Now},
	} {
		if err := insertAuditTx(ctx, tx, ev); err != nil {
			return AdminBalanceAdjustmentResult{}, err
		}
	}
	credit, billingID, err := insertTopupCreditTx(ctx, tx, order, actorKindOrDefault(ActorKindAdmin), actorID, input.RequestID, input.Now)
	if err != nil {
		return AdminBalanceAdjustmentResult{}, err
	}
	row := tx.QueryRow(ctx, `
UPDATE payment_orders
SET status='completed',
    paid_at=$3,
    recharging_at=$3,
    completed_at=$3,
    confirmed_by_admin_id=$4,
    confirmed_by_actor=$6,
    confirm_reason=$5,
    updated_at=$3
WHERE tenant_id=$1 AND id=$2
RETURNING`+orderSelectColumns,
		input.TenantID, order.ID, input.Now, nullableInt64(actorID), nullableText(input.Reason), nullableText(input.ActorRef))
	completed, err := scanOrder(row)
	if err != nil {
		return AdminBalanceAdjustmentResult{}, fmt.Errorf("payment: complete admin adjustment order: %w", err)
	}
	if err := insertAuditTx(ctx, tx, auditInsert{
		TenantID:  input.TenantID,
		OrderID:   completed.ID,
		EventType: AuditCredited,
		ActorKind: ActorKindAdmin,
		ActorID:   actorID,
		ActorRef:  input.ActorRef,
		RequestID: input.RequestID,
		Payload:   map[string]any{"amount_cents": completed.AmountCents, "credit_id": credit.ID, "billing_event_id": billingID},
		Now:       input.Now,
	}); err != nil {
		return AdminBalanceAdjustmentResult{}, err
	}
	balance, err := userBalanceTx(ctx, tx, input.TenantID, input.UserID)
	if err != nil {
		return AdminBalanceAdjustmentResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AdminBalanceAdjustmentResult{}, fmt.Errorf("payment: commit admin balance adjustment: %w", err)
	}
	return AdminBalanceAdjustmentResult{
		TenantID:        input.TenantID,
		UserID:          input.UserID,
		NewBalance:      centsToDecimal(balance),
		CurrencyCode:    input.CurrencyCode,
		RechargeOrderID: completed.ID,
	}, nil
}

func normalizeAdminBalanceAdjustmentInput(input AdminBalanceAdjustmentInput) AdminBalanceAdjustmentInput {
	input.CurrencyCode = strings.ToUpper(strings.TrimSpace(input.CurrencyCode))
	if input.CurrencyCode == "" {
		input.CurrencyCode = "USD"
	}
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.ActorRef = strings.TrimSpace(input.ActorRef)
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
	if len(input.ExternalTradeNo) > maxOutTradeNoLen {
		return ErrInvalidInput
	}
	if input.CurrencyCode != "USD" {
		return ErrUnsupportedCurrency
	}
	if _, err := decimalAmountToCents(input.Amount.Abs()); err != nil {
		return err
	}
	return nil
}

func lockAdminAdjustmentKey(ctx context.Context, tx pgx.Tx, input AdminBalanceAdjustmentInput) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`,
		fmt.Sprintf("payment-admin:%d", input.TenantID), input.ExternalTradeNo,
	); err != nil {
		return fmt.Errorf("payment: lock admin adjustment key: %w", err)
	}
	return nil
}

func getExistingAdminAdjustmentTx(ctx context.Context, tx pgx.Tx, input AdminBalanceAdjustmentInput) (AdminBalanceAdjustmentResult, bool, error) {
	existing, err := getOrderByOutTradeNoTx(ctx, tx, input.TenantID, input.ExternalTradeNo)
	if errors.Is(err, ErrOrderNotFound) {
		return getExistingLegacyAdminAdjustmentTx(ctx, tx, input)
	}
	if err != nil {
		return AdminBalanceAdjustmentResult{}, false, err
	}
	wantCents, err := decimalAmountToCents(input.Amount.Abs())
	if err != nil {
		return AdminBalanceAdjustmentResult{}, true, err
	}
	if input.Amount.IsNegative() ||
		existing.UserID != input.UserID ||
		existing.AmountCents != wantCents ||
		existing.ProviderKind != ProviderManual ||
		existing.RequestFingerprint != adminAdjustmentFingerprint {
		return AdminBalanceAdjustmentResult{}, true, ErrExternalTradeConflict
	}
	if existing.Status != StatusCompleted {
		return AdminBalanceAdjustmentResult{}, true, ErrExternalTradeConflict
	}
	balance, err := userBalanceTx(ctx, tx, input.TenantID, input.UserID)
	if err != nil {
		return AdminBalanceAdjustmentResult{}, true, err
	}
	return AdminBalanceAdjustmentResult{
		TenantID:        input.TenantID,
		UserID:          input.UserID,
		NewBalance:      centsToDecimal(balance),
		CurrencyCode:    input.CurrencyCode,
		RechargeOrderID: existing.ID,
	}, true, nil
}

func getExistingLegacyAdminAdjustmentTx(ctx context.Context, tx pgx.Tx, input AdminBalanceAdjustmentInput) (AdminBalanceAdjustmentResult, bool, error) {
	var (
		userID          int64
		rechargeOrderID sql.NullInt64
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
		return AdminBalanceAdjustmentResult{}, false, fmt.Errorf("payment: lookup legacy admin adjustment replay: %w", err)
	}
	if userID != input.UserID || !amount.Equal(input.Amount) || strings.TrimSpace(currencyCode) != input.CurrencyCode {
		return AdminBalanceAdjustmentResult{}, true, ErrExternalTradeConflict
	}
	balance, err := readLegacyUserBalanceTx(ctx, tx, input.TenantID, input.UserID)
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

func readLegacyUserBalanceTx(ctx context.Context, tx pgx.Tx, tenantID, userID int64) (decimal.Decimal, error) {
	var balance decimal.Decimal
	if err := tx.QueryRow(ctx, `
SELECT COALESCE((
    SELECT balance
    FROM user_balances
    WHERE tenant_id=$1 AND user_id=$2
), 0)`,
		tenantID, userID,
	).Scan(&balance); err != nil {
		return decimal.Decimal{}, fmt.Errorf("payment: read legacy admin adjustment balance: %w", err)
	}
	return balance, nil
}

func lockPaymentUserTx(ctx context.Context, tx pgx.Tx, tenantID, userID int64) error {
	var one int
	err := tx.QueryRow(ctx, `SELECT 1 FROM users WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, userID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return fmt.Errorf("payment: lock user: %w", err)
	}
	return nil
}

func parseAdminActorID(raw string) int64 {
	id, _ := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	return id
}
