package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const (
	AuditOutcomeAccepted = "ACCEPTED"
	AuditOutcomeRejected = "REJECTED"
	AuditOutcomeReplay   = "REPLAY_NOOP"

	AuditReasonCompleted        = "PAYMENT_COMPLETED"
	AuditReasonReplay           = "PAYMENT_REPLAY"
	AuditReasonAmountMismatch   = "PAYMENT_AMOUNT_MISMATCH"
	AuditReasonProviderMismatch = "PAYMENT_PROVIDER_MISMATCH"
	AuditReasonOrderNotFound    = "PAYMENT_ORDER_NOT_FOUND"
	AuditReasonStateMismatch    = "PAYMENT_ORDER_STATE_MISMATCH"
)

type CallbackInput struct {
	TenantID        int64
	Provider        string
	ExternalTradeNo string
	ProviderEventID string
	PaidAmount      decimal.Decimal
	CurrencyCode    string
	Timestamp       time.Time
	Signature       string
	Secret          string
}

type CallbackExpectation struct {
	Provider     string
	Amount       decimal.Decimal
	CurrencyCode string
}

type VerifiedCallback struct {
	TenantID        int64
	Provider        string
	ExternalTradeNo string
	ProviderEventID string
	PaidAmount      decimal.Decimal
	CurrencyCode    string
	Timestamp       time.Time
}

type VerifiedCallbackResult struct {
	HTTPStatus  int
	OrderID     int64
	UserID      int64
	NewBalance  decimal.Decimal
	Idempotent  bool
	Completed   bool
	AuditReason string
}

func VerifyCallback(input CallbackInput, expected CallbackExpectation) (VerifiedCallbackResult, error) {
	input = normalizeCallbackInput(input)
	if input.TenantID <= 0 || input.ExternalTradeNo == "" || input.ProviderEventID == "" ||
		input.Provider == "" || input.CurrencyCode != "USD" || !input.PaidAmount.IsPositive() {
		return VerifiedCallbackResult{HTTPStatus: 400}, ErrInvalidInput
	}
	if _, err := decimalAmountToCents(input.PaidAmount); err != nil {
		return VerifiedCallbackResult{HTTPStatus: 400}, err
	}
	if input.Secret == "" || input.Signature == "" {
		return VerifiedCallbackResult{HTTPStatus: 400}, ErrInvalidSignature
	}
	want := mockCallbackSignature(input, input.Secret)
	if !hmac.Equal([]byte(strings.ToLower(input.Signature)), []byte(want)) {
		return VerifiedCallbackResult{HTTPStatus: 400}, ErrInvalidSignature
	}
	expected.Provider = normalizeProvider(expected.Provider)
	if expected.Provider != "" && input.Provider != expected.Provider {
		return VerifiedCallbackResult{HTTPStatus: 200, AuditReason: AuditReasonProviderMismatch}, ErrPaymentProviderMismatch
	}
	expected.CurrencyCode = strings.ToUpper(strings.TrimSpace(expected.CurrencyCode))
	if expected.CurrencyCode != "" && input.CurrencyCode != expected.CurrencyCode {
		return VerifiedCallbackResult{HTTPStatus: 200, AuditReason: AuditReasonAmountMismatch}, ErrPaymentAmountMismatch
	}
	if expected.Amount.IsPositive() && !input.PaidAmount.Equal(expected.Amount) {
		return VerifiedCallbackResult{HTTPStatus: 200, AuditReason: AuditReasonAmountMismatch}, ErrPaymentAmountMismatch
	}
	return VerifiedCallbackResult{HTTPStatus: 200}, nil
}

func (s *Service) FulfillVerifiedCallback(ctx context.Context, cb VerifiedCallback) (VerifiedCallbackResult, error) {
	if s == nil || s.store == nil {
		return VerifiedCallbackResult{HTTPStatus: 500}, ErrStoreNotConfigured
	}
	cb = normalizeVerifiedCallback(cb)
	if err := validateVerifiedCallback(cb); err != nil {
		return VerifiedCallbackResult{HTTPStatus: 400}, err
	}
	order, err := s.store.GetOrderByOutTradeNo(ctx, cb.TenantID, cb.ExternalTradeNo)
	if err != nil {
		if errorsIsOrderNotFound(err) {
			return VerifiedCallbackResult{HTTPStatus: 200, AuditReason: AuditReasonOrderNotFound}, err
		}
		return VerifiedCallbackResult{HTTPStatus: 500}, err
	}
	result := VerifiedCallbackResult{HTTPStatus: 200, OrderID: order.ID, UserID: order.UserID}
	if orderProviderName(order) != cb.Provider {
		recordCallbackRejection(ctx, s.store, order, AuditReasonProviderMismatch, cb.ProviderEventID)
		result.AuditReason = AuditReasonProviderMismatch
		return result, ErrPaymentProviderMismatch
	}
	paidCents, err := decimalAmountToCents(cb.PaidAmount)
	if err != nil {
		recordCallbackRejection(ctx, s.store, order, AuditReasonAmountMismatch, cb.ProviderEventID)
		result.AuditReason = AuditReasonAmountMismatch
		return result, ErrPaymentAmountMismatch
	}
	if order.AmountCents != paidCents || !strings.EqualFold(order.CurrencyCode, cb.CurrencyCode) {
		recordCallbackRejection(ctx, s.store, order, AuditReasonAmountMismatch, cb.ProviderEventID)
		result.AuditReason = AuditReasonAmountMismatch
		return result, ErrPaymentAmountMismatch
	}
	if _, err := s.store.ConfirmPaid(ctx, confirmRecord{
		TenantID:      order.TenantID,
		OrderID:       order.ID,
		ActorKind:     ActorKindSystem,
		ConfirmReason: "hmac_callback:" + cb.Provider,
		RequestID:     cb.ProviderEventID,
		Now:           callbackTimeOrNow(cb.Timestamp, s.now()),
	}); err != nil {
		result.AuditReason = AuditReasonStateMismatch
		return result, err
	}
	fulfill, err := s.Fulfill(ctx, FulfillInput{
		TenantID:  order.TenantID,
		OrderID:   order.ID,
		ActorKind: ActorKindSystem,
		RequestID: cb.ProviderEventID,
	})
	if err != nil {
		result.AuditReason = AuditReasonStateMismatch
		return result, err
	}
	result.Idempotent = fulfill.Idempotent
	result.Completed = fulfill.Order.Status == StatusCompleted
	result.NewBalance = centsToDecimal(fulfill.BalanceCents)
	if fulfill.Idempotent {
		result.AuditReason = AuditReasonReplay
	} else {
		result.AuditReason = AuditReasonCompleted
	}
	return result, nil
}

func (s *Service) HandleCallback(ctx context.Context, input CallbackInput) (VerifiedCallbackResult, error) {
	if s == nil || s.store == nil {
		return VerifiedCallbackResult{HTTPStatus: 500}, ErrStoreNotConfigured
	}
	if result, err := VerifyCallback(input, CallbackExpectation{}); err != nil {
		return result, err
	}
	return s.FulfillVerifiedCallback(ctx, verifiedCallback(input))
}

type callbackRejectionStore interface {
	RecordCallbackRejection(ctx context.Context, order Order, reason, requestID string) error
}

func recordCallbackRejection(ctx context.Context, store Store, order Order, reason, requestID string) {
	if s, ok := store.(callbackRejectionStore); ok {
		_ = s.RecordCallbackRejection(ctx, order, reason, requestID)
	}
}

func normalizeVerifiedCallback(cb VerifiedCallback) VerifiedCallback {
	cb.Provider = normalizeProvider(cb.Provider)
	cb.ExternalTradeNo = strings.TrimSpace(cb.ExternalTradeNo)
	cb.ProviderEventID = strings.TrimSpace(cb.ProviderEventID)
	cb.CurrencyCode = strings.ToUpper(strings.TrimSpace(cb.CurrencyCode))
	if !cb.Timestamp.IsZero() {
		cb.Timestamp = cb.Timestamp.UTC()
	}
	return cb
}

func validateVerifiedCallback(cb VerifiedCallback) error {
	if cb.TenantID <= 0 || cb.Provider == "" || cb.ExternalTradeNo == "" ||
		cb.ProviderEventID == "" || !cb.PaidAmount.IsPositive() || cb.CurrencyCode == "" {
		return ErrInvalidInput
	}
	_, err := decimalAmountToCents(cb.PaidAmount)
	return err
}

func verifiedCallback(input CallbackInput) VerifiedCallback {
	input = normalizeCallbackInput(input)
	return VerifiedCallback{
		TenantID:        input.TenantID,
		Provider:        input.Provider,
		ExternalTradeNo: input.ExternalTradeNo,
		ProviderEventID: input.ProviderEventID,
		PaidAmount:      input.PaidAmount,
		CurrencyCode:    input.CurrencyCode,
		Timestamp:       input.Timestamp,
	}
}

func normalizeCallbackInput(input CallbackInput) CallbackInput {
	input.Provider = normalizeProvider(input.Provider)
	input.ExternalTradeNo = strings.TrimSpace(input.ExternalTradeNo)
	input.ProviderEventID = strings.TrimSpace(input.ProviderEventID)
	input.CurrencyCode = strings.ToUpper(strings.TrimSpace(input.CurrencyCode))
	input.Signature = strings.TrimSpace(input.Signature)
	if !input.Timestamp.IsZero() {
		input.Timestamp = input.Timestamp.UTC()
	}
	return input
}

func orderProviderName(order Order) string {
	const prefix = "http_provider:"
	if strings.HasPrefix(order.RequestFingerprint, prefix) {
		return strings.TrimPrefix(order.RequestFingerprint, prefix)
	}
	return normalizeProvider(string(order.ProviderKind))
}

func callbackTimeOrNow(ts time.Time, now time.Time) time.Time {
	if ts.IsZero() {
		return now
	}
	return ts
}

func errorsIsOrderNotFound(err error) bool {
	return errors.Is(err, ErrOrderNotFound)
}

func mockCallbackSignature(input CallbackInput, secret string) string {
	input = normalizeCallbackInput(input)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(callbackCanonicalString(input)))
	return hex.EncodeToString(mac.Sum(nil))
}

func callbackCanonicalString(input CallbackInput) string {
	ts := input.Timestamp.UTC().Unix()
	return strings.Join([]string{
		strconv.FormatInt(input.TenantID, 10),
		input.Provider,
		input.ExternalTradeNo,
		input.PaidAmount.StringFixed(8),
		input.CurrencyCode,
		input.ProviderEventID,
		strconv.FormatInt(ts, 10),
	}, "\n")
}
