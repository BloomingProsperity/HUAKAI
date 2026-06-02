// HUAKAI · iKun

package subscription

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// FulfillVoucherInput 兑换码购订阅的履约输入。调用方 = voucher 在其兑换 SERIALIZABLE 事务内调用,
// 传入已开的 pgx.Tx; 本入口不 begin/commit, 与兑换同事务保证"消券必开通、绝不双开"。
type FulfillVoucherInput struct {
	TenantID int64
	UserID   int64
	PlanID   int64
	// VoucherRedemptionID: 本次兑换写入的 voucher_redemption 行 id, 作 effect 幂等锚 + 外键。
	VoucherRedemptionID int64
	RequestID           string
	Now                 time.Time
}

// FulfillResult 兑换履约结果。Idempotent=true 表示命中已有 effect (完成态重放, 未重复激活)。
type FulfillResult struct {
	Subscription        UserSubscription
	ResultKind          string // ResultCreated / ResultRenewed
	PlanID              int64
	NewExpiresAt        time.Time
	AppliedValidityDays int
	Idempotent          bool
}

// FulfillVoucherTx 在调用方事务内把一次兑换履约为订阅激活/续期 (不 begin/commit)。
//   - 自助购买语义: EnforceUpgradeOnly=true (同组叠买只能往高, 往低返回 ErrDowngradeNotAllowed, 调用方据此回滚整事务)。
//   - 幂等: 先按 voucher_redemption_id 查效果账本, 命中即回放原结果不重复激活;
//     否则激活/续期 + 写一条效果行 (撞唯一索引由 insert 暴露 23505, 调用方处理)。
//   - 零碰 billing_events / payment_credits / 余额: 订阅是配额权益, 不进任何钱账本。
//
// 把"幂等读 → 激活 → 写效果账本"封为一个事务单元, 调用方 (voucher / 后续 payment) 无需手拼效果字段。
func FulfillVoucherTx(ctx context.Context, tx pgx.Tx, in FulfillVoucherInput) (FulfillResult, error) {
	existing, ok, err := getFulfillmentEffectByVoucherTx(ctx, tx, in.TenantID, in.VoucherRedemptionID)
	if err != nil {
		return FulfillResult{}, err
	}
	if ok {
		return fulfillResultFromEffect(existing, true), nil
	}

	res, err := ActivateOrRenewTx(ctx, tx, ActivateInput{
		TenantID:           in.TenantID,
		UserID:             in.UserID,
		PlanID:             in.PlanID,
		SourceKind:         EffectSourceVoucher,
		ActorKind:          ActorKindUser,
		ActorID:            in.UserID,
		RequestID:          in.RequestID,
		EnforceUpgradeOnly: true,
		Now:                in.Now,
	})
	if err != nil {
		return FulfillResult{}, err
	}

	redemptionID := in.VoucherRedemptionID
	if _, err := insertFulfillmentEffectTx(ctx, tx, FulfillmentEffect{
		TenantID:            in.TenantID,
		SourceKind:          EffectSourceVoucher,
		VoucherRedemptionID: &redemptionID,
		UserID:              in.UserID,
		PlanID:              in.PlanID,
		UserSubscriptionID:  res.Subscription.ID,
		ResultKind:          res.ResultKind,
		AppliedValidityDays: res.AppliedValidityDays,
		PrevExpiresAt:       res.PrevExpiresAt,
		NewExpiresAt:        res.NewExpiresAt,
	}); err != nil {
		return FulfillResult{}, err
	}

	return FulfillResult{
		Subscription:        res.Subscription,
		ResultKind:          res.ResultKind,
		PlanID:              in.PlanID,
		NewExpiresAt:        res.NewExpiresAt,
		AppliedValidityDays: res.AppliedValidityDays,
		Idempotent:          false,
	}, nil
}

// fulfillResultFromEffect 从已有效果行回放结果 (重放命中分支)。Subscription 仅带身份字段,
// 重放调用方通常只需 result_kind / 到期日 / plan_id, 不需完整订阅行。
func fulfillResultFromEffect(e FulfillmentEffect, idempotent bool) FulfillResult {
	return FulfillResult{
		Subscription: UserSubscription{
			ID:       e.UserSubscriptionID,
			TenantID: e.TenantID,
			UserID:   e.UserID,
			PlanID:   e.PlanID,
		},
		ResultKind:          e.ResultKind,
		PlanID:              e.PlanID,
		NewExpiresAt:        e.NewExpiresAt,
		AppliedValidityDays: e.AppliedValidityDays,
		Idempotent:          idempotent,
	}
}
