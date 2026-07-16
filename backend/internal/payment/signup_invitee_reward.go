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
// 指纹 / 配置键
// ============================================================

const (
	signupBonusFingerprint   = "signup_bonus"
	inviteeRewardFingerprint = "invitee_reward"

	envSignupBonusUSDCents   = "HUAKAI_SIGNUP_BONUS_USD_CENTS"
	envInviteeRewardUSDCents = "HUAKAI_REFERRAL_INVITEE_USD_CENTS"
)

// ============================================================
// 配置
// ============================================================

// SignupInviteeConfig 持有两笔注册时钱包入账的奖励金额。
// 两者默认都为 0 (功能关闭)。**金额单位直接为美分(cents)= 钱包入账的最小单位**,正值即启用。
// 历史上配置用 USD micros 再 /10_000 换算成美分,会把非整万 micros 的亚分静默截断丢失;改为直接以
// 美分配置后从源头消除该截断入口。钱包入账本以美分为
// 最小单位,故"直接用美分"不丢任何可表达的奖励额。
type SignupInviteeConfig struct {
	// SignupBonusCents 是发给每位新注册用户的欢迎入账(美分)。
	// Env: HUAKAI_SIGNUP_BONUS_USD_CENTS (默认 0 = 禁用)。
	SignupBonusCents int64
	// ReferralInviteeCents 是注册时应用邀请绑定后, 发给新用户的入账(美分)。
	// Env: HUAKAI_REFERRAL_INVITEE_USD_CENTS (默认 0 = 禁用)。
	ReferralInviteeCents int64
}

// SignupInviteeConfigFromEnv 从环境变量读取配置。
// 缺失或为零值时功能保持禁用 (默认关闭的安全姿态)。
func SignupInviteeConfigFromEnv() SignupInviteeConfig {
	return SignupInviteeConfig{
		SignupBonusCents:     parseEnvCents(envSignupBonusUSDCents),
		ReferralInviteeCents: parseEnvCents(envInviteeRewardUSDCents),
	}
}

// parseEnvCents 读取一个非负整数美分配置;缺失/非法/负值一律回落到 0(禁用)。
func parseEnvCents(key string) int64 {
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

// ============================================================
// 结果类型
// ============================================================

// SignupBonusResult 由 IssueSignupBonus 返回。
type SignupBonusResult struct {
	Issued         bool  // 首次发放时为 true
	AlreadyIssued  bool  // 幂等重放时为 true
	NewBalance     int64 // 美分
	BillingEventID int64
}

// InviteeRewardResult 由 IssueInviteeReward 返回。
type InviteeRewardResult struct {
	Issued         bool
	AlreadyIssued  bool
	NewBalance     int64
	BillingEventID int64
}

// ============================================================
// Store 接口
// ============================================================

// SignupBonusStore 由 PostgresStore 实现。
type SignupBonusStore interface {
	ApplySignupBonus(context.Context, signupBonusInput) (SignupBonusResult, error)
}

// InviteeRewardStore 由 PostgresStore 实现。
type InviteeRewardStore interface {
	ApplyInviteeReward(context.Context, inviteeRewardInput) (InviteeRewardResult, error)
}

// ============================================================
// 内部输入类型
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
// 可注入的入账函数 (便于测试, 与 checkin/referral 同款)
// ============================================================

var insertSignupBonusTopupCreditTx = insertTopupCreditTx
var insertInviteeRewardTopupCreditTx = insertTopupCreditTx

// ============================================================
// IssueSignupBonus — Service 门面
// ============================================================

// IssueSignupBonus 给新用户发放一次性的欢迎钱包入账。
// 若 cfg.SignupBonusCents <= 0 则本次调用为 no-op (默认关闭的安全姿态)。
// 遇幂等冲突时错误被吞掉 — 对注册重试和 OAuth 重入是安全的。
func (s *Service) IssueSignupBonus(ctx context.Context, cfg SignupInviteeConfig, tenantID, userID int64) (SignupBonusResult, error) {
	if s == nil || s.store == nil {
		return SignupBonusResult{}, ErrStoreNotConfigured
	}
	cents := cfg.SignupBonusCents
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
// IssueInviteeReward — Service 门面
// ============================================================

// IssueInviteeReward 在注册时应用邀请绑定后, 给被邀请人 (新用户)
// 发放一次性的、基于邀请的钱包入账。
// 若 cfg.ReferralInviteeCents <= 0 则本次调用为 no-op。
func (s *Service) IssueInviteeReward(ctx context.Context, cfg SignupInviteeConfig, tenantID, userID int64) (InviteeRewardResult, error) {
	if s == nil || s.store == nil {
		return InviteeRewardResult{}, ErrStoreNotConfigured
	}
	cents := cfg.ReferralInviteeCents
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
			// out_trade_no 唯一约束冲突 = 已发放;按幂等处理。
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

	// 检查是否已存在订单(幂等重放)。
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
// Request key 辅助函数
// ============================================================

func signupBonusRequestKey(tenantID, userID int64) string {
	return fmt.Sprintf("signup_bonus:%d:%d", tenantID, userID)
}

func inviteeRewardRequestKey(tenantID, userID int64) string {
	return fmt.Sprintf("invitee_reward:%d:%d", tenantID, userID)
}

// ============================================================
// 辅助函数:检查 out_trade_no 是否已存在
// ============================================================

// orderExistsByOutTradeNoTx 在事务内,若该 tenant 下已存在带给定 out_trade_no 的
// payment_order,则返回 true。
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
