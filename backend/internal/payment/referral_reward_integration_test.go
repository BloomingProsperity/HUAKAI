//go:build integration_pg

package payment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestReferralRewardIssuedOnceOnQualify(t *testing.T) {
	// 变异:去掉唯一推荐奖励闸门或 status='pending' 资格守卫 -> 本测试会重复创建奖励/支付事实。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	referral := f.seedPendingReferral(ctx, pool)
	svc := NewService(NewPostgresStore(pool))
	now := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)

	res, err := svc.ApplyReferralReward(ctx, ReferralRewardInput{
		TenantID:       f.tenantA,
		RefereeUserID:  referral.refereeID,
		BillingEventID: 4242,
		RewardCents:    50,
		CurrencyCode:   "USD",
		Now:            now,
	})
	if err != nil {
		t.Fatalf("ApplyReferralReward: %v", err)
	}
	if !res.Rewarded || res.AlreadyRewarded {
		t.Fatalf("result rewarded=%v already=%v want rewarded once", res.Rewarded, res.AlreadyRewarded)
	}
	if res.ReferralID != referral.referralID || res.ReferrerUserID != f.userA || res.NewReferrerBalance != 50 || res.BillingEventID <= 0 {
		t.Fatalf("result=%+v want referral=%d referrer=%d balance=50 billing_event>0", res, referral.referralID, f.userA)
	}
	assertReferralRewardState(t, ctx, pool, f.tenantA, referral.referralID, f.userA, referral.refereeID, 4242, 50, res.BillingEventID)
}

func TestReferralRewardIdempotent(t *testing.T) {
	// 变异:移除 ON CONFLICT (tenant_id, referral_id) 或改动"已奖励"检测 -> 第二次调用会给推荐人重复入账。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	referral := f.seedPendingReferral(ctx, pool)
	svc := NewService(NewPostgresStore(pool))
	input := ReferralRewardInput{
		TenantID:       f.tenantA,
		RefereeUserID:  referral.refereeID,
		BillingEventID: 4243,
		RewardCents:    50,
		CurrencyCode:   "USD",
		Now:            time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC),
	}

	first, err := svc.ApplyReferralReward(ctx, input)
	if err != nil {
		t.Fatalf("first ApplyReferralReward: %v", err)
	}
	second, err := svc.ApplyReferralReward(ctx, input)
	if err != nil {
		t.Fatalf("second ApplyReferralReward: %v", err)
	}
	if !first.Rewarded || !second.AlreadyRewarded || second.Rewarded {
		t.Fatalf("first=%+v second=%+v want first rewarded and second already-rewarded no-op", first, second)
	}
	if got := countReferralRewards(t, ctx, pool, f.tenantA, referral.referralID); got != 1 {
		t.Fatalf("referral_rewards rows=%d want 1", got)
	}
	if got := sumPaymentCredits(t, ctx, pool, f.tenantA, f.userA); got != 50 {
		t.Fatalf("referrer credited cents=%d want exactly one 50-cent reward", got)
	}
	if got := countReferralRewardBillingEvents(t, ctx, pool, f.tenantA, referral.referralID); got != 1 {
		t.Fatalf("referral reward billing events=%d want 1", got)
	}
}

func TestReferralRewardAtomic_CreditFailureRollsBack(t *testing.T) {
	// 变异:在入账事务之外插入 referral_rewards 或设置 referrals.status,会在本次强制入账失败后残留记录。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	referral := f.seedPendingReferral(ctx, pool)
	orig := insertReferralTopupCreditTx
	insertReferralTopupCreditTx = func(context.Context, pgx.Tx, Order, string, int64, string, time.Time) (CreditRecord, int64, error) {
		return CreditRecord{}, 0, errors.New("forced referral credit failure")
	}
	t.Cleanup(func() { insertReferralTopupCreditTx = orig })
	svc := NewService(NewPostgresStore(pool))

	_, err := svc.ApplyReferralReward(ctx, ReferralRewardInput{
		TenantID:       f.tenantA,
		RefereeUserID:  referral.refereeID,
		BillingEventID: 4244,
		RewardCents:    50,
		CurrencyCode:   "USD",
		Now:            time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("ApplyReferralReward with forced credit failure returned nil error")
	}
	if status := referralStatus(t, ctx, pool, f.tenantA, referral.referralID); status != "pending" {
		t.Fatalf("referral status after failed credit=%q want pending rollback", status)
	}
	if got := countReferralRewards(t, ctx, pool, f.tenantA, referral.referralID); got != 0 {
		t.Fatalf("referral_rewards rows after failed credit=%d want 0", got)
	}
	if got := sumPaymentCredits(t, ctx, pool, f.tenantA, f.userA); got != 0 {
		t.Fatalf("referrer credited cents after failed credit=%d want 0", got)
	}
	if got := countReferralRewardOrders(t, ctx, pool, f.tenantA, referral.referralID); got != 0 {
		t.Fatalf("referral reward payment orders after failed credit=%d want 0", got)
	}
}

func TestReferralRewardAmount(t *testing.T) {
	// 变异:写死默认奖励金额而非使用 RewardCents -> balance 与 amount_usd_micros 断言变红。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	referral := f.seedPendingReferral(ctx, pool)
	svc := NewService(NewPostgresStore(pool))

	res, err := svc.ApplyReferralReward(ctx, ReferralRewardInput{
		TenantID:       f.tenantA,
		RefereeUserID:  referral.refereeID,
		BillingEventID: 4245,
		RewardCents:    73,
		CurrencyCode:   "USD",
		Now:            time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("ApplyReferralReward amount: %v", err)
	}
	assertReferralRewardState(t, ctx, pool, f.tenantA, referral.referralID, f.userA, referral.refereeID, 4245, 73, res.BillingEventID)
	if got := sumPaymentCredits(t, ctx, pool, f.tenantA, f.userA); got != 73 {
		t.Fatalf("referrer credited cents=%d want configured 73", got)
	}
}

type referralRewardSeed struct {
	refereeID  int64
	referralID int64
}

func (f *paymentFixture) seedPendingReferral(ctx context.Context, pool *pgxpool.Pool) referralRewardSeed {
	f.t.Helper()
	var refereeID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		f.tenantA, "referee-"+f.suffix).Scan(&refereeID); err != nil {
		f.t.Fatalf("seed referee: %v", err)
	}
	var invitationID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO invitations (tenant_id, code, inviter_user_id, created_at, max_usage)
VALUES ($1, $2, $3, $4, 1)
RETURNING id`, f.tenantA, "ref-"+f.suffix, f.userA, time.Date(2026, 6, 6, 9, 0, 0, 0, time.UTC)).Scan(&invitationID); err != nil {
		f.t.Fatalf("seed invitation: %v", err)
	}
	var referralID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO referrals (tenant_id, referee_user_id, referrer_user_id, invitation_id, status, created_at)
VALUES ($1, $2, $3, $4, 'pending', $5)
RETURNING id`, f.tenantA, refereeID, f.userA, invitationID, time.Date(2026, 6, 6, 9, 5, 0, 0, time.UTC)).Scan(&referralID); err != nil {
		f.t.Fatalf("seed referral: %v", err)
	}
	return referralRewardSeed{refereeID: refereeID, referralID: referralID}
}

func assertReferralRewardState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, referralID, referrerID, refereeID, firstBillingEventID, rewardCents, rewardBillingEventID int64) {
	t.Helper()
	var status string
	var gotFirstBillingEventID int64
	if err := pool.QueryRow(ctx, `
SELECT status, COALESCE(first_billing_event_id, 0)
FROM referrals
WHERE tenant_id=$1 AND id=$2`, tenantID, referralID).Scan(&status, &gotFirstBillingEventID); err != nil {
		t.Fatalf("read referral: %v", err)
	}
	if status != "rewarded" || gotFirstBillingEventID != firstBillingEventID {
		t.Fatalf("referral status=%q first_billing_event_id=%d want rewarded/%d", status, gotFirstBillingEventID, firstBillingEventID)
	}
	var rewardType, currency string
	var amountMicros, gotRewardBillingEventID int64
	var receiptNull bool
	if err := pool.QueryRow(ctx, `
SELECT reward_type, amount_usd_micros, receipt_id IS NULL, COALESCE(billing_event_id, 0), currency_code
FROM referral_rewards
WHERE tenant_id=$1 AND referral_id=$2 AND referrer_user_id=$3 AND referee_user_id=$4`,
		tenantID, referralID, referrerID, refereeID).Scan(&rewardType, &amountMicros, &receiptNull, &gotRewardBillingEventID, &currency); err != nil {
		t.Fatalf("read referral reward: %v", err)
	}
	if rewardType != "credit" || amountMicros != rewardCents*10000 || !receiptNull || gotRewardBillingEventID != rewardBillingEventID || currency != "USD" {
		t.Fatalf("reward fact type=%q amount=%d receiptNull=%v billing=%d currency=%q want credit/%d/null/%d/USD",
			rewardType, amountMicros, receiptNull, gotRewardBillingEventID, currency, rewardCents*10000, rewardBillingEventID)
	}
}

func referralStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, referralID int64) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM referrals WHERE tenant_id=$1 AND id=$2`, tenantID, referralID).Scan(&status); err != nil {
		t.Fatalf("read referral status: %v", err)
	}
	return status
}

func countReferralRewards(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, referralID int64) int64 {
	t.Helper()
	return queryReferralRewardInt(t, ctx, pool, `SELECT count(*) FROM referral_rewards WHERE tenant_id=$1 AND referral_id=$2`, tenantID, referralID)
}

func countReferralRewardOrders(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, referralID int64) int64 {
	t.Helper()
	return queryReferralRewardInt(t, ctx, pool, `SELECT count(*) FROM payment_orders WHERE tenant_id=$1 AND out_trade_no=$2`, tenantID, referralRewardRequestKey(tenantID, referralID))
}

func countReferralRewardBillingEvents(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, referralID int64) int64 {
	t.Helper()
	return queryReferralRewardInt(t, ctx, pool, `
SELECT count(*)
FROM billing_events be
JOIN payment_credits pc ON pc.tenant_id=be.tenant_id AND pc.billing_event_id=be.id
JOIN payment_orders po ON po.tenant_id=pc.tenant_id AND po.id=pc.payment_order_id
WHERE be.tenant_id=$1
  AND be.event_type='payment_credited'
  AND po.request_fingerprint='referral_reward'
  AND po.out_trade_no=$2`, tenantID, referralRewardRequestKey(tenantID, referralID))
}

func sumPaymentCredits(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID int64) int64 {
	t.Helper()
	return queryReferralRewardInt(t, ctx, pool, `SELECT COALESCE(SUM(amount_cents), 0)::bigint FROM payment_credits WHERE tenant_id=$1 AND user_id=$2`, tenantID, userID)
}

func queryReferralRewardInt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n
}
