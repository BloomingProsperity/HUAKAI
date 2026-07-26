package payment

import (
	"context"
	"errors"
	"testing"
	"time"

	obsdlq "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
)

type signupRewardIssuerStub struct {
	bonusCalls   int
	inviteeCalls int
	bonusCents   []int64
	inviteeCents []int64
	err          error
}

func (s *signupRewardIssuerStub) IssueSignupBonus(_ context.Context, cfg SignupInviteeConfig, _, _ int64) (SignupBonusResult, error) {
	s.bonusCalls++
	s.bonusCents = append(s.bonusCents, cfg.SignupBonusCents)
	return SignupBonusResult{}, s.err
}

func (s *signupRewardIssuerStub) IssueInviteeReward(_ context.Context, cfg SignupInviteeConfig, _, _ int64) (InviteeRewardResult, error) {
	s.inviteeCalls++
	s.inviteeCents = append(s.inviteeCents, cfg.ReferralInviteeCents)
	return InviteeRewardResult{}, s.err
}

func TestSignupRewardRecoveryRoundTripAndRetry(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	box := obsdlq.NewMemoryOutbox(obsdlq.WithMemoryClock(func() time.Time { return now }))
	if err := EnqueueSignupRewardRecovery(context.Background(), box, 7, 42, RewardKindSignupBonus, 100); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	issuer := &signupRewardIssuerStub{}
	worker := obsdlq.NewWorker(box, obsdlq.WorkerConfig{RetryPolicy: obsdlq.RetryPolicy{MaxAttempts: 2}}, obsdlq.WithWorkerClock(func() time.Time { return now }))
	worker.Register(obsdlq.EventTypeSignupReward, NewSignupRewardRecoveryHandler(issuer))
	processed, err := worker.RunOnce(context.Background(), obsdlq.PriorityHigh, "test-worker")
	if err != nil || !processed || issuer.bonusCalls != 1 || issuer.inviteeCalls != 0 ||
		len(issuer.bonusCents) != 1 || issuer.bonusCents[0] != 100 {
		t.Fatalf("processed=%v err=%v calls=%d/%d cents=%v",
			processed, err, issuer.bonusCalls, issuer.inviteeCalls, issuer.bonusCents)
	}
}

func TestSignupRewardRecoveryRejectsMalformedAndRetriesFailure(t *testing.T) {
	issuer := &signupRewardIssuerStub{err: errors.New("wallet unavailable")}
	handler := NewSignupRewardRecoveryHandler(issuer)
	if err := handler(context.Background(), obsdlq.OutboxEvent{
		ID: "signup-reward-7-42-signup_bonus", TenantID: 7, EventType: obsdlq.EventTypeSignupReward,
		Payload: []byte(`{"tenant_id":8,"user_id":42,"reward_kind":"signup_bonus","amount_cents":100}`),
	}); err == nil {
		t.Fatal("跨租户 payload 应拒绝")
	}
	if err := handler(context.Background(), obsdlq.OutboxEvent{
		ID: "signup-reward-7-42-signup_bonus", TenantID: 7, EventType: obsdlq.EventTypeSignupReward,
		Payload: []byte(`{"tenant_id":7,"user_id":42,"reward_kind":"signup_bonus","amount_cents":100}`),
	}); err == nil || issuer.bonusCalls != 1 {
		t.Fatalf("钱包失败应返回给 worker 重试,err=%v calls=%d", err, issuer.bonusCalls)
	}
}
