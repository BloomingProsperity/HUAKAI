//go:build integration_pg

package payment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// ============================================================
// 注册奖励(Signup Bonus)测试
// ============================================================

func TestSignupBonusDefaultOff(t *testing.T) {
	// 变异:去掉 IssueSignupBonus 里的 cents<=0 守卫 -> 即使 amount=0 也发放 credit。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool))

	cfg := SignupInviteeConfig{SignupBonusUSDMicros: 0} // 默认关闭
	res, err := svc.IssueSignupBonus(ctx, cfg, f.tenantA, f.userA)
	if err != nil {
		t.Fatalf("IssueSignupBonus amount=0: %v", err)
	}
	if res.Issued || res.AlreadyIssued {
		t.Fatalf("IssueSignupBonus amount=0: want no-op, got issued=%v alreadyIssued=%v", res.Issued, res.AlreadyIssued)
	}
	if got := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1 AND user_id=$2`, f.tenantA, f.userA); got != 0 {
		t.Fatalf("payment_credits after amount=0 signup bonus=%d want 0", got)
	}
}

func TestSignupBonusIssuedOnce(t *testing.T) {
	// 变异:去掉 insertOrderTx 的唯一约束或 out_trade_no 指纹 -> 可能重复发放。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool))
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	cfg := SignupInviteeConfig{SignupBonusUSDMicros: 500_000} // 50 美分
	res, err := svc.IssueSignupBonus(ctx, cfg, f.tenantA, f.userA)
	if err != nil {
		t.Fatalf("IssueSignupBonus: %v", err)
	}
	if !res.Issued || res.AlreadyIssued {
		t.Fatalf("first call: want Issued=true AlreadyIssued=false, got %+v", res)
	}
	if res.NewBalance != 50 {
		t.Fatalf("balance=%d want 50", res.NewBalance)
	}
	if res.BillingEventID <= 0 {
		t.Fatalf("billing_event_id=%d want >0", res.BillingEventID)
	}
	// 校验 DB 状态
	if got := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1 AND user_id=$2`, f.tenantA, f.userA); got != 1 {
		t.Fatalf("payment_credits=%d want 1", got)
	}
	if got := sumPaymentCredits(t, ctx, pool, f.tenantA, f.userA); got != 50 {
		t.Fatalf("sum credits=%d want 50", got)
	}
	// 校验指纹
	if got := f.countInt(`SELECT count(*) FROM payment_orders WHERE tenant_id=$1 AND out_trade_no=$2`,
		f.tenantA, signupBonusRequestKey(f.tenantA, f.userA)); got != 1 {
		t.Fatalf("orders with signup_bonus key=%d want 1", got)
	}
}

func TestSignupBonusIdempotent(t *testing.T) {
	// 变异:移除 orderExistsByOutTradeNoTx 检查或 out_trade_no 唯一约束 -> 第二次调用会重复入账。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool))
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	cfg := SignupInviteeConfig{SignupBonusUSDMicros: 250_000} // 25 美分

	first, err := svc.IssueSignupBonus(ctx, cfg, f.tenantA, f.userA)
	if err != nil {
		t.Fatalf("first IssueSignupBonus: %v", err)
	}
	if !first.Issued {
		t.Fatalf("first call: want Issued=true, got %+v", first)
	}

	second, err := svc.IssueSignupBonus(ctx, cfg, f.tenantA, f.userA)
	if err != nil {
		t.Fatalf("second IssueSignupBonus: %v", err)
	}
	if second.Issued || !second.AlreadyIssued {
		t.Fatalf("second call: want Issued=false AlreadyIssued=true, got %+v", second)
	}

	// 仍然恰好一条 credit 行,总计 25 美分。
	if got := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1 AND user_id=$2`, f.tenantA, f.userA); got != 1 {
		t.Fatalf("payment_credits after double call=%d want 1", got)
	}
	if got := sumPaymentCredits(t, ctx, pool, f.tenantA, f.userA); got != 25 {
		t.Fatalf("sum credits=%d want exactly 25 (no double credit)", got)
	}
}

func TestSignupBonusAtomic_CreditFailureRollsBack(t *testing.T) {
	// 变异:在插入 order 之后、入账之前提交,会留下孤儿 order 行。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)

	orig := insertSignupBonusTopupCreditTx
	insertSignupBonusTopupCreditTx = func(context.Context, pgx.Tx, Order, string, int64, string, time.Time) (CreditRecord, int64, error) {
		return CreditRecord{}, 0, errors.New("forced signup bonus credit failure")
	}
	t.Cleanup(func() { insertSignupBonusTopupCreditTx = orig })

	svc := NewService(NewPostgresStore(pool))
	_, err := svc.IssueSignupBonus(ctx, SignupInviteeConfig{SignupBonusUSDMicros: 100_000}, f.tenantA, f.userA)
	if err == nil {
		t.Fatal("IssueSignupBonus with forced credit failure returned nil error")
	}
	if got := f.countInt(`SELECT count(*) FROM payment_orders WHERE tenant_id=$1 AND out_trade_no=$2`,
		f.tenantA, signupBonusRequestKey(f.tenantA, f.userA)); got != 0 {
		t.Fatalf("payment_orders after failed credit=%d want 0 (rolled back)", got)
	}
	if got := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1 AND user_id=$2`, f.tenantA, f.userA); got != 0 {
		t.Fatalf("payment_credits after failed credit=%d want 0", got)
	}
}

// ============================================================
// 被邀请人奖励(Invitee Reward)测试
// ============================================================

func TestInviteeRewardDefaultOff(t *testing.T) {
	// 变异:去掉 IssueInviteeReward 里的 cents<=0 守卫 -> 即使 amount=0 也发放 credit。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool))

	cfg := SignupInviteeConfig{ReferralInviteeUSDMicros: 0} // 默认关闭
	res, err := svc.IssueInviteeReward(ctx, cfg, f.tenantA, f.userA)
	if err != nil {
		t.Fatalf("IssueInviteeReward amount=0: %v", err)
	}
	if res.Issued || res.AlreadyIssued {
		t.Fatalf("IssueInviteeReward amount=0: want no-op, got issued=%v alreadyIssued=%v", res.Issued, res.AlreadyIssued)
	}
	if got := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1 AND user_id=$2`, f.tenantA, f.userA); got != 0 {
		t.Fatalf("payment_credits after amount=0 invitee reward=%d want 0", got)
	}
}

func TestInviteeRewardIssuedOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool))
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	cfg := SignupInviteeConfig{ReferralInviteeUSDMicros: 1_000_000} // 100 美分 = $1
	res, err := svc.IssueInviteeReward(ctx, cfg, f.tenantA, f.userA)
	if err != nil {
		t.Fatalf("IssueInviteeReward: %v", err)
	}
	if !res.Issued || res.AlreadyIssued {
		t.Fatalf("first call: want Issued=true AlreadyIssued=false, got %+v", res)
	}
	if res.NewBalance != 100 {
		t.Fatalf("balance=%d want 100", res.NewBalance)
	}
	if res.BillingEventID <= 0 {
		t.Fatalf("billing_event_id=%d want >0", res.BillingEventID)
	}
	if got := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1 AND user_id=$2`, f.tenantA, f.userA); got != 1 {
		t.Fatalf("payment_credits=%d want 1", got)
	}
	if got := sumPaymentCredits(t, ctx, pool, f.tenantA, f.userA); got != 100 {
		t.Fatalf("sum credits=%d want 100", got)
	}
	// 校验指纹 key
	if got := f.countInt(`SELECT count(*) FROM payment_orders WHERE tenant_id=$1 AND out_trade_no=$2`,
		f.tenantA, inviteeRewardRequestKey(f.tenantA, f.userA)); got != 1 {
		t.Fatalf("orders with invitee_reward key=%d want 1", got)
	}
}

func TestInviteeRewardIdempotent(t *testing.T) {
	// 变异:移除 orderExistsByOutTradeNoTx 检查 -> 第二次调用会重复入账。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool))
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	cfg := SignupInviteeConfig{ReferralInviteeUSDMicros: 750_000} // 75 美分

	first, err := svc.IssueInviteeReward(ctx, cfg, f.tenantA, f.userA)
	if err != nil {
		t.Fatalf("first IssueInviteeReward: %v", err)
	}
	if !first.Issued {
		t.Fatalf("first call: want Issued=true, got %+v", first)
	}

	second, err := svc.IssueInviteeReward(ctx, cfg, f.tenantA, f.userA)
	if err != nil {
		t.Fatalf("second IssueInviteeReward: %v", err)
	}
	if second.Issued || !second.AlreadyIssued {
		t.Fatalf("second call: want Issued=false AlreadyIssued=true, got %+v", second)
	}

	if got := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1 AND user_id=$2`, f.tenantA, f.userA); got != 1 {
		t.Fatalf("payment_credits after double call=%d want 1", got)
	}
	if got := sumPaymentCredits(t, ctx, pool, f.tenantA, f.userA); got != 75 {
		t.Fatalf("sum credits=%d want exactly 75 (no double credit)", got)
	}
}

func TestInviteeRewardAtomic_CreditFailureRollsBack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)

	orig := insertInviteeRewardTopupCreditTx
	insertInviteeRewardTopupCreditTx = func(context.Context, pgx.Tx, Order, string, int64, string, time.Time) (CreditRecord, int64, error) {
		return CreditRecord{}, 0, errors.New("forced invitee reward credit failure")
	}
	t.Cleanup(func() { insertInviteeRewardTopupCreditTx = orig })

	svc := NewService(NewPostgresStore(pool))
	_, err := svc.IssueInviteeReward(ctx, SignupInviteeConfig{ReferralInviteeUSDMicros: 200_000}, f.tenantA, f.userA)
	if err == nil {
		t.Fatal("IssueInviteeReward with forced credit failure returned nil error")
	}
	if got := f.countInt(`SELECT count(*) FROM payment_orders WHERE tenant_id=$1 AND out_trade_no=$2`,
		f.tenantA, inviteeRewardRequestKey(f.tenantA, f.userA)); got != 0 {
		t.Fatalf("payment_orders after failed credit=%d want 0 (rolled back)", got)
	}
	if got := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1 AND user_id=$2`, f.tenantA, f.userA); got != 0 {
		t.Fatalf("payment_credits after failed credit=%d want 0", got)
	}
}

// ============================================================
// 跨功能:注册奖励与被邀请人奖励是相互独立的 credit
// ============================================================

func TestSignupBonusAndInviteeRewardAreIndependent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool))
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	bonusCfg := SignupInviteeConfig{SignupBonusUSDMicros: 300_000}    // 30 美分
	inviteeCfg := SignupInviteeConfig{ReferralInviteeUSDMicros: 200_000} // 20 美分

	_, err := svc.IssueSignupBonus(ctx, bonusCfg, f.tenantA, f.userA)
	if err != nil {
		t.Fatalf("IssueSignupBonus: %v", err)
	}
	_, err = svc.IssueInviteeReward(ctx, inviteeCfg, f.tenantA, f.userA)
	if err != nil {
		t.Fatalf("IssueInviteeReward: %v", err)
	}

	// 两条不同的 credit 行,总计 50 美分。
	if got := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1 AND user_id=$2`, f.tenantA, f.userA); got != 2 {
		t.Fatalf("payment_credits=%d want 2 (bonus + invitee)", got)
	}
	if got := sumPaymentCredits(t, ctx, pool, f.tenantA, f.userA); got != 50 {
		t.Fatalf("sum credits=%d want 50 (30+20)", got)
	}
	// 各自有独立的 out_trade_no。
	if got := f.countInt(`SELECT count(*) FROM payment_orders WHERE tenant_id=$1 AND request_fingerprint=$2`, f.tenantA, signupBonusFingerprint); got != 1 {
		t.Fatalf("signup_bonus orders=%d want 1", got)
	}
	if got := f.countInt(`SELECT count(*) FROM payment_orders WHERE tenant_id=$1 AND request_fingerprint=$2`, f.tenantA, inviteeRewardFingerprint); got != 1 {
		t.Fatalf("invitee_reward orders=%d want 1", got)
	}
}
