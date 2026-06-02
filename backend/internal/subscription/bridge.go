package subscription

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

// PaymentBridge 让现有 payment webhook 先完成充值入账，再按 trade_no
// 幂等激活订阅订单。普通充值没有订阅订单时直接 no-op。
type PaymentBridge struct {
	payments      PaymentBackend
	subscriptions *Service
}

func NewPaymentBridge(payments PaymentBackend, subscriptions *Service) *PaymentBridge {
	return &PaymentBridge{payments: payments, subscriptions: subscriptions}
}

func (b *PaymentBridge) OpenRecharge(ctx context.Context, input payment.OpenInput) (payment.OpenResult, error) {
	if b == nil || b.payments == nil {
		return payment.OpenResult{}, payment.ErrStoreNotConfigured
	}
	return b.payments.OpenRecharge(ctx, input)
}

func (b *PaymentBridge) FulfillVerifiedCallback(ctx context.Context, cb payment.VerifiedCallback) (payment.CallbackResult, error) {
	if b == nil || b.payments == nil {
		return payment.CallbackResult{HTTPStatus: 500}, payment.ErrStoreNotConfigured
	}
	result, err := b.payments.FulfillVerifiedCallback(ctx, cb)
	if err != nil {
		return result, err
	}
	if b.subscriptions == nil || !result.Completed {
		return result, nil
	}
	_, activateErr := b.subscriptions.ActivatePaidOrder(ctx, ActivatePaidOrderInput{
		TenantID:        cb.TenantID,
		UserID:          result.UserID,
		RechargeOrderID: result.OrderID,
		TradeNo:         cb.ExternalTradeNo,
		PaidAt:          cb.Timestamp,
	})
	if activateErr != nil && activateErr != ErrOrderNotFound {
		return payment.CallbackResult{HTTPStatus: 500, OrderID: result.OrderID, UserID: result.UserID}, activateErr
	}
	return result, nil
}
