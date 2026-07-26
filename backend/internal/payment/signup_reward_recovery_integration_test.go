//go:build integration_pg

package payment

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	obsdlq "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

type flakySignupRewardIssuer struct {
	delegate       *Service
	failBonusCalls int
}

func (f *flakySignupRewardIssuer) IssueSignupBonus(
	ctx context.Context,
	cfg SignupInviteeConfig,
	tenantID, userID int64,
) (SignupBonusResult, error) {
	if f.failBonusCalls > 0 {
		f.failBonusCalls--
		return SignupBonusResult{}, errors.New("测试注入：奖励钱包暂不可用")
	}
	return f.delegate.IssueSignupBonus(ctx, cfg, tenantID, userID)
}

func (f *flakySignupRewardIssuer) IssueInviteeReward(
	ctx context.Context,
	cfg SignupInviteeConfig,
	tenantID, userID int64,
) (InviteeRewardResult, error) {
	return f.delegate.IssueInviteeReward(ctx, cfg, tenantID, userID)
}

func TestSignupRewardPostgresRecoverySurvivesFailureAndWorkerRestart(t *testing.T) {
	// 变异：
	// 1. 注册事务不写 outbox，首次 RunOnce 会拿不到事件；
	// 2. worker 失败不持久化 next_retry_at，第二个 worker 无法恢复；
	// 3. 奖励幂等键失效，重放后 payment_credits/订单/账单事件会超过一条。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	fixture := newPaymentFixture(t, ctx, pool)
	outbox := obsdlq.NewPostgresOutbox(pool)
	rewardCents := int64(37)

	authService := userauth.NewService(userauth.NewPostgresStore(pool))
	authService.RequireVerified = false
	authService.PasswordPolicy = userauth.PasswordPolicy{
		MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16,
	}
	authService.SignupRewards = userauth.SignupRewardConfig{SignupBonusCents: rewardCents}
	authService.SignupBonusFn = func(context.Context, int64, int64) error {
		return errors.New("测试注入：注册请求内即时发放失败")
	}
	authService.SignupRewardRecoveryFn = func(
		recoveryCtx context.Context,
		tenantID, userID int64,
		rewardKind string,
		amountCents int64,
	) error {
		return EnqueueSignupRewardRecovery(
			recoveryCtx, outbox, tenantID, userID, rewardKind, amountCents,
		)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	registered, err := authService.Register(ctx, userauth.RegisterInput{
		TenantID: fixture.tenantA,
		Email:    "signup-recovery-" + suffix + "@example.test",
		Password: "secret12",
	})
	if err != nil {
		t.Fatalf("注册用户：%v", err)
	}
	eventID := fmt.Sprintf(
		"signup-reward-%d-%d-signup_bonus",
		fixture.tenantA,
		registered.User.ID,
	)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM dlq_events WHERE outbox_event_id=$1`, eventID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM outbox_events WHERE id=$1`, eventID)
	})

	var status string
	var attempts int
	if err := pool.QueryRow(ctx,
		`SELECT status, attempt_count FROM outbox_events WHERE id=$1`,
		eventID,
	).Scan(&status, &attempts); err != nil {
		t.Fatalf("读取注册事务 outbox：%v", err)
	}
	if status != "pending" || attempts != 0 {
		t.Fatalf("注册后 outbox=%s/%d，期望 pending/0", status, attempts)
	}

	base := time.Now().UTC().Add(time.Minute)
	policy := obsdlq.RetryPolicy{
		MaxAttempts: 3,
		MaxBackoff:  time.Second,
		Schedule:    []time.Duration{time.Second},
	}
	issuer := &flakySignupRewardIssuer{
		delegate:       NewService(NewPostgresStore(pool)),
		failBonusCalls: 1,
	}
	firstWorker := obsdlq.NewWorker(
		outbox,
		obsdlq.WorkerConfig{RetryPolicy: policy},
		obsdlq.WithWorkerClock(func() time.Time { return base }),
	)
	firstWorker.Register(obsdlq.EventTypeSignupReward, NewSignupRewardRecoveryHandler(issuer))
	processed, err := firstWorker.RunOnce(ctx, obsdlq.PriorityHigh, "signup-worker-first")
	if err != nil || !processed {
		t.Fatalf("首次失败处理 processed=%v err=%v", processed, err)
	}
	var nextRetry time.Time
	if err := pool.QueryRow(ctx,
		`SELECT status, attempt_count, next_retry_at FROM outbox_events WHERE id=$1`,
		eventID,
	).Scan(&status, &attempts, &nextRetry); err != nil {
		t.Fatalf("读取失败重试状态：%v", err)
	}
	if status != "failed_retry" || attempts != 1 || !nextRetry.After(base) {
		t.Fatalf("首次失败后 outbox=%s/%d/%s，期望 failed_retry/1/未来重试",
			status, attempts, nextRetry)
	}

	secondNow := nextRetry.Add(time.Second)
	secondWorker := obsdlq.NewWorker(
		outbox,
		obsdlq.WorkerConfig{RetryPolicy: policy},
		obsdlq.WithWorkerClock(func() time.Time { return secondNow }),
	)
	secondWorker.Register(obsdlq.EventTypeSignupReward, NewSignupRewardRecoveryHandler(issuer))
	processed, err = secondWorker.RunOnce(ctx, obsdlq.PriorityHigh, "signup-worker-restarted")
	if err != nil || !processed {
		t.Fatalf("重启后恢复 processed=%v err=%v", processed, err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT status, attempt_count FROM outbox_events WHERE id=$1`,
		eventID,
	).Scan(&status, &attempts); err != nil {
		t.Fatalf("读取恢复完成状态：%v", err)
	}
	if status != "completed" || attempts != 1 {
		t.Fatalf("恢复后 outbox=%s/%d，期望 completed/1", status, attempts)
	}

	if got := fixture.countInt(
		`SELECT count(*) FROM payment_credits WHERE tenant_id=$1 AND user_id=$2`,
		fixture.tenantA, registered.User.ID,
	); got != 1 {
		t.Fatalf("奖励 credit 行=%d，期望 1", got)
	}
	if got := sumPaymentCredits(t, ctx, pool, fixture.tenantA, registered.User.ID); got != rewardCents {
		t.Fatalf("奖励总额=%d，期望 %d 美分", got, rewardCents)
	}
	if got := fixture.countInt(
		`SELECT count(*) FROM payment_orders WHERE tenant_id=$1 AND out_trade_no=$2`,
		fixture.tenantA, signupBonusRequestKey(fixture.tenantA, registered.User.ID),
	); got != 1 {
		t.Fatalf("奖励订单=%d，期望 1", got)
	}
	if got := fixture.countInt(
		`SELECT count(*)
		   FROM billing_events b
		   JOIN payment_credits c
		     ON c.tenant_id=b.tenant_id AND c.id=b.payment_credit_id
		  WHERE b.tenant_id=$1 AND c.user_id=$2`,
		fixture.tenantA, registered.User.ID,
	); got != 1 {
		t.Fatalf("奖励账单事实=%d，期望 1", got)
	}

	if err := EnqueueSignupRewardRecovery(
		ctx, outbox, fixture.tenantA, registered.User.ID, RewardKindSignupBonus, rewardCents,
	); err != nil {
		t.Fatalf("完成后幂等重入队：%v", err)
	}
	processed, err = secondWorker.RunOnce(ctx, obsdlq.PriorityHigh, "signup-worker-replay")
	if err != nil || processed {
		t.Fatalf("完成事件不得重放 processed=%v err=%v", processed, err)
	}
	if got := fixture.countInt(
		`SELECT count(*) FROM payment_credits WHERE tenant_id=$1 AND user_id=$2`,
		fixture.tenantA, registered.User.ID,
	); got != 1 {
		t.Fatalf("完成事件重放后 credit 行=%d，期望仍为 1", got)
	}
}
