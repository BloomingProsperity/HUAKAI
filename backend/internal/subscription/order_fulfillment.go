// HUAKAI · iKun

package subscription

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// FulfillOrderInput 订单购订阅的履约输入。调用方 = payment 在订单完成 SERIALIZABLE 事务内调用,
// 传入已开的 pgx.Tx; 本入口不 begin/commit, 与订单完成同事务保证"标完成必开通、绝不双开"。
type FulfillOrderInput struct {
	TenantID int64
	UserID   int64
	PlanID   int64
	// PaymentOrderID: 本订单 id, 作 effect 幂等锚 + 外键。
	PaymentOrderID int64
	// ActorKind/ActorID: 自助下单=user; 管理员代开/系统回调可传 admin/system。
	ActorKind string
	ActorID   int64
	RequestID string
	Now       time.Time
}

// FulfillOrderTx 在调用方事务内把一笔订单履约为订阅激活/续期 (不 begin/commit)。
// 自助购买语义: EnforceUpgradeOnly=true (同组叠买只能往高)。幂等: 先按 payment_order_id 查效果账本,
// 命中即回放不重复激活; 否则激活/续期 + 写效果行。零碰 payment_credits / billing_events / 余额。
//
// 与 FulfillVoucherTx 同形 (仅幂等键与来源不同); 共享 fulfillResultFromEffect 回放助手。
func FulfillOrderTx(ctx context.Context, tx pgx.Tx, in FulfillOrderInput) (FulfillResult, error) {
	existing, ok, err := getFulfillmentEffectByOrderTx(ctx, tx, in.TenantID, in.PaymentOrderID)
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
		SourceKind:         EffectSourceOrder,
		ActorKind:          in.ActorKind,
		ActorID:            in.ActorID,
		RequestID:          in.RequestID,
		EnforceUpgradeOnly: true,
		Now:                in.Now,
	})
	if err != nil {
		return FulfillResult{}, err
	}

	orderID := in.PaymentOrderID
	if _, err := insertFulfillmentEffectTx(ctx, tx, FulfillmentEffect{
		TenantID:            in.TenantID,
		SourceKind:          EffectSourceOrder,
		PaymentOrderID:      &orderID,
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
