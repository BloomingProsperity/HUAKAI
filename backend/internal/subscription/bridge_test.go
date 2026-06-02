package subscription

import (
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

func TestPaymentBridgePreservesPaymentAuditOnlyErrors(t *testing.T) {
	backend := &paymentBackendStub{
		callbackResult: payment.CallbackResult{HTTPStatus: 200, AuditReason: payment.AuditReasonAmountMismatch},
		callbackErr:    payment.ErrPaymentAmountMismatch,
	}
	bridge := NewPaymentBridge(backend, nil)
	result, err := bridge.FulfillVerifiedCallback(context.Background(), payment.VerifiedCallback{TenantID: 7, ExternalTradeNo: "rech_t7_sub_unit"})
	if !errors.Is(err, payment.ErrPaymentAmountMismatch) {
		t.Fatalf("error=%v want payment.ErrPaymentAmountMismatch preserved for paymenthttp audit-only handling", err)
	}
	if result.HTTPStatus != 200 || result.AuditReason != payment.AuditReasonAmountMismatch {
		t.Fatalf("result=%+v want original payment audit result", result)
	}
}

type paymentBackendStub struct {
	callbackResult payment.CallbackResult
	callbackErr    error
}

func (s *paymentBackendStub) OpenRecharge(context.Context, payment.OpenInput) (payment.OpenResult, error) {
	return payment.OpenResult{}, nil
}

func (s *paymentBackendStub) FulfillVerifiedCallback(context.Context, payment.VerifiedCallback) (payment.CallbackResult, error) {
	return s.callbackResult, s.callbackErr
}
