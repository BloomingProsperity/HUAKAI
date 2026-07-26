package userauth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/signupreward"
)

func TestRegisterPersistsBothRewardExpectationsBeforeImmediateIssue(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	rawInvite, inviteHash, err := GenerateInviteCode()
	if err != nil {
		t.Fatalf("GenerateInviteCode: %v", err)
	}
	store.invites[inviteHash] = InviteCode{
		Code: inviteHash, TenantID: 7, MaxUses: 1, Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}
	svc := NewService(store)
	svc.RequireVerified = false
	svc.PasswordPolicy = cheapPasswordPolicy()
	svc.Now = func() time.Time { return now }
	svc.SignupRewards = SignupRewardConfig{SignupBonusCents: 125, InviteeRewardCents: 75}
	var immediateCalls int
	svc.SignupBonusFn = func(context.Context, int64, int64) error {
		immediateCalls++
		if len(store.rewardExpectations) != 2 {
			t.Fatalf("即时发放开始时 durable expectations=%d，期望两笔已提交", len(store.rewardExpectations))
		}
		return nil
	}
	svc.InviteeRewardFn = func(context.Context, int64, int64) error {
		immediateCalls++
		return nil
	}

	result, err := svc.Register(ctx, RegisterInput{
		TenantID: 7, Email: "reward@example.test", Password: "secret12", InviteCode: rawInvite,
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if result.User.ID <= 0 || immediateCalls != 2 {
		t.Fatalf("user=%d immediateCalls=%d，期望建号成功且两笔即时发放", result.User.ID, immediateCalls)
	}
	want := []rewardExpectationRecord{
		{TenantID: 7, UserID: result.User.ID, Kind: signupreward.KindSignupBonus, AmountCents: 125},
		{TenantID: 7, UserID: result.User.ID, Kind: signupreward.KindInviteeReward, AmountCents: 75},
	}
	if len(store.rewardExpectations) != len(want) {
		t.Fatalf("expectations=%+v want=%+v", store.rewardExpectations, want)
	}
	for i := range want {
		if store.rewardExpectations[i] != want[i] {
			t.Fatalf("expectation[%d]=%+v want=%+v", i, store.rewardExpectations[i], want[i])
		}
	}
}

func TestRegisterRollsBackUserWhenRewardExpectationCannotPersist(t *testing.T) {
	ctx := context.Background()
	store := newMemoryAuthStore(time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC))
	store.failRewardExpectation = true
	svc := NewService(store)
	svc.RequireVerified = false
	svc.PasswordPolicy = cheapPasswordPolicy()
	svc.SignupRewards = SignupRewardConfig{SignupBonusCents: 100}

	_, err := svc.Register(ctx, RegisterInput{
		TenantID: 7, Email: "rollback-reward@example.test", Password: "secret12",
	})
	if err == nil {
		t.Fatal("奖励期待无法落库时注册必须失败")
	}
	if len(store.users) != 0 || len(store.byEmail) != 0 || len(store.rewardExpectations) != 0 {
		t.Fatalf("事务未完整回滚 users=%d emails=%d expectations=%d",
			len(store.users), len(store.byEmail), len(store.rewardExpectations))
	}
}

func TestBothSocialSignupPathsPersistRewardExpectation(t *testing.T) {
	ctx := context.Background()
	store := newMemoryAuthStore(time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC))
	svc := NewService(store)
	svc.SignupRewards = SignupRewardConfig{SignupBonusCents: 88}

	first, err := svc.ApplyVerifiedSocialIdentity(ctx, 7, VerifiedIdentity{
		Provider: SocialProviderGoogle, Subject: "subject-one",
		Email: "social-one@example.test", EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("ApplyVerifiedSocialIdentity: %v", err)
	}
	second, err := svc.CompleteSocialSignupWithVerifiedEmail(ctx, 7, VerifiedIdentity{
		Provider: SocialProviderQQ, Subject: "subject-two",
	}, "social-two@example.test")
	if err != nil {
		t.Fatalf("CompleteSocialSignupWithVerifiedEmail: %v", err)
	}
	if first.ID == second.ID || len(store.rewardExpectations) != 2 {
		t.Fatalf("social users=%d/%d expectations=%+v", first.ID, second.ID, store.rewardExpectations)
	}
	for _, expectation := range store.rewardExpectations {
		if expectation.Kind != signupreward.KindSignupBonus || expectation.AmountCents != 88 {
			t.Fatalf("social expectation=%+v want signup_bonus/88", expectation)
		}
	}
}

func TestSocialSignupRollsBackIdentityWhenRewardExpectationFails(t *testing.T) {
	ctx := context.Background()
	store := newMemoryAuthStore(time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC))
	store.failRewardExpectation = true
	svc := NewService(store)
	svc.SignupRewards = SignupRewardConfig{SignupBonusCents: 88}

	_, err := svc.ApplyVerifiedSocialIdentity(ctx, 7, VerifiedIdentity{
		Provider: SocialProviderGoogle, Subject: "subject-rollback",
		Email: "social-rollback@example.test", EmailVerified: true,
	})
	if err == nil || !strings.Contains(err.Error(), "reward expectation") {
		t.Fatalf("social signup err=%v，期望奖励期待落库失败", err)
	}
	if len(store.users) != 0 || len(store.socialLinks) != 0 {
		t.Fatalf("社交注册未完整回滚 users=%d links=%d", len(store.users), len(store.socialLinks))
	}
}
