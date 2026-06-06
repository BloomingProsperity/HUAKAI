package payment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const checkinRewardFingerprint = "checkin_reward"

type CheckinRewardInput struct {
	TenantID     int64
	UserID       int64
	Date         time.Time
	RewardCents  int64
	CurrencyCode string
	Now          time.Time
}

type CheckinRewardResult struct {
	NewBalance       int64
	CheckinID        int64
	BillingEventID   int64
	RewardCents      int64
	AlreadyCheckedIn bool
}

type CheckinRewardStore interface {
	ApplyCheckinReward(context.Context, CheckinRewardInput) (CheckinRewardResult, error)
}

var insertCheckinTopupCreditTx = insertTopupCreditTx

func (s *Service) ApplyCheckinReward(ctx context.Context, input CheckinRewardInput) (CheckinRewardResult, error) {
	if s == nil || s.store == nil {
		return CheckinRewardResult{}, ErrStoreNotConfigured
	}
	input = normalizeCheckinRewardInput(input)
	if err := validateCheckinRewardInput(input); err != nil {
		return CheckinRewardResult{}, err
	}
	store, ok := s.store.(CheckinRewardStore)
	if !ok || store == nil {
		return CheckinRewardResult{}, ErrStoreNotConfigured
	}
	return store.ApplyCheckinReward(ctx, input)
}

func (s *PostgresStore) ApplyCheckinReward(ctx context.Context, input CheckinRewardInput) (CheckinRewardResult, error) {
	if s == nil || s.pool == nil {
		return CheckinRewardResult{}, ErrStoreNotConfigured
	}
	input = normalizeCheckinRewardInput(input)
	if err := validateCheckinRewardInput(input); err != nil {
		return CheckinRewardResult{}, err
	}
	var lastErr error
	for attempt := 0; attempt < fulfillTxRetryAttempts; attempt++ {
		result, err := s.applyCheckinRewardOnce(ctx, input)
		if err == nil {
			return result, nil
		}
		if isPgRetryableTxConflict(err) || isUniqueViolation(err) {
			lastErr = err
			continue
		}
		return CheckinRewardResult{}, err
	}
	return CheckinRewardResult{}, fmt.Errorf("payment: checkin reward exhausted retries: %w", lastErr)
}

func (s *PostgresStore) applyCheckinRewardOnce(ctx context.Context, input CheckinRewardInput) (CheckinRewardResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return CheckinRewardResult{}, fmt.Errorf("payment: begin checkin reward: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockPaymentUserTx(ctx, tx, input.TenantID, input.UserID); err != nil {
		return CheckinRewardResult{}, err
	}
	checkinID, inserted, err := insertDailyCheckinTx(ctx, tx, input)
	if err != nil {
		return CheckinRewardResult{}, err
	}
	if !inserted {
		result, err := existingCheckinRewardTx(ctx, tx, input)
		if err != nil {
			return CheckinRewardResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return CheckinRewardResult{}, fmt.Errorf("payment: commit checkin replay: %w", err)
		}
		return result, nil
	}

	requestKey := checkinRewardRequestKey(input)
	order, err := insertOrderTx(ctx, tx, createOrderRecord{
		TenantID:           input.TenantID,
		UserID:             input.UserID,
		OutTradeNo:         requestKey,
		AmountCents:        input.RewardCents,
		CurrencyCode:       input.CurrencyCode,
		ProviderKind:       ProviderManual,
		RequestFingerprint: checkinRewardFingerprint,
		RequestID:          requestKey,
		OrderKind:          OrderKindTopup,
		Now:                input.Now,
	})
	if errors.Is(err, ErrIdempotencyConflict) || isUniqueViolation(err) {
		return CheckinRewardResult{}, ErrExternalTradeConflict
	}
	if err != nil {
		return CheckinRewardResult{}, fmt.Errorf("payment: insert checkin reward order: %w", err)
	}
	for _, ev := range []auditInsert{
		{TenantID: input.TenantID, OrderID: order.ID, EventType: AuditOrderCreated, ActorKind: ActorKindUser, ActorID: input.UserID, ReasonClass: checkinRewardFingerprint, RequestID: requestKey, Payload: map[string]any{"source": checkinRewardFingerprint, "amount_cents": input.RewardCents}, Now: input.Now},
		{TenantID: input.TenantID, OrderID: order.ID, EventType: AuditPaidConfirmed, ActorKind: ActorKindUser, ActorID: input.UserID, ReasonClass: checkinRewardFingerprint, RequestID: requestKey, Now: input.Now},
		{TenantID: input.TenantID, OrderID: order.ID, EventType: AuditFulfillmentStarted, ActorKind: ActorKindUser, ActorID: input.UserID, ReasonClass: checkinRewardFingerprint, RequestID: requestKey, Now: input.Now},
	} {
		if err := insertAuditTx(ctx, tx, ev); err != nil {
			return CheckinRewardResult{}, err
		}
	}
	credit, billingID, err := insertCheckinTopupCreditTx(ctx, tx, order, ActorKindUser, input.UserID, requestKey, input.Now)
	if err != nil {
		return CheckinRewardResult{}, err
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
		return CheckinRewardResult{}, fmt.Errorf("payment: complete checkin reward order: %w", err)
	}
	if err := insertAuditTx(ctx, tx, auditInsert{
		TenantID:    input.TenantID,
		OrderID:     completed.ID,
		EventType:   AuditCredited,
		ActorKind:   ActorKindUser,
		ActorID:     input.UserID,
		ReasonClass: checkinRewardFingerprint,
		RequestID:   requestKey,
		Payload:     map[string]any{"amount_cents": completed.AmountCents, "credit_id": credit.ID, "billing_event_id": billingID},
		Now:         input.Now,
	}); err != nil {
		return CheckinRewardResult{}, err
	}
	if _, err := tx.Exec(ctx, `
UPDATE daily_checkin
SET billing_event_id=$3
WHERE tenant_id=$1 AND id=$2`, input.TenantID, checkinID, billingID); err != nil {
		return CheckinRewardResult{}, fmt.Errorf("payment: link checkin billing event: %w", err)
	}
	balance, err := userBalanceTx(ctx, tx, input.TenantID, input.UserID)
	if err != nil {
		return CheckinRewardResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CheckinRewardResult{}, fmt.Errorf("payment: commit checkin reward: %w", err)
	}
	return CheckinRewardResult{
		NewBalance:     balance,
		CheckinID:      checkinID,
		BillingEventID: billingID,
		RewardCents:    input.RewardCents,
	}, nil
}

func insertDailyCheckinTx(ctx context.Context, tx pgx.Tx, input CheckinRewardInput) (int64, bool, error) {
	var id int64
	err := tx.QueryRow(ctx, `
INSERT INTO daily_checkin (tenant_id, user_id, checkin_date, reward_cents, currency_code, created_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (tenant_id, user_id, checkin_date) DO NOTHING
RETURNING id`, input.TenantID, input.UserID, input.Date, input.RewardCents, input.CurrencyCode, input.Now).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("payment: insert daily checkin: %w", err)
	}
	return id, true, nil
}

func existingCheckinRewardTx(ctx context.Context, tx pgx.Tx, input CheckinRewardInput) (CheckinRewardResult, error) {
	var result CheckinRewardResult
	err := tx.QueryRow(ctx, `
SELECT id, reward_cents, COALESCE(billing_event_id, 0)
FROM daily_checkin
WHERE tenant_id=$1 AND user_id=$2 AND checkin_date=$3`,
		input.TenantID, input.UserID, input.Date,
	).Scan(&result.CheckinID, &result.RewardCents, &result.BillingEventID)
	if err != nil {
		return CheckinRewardResult{}, fmt.Errorf("payment: read existing daily checkin: %w", err)
	}
	balance, err := userBalanceTx(ctx, tx, input.TenantID, input.UserID)
	if err != nil {
		return CheckinRewardResult{}, err
	}
	result.NewBalance = balance
	result.AlreadyCheckedIn = true
	return result, nil
}

func normalizeCheckinRewardInput(input CheckinRewardInput) CheckinRewardInput {
	input.CurrencyCode = strings.ToUpper(strings.TrimSpace(input.CurrencyCode))
	if input.CurrencyCode == "" {
		input.CurrencyCode = "USD"
	}
	input.Date = normalizeCheckinDate(input.Date)
	if input.Now.IsZero() {
		input.Now = time.Now().UTC()
	} else {
		input.Now = input.Now.UTC()
	}
	return input
}

func validateCheckinRewardInput(input CheckinRewardInput) error {
	if input.TenantID <= 0 || input.UserID <= 0 || input.Date.IsZero() {
		return ErrInvalidInput
	}
	if input.CurrencyCode != "USD" {
		return ErrUnsupportedCurrency
	}
	if input.RewardCents < 0 || input.RewardCents > maxAmountCents {
		return ErrInvalidAmount
	}
	return nil
}

func normalizeCheckinDate(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func checkinRewardRequestKey(input CheckinRewardInput) string {
	return fmt.Sprintf("checkin:%d:%d:%s", input.TenantID, input.UserID, input.Date.Format("2006-01-02"))
}
