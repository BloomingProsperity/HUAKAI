package payment

import (
	"context"
	"errors"
	"testing"
)

// 守: 内置 provider 必须显式表达 PSP 对账/退款/撤销能力；当前未接真实 PSP,
// 所以全部返回 typed not-supported, 不静默伪装成成功。
func TestBuiltInProvidersReturnNotSupportedForPSPOperations(t *testing.T) {
	providers := []struct {
		name     string
		provider Provider
	}{
		{name: "manual", provider: NewManualProvider()},
		{name: "taobao", provider: NewTaobaoProvider("https://item.taobao.com/x")},
		{name: "hmac", provider: NewHMACProvider()},
		{name: "test", provider: NewTestProvider()},
	}

	order := Order{OutTradeNo: "OT-147", AmountCents: 1200, CurrencyCode: "USD"}
	for _, tc := range providers {
		t.Run(tc.name, func(t *testing.T) {
			state, err := tc.provider.QueryOrder(context.Background(), order)
			if !errors.Is(err, ErrProviderOperationNotSupported) {
				t.Fatalf("QueryOrder err=%v want ErrProviderOperationNotSupported", err)
			}
			if state.Status != "" || len(state.Raw) != 0 {
				t.Fatalf("QueryOrder state=%+v want zero value", state)
			}

			refund, err := tc.provider.Refund(context.Background(), order, order.AmountCents)
			if !errors.Is(err, ErrProviderOperationNotSupported) {
				t.Fatalf("Refund err=%v want ErrProviderOperationNotSupported", err)
			}
			if refund.ProviderRefundID != "" || refund.Status != "" {
				t.Fatalf("Refund result=%+v want zero value", refund)
			}

			if err := tc.provider.Cancel(context.Background(), order); !errors.Is(err, ErrProviderOperationNotSupported) {
				t.Fatalf("Cancel err=%v want ErrProviderOperationNotSupported", err)
			}
		})
	}
}
