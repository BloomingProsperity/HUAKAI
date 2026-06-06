package payment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	referralRewardFingerprint   = "referral_reward"
	referralRewardMicrosPerCent = 10_000
)

type ReferralRewardInput struct {
	TenantID       int64
	RefereeUserID  int64
	BillingEventID int64
	RewardCents    int64
	CurrencyCode   string
	Now            time.Time
}

type ReferralRewardResult struct {
	Rewarded           bool
	ReferralID         int64
	ReferrerUserID     int64
	NewReferrerBalance int64
	BillingEventID     int64
	AlreadyRewarded    bool
}

type ReferralRewardStore interface {
	ApplyReferralReward(context.Context, ReferralRewardInput) (ReferralRewardResult, error)
}

type referralRewardRow struct {
	ID             int64
	ReferrerUserID int64
	RefereeUserID  int64
	Status         string
}

var (
	insertReferralTopupCreditTx = insertTopupCreditTx
	errReferralRewardGateClosed = errors.New("payment: referral reward already recorded")
)

func (s *Service) ApplyReferralReward(ctx context.Context, input ReferralRewardInput) (ReferralRewardResult, error) {
	if s == nil || s.store == nil {
		return ReferralRewardResult{}, ErrStoreNotConfigured
	}
	input = normalizeReferralRewardInput(input)
	if err := validateReferralRewardInput(input); err != nil {
		return ReferralRewardResult{}, err
	}
	store, ok := s.store.(ReferralRewardStore)
	if !ok || store == nil {
		return ReferralRewardResult{}, ErrStoreNotConfigured
	}
	return store.ApplyReferralReward(ctx, input)
}

func (s *PostgresStore) ApplyReferralReward(ctx context.Context, input ReferralRewardInput) (ReferralRewardResult, error) {
	if s == nil || s.pool == nil {
		return ReferralRewardResult{}, ErrStoreNotConfigured
	}
	input = normalizeReferralRewardInput(input)
	if err := validateReferralRewardInput(input); err != nil {
		return ReferralRewardResult{}, err
	}
	var lastErr error
	for attempt := 0; attempt < fulfillTxRetryAttempts; attempt++ {
		result, err := s.applyReferralRewardOnce(ctx, input)
		if err == nil {
			return result, nil
		}
		if errors.Is(err, errReferralRewardGateClosed) {
			return s.existingReferralRewardResult(ctx, input)
		}
		if isPgRetryableTxConflict(err) || isUniqueViolation(err) {
			lastErr = err
			continue
		}
		return ReferralRewardResult{}, err
	}
	return ReferralRewardResult{}, fmt.Errorf("payment: referral reward exhausted retries: %w", lastErr)
}

func (s *PostgresStore) applyReferralRewardOnce(ctx context.Context, input ReferralRewardInput) (ReferralRewardResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ReferralRewardResult{}, fmt.Errorf("payment: begin referral reward: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	referral, changed, err := qualifyPendingReferralRewardTx(ctx, tx, input)
	if err != nil {
		return ReferralRewardResult{}, err
	}
	if !changed {
		result, err := existingReferralRewardTx(ctx, tx, input)
		if err != nil {
			return ReferralRewardResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ReferralRewardResult{}, fmt.Errorf("payment: commit referral reward replay: %w", err)
		}
		return result, nil
	}

	rewardID, inserted, err := insertReferralRewardGateTx(ctx, tx, input, referral)
	if err != nil {
		return ReferralRewardResult{}, err
	}
	if !inserted {
		return ReferralRewardResult{}, errReferralRewardGateClosed
	}
	if err := lockPaymentUserTx(ctx, tx, input.TenantID, referral.ReferrerUserID); err != nil {
		return ReferralRewardResult{}, err
	}

	requestKey := referralRewardRequestKey(input.TenantID, referral.ID)
	order, err := insertOrderTx(ctx, tx, createOrderRecord{
		TenantID:           input.TenantID,
		UserID:             referral.ReferrerUserID,
		OutTradeNo:         requestKey,
		AmountCents:        input.RewardCents,
		CurrencyCode:       input.CurrencyCode,
		ProviderKind:       ProviderManual,
		RequestFingerprint: referralRewardFingerprint,
		RequestID:          requestKey,
		OrderKind:          OrderKindTopup,
		Now:                input.Now,
	})
	if errors.Is(err, ErrIdempotencyConflict) || isUniqueViolation(err) {
		return ReferralRewardResult{}, ErrExternalTradeConflict
	}
	if err != nil {
		return ReferralRewardResult{}, fmt.Errorf("payment: insert referral reward order: %w", err)
	}
	for _, ev := range []auditInsert{
		{TenantID: input.TenantID, OrderID: order.ID, EventType: AuditOrderCreated, ActorKind: ActorKindUser, ActorID: referral.ReferrerUserID, ReasonClass: referralRewardFingerprint, RequestID: requestKey, Payload: map[string]any{"source": referralRewardFingerprint, "amount_cents": input.RewardCents, "referral_id": referral.ID}, Now: input.Now},
		{TenantID: input.TenantID, OrderID: order.ID, EventType: AuditPaidConfirmed, ActorKind: ActorKindUser, ActorID: referral.ReferrerUserID, ReasonClass: referralRewardFingerprint, RequestID: requestKey, Now: input.Now},
		{TenantID: input.TenantID, OrderID: order.ID, EventType: AuditFulfillmentStarted, ActorKind: ActorKindUser, ActorID: referral.ReferrerUserID, ReasonClass: referralRewardFingerprint, RequestID: requestKey, Now: input.Now},
	} {
		if err := insertAuditTx(ctx, tx, ev); err != nil {
			return ReferralRewardResult{}, err
		}
	}
	credit, billingID, err := insertReferralTopupCreditTx(ctx, tx, order, ActorKindUser, referral.ReferrerUserID, requestKey, input.Now)
	if err != nil {
		return ReferralRewardResult{}, err
	}
	row := tx.QueryRow(ctx, `
UPDATE payment_orders
SET status='completed',
    paid_at=$3,
    recharging_at=$3,
    completed_at=$3,
    updated_at=$3
WHERE tenant_id=$1 AND id=$2
RETURNING`+orderSelectColumns, input.TenantID, order.ID, input.Now)
	completed, err := scanOrder(row)
	if err != nil {
		return ReferralRewardResult{}, fmt.Errorf("payment: complete referral reward order: %w", err)
	}
	if err := insertAuditTx(ctx, tx, auditInsert{
		TenantID:    input.TenantID,
		OrderID:     completed.ID,
		EventType:   AuditCredited,
		ActorKind:   ActorKindUser,
		ActorID:     referral.ReferrerUserID,
		ReasonClass: referralRewardFingerprint,
		RequestID:   requestKey,
		Payload:     map[string]any{"amount_cents": completed.AmountCents, "credit_id": credit.ID, "billing_event_id": billingID, "referral_id": referral.ID, "reward_id": rewardID},
		Now:         input.Now,
	}); err != nil {
		return ReferralRewardResult{}, err
	}
	if _, err := tx.Exec(ctx, `
UPDATE referral_rewards
SET billing_event_id=$3,
    currency_code=$4
WHERE tenant_id=$1 AND id=$2`, input.TenantID, rewardID, billingID, input.CurrencyCode); err != nil {
		return ReferralRewardResult{}, fmt.Errorf("payment: link referral reward billing event: %w", err)
	}
	tag, err := tx.Exec(ctx, `
UPDATE referrals
SET status='rewarded'
WHERE tenant_id=$1 AND id=$2 AND status='qualified'`, input.TenantID, referral.ID)
	if err != nil {
		return ReferralRewardResult{}, fmt.Errorf("payment: mark referral rewarded: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ReferralRewardResult{}, fmt.Errorf("payment: mark referral rewarded affected %d rows", tag.RowsAffected())
	}
	balance, err := userBalanceTx(ctx, tx, input.TenantID, referral.ReferrerUserID)
	if err != nil {
		return ReferralRewardResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ReferralRewardResult{}, fmt.Errorf("payment: commit referral reward: %w", err)
	}
	return ReferralRewardResult{
		Rewarded:           true,
		ReferralID:         referral.ID,
		ReferrerUserID:     referral.ReferrerUserID,
		NewReferrerBalance: balance,
		BillingEventID:     billingID,
	}, nil
}

func qualifyPendingReferralRewardTx(ctx context.Context, tx pgx.Tx, input ReferralRewardInput) (referralRewardRow, bool, error) {
	var row referralRewardRow
	err := tx.QueryRow(ctx, `
UPDATE referrals
SET status='qualified',
    qualified_at=$4,
    first_billing_event_id=$3
WHERE tenant_id=$1
  AND referee_user_id=$2
  AND status='pending'
RETURNING id, referrer_user_id, referee_user_id, status`,
		input.TenantID, input.RefereeUserID, input.BillingEventID, input.Now).Scan(
		&row.ID, &row.ReferrerUserID, &row.RefereeUserID, &row.Status,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return referralRewardRow{}, false, nil
	}
	if err != nil {
		return referralRewardRow{}, false, fmt.Errorf("payment: qualify referral reward: %w", err)
	}
	return row, true, nil
}

func insertReferralRewardGateTx(ctx context.Context, tx pgx.Tx, input ReferralRewardInput, referral referralRewardRow) (int64, bool, error) {
	var rewardID int64
	err := tx.QueryRow(ctx, `
INSERT INTO referral_rewards (
	tenant_id, referrer_user_id, referee_user_id, referral_id,
	reward_type, amount_usd_micros, receipt_id, billing_event_id, currency_code, issued_at
) VALUES ($1, $2, $3, $4, 'credit', $5, NULL, NULL, $6, $7)
ON CONFLICT (tenant_id, referral_id) DO NOTHING
RETURNING id`,
		input.TenantID, referral.ReferrerUserID, referral.RefereeUserID, referral.ID,
		input.RewardCents*referralRewardMicrosPerCent, input.CurrencyCode, input.Now,
	).Scan(&rewardID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("payment: insert referral reward gate: %w", err)
	}
	return rewardID, true, nil
}

func existingReferralRewardTx(ctx context.Context, tx pgx.Tx, input ReferralRewardInput) (ReferralRewardResult, error) {
	var row referralRewardRow
	err := tx.QueryRow(ctx, `
SELECT id, referrer_user_id, referee_user_id, status
FROM referrals
WHERE tenant_id=$1 AND referee_user_id=$2`,
		input.TenantID, input.RefereeUserID,
	).Scan(&row.ID, &row.ReferrerUserID, &row.RefereeUserID, &row.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReferralRewardResult{}, nil
	}
	if err != nil {
		return ReferralRewardResult{}, fmt.Errorf("payment: read referral reward replay: %w", err)
	}
	result := ReferralRewardResult{
		ReferralID:     row.ID,
		ReferrerUserID: row.ReferrerUserID,
	}
	if row.ReferrerUserID > 0 {
		balance, err := userBalanceTx(ctx, tx, input.TenantID, row.ReferrerUserID)
		if err != nil {
			return ReferralRewardResult{}, err
		}
		result.NewReferrerBalance = balance
	}
	if row.Status == "rewarded" {
		result.AlreadyRewarded = true
		_ = tx.QueryRow(ctx, `
SELECT COALESCE(billing_event_id, 0)
FROM referral_rewards
WHERE tenant_id=$1 AND referral_id=$2`,
			input.TenantID, row.ID,
		).Scan(&result.BillingEventID)
	}
	return result, nil
}

func (s *PostgresStore) existingReferralRewardResult(ctx context.Context, input ReferralRewardInput) (ReferralRewardResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return ReferralRewardResult{}, fmt.Errorf("payment: begin referral reward replay read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := existingReferralRewardTx(ctx, tx, input)
	if err != nil {
		return ReferralRewardResult{}, err
	}
	if result.ReferralID != 0 {
		result.AlreadyRewarded = true
	}
	if err := tx.Commit(ctx); err != nil {
		return ReferralRewardResult{}, fmt.Errorf("payment: commit referral reward replay read: %w", err)
	}
	return result, nil
}

func normalizeReferralRewardInput(input ReferralRewardInput) ReferralRewardInput {
	input.CurrencyCode = strings.ToUpper(strings.TrimSpace(input.CurrencyCode))
	if input.CurrencyCode == "" {
		input.CurrencyCode = "USD"
	}
	if input.Now.IsZero() {
		input.Now = time.Now().UTC()
	} else {
		input.Now = input.Now.UTC()
	}
	return input
}

func validateReferralRewardInput(input ReferralRewardInput) error {
	if input.TenantID <= 0 || input.RefereeUserID <= 0 || input.BillingEventID <= 0 {
		return ErrInvalidInput
	}
	if input.CurrencyCode != "USD" {
		return ErrUnsupportedCurrency
	}
	if input.RewardCents <= 0 || input.RewardCents > maxAmountCents {
		return ErrInvalidAmount
	}
	if input.RewardCents > (1<<63-1)/referralRewardMicrosPerCent {
		return ErrInvalidAmount
	}
	return nil
}

func referralRewardRequestKey(tenantID, referralID int64) string {
	return fmt.Sprintf("referral:%d:%d", tenantID, referralID)
}
