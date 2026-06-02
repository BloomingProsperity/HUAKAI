package subscription

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

const (
	defaultCurrencyCode      = "USD"
	defaultMaxPendingPerUser = 3
	defaultDailyAmountLimit  = "500.00000000"
	paymentHTTPTradeNoPrefix = "rech_t"
)

type TradeSuffixGenerator func(context.Context) (string, error)

type Store interface {
	CreatePlan(context.Context, PlanInput) (Plan, error)
	ListPlans(context.Context, int64, bool) ([]Plan, error)
	GetPlan(context.Context, int64, int64) (Plan, error)
	UpdatePlan(context.Context, PlanPatch) (Plan, error)
	ArchivePlan(context.Context, int64, int64, time.Time) error
	CreateOrder(context.Context, createOrderRecord) (Order, error)
	CancelRechargeOrder(context.Context, int64, int64, time.Time) error
	ActivatePaidOrder(context.Context, ActivatePaidOrderInput) (ActivationResult, error)
	ListUserSubscriptions(context.Context, ListUserSubscriptionsInput) ([]UserSubscription, error)
	ExpireDueSubscriptions(context.Context, ExpireDueInput) (int, error)
	ResetDueSubscriptions(context.Context, ResetDueInput) (int, error)
}

type PaymentBackend interface {
	OpenRecharge(context.Context, payment.OpenInput) (payment.OpenResult, error)
	FulfillVerifiedCallback(context.Context, payment.VerifiedCallback) (payment.CallbackResult, error)
}

type Service struct {
	store             Store
	payments          PaymentBackend
	clock             func() time.Time
	tradeSuffixGen    TradeSuffixGenerator
	maxPendingPerUser int
	dailyAmountLimit  decimal.Decimal
}

type Option func(*Service)

func WithClock(clock func() time.Time) Option {
	return func(s *Service) {
		if clock != nil {
			s.clock = clock
		}
	}
}

func WithTradeSuffixGenerator(gen TradeSuffixGenerator) Option {
	return func(s *Service) {
		if gen != nil {
			s.tradeSuffixGen = gen
		}
	}
}

func NewService(store Store, payments PaymentBackend, opts ...Option) *Service {
	s := &Service{
		store:             store,
		payments:          payments,
		clock:             func() time.Time { return time.Now().UTC() },
		tradeSuffixGen:    randomTradeSuffix,
		maxPendingPerUser: defaultMaxPendingPerUser,
		dailyAmountLimit:  decimal.RequireFromString(defaultDailyAmountLimit),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Service) CreatePlan(ctx context.Context, input PlanInput) (Plan, error) {
	if s == nil || s.store == nil {
		return Plan{}, ErrStoreNotConfigured
	}
	input = normalizePlanInput(input, s.now())
	if err := validatePlanInput(input); err != nil {
		return Plan{}, err
	}
	return s.store.CreatePlan(ctx, input)
}

func (s *Service) ListPlans(ctx context.Context, tenantID int64, includeArchived bool) ([]Plan, error) {
	if s == nil || s.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if tenantID <= 0 {
		return nil, ErrInvalidInput
	}
	return s.store.ListPlans(ctx, tenantID, includeArchived)
}

func (s *Service) GetPlan(ctx context.Context, tenantID, planID int64) (Plan, error) {
	if s == nil || s.store == nil {
		return Plan{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || planID <= 0 {
		return Plan{}, ErrInvalidInput
	}
	return s.store.GetPlan(ctx, tenantID, planID)
}

func (s *Service) UpdatePlan(ctx context.Context, patch PlanPatch) (Plan, error) {
	if s == nil || s.store == nil {
		return Plan{}, ErrStoreNotConfigured
	}
	patch.Now = normalizeTime(defaultTime(patch.Now, s.now()))
	if patch.TenantID <= 0 || patch.ID <= 0 {
		return Plan{}, ErrInvalidInput
	}
	return s.store.UpdatePlan(ctx, patch)
}

func (s *Service) ArchivePlan(ctx context.Context, tenantID, planID int64) error {
	if s == nil || s.store == nil {
		return ErrStoreNotConfigured
	}
	if tenantID <= 0 || planID <= 0 {
		return ErrInvalidInput
	}
	return s.store.ArchivePlan(ctx, tenantID, planID, s.now())
}

func (s *Service) CreateOrder(ctx context.Context, input CreateOrderInput) (Order, error) {
	if s == nil || s.store == nil {
		return Order{}, ErrStoreNotConfigured
	}
	if s.payments == nil {
		return Order{}, ErrPaymentRequired
	}
	input.Provider = strings.ToLower(strings.TrimSpace(input.Provider))
	if input.TenantID <= 0 || input.UserID <= 0 || input.PlanID <= 0 || input.Provider == "" {
		return Order{}, ErrInvalidInput
	}
	now := normalizeTime(defaultTime(input.Now, s.now()))
	plan, err := s.store.GetPlan(ctx, input.TenantID, input.PlanID)
	if err != nil {
		return Order{}, err
	}
	if !plan.Enabled || plan.ArchivedAt != nil {
		return Order{}, ErrPlanDisabled
	}
	tradeNo, err := s.nextTradeNo(ctx, input.TenantID)
	if err != nil {
		return Order{}, err
	}
	recharge, err := s.payments.OpenRecharge(ctx, payment.OpenInput{
		TenantID:          input.TenantID,
		UserID:            input.UserID,
		ExternalTradeNo:   tradeNo,
		Provider:          input.Provider,
		Amount:            plan.Price,
		CurrencyCode:      plan.CurrencyCode,
		MaxPendingPerUser: s.maxPendingPerUser,
		DailyAmountLimit:  s.dailyAmountLimit,
		Now:               now,
	})
	if err != nil {
		return Order{}, err
	}
	order, err := s.store.CreateOrder(ctx, createOrderRecord{
		TenantID:        input.TenantID,
		UserID:          input.UserID,
		Plan:            plan,
		RechargeOrderID: recharge.Order.ID,
		TradeNo:         recharge.Order.ExternalTradeNo,
		Provider:        input.Provider,
		Now:             now,
	})
	if err != nil {
		if recharge.Order.ID > 0 {
			if cancelErr := s.store.CancelRechargeOrder(ctx, input.TenantID, recharge.Order.ID, now); cancelErr != nil {
				return Order{}, fmt.Errorf("%w; cancel orphan recharge: %v", err, cancelErr)
			}
		}
		return Order{}, err
	}
	return order, nil
}

func (s *Service) ActivatePaidOrder(ctx context.Context, input ActivatePaidOrderInput) (ActivationResult, error) {
	if s == nil || s.store == nil {
		return ActivationResult{}, ErrStoreNotConfigured
	}
	input.TradeNo = strings.TrimSpace(input.TradeNo)
	input.PaidAt = normalizeTime(defaultTime(input.PaidAt, s.now()))
	if input.TenantID <= 0 || input.TradeNo == "" {
		return ActivationResult{}, ErrInvalidInput
	}
	return s.store.ActivatePaidOrder(ctx, input)
}

func (s *Service) ListUserSubscriptions(ctx context.Context, input ListUserSubscriptionsInput) ([]UserSubscription, error) {
	if s == nil || s.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if input.TenantID <= 0 || input.UserID <= 0 {
		return nil, ErrInvalidInput
	}
	return s.store.ListUserSubscriptions(ctx, input)
}

func (s *Service) ExpireDueSubscriptions(ctx context.Context, input ExpireDueInput) (int, error) {
	if s == nil || s.store == nil {
		return 0, ErrStoreNotConfigured
	}
	input.Now = normalizeTime(defaultTime(input.Now, s.now()))
	if input.TenantID <= 0 {
		return 0, ErrInvalidInput
	}
	return s.store.ExpireDueSubscriptions(ctx, input)
}

func (s *Service) ResetDueSubscriptions(ctx context.Context, input ResetDueInput) (int, error) {
	if s == nil || s.store == nil {
		return 0, ErrStoreNotConfigured
	}
	input.Now = normalizeTime(defaultTime(input.Now, s.now()))
	if input.TenantID <= 0 {
		return 0, ErrInvalidInput
	}
	return s.store.ResetDueSubscriptions(ctx, input)
}

func (s *Service) nextTradeNo(ctx context.Context, tenantID int64) (string, error) {
	suffix, err := s.tradeSuffixGen(ctx)
	if err != nil {
		return "", fmt.Errorf("subscription: generate trade suffix: %w", err)
	}
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return "", ErrInvalidInput
	}
	return fmt.Sprintf("%s%d_sub_%s", paymentHTTPTradeNoPrefix, tenantID, suffix), nil
}

func (s *Service) now() time.Time {
	if s == nil || s.clock == nil {
		return time.Now().UTC()
	}
	return normalizeTime(s.clock())
}

func normalizePlanInput(input PlanInput, fallback time.Time) PlanInput {
	input.Code = strings.TrimSpace(input.Code)
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.CurrencyCode = normalizeCurrency(input.CurrencyCode)
	input.DurationUnit = normalizeDurationUnit(input.DurationUnit)
	input.QuotaResetPeriod = normalizeResetPeriod(input.QuotaResetPeriod)
	input.Now = normalizeTime(defaultTime(input.Now, fallback))
	return input
}

func validatePlanInput(input PlanInput) error {
	if input.TenantID <= 0 || input.Code == "" || input.Name == "" || !input.Price.IsPositive() ||
		input.DurationValue <= 0 || input.QuotaLimit < 0 || input.MaxPurchasesPerUser < 0 {
		return ErrInvalidInput
	}
	if input.CurrencyCode != defaultCurrencyCode {
		return ErrInvalidInput
	}
	if !isValidDurationUnit(input.DurationUnit) {
		return ErrInvalidInput
	}
	if !isValidResetPeriod(input.QuotaResetPeriod) {
		return ErrInvalidInput
	}
	if input.DurationUnit == DurationCustom && input.DurationSeconds <= 0 {
		return ErrInvalidInput
	}
	if input.QuotaResetPeriod == ResetCustom && input.QuotaResetIntervalSeconds <= 0 {
		return ErrInvalidInput
	}
	return nil
}

func normalizeCurrency(currency string) string {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		return defaultCurrencyCode
	}
	return currency
}

func normalizeDurationUnit(unit DurationUnit) DurationUnit {
	unit = DurationUnit(strings.ToLower(strings.TrimSpace(string(unit))))
	if unit == "" {
		return DurationMonth
	}
	return unit
}

func isValidDurationUnit(unit DurationUnit) bool {
	switch unit {
	case DurationHour, DurationDay, DurationMonth, DurationYear, DurationCustom:
		return true
	default:
		return false
	}
}

func normalizeResetPeriod(period ResetPeriod) ResetPeriod {
	period = ResetPeriod(strings.ToLower(strings.TrimSpace(string(period))))
	if period == "" {
		return ResetNever
	}
	return period
}

func isValidResetPeriod(period ResetPeriod) bool {
	switch period {
	case ResetNever, ResetDaily, ResetWeekly, ResetMonthly, ResetCustom:
		return true
	default:
		return false
	}
}

func defaultTime(value, fallback time.Time) time.Time {
	if value.IsZero() {
		return fallback
	}
	return value
}

func normalizeTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func planEnd(start time.Time, order Order) (time.Time, error) {
	switch order.DurationUnit {
	case DurationHour:
		return start.Add(time.Duration(order.DurationValue) * time.Hour), nil
	case DurationDay:
		return start.AddDate(0, 0, order.DurationValue), nil
	case DurationMonth:
		return start.AddDate(0, order.DurationValue, 0), nil
	case DurationYear:
		return start.AddDate(order.DurationValue, 0, 0), nil
	case DurationCustom:
		if order.DurationSeconds <= 0 {
			return time.Time{}, ErrInvalidInput
		}
		return start.Add(time.Duration(order.DurationSeconds) * time.Second), nil
	default:
		return time.Time{}, ErrInvalidInput
	}
}

func nextResetAfter(base, end time.Time, period ResetPeriod, customSeconds int64) *time.Time {
	var next time.Time
	switch period {
	case ResetDaily:
		next = time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	case ResetWeekly:
		weekday := int(base.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		next = time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 8-weekday)
	case ResetMonthly:
		next = time.Date(base.Year(), base.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
	case ResetCustom:
		if customSeconds <= 0 {
			return nil
		}
		next = base.Add(time.Duration(customSeconds) * time.Second)
	default:
		return nil
	}
	if !end.IsZero() && next.After(end) {
		return nil
	}
	return &next
}

func randomTradeSuffix(context.Context) (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
