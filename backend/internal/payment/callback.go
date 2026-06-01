package payment

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
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

type CallbackResult struct {
	HTTPStatus  int
	OrderID     int64
	UserID      int64
	NewBalance  decimal.Decimal
	Idempotent  bool
	Completed   bool
	AuditReason string
}

func VerifyCallback(input CallbackInput, expected CallbackExpectation) (CallbackResult, error) {
	input = normalizeCallbackInput(input)
	if input.TenantID <= 0 || input.ExternalTradeNo == "" || input.ProviderEventID == "" ||
		input.Provider == "" || input.CurrencyCode != "USD" || !input.PaidAmount.IsPositive() ||
		!fitsMoneyColumn(input.PaidAmount) {
		return CallbackResult{HTTPStatus: 400}, ErrInvalidInput
	}
	if input.Secret == "" || input.Signature == "" {
		return CallbackResult{HTTPStatus: 400}, ErrInvalidSignature
	}
	want := mockCallbackSignature(input, input.Secret)
	if !hmac.Equal([]byte(strings.ToLower(input.Signature)), []byte(want)) {
		return CallbackResult{HTTPStatus: 400}, ErrInvalidSignature
	}
	expected.Provider = normalizeProvider(expected.Provider)
	if expected.Provider != "" && input.Provider != expected.Provider {
		return CallbackResult{HTTPStatus: 200, AuditReason: AuditReasonProviderMismatch}, ErrPaymentProviderMismatch
	}
	expected.CurrencyCode = strings.ToUpper(strings.TrimSpace(expected.CurrencyCode))
	if expected.CurrencyCode != "" && input.CurrencyCode != expected.CurrencyCode {
		return CallbackResult{HTTPStatus: 200, AuditReason: AuditReasonAmountMismatch}, ErrPaymentAmountMismatch
	}
	if expected.Amount.IsPositive() && !input.PaidAmount.Equal(expected.Amount) {
		return CallbackResult{HTTPStatus: 200, AuditReason: AuditReasonAmountMismatch}, ErrPaymentAmountMismatch
	}
	return CallbackResult{HTTPStatus: 200}, nil
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

func normalizeProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
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
