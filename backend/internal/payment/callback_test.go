package payment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestVerifyMockCallbackRejectsBadSignature(t *testing.T) {
	ts := time.Unix(1_800_000_000, 0).UTC()
	input := CallbackInput{
		TenantID:        1,
		Provider:        "mock",
		ExternalTradeNo: "trade-bad-signature",
		ProviderEventID: "evt_bad_signature",
		PaidAmount:      decimal.RequireFromString("50.00000000"),
		CurrencyCode:    "USD",
		Timestamp:       ts,
		Signature:       "not-a-valid-signature",
		Secret:          "secret-one",
	}

	result, err := VerifyCallback(input, CallbackExpectation{
		Provider:     "mock",
		Amount:       decimal.RequireFromString("50.00000000"),
		CurrencyCode: "USD",
	})
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("VerifyCallback err=%v want ErrInvalidSignature", err)
	}
	if result.HTTPStatus != 400 {
		t.Fatalf("HTTPStatus=%d want 400 so provider retries bad signatures", result.HTTPStatus)
	}
}

func TestVerifyMockCallbackRejectsAmountMismatchBeforeStore(t *testing.T) {
	ts := time.Unix(1_800_000_100, 0).UTC()
	input := CallbackInput{
		TenantID:        1,
		Provider:        "mock",
		ExternalTradeNo: "trade-underpaid",
		ProviderEventID: "evt_underpaid",
		PaidAmount:      decimal.RequireFromString("5.00000000"),
		CurrencyCode:    "USD",
		Timestamp:       ts,
		Secret:          "secret-two",
	}
	input.Signature = mockCallbackSignature(input, input.Secret)

	result, err := VerifyCallback(input, CallbackExpectation{
		Provider:     "mock",
		Amount:       decimal.RequireFromString("50.00000000"),
		CurrencyCode: "USD",
	})
	if !errors.Is(err, ErrPaymentAmountMismatch) {
		t.Fatalf("VerifyCallback err=%v want ErrPaymentAmountMismatch", err)
	}
	if result.HTTPStatus != 200 {
		t.Fatalf("HTTPStatus=%d want 200 for verified anti-tamper rejection", result.HTTPStatus)
	}
}

func TestHandleCallbackRejectsBadSignatureBeforeStore(t *testing.T) {
	store := &callbackRecordingStore{}
	svc := NewService(store)

	_, err := svc.HandleCallback(context.Background(), CallbackInput{
		TenantID:        1,
		Provider:        "mock",
		ExternalTradeNo: "trade-no-store",
		ProviderEventID: "evt_no_store",
		PaidAmount:      decimal.RequireFromString("50.00000000"),
		CurrencyCode:    "USD",
		Timestamp:       time.Unix(1_800_000_200, 0).UTC(),
		Signature:       "bad",
		Secret:          "secret-three",
	})
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("HandleCallback err=%v want ErrInvalidSignature", err)
	}
	if store.fulfillCalls != 0 {
		t.Fatalf("store fulfill calls=%d want 0; bad signatures must not reach persistence", store.fulfillCalls)
	}
}

type callbackRecordingStore struct {
	*MemoryStore
	fulfillCalls int
}

func (s *callbackRecordingStore) GetOrderByOutTradeNo(ctx context.Context, tenantID int64, outTradeNo string) (Order, error) {
	s.fulfillCalls++
	if s.MemoryStore == nil {
		s.MemoryStore = NewMemoryStore()
	}
	return s.MemoryStore.GetOrderByOutTradeNo(ctx, tenantID, outTradeNo)
}
