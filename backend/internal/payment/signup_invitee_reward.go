package payment

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ============================================================
// Fingerprints / config keys
// ============================================================

const (
	signupBonusFingerprint   = "signup_bonus"
	inviteeRewardFingerprint = "invitee_reward"

	envSignupBonusUSDMicros   = "HUAKAI_SIGNUP_BONUS_USD_MICROS"
	envInviteeRewardUSDMicros = "HUAKAI_REFERRAL_INVITEE_USD_MICROS"

	// signupInviteeMicrosPerCent: 1 USD = 100 cents = 1 000 000 micros => 1 cent = 10 000 micros
	signupInviteeMicrosPerCent = 10_000
)

// ============================================================
// Config
// ============================================================

// SignupInviteeConfig holds the bonus/reward amounts for the two signup-time
// wallet credits. Both default to 0 (feature off). Amounts are in USD micros;
// a positive value enables the credit.
type SignupInviteeConfig struct {
	// SignupBonusUSDMicros is the welcome credit issued to every new registrant.
	// Env: HUAKAI_SIGNUP_BONUS_USD_MICROS (default 0 = disabled).
	SignupBonusUSDMicros int64
	// ReferralInviteeUSDMicros is the credit issued to the new user when an
	// invite binding is applied at registration.
	// Env: HUAKAI_REFERRAL_INVITEE_USD_MICROS (default 0 = disabled).
	ReferralInviteeUSDMicros int64
}

// SignupInviteeConfigFromEnv reads config from environment variables.
// Missing or zero values leave the feature disabled (default-OFF safety).
func SignupInviteeConfigFromEnv() SignupInviteeConfig {
	return SignupInviteeConfig{
		SignupBonusUSDMicros:     parseEnvMicros(envSignupBonusUSDMicros),
		ReferralInviteeUSDMicros: parseEnvMicros(envInviteeRewardUSDMicros),
	}
}

func parseEnvMicros(key string) int64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// signupInviteeCents converts USD micros to cents (integer division).
func signupInviteeCents(micros int64) int64 {
	return micros / signupInviteeMicrosPerCent
}

// ============================================================
// Result types
// ============================================================

// SignupBonusResult is returned by IssueSignupBonus.
type SignupBonusResult struct {
	Issued         bool  // true on first issue
	AlreadyIssued  bool  // true when idempotent replay
	NewBalance     int64 // cents
	BillingEventID int64
}

// InviteeRewardResult is returned by IssueInviteeReward.
type InviteeRewardResult struct {
	Issued         bool
	AlreadyIssued  bool
	NewBalance     int64
	BillingEventID int64
}

// ============================================================
// Store interfaces
// ============================================================

// SignupBonusStore is implemented by PostgresStore.
type SignupBonusStore interface {
	ApplySignupBonus(context.Context, signupBonusInput) (SignupBonusResult, error)
}

// InviteeRewardStore is implemented by PostgresStore.
type InviteeRewardStore interface {
	ApplyInviteeReward(context.Context, inviteeRewardInput) (InviteeRewardResult, error)
}

// ============================================================
// Internal input types
// ============================================================

type signupBonusInput struct {
	TenantID    int64
	UserID      int64
	RewardCents int64
	Now         time.Time
}

type inviteeRewardInput struct {
	TenantID    int64
	UserID      int64
	RewardCents int64
	Now         time.Time
}

// ============================================================
// Injectable credit functions (testable, mirrors checkin/referral)
// ============================================================

var insertSignupBonusTopupCreditTx = insertTopupCreditTx
var insertInviteeRewardTopupCreditTx = insertTopupCreditTx

// ============================================================
// IssueSignupBonus — Service facade
// ============================================================

// IssueSignupBonus issues a one-time welcome wallet credit to a new user.
// If cfg.SignupBonusUSDMicros <= 0 the call is a no-op (default-OFF safety).
// On idempotency conflict the error is suppressed — safe for registration
// retries and OAuth re-entry.
func (s *Service) IssueSignupBonus(ctx context.Context, cfg SignupInviteeConfig, tenantID, userID int64) (SignupBonusResult, error) {
	if s == nil || s.store == nil {
		return SignupBonusResult{}, ErrStoreNotConfigured
	}
	cents := signupInviteeCents(cfg.SignupBonusUSDMicros)
	if cents <= 0 {
		return SignupBonusResult{}, nil
	}
	now := s.now()
	input := signupBonusInput{
		TenantID:    tenantID,
		UserID:      userID,
		RewardCents: cents,
		Now:         now,
	}
	store, ok := s.store.(SignupBonusStore)
	if !ok || store == nil {
		return SignupBonusResult{}, ErrStoreNotConfigured
	}
	return store.ApplySignupBonus(ctx, input)
}

// ============================================================
// IssueInviteeReward — Service facade
// ============================================================

// IssueInviteeReward issues a one-time invite-based wallet credit to the
// referee (new user) when an invite binding is applied at registration.
// If cfg.ReferralInviteeUSDMicros <= 0 the call is a no-op.
func (s *Service) IssueInviteeReward(ctx context.Context, cfg SignupInviteeConfig, tenantID, userID int64) (InviteeRewardResult, error) {
	if s == nil || s.store == nil {
		return InviteeRewardResult{}, ErrStoreNotConfigured
	}
	cents := signupInviteeCents(cfg.ReferralInviteeUSDMicros)
	if cents <= 0 {
		return InviteeRewardResult{}, nil
	}
	now := s.now()
	input := inviteeRewardInput{
		TenantID:    tenantID,
		UserID:      userID,
		RewardCents: cents,
		Now:         now,
	}
	store, ok := s.store.(InviteeRewardStore)
	if !ok || store == nil {
		return InviteeRewardResult{}, ErrStoreNotConfigured
	}
	return store.ApplyInviteeReward(ctx, input)
}

// ============================================================
// PostgresStore — ApplySignupBonus
// ============================================================

func (s *PostgresStore) ApplySignupBonus(ctx context.Context, input signupBonusInput) (SignupBonusResult, error) {
	if s == nil || s.pool == nil {
		return SignupBonusResult{}, ErrStoreNotConfigured
	}
	var lastErr error
	for attempt := 0; attempt < fulfillTxRetryAttempts; attempt++ {
		result, err := s.applySignupBonusOnce(ctx, input)
		if err == nil {
			return result, nil
		}
		if isPgRetryableTxConflict(err) {
			lastErr = err
			continue
		}
		if isUniqueViolation(err) {
			// out_trade_no unique conflict = already issued; treat as idempotent.
			return SignupBonusResult{AlreadyIssued: true}, nil
		}
		return SignupBonusResult{}, err
	}
	return SignupBonusResult{}, fmt.Errorf("payment: signup bonus exhausted retries: %w", lastErr)
}

func (s *PostgresStore) applySignupBonusOnce(ctx context.Context, input signupBonusInput) (SignupBonusResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return SignupBonusResult{}, fmt.Errorf("payment: begin signup bonus: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockPaymentUserTx(ctx, tx, input.TenantID, input.UserID); err != nil {
		return SignupBonusResult{}, err
	}

	requestKey := signupBonusRequestKey(input.TenantID, input.UserID)

	// Check for existing order (idempotency replay).
	found, err := orderExistsByOutTradeNoTx(ctx, tx, input.TenantID, requestKey)
	if err != nil {
		return SignupBonusResult{}, err
	}
	if found {
		balance, _ := userBalanceTx(ctx, tx, input.TenantID, input.UserID)
		if err := tx.Commit(ctx); err != nil {
			return SignupBonusResult{}, fmt.Errorf("payment: commit signup bonus replay: %w", err)
		}
		return SignupBonusResult{AlreadyIssued: true, NewBalance: balance}, nil
	}

	order, err := insertOrderTx(ctx, tx, createOrderRecord{
		TenantID:           input.TenantID,
		UserID:             input.UserID,
		OutTradeNo:         requestKey,
		AmountCents:        input.RewardCents,
		CurrencyCode:       "USD",
		ProviderKind:       ProviderManual,
		RequestFingerprint: signupBonusFingerprint,
		RequestID:          requestKey,
		OrderKind:          OrderKindTopup,
		Now:                input.Now,
	})
	if errors.Is(err, ErrIdempotencyConflict) || isUniqueViolation(err) {
		return SignupBonusResult{AlreadyIssued: true}, nil
	}
	if err != nil {
		return SignupBonusResult{}, fmt.Errorf("payment: insert signup bonus order: %w", err)
	}
	for _, ev := range []auditInsert{
		{TenantID: input.TenantID, OrderID: order.ID, EventType: AuditOrderCreated, ActorKind: ActorKindUser, ActorID: input.UserID, ReasonClass: signupBonusFingerprint, RequestID: requestKey, Payload: map[string]any{"source": signupBonusFingerprint, "amount_cents": input.RewardCents}, Now: input.Now},
		{TenantID: input.TenantID, OrderID: order.ID, EventType: AuditPaidConfirmed, ActorKind: ActorKindUser, ActorID: input.UserID, ReasonClass: signupBonusFingerprint, RequestID: requestKey, Now: input.Now},
		{TenantID: input.TenantID, OrderID: order.ID, EventType: AuditFulfillmentStarted, ActorKind: ActorKindUser, ActorID: input.UserID, ReasonClass: signupBonusFingerprint, RequestID: requestKey, Now: input.Now},
	} {
		if err := insertAuditTx(ctx, tx, ev); err != nil {
			return SignupBonusResult{}, err
		}
	}
	credit, billingID, err := insertSignupBonusTopupCreditTx(ctx, tx, order, ActorKindUser, input.UserID, requestKey, input.Now)
	if err != nil {
		return SignupBonusResult{}, err
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
		return SignupBonusResult{}, fmt.Errorf("payment: complete signup bonus order: %w", err)
	}
	if err := insertAuditTx(ctx, tx, auditInsert{
		TenantID:    input.TenantID,
		OrderID:     completed.ID,
		EventType:   AuditCredited,
		ActorKind:   ActorKindUser,
		ActorID:     input.UserID,
		ReasonClass: signupBonusFingerprint,
		RequestID:   requestKey,
		Payload:     map[string]any{"amount_cents": completed.AmountCents, "credit_id": credit.ID, "billing_event_id": billingID},
		Now:         input.Now,
	}); err != nil {
		return SignupBonusResult{}, err
	}
	balance, err := userBalanceTx(ctx, tx, input.TenantID, input.UserID)
	if err != nil {
		return SignupBonusResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SignupBonusResult{}, fmt.Errorf("payment: commit signup bonus: %w", err)
	}
	return SignupBonusResult{
		Issued:         true,
		NewBalance:     balance,
		BillingEventID: billingID,
	}, nil
}

// ============================================================
// PostgresStore — ApplyInviteeReward
// ============================================================

func (s *PostgresStore) ApplyInviteeReward(ctx context.Context, input inviteeRewardInput) (InviteeRewardResult, error) {
	if s == nil || s.pool == nil {
		return InviteeRewardResult{}, ErrStoreNotConfigured
	}
	var lastErr error
	for attempt := 0; attempt < fulfillTxRetryAttempts; attempt++ {
		result, err := s.applyInviteeRewardOnce(ctx, input)
		if err == nil {
			return result, nil
		}
		if isPgRetryableTxConflict(err) {
			lastErr = err
			continue
		}
		if isUniqueViolation(err) {
			return InviteeRewardResult{AlreadyIssued: true}, nil
		}
		return InviteeRewardResult{}, err
	}
	return InviteeRewardResult{}, fmt.Errorf("payment: invitee reward exhausted retries: %w", lastErr)
}

func (s *PostgresStore) applyInviteeRewardOnce(ctx context.Context, input inviteeRewardInput) (InviteeRewardResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return InviteeRewardResult{}, fmt.Errorf("payment: begin invitee reward: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockPaymentUserTx(ctx, tx, input.TenantID, input.UserID); err != nil {
		return InviteeRewardResult{}, err
	}

	requestKey := inviteeRewardRequestKey(input.TenantID, input.UserID)

	found, err := orderExistsByOutTradeNoTx(ctx, tx, input.TenantID, requestKey)
	if err != nil {
		return InviteeRewardResult{}, err
	}
	if found {
		balance, _ := userBalanceTx(ctx, tx, input.TenantID, input.UserID)
		if err := tx.Commit(ctx); err != nil {
			return InviteeRewardResult{}, fmt.Errorf("payment: commit invitee reward replay: %w", err)
		}
		return InviteeRewardResult{AlreadyIssued: true, NewBalance: balance}, nil
	}

	order, err := insertOrderTx(ctx, tx, createOrderRecord{
		TenantID:           input.TenantID,
		UserID:             input.UserID,
		OutTradeNo:         requestKey,
		AmountCents:        input.RewardCents,
		CurrencyCode:       "USD",
		ProviderKind:       ProviderManual,
		RequestFingerprint: inviteeRewardFingerprint,
		RequestID:          requestKey,
		OrderKind:          OrderKindTopup,
		Now:                input.Now,
	})
	if errors.Is(err, ErrIdempotencyConflict) || isUniqueViolation(err) {
		return InviteeRewardResult{AlreadyIssued: true}, nil
	}
	if err != nil {
		return InviteeRewardResult{}, fmt.Errorf("payment: insert invitee reward order: %w", err)
	}
	for _, ev := range []auditInsert{
		{TenantID: input.TenantID, OrderID: order.ID, EventType: AuditOrderCreated, ActorKind: ActorKindUser, ActorID: input.UserID, ReasonClass: inviteeRewardFingerprint, RequestID: requestKey, Payload: map[string]any{"source": inviteeRewardFingerprint, "amount_cents": input.RewardCents}, Now: input.Now},
		{TenantID: input.TenantID, OrderID: order.ID, EventType: AuditPaidConfirmed, ActorKind: ActorKindUser, ActorID: input.UserID, ReasonClass: inviteeRewardFingerprint, RequestID: requestKey, Now: input.Now},
		{TenantID: input.TenantID, OrderID: order.ID, EventType: AuditFulfillmentStarted, ActorKind: ActorKindUser, ActorID: input.UserID, ReasonClass: inviteeRewardFingerprint, RequestID: requestKey, Now: input.Now},
	} {
		if err := insertAuditTx(ctx, tx, ev); err != nil {
			return InviteeRewardResult{}, err
		}
	}
	credit, billingID, err := insertInviteeRewardTopupCreditTx(ctx, tx, order, ActorKindUser, input.UserID, requestKey, input.Now)
	if err != nil {
		return InviteeRewardResult{}, err
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
		return InviteeRewardResult{}, fmt.Errorf("payment: complete invitee reward order: %w", err)
	}
	if err := insertAuditTx(ctx, tx, auditInsert{
		TenantID:    input.TenantID,
		OrderID:     completed.ID,
		EventType:   AuditCredited,
		ActorKind:   ActorKindUser,
		ActorID:     input.UserID,
		ReasonClass: inviteeRewardFingerprint,
		RequestID:   requestKey,
		Payload:     map[string]any{"amount_cents": completed.AmountCents, "credit_id": credit.ID, "billing_event_id": billingID},
		Now:         input.Now,
	}); err != nil {
		return InviteeRewardResult{}, err
	}
	balance, err := userBalanceTx(ctx, tx, input.TenantID, input.UserID)
	if err != nil {
		return InviteeRewardResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return InviteeRewardResult{}, fmt.Errorf("payment: commit invitee reward: %w", err)
	}
	return InviteeRewardResult{
		Issued:         true,
		NewBalance:     balance,
		BillingEventID: billingID,
	}, nil
}

// ============================================================
// Request key helpers
// ============================================================

func signupBonusRequestKey(tenantID, userID int64) string {
	return fmt.Sprintf("signup_bonus:%d:%d", tenantID, userID)
}

func inviteeRewardRequestKey(tenantID, userID int64) string {
	return fmt.Sprintf("invitee_reward:%d:%d", tenantID, userID)
}

// ============================================================
// Helper: check whether out_trade_no already exists
// ============================================================

// orderExistsByOutTradeNoTx returns true if a payment_order with the given
// out_trade_no already exists for the tenant (within a transaction).
func orderExistsByOutTradeNoTx(ctx context.Context, tx pgx.Tx, tenantID int64, outTradeNo string) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM payment_orders WHERE tenant_id=$1 AND out_trade_no=$2)`,
		tenantID, outTradeNo,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("payment: check out_trade_no existence: %w", err)
	}
	return exists, nil
}
