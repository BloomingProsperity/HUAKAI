package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	obsdlq "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
)

const (
	RewardKindSignupBonus   = "signup_bonus"
	RewardKindInviteeReward = "invitee_reward"
)

type signupRewardRecoveryPayload struct {
	TenantID   int64  `json:"tenant_id"`
	UserID     int64  `json:"user_id"`
	RewardKind string `json:"reward_kind"`
}

// EnqueueSignupRewardRecovery 把单笔未成功发放的注册奖励交给通用 outbox。
// 事件 ID 按租户、用户和奖励种类固定，重复注册回放不会堆出平行重试任务。
func EnqueueSignupRewardRecovery(ctx context.Context, outbox obsdlq.Outbox, tenantID, userID int64, rewardKind string) error {
	if outbox == nil || tenantID <= 0 || userID <= 0 || !validSignupRewardKind(rewardKind) {
		return errors.New("payment: invalid signup reward recovery input")
	}
	payload, err := json.Marshal(signupRewardRecoveryPayload{TenantID: tenantID, UserID: userID, RewardKind: rewardKind})
	if err != nil {
		return err
	}
	_, err = outbox.Enqueue(ctx, obsdlq.OutboxEvent{
		ID:        fmt.Sprintf("signup-reward-%d-%d-%s", tenantID, userID, rewardKind),
		TenantID:  tenantID,
		EventType: obsdlq.EventTypeSignupReward,
		Priority:  obsdlq.PriorityHigh,
		Payload:   payload,
	})
	return err
}

type signupRewardIssuer interface {
	IssueSignupBonus(context.Context, SignupInviteeConfig, int64, int64) (SignupBonusResult, error)
	IssueInviteeReward(context.Context, SignupInviteeConfig, int64, int64) (InviteeRewardResult, error)
}

// NewSignupRewardRecoveryHandler 返回 outbox 重试处理器。底层两种发放方法都有业务幂等键，
// 因此 worker 超时重放或人工重放不会重复入账。
func NewSignupRewardRecoveryHandler(issuer signupRewardIssuer, cfg SignupInviteeConfig) obsdlq.Handler {
	return func(ctx context.Context, event obsdlq.OutboxEvent) error {
		if issuer == nil {
			return ErrStoreNotConfigured
		}
		var payload signupRewardRecoveryPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Errorf("payment: decode signup reward recovery: %w", err)
		}
		if payload.TenantID <= 0 || payload.UserID <= 0 || payload.TenantID != event.TenantID || !validSignupRewardKind(payload.RewardKind) {
			return errors.New("payment: invalid signup reward recovery payload")
		}
		switch payload.RewardKind {
		case RewardKindSignupBonus:
			_, err := issuer.IssueSignupBonus(ctx, cfg, payload.TenantID, payload.UserID)
			return err
		case RewardKindInviteeReward:
			_, err := issuer.IssueInviteeReward(ctx, cfg, payload.TenantID, payload.UserID)
			return err
		default:
			return errors.New("payment: unsupported signup reward kind")
		}
	}
}

func validSignupRewardKind(kind string) bool {
	return kind == RewardKindSignupBonus || kind == RewardKindInviteeReward
}
