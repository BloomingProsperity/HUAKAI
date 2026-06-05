package invitation

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

type qualifiedReferralRow struct {
	ID             int64
	ReferrerUserID int64
	RefereeUserID  int64
}

const (
	referralRewardReasonClass = "referral_reward"
	referralPaymentProvider   = "manual"
	referralPaymentOrderKind  = "topup"
	referralPaymentStatus     = "completed"
	referralAuditIssued       = "REWARD_ISSUED"
	referralAuditFailed       = "REWARD_FAILED"
	referralAuditSkipped      = "REWARD_SKIPPED"
	referralTxRetryAttempts   = 5
)

func (s *PostgresStore) qualifyPendingReferralWithReward(ctx context.Context, input qualifyReferralInput) error {
	input = normalizeQualifyReferralInput(input)
	if err := validateQualifyReferralInput(input); err != nil {
		return err
	}
	if s == nil || s.pool == nil {
		return ErrStoreNotConfigured
	}
	var lastErr error
	for attempt := 0; attempt < referralTxRetryAttempts; attempt++ {
		err := s.qualifyPendingReferralWithRewardOnce(ctx, input)
		if err == nil {
			return nil
		}
		if isPgRetryableReferralTx(err) || isUniqueViolation(err) {
			lastErr = err
			continue
		}
		_ = s.writeReferralRewardFailureAudit(ctx, input, err)
		return err
	}
	_ = s.writeReferralRewardFailureAudit(ctx, input, lastErr)
	return fmt.Errorf("invitation: qualify referral reward exhausted retries: %w", lastErr)
}

func (s *PostgresStore) qualifyPendingReferralWithRewardOnce(ctx context.Context, input qualifyReferralInput) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("invitation: begin qualify referral reward: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	referral, changed, err := qualifyReferralTx(ctx, tx, input)
	if err != nil || !changed {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if input.RewardUSDMicros == 0 || referral.ReferrerUserID <= 0 {
		if err := insertReferralRewardAuditTx(ctx, tx, referralRewardAuditInsert{
			TenantID: input.TenantID, Referral: referral, BillingEventID: input.BillingEventID,
			EventType: referralAuditSkipped, Reason: referralSkipReason(input, referral), Now: input.QualifiedAt,
		}); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	return s.issueReferralRewardTx(ctx, tx, input, referral)
}

func (s *PostgresStore) issueReferralRewardTx(ctx context.Context, tx pgx.Tx, input qualifyReferralInput, referral qualifiedReferralRow) error {
	rewardID, inserted, err := insertReferralRewardTx(ctx, tx, input, referral)
	if err != nil {
		return err
	}
	if !inserted {
		if err := markReferralRewardedTx(ctx, tx, input.TenantID, referral.ID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	orderID, creditID, billingID, err := creditReferralRewardTx(ctx, tx, input, referral)
	if err != nil {
		return err
	}
	if err := markReferralRewardedTx(ctx, tx, input.TenantID, referral.ID); err != nil {
		return err
	}
	if err := upsertReferralTierProgressTx(ctx, tx, input, referral.ReferrerUserID); err != nil {
		return err
	}
	if err := insertReferralRewardAuditTx(ctx, tx, referralRewardAuditInsert{
		TenantID: input.TenantID, Referral: referral, BillingEventID: input.BillingEventID,
		RewardID: rewardID, PaymentOrderID: orderID, PaymentCreditID: creditID,
		PaymentBillingEventID: billingID, EventType: referralAuditIssued, Now: input.QualifiedAt,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func qualifyReferralTx(ctx context.Context, tx pgx.Tx, input qualifyReferralInput) (qualifiedReferralRow, bool, error) {
	var row qualifiedReferralRow
	err := tx.QueryRow(ctx, `
UPDATE referrals
SET status = 'qualified',
    qualified_at = $4,
    first_billing_event_id = $3
WHERE tenant_id = $1
  AND referee_user_id = $2
  AND status = 'pending'
RETURNING id, referrer_user_id, referee_user_id`,
		input.TenantID, input.RefereeUserID, input.BillingEventID, input.QualifiedAt.UTC()).Scan(
		&row.ID, &row.ReferrerUserID, &row.RefereeUserID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return qualifiedReferralRow{}, false, nil
	}
	if err != nil {
		return qualifiedReferralRow{}, false, fmt.Errorf("invitation: qualify pending referral: %w", err)
	}
	return row, true, nil
}

func insertReferralRewardTx(ctx context.Context, tx pgx.Tx, input qualifyReferralInput, referral qualifiedReferralRow) (int64, bool, error) {
	var rewardID int64
	err := tx.QueryRow(ctx, `
INSERT INTO referral_rewards (
	tenant_id, referrer_user_id, referee_user_id, referral_id, reward_type, amount_usd_micros, issued_at
) VALUES ($1, $2, $3, $4, 'credit', $5, $6)
ON CONFLICT (tenant_id, referral_id) DO NOTHING
RETURNING id`,
		input.TenantID, referral.ReferrerUserID, referral.RefereeUserID, referral.ID,
		input.RewardUSDMicros, input.QualifiedAt.UTC(),
	).Scan(&rewardID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("invitation: insert referral reward: %w", err)
	}
	return rewardID, true, nil
}

func creditReferralRewardTx(ctx context.Context, tx pgx.Tx, input qualifyReferralInput, referral qualifiedReferralRow) (int64, int64, int64, error) {
	amountCents, err := rewardMicrosToCents(input.RewardUSDMicros)
	if err != nil {
		return 0, 0, 0, err
	}
	if err := lockReferralPaymentKeyTx(ctx, tx, input.TenantID, referral.ID); err != nil {
		return 0, 0, 0, err
	}
	if err := lockReferralPaymentUserTx(ctx, tx, input.TenantID, referral.ReferrerUserID); err != nil {
		return 0, 0, 0, err
	}
	orderID, err := insertReferralPaymentOrderTx(ctx, tx, input, referral, amountCents)
	if err != nil {
		return 0, 0, 0, err
	}
	return creditReferralPaymentOrderTx(ctx, tx, input, orderID, referral.ReferrerUserID, amountCents)
}

func creditReferralPaymentOrderTx(ctx context.Context, tx pgx.Tx, input qualifyReferralInput, orderID, userID, amountCents int64) (int64, int64, int64, error) {
	for _, eventType := range []string{"order_created", "paid_confirmed", "fulfillment_started"} {
		if err := insertReferralPaymentAuditTx(ctx, tx, input, orderID, eventType, nil); err != nil {
			return 0, 0, 0, err
		}
	}
	creditID, billingID, err := insertReferralPaymentCreditTx(ctx, tx, input, orderID, userID, amountCents)
	if err != nil {
		return 0, 0, 0, err
	}
	if err := syncReferralLegacyUserBalanceTx(ctx, tx, input.TenantID, userID, amountCents, input.QualifiedAt); err != nil {
		return 0, 0, 0, err
	}
	if err := completeReferralPaymentOrderTx(ctx, tx, input, orderID); err != nil {
		return 0, 0, 0, err
	}
	payload := map[string]any{"amount_cents": amountCents, "credit_id": creditID, "billing_event_id": billingID}
	if err := insertReferralPaymentAuditTx(ctx, tx, input, orderID, "credited", payload); err != nil {
		return 0, 0, 0, err
	}
	return orderID, creditID, billingID, nil
}

func lockReferralPaymentKeyTx(ctx context.Context, tx pgx.Tx, tenantID, referralID int64) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`,
		fmt.Sprintf("referral-reward:%d", tenantID), referralPaymentOutTradeNo(referralID)); err != nil {
		return fmt.Errorf("invitation: lock referral reward payment key: %w", err)
	}
	return nil
}

func lockReferralPaymentUserTx(ctx context.Context, tx pgx.Tx, tenantID, userID int64) error {
	var one int
	err := tx.QueryRow(ctx, `SELECT 1 FROM users WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, userID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidInput
	}
	if err != nil {
		return fmt.Errorf("invitation: lock referral reward user: %w", err)
	}
	return nil
}

func insertReferralPaymentOrderTx(ctx context.Context, tx pgx.Tx, input qualifyReferralInput, referral qualifiedReferralRow, amountCents int64) (int64, error) {
	var orderID int64
	err := tx.QueryRow(ctx, `
INSERT INTO payment_orders (
	tenant_id, user_id, out_trade_no, amount_cents, currency_code, status,
	provider_kind, request_fingerprint, created_at, updated_at, paid_at,
	recharging_at, completed_at, order_kind
) VALUES ($1, $2, $3, $4, 'USD', $5, $6, $7, $8, $8, $8, $8, $8, $9)
RETURNING id`,
		input.TenantID, referral.ReferrerUserID, referralPaymentOutTradeNo(referral.ID),
		amountCents, referralPaymentStatus, referralPaymentProvider, referralRewardReasonClass,
		input.QualifiedAt.UTC(), referralPaymentOrderKind,
	).Scan(&orderID)
	if isUniqueViolation(err) {
		return 0, fmt.Errorf("invitation: referral reward payment order conflicts with %s: %w", "uq_payment_orders_out_trade_no", err)
	}
	if err != nil {
		return 0, fmt.Errorf("invitation: insert referral reward payment order: %w", err)
	}
	return orderID, nil
}

func insertReferralPaymentCreditTx(ctx context.Context, tx pgx.Tx, input qualifyReferralInput, orderID, userID, amountCents int64) (int64, int64, error) {
	var creditID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO payment_credits (tenant_id, payment_order_id, user_id, amount_cents, currency_code, reason_class, created_at)
VALUES ($1, $2, $3, $4, 'USD', $5, $6)
RETURNING id`, input.TenantID, orderID, userID, amountCents, referralRewardReasonClass, input.QualifiedAt.UTC()).Scan(&creditID); err != nil {
		return 0, 0, fmt.Errorf("invitation: insert referral reward payment credit: %w", err)
	}
	amount := decimal.NewFromInt(amountCents).Div(decimal.NewFromInt(100))
	fingerprint := fmt.Sprintf("referral-reward:t%d:r%d:c%d", input.TenantID, orderID, creditID)
	var billingID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO billing_events (tenant_id, event_type, actual_cost, actual_cost_signed,
	stream_state, delivered_token_count, fingerprint, payment_credit_id)
VALUES ($1, 'payment_credited', $2, $2, 2, 0, $3, $4)
RETURNING id`, input.TenantID, amount, fingerprint, creditID).Scan(&billingID); err != nil {
		return 0, 0, fmt.Errorf("invitation: insert referral reward billing event: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_credits SET billing_event_id=$3 WHERE tenant_id=$1 AND id=$2`,
		input.TenantID, creditID, billingID); err != nil {
		return 0, 0, fmt.Errorf("invitation: link referral reward billing event: %w", err)
	}
	return creditID, billingID, nil
}

func completeReferralPaymentOrderTx(ctx context.Context, tx pgx.Tx, input qualifyReferralInput, orderID int64) error {
	tag, err := tx.Exec(ctx, `
UPDATE payment_orders
SET status='completed', paid_at=$3, recharging_at=$3, completed_at=$3, updated_at=$3
WHERE tenant_id=$1 AND id=$2`, input.TenantID, orderID, input.QualifiedAt.UTC())
	if err != nil {
		return fmt.Errorf("invitation: complete referral reward payment order: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("invitation: complete referral reward payment order affected %d rows", tag.RowsAffected())
	}
	return nil
}

func syncReferralLegacyUserBalanceTx(ctx context.Context, tx pgx.Tx, tenantID, userID, amountCents int64, now time.Time) error {
	amount := decimal.NewFromInt(amountCents).Div(decimal.NewFromInt(100))
	if _, err := tx.Exec(ctx, `
INSERT INTO user_balances (tenant_id, user_id, balance, held, version, updated_at)
VALUES ($1, $2, $3, 0, 1, $4)
ON CONFLICT (tenant_id, user_id) DO UPDATE
SET balance = user_balances.balance + EXCLUDED.balance,
    version = user_balances.version + 1,
    updated_at = EXCLUDED.updated_at`, tenantID, userID, amount, now.UTC()); err != nil {
		return fmt.Errorf("invitation: sync referral reward balance: %w", err)
	}
	return nil
}

func insertReferralPaymentAuditTx(ctx context.Context, tx pgx.Tx, input qualifyReferralInput, orderID int64, eventType string, payload map[string]any) error {
	var raw []byte
	if payload != nil {
		raw, _ = json.Marshal(payload)
	}
	_, err := tx.Exec(ctx, `
INSERT INTO payment_audit_events (
	tenant_id, payment_order_id, event_type, actor_kind, reason_class, request_id, redacted_payload, occurred_at
) VALUES ($1, $2, $3, 'system', $4, $5, $6, $7)`,
		input.TenantID, orderID, eventType, referralRewardReasonClass,
		referralRewardRequestID(input.RefereeUserID), nullableJSONBytes(raw), input.QualifiedAt.UTC())
	if err != nil {
		return fmt.Errorf("invitation: insert referral reward payment audit: %w", err)
	}
	return nil
}

func markReferralRewardedTx(ctx context.Context, tx pgx.Tx, tenantID, referralID int64) error {
	tag, err := tx.Exec(ctx, `
UPDATE referrals
SET status = 'rewarded'
WHERE tenant_id = $1
  AND id = $2
  AND status = 'qualified'`, tenantID, referralID)
	if err != nil {
		return fmt.Errorf("invitation: mark referral rewarded: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("invitation: mark referral rewarded affected %d rows", tag.RowsAffected())
	}
	return nil
}

func upsertReferralTierProgressTx(ctx context.Context, tx pgx.Tx, input qualifyReferralInput, referrerUserID int64) error {
	var total int
	err := tx.QueryRow(ctx, `
INSERT INTO tier_progress (tenant_id, user_id, total_qualified_referrals, current_tier, tier_unlocked_at)
VALUES ($1, $2, 1, $6, $7)
ON CONFLICT (user_id) DO UPDATE
SET total_qualified_referrals = tier_progress.total_qualified_referrals + 1,
    current_tier = CASE
        WHEN tier_progress.total_qualified_referrals + 1 >= $5 THEN 'platinum'
        WHEN tier_progress.total_qualified_referrals + 1 >= $4 THEN 'gold'
        WHEN tier_progress.total_qualified_referrals + 1 >= $3 THEN 'silver'
        ELSE 'none'
    END,
    tier_unlocked_at = CASE
        WHEN tier_progress.current_tier <> CASE
            WHEN tier_progress.total_qualified_referrals + 1 >= $5 THEN 'platinum'
            WHEN tier_progress.total_qualified_referrals + 1 >= $4 THEN 'gold'
            WHEN tier_progress.total_qualified_referrals + 1 >= $3 THEN 'silver'
            ELSE 'none'
        END THEN $7
        ELSE tier_progress.tier_unlocked_at
    END
WHERE tier_progress.tenant_id = EXCLUDED.tenant_id
RETURNING total_qualified_referrals`,
		input.TenantID, referrerUserID, input.TierThresholds.Silver, input.TierThresholds.Gold,
		input.TierThresholds.Platinum, tierForQualifiedReferralCount(1, input.TierThresholds),
		input.QualifiedAt.UTC()).Scan(&total)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidInput
	}
	if err != nil {
		return fmt.Errorf("invitation: upsert referral tier progress: %w", err)
	}
	return nil
}

type referralRewardAuditInsert struct {
	TenantID              int64
	Referral              qualifiedReferralRow
	BillingEventID        int64
	RewardID              int64
	PaymentOrderID        int64
	PaymentCreditID       int64
	PaymentBillingEventID int64
	EventType             string
	Reason                string
	Now                   time.Time
}

func insertReferralRewardAuditTx(ctx context.Context, tx pgx.Tx, ev referralRewardAuditInsert) error {
	payload := map[string]any{
		"reward_id":                ev.RewardID,
		"payment_order_id":         ev.PaymentOrderID,
		"payment_credit_id":        ev.PaymentCreditID,
		"payment_billing_event_id": ev.PaymentBillingEventID,
	}
	raw, _ := json.Marshal(payload)
	_, err := tx.Exec(ctx, `
INSERT INTO referral_reward_audit_events (
	tenant_id, referral_id, referrer_user_id, referee_user_id, billing_event_id,
	reward_id, payment_order_id, event_type, reason, redacted_payload, occurred_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		ev.TenantID, nullableInt64Value(ev.Referral.ID), nullableInt64Value(ev.Referral.ReferrerUserID),
		nullableInt64Value(ev.Referral.RefereeUserID), nullableInt64Value(ev.BillingEventID),
		nullableInt64Value(ev.RewardID), nullableInt64Value(ev.PaymentOrderID),
		ev.EventType, nullableTextValue(ev.Reason), raw, ev.Now.UTC())
	if err != nil {
		return fmt.Errorf("invitation: insert referral reward audit: %w", err)
	}
	return nil
}

func (s *PostgresStore) writeReferralRewardFailureAudit(ctx context.Context, input qualifyReferralInput, cause error) error {
	reason := "unknown"
	if cause != nil {
		reason = cause.Error()
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO referral_reward_audit_events (
	tenant_id, referee_user_id, billing_event_id, event_type, reason, occurred_at
) VALUES ($1, $2, $3, $4, $5, $6)`,
		input.TenantID, input.RefereeUserID, input.BillingEventID,
		referralAuditFailed, trimAuditReason(reason), input.QualifiedAt.UTC())
	return err
}

func referralPaymentOutTradeNo(referralID int64) string {
	return fmt.Sprintf("%s:%d", referralRewardRequestPrefix, referralID)
}

func referralRewardRequestID(refereeUserID int64) string {
	return fmt.Sprintf("%s:referee:%d", referralRewardRequestPrefix, refereeUserID)
}

func referralSkipReason(input qualifyReferralInput, referral qualifiedReferralRow) string {
	if input.RewardUSDMicros == 0 {
		return "reward_disabled"
	}
	if referral.ReferrerUserID <= 0 {
		return "missing_referrer"
	}
	return "reward_not_inserted"
}

func isPgRetryableReferralTx(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01")
}

func nullableInt64Value(v int64) sql.NullInt64 {
	return sql.NullInt64{Int64: v, Valid: v > 0}
}

func nullableTextValue(v string) sql.NullString {
	v = strings.TrimSpace(v)
	return sql.NullString{String: v, Valid: v != ""}
}

func nullableJSONBytes(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}
