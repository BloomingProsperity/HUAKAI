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
	err          error
}

func (s *signupRewardIssuerStub) IssueSignupBonus(context.Context, SignupInviteeConfig, int64, int64) (SignupBonusResult, error) {
	s.bonusCalls++
	return SignupBonusResult{}, s.err
}

func (s *signupRewardIssuerStub) IssueInviteeReward(context.Context, SignupInviteeConfig, int64, int64) (InviteeRewardResult, error) {
	s.inviteeCalls++
	return InviteeRewardResult{}, s.err
}

func TestSignupRewardRecoveryRoundTripAndRetry(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	box := obsdlq.NewMemoryOutbox(obsdlq.WithMemoryClock(func() time.Time { return now }))
	if err := EnqueueSignupRewardRecovery(context.Background(), box, 7, 42, RewardKindSignupBonus); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	issuer := &signupRewardIssuerStub{}
	worker := obsdlq.NewWorker(box, obsdlq.WorkerConfig{RetryPolicy: obsdlq.RetryPolicy{MaxAttempts: 2}}, obsdlq.WithWorkerClock(func() time.Time { return now }))
	worker.Register(obsdlq.EventTypeSignupReward, NewSignupRewardRecoveryHandler(issuer, SignupInviteeConfig{SignupBonusCents: 100}))
	processed, err := worker.RunOnce(context.Background(), obsdlq.PriorityHigh, "test-worker")
	if err != nil || !processed || issuer.bonusCalls != 1 || issuer.inviteeCalls != 0 {
		t.Fatalf("processed=%v err=%v calls=%d/%d", processed, err, issuer.bonusCalls, issuer.inviteeCalls)
	}
}

func TestSignupRewardRecoveryRejectsMalformedAndRetriesFailure(t *testing.T) {
	issuer := &signupRewardIssuerStub{err: errors.New("wallet unavailable")}
	handler := NewSignupRewardRecoveryHandler(issuer, SignupInviteeConfig{SignupBonusCents: 100})
	if err := handler(context.Background(), obsdlq.OutboxEvent{TenantID: 7, Payload: []byte(`{"tenant_id":8,"user_id":42,"reward_kind":"signup_bonus"}`)}); err == nil {
		t.Fatal("跨租户 payload 应拒绝")
	}
	if err := handler(context.Background(), obsdlq.OutboxEvent{TenantID: 7, Payload: []byte(`{"tenant_id":7,"user_id":42,"reward_kind":"signup_bonus"}`)}); err == nil || issuer.bonusCalls != 1 {
		t.Fatalf("钱包失败应返回给 worker 重试,err=%v calls=%d", err, issuer.bonusCalls)
	}
}
