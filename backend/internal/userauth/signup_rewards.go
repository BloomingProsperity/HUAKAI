package userauth

import (
	"context"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/signupreward"
)

const signupRewardRecoveryEnqueueTimeout = 5 * time.Second

type SignupRewardConfig struct {
	SignupBonusCents   int64
	InviteeRewardCents int64
}

type signupRewardExpectationStore interface {
	EnsureSignupRewardExpectation(context.Context, int64, int64, signupreward.Kind, int64) error
}

// issueSignupCredits 在新用户注册成功后发放可选的注册时钱包额度。
// 发放失败不回滚已经创建的用户，而是进入可靠恢复层。
func (s *Service) issueSignupCredits(ctx context.Context, tenantID, userID int64, hadInvite bool) {
	if s == nil {
		return
	}
	if s.SignupRewards.SignupBonusCents > 0 && s.SignupBonusFn != nil {
		if err := s.SignupBonusFn(ctx, tenantID, userID); err != nil {
			logSignupRewardFailure(ctx, "signup_bonus", tenantID, userID, err)
			s.enqueueSignupRewardRecovery(ctx, tenantID, userID, "signup_bonus", s.SignupRewards.SignupBonusCents)
		}
	}
	if hadInvite && s.SignupRewards.InviteeRewardCents > 0 && s.InviteeRewardFn != nil {
		if err := s.InviteeRewardFn(ctx, tenantID, userID); err != nil {
			logSignupRewardFailure(ctx, "invitee_reward", tenantID, userID, err)
			s.enqueueSignupRewardRecovery(ctx, tenantID, userID, "invitee_reward", s.SignupRewards.InviteeRewardCents)
		}
	}
}

func (s *Service) ensureSignupRewardExpectations(ctx context.Context, store Store, tenantID, userID int64, hadInvite bool) error {
	if s == nil {
		return nil
	}
	if s.SignupRewards.SignupBonusCents <= 0 &&
		(!hadInvite || s.SignupRewards.InviteeRewardCents <= 0) {
		return nil
	}
	writer, ok := store.(signupRewardExpectationStore)
	if !ok {
		return ErrStoreNotConfigured
	}
	if s.SignupRewards.SignupBonusCents > 0 {
		if err := writer.EnsureSignupRewardExpectation(ctx, tenantID, userID,
			signupreward.KindSignupBonus, s.SignupRewards.SignupBonusCents); err != nil {
			return err
		}
	}
	if hadInvite && s.SignupRewards.InviteeRewardCents > 0 {
		if err := writer.EnsureSignupRewardExpectation(ctx, tenantID, userID,
			signupreward.KindInviteeReward, s.SignupRewards.InviteeRewardCents); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) linkSocialIdentityAndEnsureRewards(
	ctx context.Context,
	store Store,
	tenantID, userID int64,
	provider, subject string,
) (User, error) {
	linked, err := store.LinkSocialIdentity(ctx, tenantID, userID, provider, subject)
	if err != nil {
		return User{}, err
	}
	if err := s.ensureSignupRewardExpectations(ctx, store, linked.TenantID, linked.ID, false); err != nil {
		return User{}, err
	}
	return linked, nil
}

func (s *Service) enqueueSignupRewardRecovery(ctx context.Context, tenantID, userID int64, rewardKind string, amountCents int64) {
	if s == nil || s.SignupRewardRecoveryFn == nil {
		return
	}
	// 用户已经提交成功，恢复事件不能跟随 HTTP 请求取消。
	recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), signupRewardRecoveryEnqueueTimeout)
	defer cancel()
	if err := s.SignupRewardRecoveryFn(recoveryCtx, tenantID, userID, rewardKind, amountCents); err != nil {
		_ = privacy.LogSystem(recoveryCtx, privacy.SystemEvent{
			Severity:   privacy.SeverityError,
			Component:  "userauth.signup_reward",
			ErrorClass: "signup_reward_recovery_enqueue_failed",
			Attrs: map[string]any{
				"reward_kind":          rewardKind,
				"tenant_id":            tenantID,
				"user_id":              userID,
				"failure_reason_class": privacy.ErrorClassFor(recoveryCtx, err),
			},
		})
	}
}
