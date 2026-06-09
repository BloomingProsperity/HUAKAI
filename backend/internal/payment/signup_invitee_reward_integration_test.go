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
// Signup Bonus tests
// ============================================================

func TestSignupBonusDefaultOff(t *testing.T) {
	// Mutation: drop the cents<=0 guard in IssueSignupBonus -> credit is issued even when amount=0.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool))

	cfg := SignupInviteeConfig{SignupBonusUSDMicros: 0} // DEFAULT OFF
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
	// Mutation: drop insertOrderTx unique constraint or out_trade_no fingerprint -> can double-issue.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool))
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	cfg := SignupInviteeConfig{SignupBonusUSDMicros: 500_000} // 50 cents
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
	// Verify DB state
	if got := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1 AND user_id=$2`, f.tenantA, f.userA); got != 1 {
		t.Fatalf("payment_credits=%d want 1", got)
	}
	if got := sumPaymentCredits(t, ctx, pool, f.tenantA, f.userA); got != 50 {
		t.Fatalf("sum credits=%d want 50", got)
	}
	// Verify fingerprint
	if got := f.countInt(`SELECT count(*) FROM payment_orders WHERE tenant_id=$1 AND out_trade_no=$2`,
		f.tenantA, signupBonusRequestKey(f.tenantA, f.userA)); got != 1 {
		t.Fatalf("orders with signup_bonus key=%d want 1", got)
	}
}

func TestSignupBonusIdempotent(t *testing.T) {
	// Mutation: remove the orderExistsByOutTradeNoTx check or out_trade_no unique -> second call double-credits.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool))
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	cfg := SignupInviteeConfig{SignupBonusUSDMicros: 250_000} // 25 cents

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

	// Still exactly one credit row and 25 cents total.
	if got := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1 AND user_id=$2`, f.tenantA, f.userA); got != 1 {
		t.Fatalf("payment_credits after double call=%d want 1", got)
	}
	if got := sumPaymentCredits(t, ctx, pool, f.tenantA, f.userA); got != 25 {
		t.Fatalf("sum credits=%d want exactly 25 (no double credit)", got)
	}
}

func TestSignupBonusAtomic_CreditFailureRollsBack(t *testing.T) {
	// Mutation: committing after order insert but before credit leaves orphan order row.
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
// Invitee Reward tests
// ============================================================

func TestInviteeRewardDefaultOff(t *testing.T) {
	// Mutation: drop the cents<=0 guard in IssueInviteeReward -> credit issued even when amount=0.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool))

	cfg := SignupInviteeConfig{ReferralInviteeUSDMicros: 0} // DEFAULT OFF
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

	cfg := SignupInviteeConfig{ReferralInviteeUSDMicros: 1_000_000} // 100 cents = $1
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
	// Verify fingerprint key
	if got := f.countInt(`SELECT count(*) FROM payment_orders WHERE tenant_id=$1 AND out_trade_no=$2`,
		f.tenantA, inviteeRewardRequestKey(f.tenantA, f.userA)); got != 1 {
		t.Fatalf("orders with invitee_reward key=%d want 1", got)
	}
}

func TestInviteeRewardIdempotent(t *testing.T) {
	// Mutation: remove orderExistsByOutTradeNoTx check -> second call double-credits.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool))
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	cfg := SignupInviteeConfig{ReferralInviteeUSDMicros: 750_000} // 75 cents

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
// Cross-feature: signup bonus and invitee reward are independent credits
// ============================================================

func TestSignupBonusAndInviteeRewardAreIndependent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool))
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	bonusCfg := SignupInviteeConfig{SignupBonusUSDMicros: 300_000}    // 30 cents
	inviteeCfg := SignupInviteeConfig{ReferralInviteeUSDMicros: 200_000} // 20 cents

	_, err := svc.IssueSignupBonus(ctx, bonusCfg, f.tenantA, f.userA)
	if err != nil {
		t.Fatalf("IssueSignupBonus: %v", err)
	}
	_, err = svc.IssueInviteeReward(ctx, inviteeCfg, f.tenantA, f.userA)
	if err != nil {
		t.Fatalf("IssueInviteeReward: %v", err)
	}

	// Two distinct credit rows, total 50 cents.
	if got := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1 AND user_id=$2`, f.tenantA, f.userA); got != 2 {
		t.Fatalf("payment_credits=%d want 2 (bonus + invitee)", got)
	}
	if got := sumPaymentCredits(t, ctx, pool, f.tenantA, f.userA); got != 50 {
		t.Fatalf("sum credits=%d want 50 (30+20)", got)
	}
	// Each has its own out_trade_no.
	if got := f.countInt(`SELECT count(*) FROM payment_orders WHERE tenant_id=$1 AND request_fingerprint=$2`, f.tenantA, signupBonusFingerprint); got != 1 {
		t.Fatalf("signup_bonus orders=%d want 1", got)
	}
	if got := f.countInt(`SELECT count(*) FROM payment_orders WHERE tenant_id=$1 AND request_fingerprint=$2`, f.tenantA, inviteeRewardFingerprint); got != 1 {
		t.Fatalf("invitee_reward orders=%d want 1", got)
	}
}
