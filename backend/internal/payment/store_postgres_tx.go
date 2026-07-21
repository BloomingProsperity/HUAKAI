// HUAKAI · iKun

package payment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"
)

type auditInsert struct {
	TenantID    int64
	OrderID     int64
	EventType   string
	ActorKind   string
	ActorID     int64
	ActorRef    string // 双身份归属串(AuditActor() 形态),空则列落 NULL
	ReasonClass string
	RequestID   string
	Payload     map[string]any
	Now         time.Time
}

func insertAuditTx(ctx context.Context, tx pgx.Tx, ev auditInsert) error {
	var raw []byte
	if payload := sanitizeAuditPayload(ctx, ev.Payload); payload != nil {
		raw, _ = json.Marshal(payload)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO payment_audit_events (tenant_id, payment_order_id, event_type, actor_kind, actor_id, actor_ref, reason_class, request_id, redacted_payload, occurred_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		ev.TenantID, ev.OrderID, ev.EventType, actorKindOrDefault(ev.ActorKind),
		nullableInt64(ev.ActorID), nullableText(ev.ActorRef), nullableText(ev.ReasonClass), nullableText(ev.RequestID),
		nullableJSON(raw), ev.Now); err != nil {
		return fmt.Errorf("payment: insert audit event: %w", err)
	}
	return nil
}

func insertOrderTx(ctx context.Context, tx pgx.Tx, rec createOrderRecord) (Order, error) {
	row := tx.QueryRow(ctx, `
INSERT INTO payment_orders (
	tenant_id, user_id, out_trade_no, amount_cents, currency_code, status,
	provider_kind, provider_order_ref, request_fingerprint, created_by_admin_id, created_by_actor,
	created_at, updated_at, expires_at, order_kind, subscription_plan_id,
	terms_version, terms_accepted_at, terms_accepted_by, terms_accepted_ip
) VALUES ($1, $2, $3, $4, $5, 'pending', $6, $7, $8, $9, $10, $11, $11, $12, $13, $14, $15, $16, $17, $18::inet)
RETURNING`+orderSelectColumns,
		rec.TenantID, rec.UserID, rec.OutTradeNo, rec.AmountCents, rec.CurrencyCode,
		string(providerKindOrDefault(rec.ProviderKind)), nullableText(rec.ProviderOrderRef),
		nullableText(rec.RequestFingerprint), nullableInt64(rec.CreatedByAdminID), nullableText(rec.CreatedByActor),
		rec.Now, rec.ExpiresAt,
		orderKindOrDefault(rec.OrderKind), rec.SubscriptionPlanID,
		nullableText(rec.ComplianceTermsVersion), rec.ComplianceAcceptedAt, nullableInt64(rec.ComplianceAcceptedBy),
		nullableText(rec.ComplianceAcceptedIP))
	return scanOrder(row)
}

func acquireRechargeCapLockTx(ctx context.Context, tx pgx.Tx, rec createOrderRecord) error {
	if rec.RechargeMaxPending <= 0 && rec.RechargeDailyLimitCents <= 0 {
		return nil
	}
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1::bigint::text || ':' || $2::bigint::text, 0))`,
		rec.TenantID, rec.UserID); err != nil {
		return fmt.Errorf("payment: recharge cap lock: %w", err)
	}
	return nil
}

func recheckRechargeCapsAfterInsertTx(ctx context.Context, tx pgx.Tx, rec createOrderRecord) error {
	if rec.RechargeMaxPending <= 0 && rec.RechargeDailyLimitCents <= 0 {
		return nil
	}
	if rec.RechargeMaxPending > 0 {
		var pending int
		if err := tx.QueryRow(ctx, `
SELECT COUNT(*)::int
FROM payment_orders
WHERE tenant_id=$1 AND user_id=$2 AND status='pending'
  AND (expires_at IS NULL OR expires_at > $3)`, rec.TenantID, rec.UserID, rec.Now).Scan(&pending); err != nil {
			return fmt.Errorf("payment: recharge pending recheck: %w", err)
		}
		if pending > rec.RechargeMaxPending {
			return ErrPendingLimit
		}
	}
	if rec.RechargeDailyLimitCents > 0 {
		start := time.Date(rec.Now.Year(), rec.Now.Month(), rec.Now.Day(), 0, 0, 0, 0, time.UTC)
		var used int64
		if err := tx.QueryRow(ctx, `
SELECT COALESCE(SUM(amount_cents), 0)::bigint
FROM payment_orders
WHERE tenant_id=$1 AND user_id=$2 AND created_at >= $3
  AND (status IN ('paid', 'recharging', 'completed')
       OR (status = 'pending' AND (expires_at IS NULL OR expires_at > $4)))`,
			rec.TenantID, rec.UserID, start, rec.Now).Scan(&used); err != nil {
			return fmt.Errorf("payment: recharge daily recheck: %w", err)
		}
		if used > rec.RechargeDailyLimitCents {
			return ErrDailyAmountLimit
		}
	}
	return nil
}

func getOrderByOutTradeNoTx(ctx context.Context, tx pgx.Tx, tenantID int64, outTradeNo string) (Order, error) {
	row := tx.QueryRow(ctx, `SELECT`+orderSelectColumns+`
FROM payment_orders WHERE tenant_id=$1 AND out_trade_no=$2`, tenantID, outTradeNo)
	order, err := scanOrder(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrOrderNotFound
	}
	if err != nil {
		return Order{}, fmt.Errorf("payment: get order by out_trade_no: %w", err)
	}
	return order, nil
}

func getOrderForUpdateTx(ctx context.Context, tx pgx.Tx, tenantID, orderID int64) (Order, error) {
	row := tx.QueryRow(ctx, `SELECT`+orderSelectColumns+`
FROM payment_orders WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, orderID)
	order, err := scanOrder(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrOrderNotFound
	}
	if err != nil {
		return Order{}, fmt.Errorf("payment: lock order: %w", err)
	}
	return order, nil
}

func getCreditByOrderTx(ctx context.Context, tx pgx.Tx, tenantID, orderID int64) (CreditRecord, error) {
	var c CreditRecord
	var billingID sql.NullInt64
	err := tx.QueryRow(ctx, `
SELECT id, tenant_id, payment_order_id, user_id, amount_cents, currency_code, reason_class, billing_event_id, created_at
FROM payment_credits WHERE tenant_id=$1 AND payment_order_id=$2`, tenantID, orderID).Scan(
		&c.ID, &c.TenantID, &c.OrderID, &c.UserID, &c.AmountCents, &c.CurrencyCode, &c.ReasonClass, &billingID, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return CreditRecord{}, ErrOrderNotFound
	}
	if err != nil {
		return CreditRecord{}, fmt.Errorf("payment: get credit: %w", err)
	}
	if billingID.Valid {
		c.BillingEventID = billingID.Int64
	}
	c.CurrencyCode = strings.TrimSpace(c.CurrencyCode)
	c.CreatedAt = c.CreatedAt.UTC()
	return c, nil
}

func insertTopupCreditTx(ctx context.Context, tx pgx.Tx, order Order, actorKind string, actorID int64, requestID string, now time.Time) (CreditRecord, int64, error) {
	var credit CreditRecord
	credit.TenantID = order.TenantID
	credit.OrderID = order.ID
	credit.UserID = order.UserID
	credit.AmountCents = order.AmountCents
	credit.CurrencyCode = order.CurrencyCode
	credit.ReasonClass = reasonClassForProvider(order.ProviderKind)
	if err := tx.QueryRow(ctx, `
INSERT INTO payment_credits (tenant_id, payment_order_id, user_id, amount_cents, currency_code, reason_class, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, created_at`,
		credit.TenantID, credit.OrderID, credit.UserID, credit.AmountCents, credit.CurrencyCode, credit.ReasonClass, now,
	).Scan(&credit.ID, &credit.CreatedAt); err != nil {
		return CreditRecord{}, 0, err
	}
	amount := decimalFromCents(order.AmountCents)
	fingerprint := fmt.Sprintf("payment:t%d:o%d:c%d", order.TenantID, order.ID, credit.ID)
	var billingID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO billing_events (tenant_id, event_type, actual_cost, actual_cost_signed,
	stream_state, delivered_token_count, fingerprint, payment_credit_id)
VALUES ($1, 'payment_credited', $2, $2, 2, 0, $3, $4)
RETURNING id`, order.TenantID, amount, fingerprint, credit.ID).Scan(&billingID); err != nil {
		return CreditRecord{}, 0, fmt.Errorf("payment: insert billing event: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_credits SET billing_event_id=$3 WHERE tenant_id=$1 AND id=$2`,
		order.TenantID, credit.ID, billingID); err != nil {
		return CreditRecord{}, 0, fmt.Errorf("payment: link billing event: %w", err)
	}
	credit.BillingEventID = billingID
	if err := syncLegacyUserBalanceTx(ctx, tx, order.TenantID, order.UserID, order.AmountCents, now); err != nil {
		return CreditRecord{}, 0, err
	}
	_ = actorKind
	_ = actorID
	_ = requestID
	return credit, billingID, nil
}

func userBalanceTx(ctx context.Context, tx pgx.Tx, tenantID, userID int64) (int64, error) {
	var balance int64
	if err := tx.QueryRow(ctx, `
WITH credits AS (
	SELECT COALESCE(SUM(amount_cents), 0)::bigint AS total
	FROM payment_credits
	WHERE tenant_id=$1 AND user_id=$2
),
refunds AS (
	SELECT COALESCE(SUM(amount_cents), 0)::bigint AS total
	FROM payment_refunds
	WHERE tenant_id=$1 AND user_id=$2
)
SELECT credits.total - refunds.total
FROM credits, refunds`, tenantID, userID).Scan(&balance); err != nil {
		return 0, fmt.Errorf("payment: read balance: %w", err)
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

func syncLegacyUserBalanceTx(ctx context.Context, tx pgx.Tx, tenantID, userID, amountCents int64, now time.Time) error {
	if amountCents <= 0 {
		return ErrInvalidAmount
	}
	return syncLegacyUserBalanceDeltaTx(ctx, tx, tenantID, userID, amountCents, now)
}

func syncLegacyUserBalanceDeltaTx(ctx context.Context, tx pgx.Tx, tenantID, userID, amountCents int64, now time.Time) error {
	if amountCents == 0 {
		return ErrInvalidAmount
	}
	amount := decimalFromCents(amountCents)
	if _, err := tx.Exec(ctx, `
INSERT INTO user_balances (tenant_id, user_id, balance, held, version, updated_at)
VALUES ($1, $2, $3, 0, 1, $4)
ON CONFLICT (tenant_id, user_id) DO UPDATE
SET balance = user_balances.balance + EXCLUDED.balance,
    version = user_balances.version + 1,
    updated_at = EXCLUDED.updated_at`,
		tenantID, userID, amount, now); err != nil {
		return fmt.Errorf("payment: sync legacy user balance: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanOrder(row rowScanner) (Order, error) {
	var o Order
	var status, providerKind string
	var createdBy, confirmedBy, subPlanID, complianceAcceptedBy sql.NullInt64
	var expiresAt, paidAt, rechargingAt, completedAt, failedAt, complianceAcceptedAt sql.NullTime
	if err := row.Scan(
		&o.ID, &o.TenantID, &o.UserID, &o.OutTradeNo, &o.AmountCents, &o.CurrencyCode, &status, &providerKind,
		&o.ProviderOrderRef, &o.RequestFingerprint,
		&createdBy, &confirmedBy, &o.ConfirmReason,
		&o.FailureCode, &o.FailureMessage,
		&o.CreatedAt, &o.UpdatedAt, &expiresAt, &paidAt, &rechargingAt, &completedAt, &failedAt,
		&o.OrderKind, &subPlanID,
		&o.ComplianceTermsVersion, &complianceAcceptedAt, &complianceAcceptedBy, &o.ComplianceAcceptedIP,
	); err != nil {
		return Order{}, err
	}
	if subPlanID.Valid {
		o.SubscriptionPlanID = &subPlanID.Int64
	}
	o.Status = OrderStatus(status)
	o.ProviderKind = ProviderKind(providerKind)
	o.CurrencyCode = strings.TrimSpace(o.CurrencyCode)
	if createdBy.Valid {
		o.CreatedByAdminID = createdBy.Int64
	}
	if confirmedBy.Valid {
		o.ConfirmedByAdminID = confirmedBy.Int64
	}
	if complianceAcceptedBy.Valid {
		o.ComplianceAcceptedBy = complianceAcceptedBy.Int64
	}
	o.CreatedAt = o.CreatedAt.UTC()
	o.UpdatedAt = o.UpdatedAt.UTC()
	o.ExpiresAt = nullTimeToPtr(expiresAt)
	o.PaidAt = nullTimeToPtr(paidAt)
	o.RechargingAt = nullTimeToPtr(rechargingAt)
	o.CompletedAt = nullTimeToPtr(completedAt)
	o.FailedAt = nullTimeToPtr(failedAt)
	o.ComplianceAcceptedAt = nullTimeToPtr(complianceAcceptedAt)
	return o, nil
}

func reasonClassForProvider(kind ProviderKind) string {
	if kind == ProviderTest {
		return "test_provider_paid"
	}
	return "manual_confirmed"
}

// orderKindOrDefault: 空缺省充值 (向后兼容现存单)。
func orderKindOrDefault(kind string) string {
	if kind == "" {
		return OrderKindTopup
	}
	return kind
}

func providerKindOrDefault(kind ProviderKind) ProviderKind {
	if kind == "" {
		return ProviderManual
	}
	return kind
}

func actorKindOrDefault(kind string) string {
	switch kind {
	case ActorKindAdmin, ActorKindUser, ActorKindSystem:
		return kind
	default:
		return ActorKindSystem
	}
}

func decimalFromCents(cents int64) decimal.Decimal {
	return decimal.NewFromInt(cents).Div(decimal.NewFromInt(100))
}

func nullTimeToPtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	u := t.Time.UTC()
	return &u
}

func nullableInt64(v int64) any {
	if v <= 0 {
		return nil
	}
	return v
}

func nullableText(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nullableJSON(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isPgRetryableTxConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01")
}
