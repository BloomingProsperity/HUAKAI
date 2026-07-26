package payment

import (
	"context"
	"errors"

	obsdlq "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/signupreward"
)

const (
	RewardKindSignupBonus   = string(signupreward.KindSignupBonus)
	RewardKindInviteeReward = string(signupreward.KindInviteeReward)
)

// EnqueueSignupRewardRecovery 把单笔未成功发放的注册奖励交给通用 outbox。
// 事件记录注册时承诺的金额；后续配置变化不能改变这笔已承诺权益。
func EnqueueSignupRewardRecovery(ctx context.Context, outbox obsdlq.Outbox, tenantID, userID int64, rewardKind string, amountCents int64) error {
	if outbox == nil {
		return errors.New("payment: invalid signup reward recovery input")
	}
	event, err := signupreward.NewEvent(tenantID, userID, signupreward.Kind(rewardKind), amountCents)
	if err != nil {
		return err
	}
	_, err = outbox.Enqueue(ctx, event)
	return err
}

type signupRewardIssuer interface {
	IssueSignupBonus(context.Context, SignupInviteeConfig, int64, int64) (SignupBonusResult, error)
	IssueInviteeReward(context.Context, SignupInviteeConfig, int64, int64) (InviteeRewardResult, error)
}

// NewSignupRewardRecoveryHandler 返回 outbox 重试处理器。底层两种发放方法都有业务幂等键，
// 因此 worker 超时重放或人工重放不会重复入账；金额始终取事件快照。
func NewSignupRewardRecoveryHandler(issuer signupRewardIssuer) obsdlq.Handler {
	return func(ctx context.Context, event obsdlq.OutboxEvent) error {
		if issuer == nil {
			return ErrStoreNotConfigured
		}
		payload, err := signupreward.ParseEvent(event)
		if err != nil {
			return err
		}
		switch payload.Kind {
		case signupreward.KindSignupBonus:
			_, err := issuer.IssueSignupBonus(ctx, SignupInviteeConfig{
				SignupBonusCents: payload.AmountCents,
			}, payload.TenantID, payload.UserID)
			return err
		case signupreward.KindInviteeReward:
			_, err := issuer.IssueInviteeReward(ctx, SignupInviteeConfig{
				ReferralInviteeCents: payload.AmountCents,
			}, payload.TenantID, payload.UserID)
			return err
		default:
			return errors.New("payment: unsupported signup reward kind")
		}
	}
}
