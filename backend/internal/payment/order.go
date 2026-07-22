package payment

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const (
	defaultPaymentProvider = "test"
	maxOutTradeNoLen       = 128
)

type ExternalTradeNoGenerator func(context.Context) (string, error)

type OpenInput struct {
	TenantID          int64
	UserID            int64
	ExternalTradeNo   string
	Provider          string
	Amount            decimal.Decimal
	CurrencyCode      string
	MaxPendingPerUser int
	DailyAmountLimit  decimal.Decimal
	Now               time.Time
}

type OpenResult struct {
	Order Order
}

// OpenRecharge 是 Slice-A 用户开单兼容入口。它不再写旧 recharge_orders,
// 而是映射到 quota 支付底座的 payment_orders topup 订单。
func (s *Service) OpenRecharge(ctx context.Context, input OpenInput) (OpenResult, error) {
	if s == nil || s.store == nil {
		return OpenResult{}, ErrStoreNotConfigured
	}
	input = normalizeOpenInput(input)
	if input.CurrencyCode != "USD" {
		return OpenResult{}, ErrInvalidInput
	}
	amountCents, err := decimalAmountToCents(input.Amount)
	if err != nil {
		return OpenResult{}, err
	}
	if input.TenantID <= 0 || input.UserID <= 0 {
		return OpenResult{}, ErrInvalidInput
	}
	if input.ExternalTradeNo == "" {
		generated, err := s.generateExternalTradeNo(ctx)
		if err != nil {
			return OpenResult{}, err
		}
		input.ExternalTradeNo = generated
	}
	if input.MaxPendingPerUser > 0 {
		pending, err := countPendingOrders(ctx, s.store, input.TenantID, input.UserID, input.Now)
		if err != nil {
			return OpenResult{}, err
		}
		if pending >= input.MaxPendingPerUser {
			return OpenResult{}, ErrPendingLimit
		}
	}
	if input.DailyAmountLimit.IsPositive() {
		used, err := sumTodayOrderAmount(ctx, s.store, input.TenantID, input.UserID, input.Now)
		if err != nil {
			return OpenResult{}, err
		}
		if used.Add(input.Amount).GreaterThan(input.DailyAmountLimit) {
			return OpenResult{}, ErrDailyAmountLimit
		}
	}
	res, err := s.CreateOrder(ctx, CreateOrderInput{
		TenantID:                input.TenantID,
		UserID:                  input.UserID,
		AmountCents:             amountCents,
		CurrencyCode:            input.CurrencyCode,
		OutTradeNo:              input.ExternalTradeNo,
		ProviderKind:            providerKindFromHTTP(input.Provider),
		RequestFingerprint:      "http_provider:" + normalizeProvider(input.Provider),
		ActorKind:               ActorKindUser,
		ActorID:                 input.UserID,
		OrderKind:               OrderKindTopup,
		RequestID:               rechargeRef(input.TenantID, input.UserID, input.ExternalTradeNo),
		RechargeMaxPending:      input.MaxPendingPerUser,
		RechargeDailyLimitCents: dailyAmountLimitToCents(input.DailyAmountLimit),
	})
	if errorsIsIdempotencyConflict(err) {
		return OpenResult{}, ErrExternalTradeConflict
	}
	if err != nil {
		return OpenResult{}, err
	}
	return OpenResult{Order: res.Order}, nil
}

func (s *Service) generateExternalTradeNo(ctx context.Context) (string, error) {
	gen := s.tradeNoGen
	if gen == nil {
		gen = randomExternalTradeNo
	}
	attempts := s.maxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		tradeNo, err := gen(ctx)
		if err != nil {
			return "", fmt.Errorf("payment: generate external trade no: %w", err)
		}
		tradeNo = strings.TrimSpace(tradeNo)
		if tradeNo != "" {
			return tradeNo, nil
		}
		lastErr = ErrInvalidInput
	}
	return "", lastErr
}

func normalizeOpenInput(input OpenInput) OpenInput {
	input.CurrencyCode = strings.ToUpper(strings.TrimSpace(input.CurrencyCode))
	if input.CurrencyCode == "" {
		input.CurrencyCode = "USD"
	}
	input.Provider = normalizeProvider(input.Provider)
	if input.Provider == "" {
		input.Provider = defaultPaymentProvider
	}
	input.ExternalTradeNo = strings.TrimSpace(input.ExternalTradeNo)
	if input.Now.IsZero() {
		input.Now = time.Now().UTC()
	} else {
		input.Now = input.Now.UTC()
	}
	return input
}

func decimalAmountToCents(amount decimal.Decimal) (int64, error) {
	if !amount.IsPositive() {
		return 0, ErrInvalidAmount
	}
	if !amount.Equal(amount.Truncate(2)) {
		return 0, ErrInvalidInput
	}
	cents := amount.Mul(decimal.NewFromInt(100))
	if !cents.Equal(cents.Truncate(0)) {
		return 0, ErrInvalidInput
	}
	if cents.GreaterThan(decimal.NewFromInt(maxAmountCents)) {
		return 0, ErrInvalidInput
	}
	return cents.IntPart(), nil
}

func centsToDecimal(cents int64) decimal.Decimal {
	return decimal.NewFromInt(cents).Div(decimal.NewFromInt(100))
}

func dailyAmountLimitToCents(limit decimal.Decimal) int64 {
	if !limit.IsPositive() {
		return 0
	}
	return limit.Mul(decimal.NewFromInt(100)).Round(0).IntPart()
}

func rechargeRef(tenantID, userID int64, externalTradeNo string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", tenantID, userID, externalTradeNo)))
	return "rch_" + hex.EncodeToString(sum[:8])
}

func randomExternalTradeNo(context.Context) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "rech_" + hex.EncodeToString(b[:]), nil
}

// validateOutTradeNo 校验调用方提供的外部订单号：它必须稳定、长度受限，且仅包含
// [A-Za-z0-9_-]。稳定编号是支付重试幂等的基础，缺失或变化都会导致重复开单风险。
func validateOutTradeNo(value string) error {
	if value == "" || len(value) > maxOutTradeNoLen {
		return ErrInvalidInput
	}
	for _, r := range value {
		switch {
		case r == '-' || r == '_':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		default:
			return ErrInvalidInput
		}
	}
	return nil
}

func normalizeProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

func providerKindFromHTTP(provider string) ProviderKind {
	switch normalizeProvider(provider) {
	case "manual":
		return ProviderManual
	case "test", "mock":
		return ProviderTest
	default:
		return ProviderHMAC
	}
}

func errorsIsIdempotencyConflict(err error) bool {
	return errors.Is(err, ErrIdempotencyConflict)
}

func countPendingOrders(ctx context.Context, store Store, tenantID, userID int64, now time.Time) (int, error) {
	if caps, ok := store.(RechargeCapStore); ok {
		return caps.CountPendingOrders(ctx, tenantID, userID, now)
	}
	orders, err := store.ListOrdersByUser(ctx, tenantID, userID, 0)
	if err != nil {
		return 0, err
	}
	var n int
	for _, order := range orders {
		if order.Status == StatusPending {
			n++
		}
	}
	return n, nil
}

func sumTodayOrderAmount(ctx context.Context, store Store, tenantID, userID int64, now time.Time) (decimal.Decimal, error) {
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if caps, ok := store.(RechargeCapStore); ok {
		cents, err := caps.SumRechargeAmountSince(ctx, tenantID, userID, start, now)
		if err != nil {
			return decimal.Decimal{}, err
		}
		return centsToDecimal(cents), nil
	}
	orders, err := store.ListOrdersByUser(ctx, tenantID, userID, 0)
	if err != nil {
		return decimal.Decimal{}, err
	}
	sum := decimal.Zero
	for _, order := range orders {
		if order.CreatedAt.Before(start) {
			continue
		}
		switch order.Status {
		case StatusPending, StatusPaid, StatusRecharging, StatusCompleted:
			sum = sum.Add(centsToDecimal(order.AmountCents))
		}
	}
	return sum, nil
}
