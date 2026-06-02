// Package subscription 实现订阅计划、订阅订单和用户订阅实例。
//
// 本包只承担订阅域业务状态；支付验签、充值入账和付款审计仍由
// internal/payment 负责。
package subscription

import (
	"errors"
	"time"

	"github.com/shopspring/decimal"
)

type DurationUnit string

const (
	DurationHour   DurationUnit = "hour"
	DurationDay    DurationUnit = "day"
	DurationMonth  DurationUnit = "month"
	DurationYear   DurationUnit = "year"
	DurationCustom DurationUnit = "custom"
)

type ResetPeriod string

const (
	ResetNever   ResetPeriod = "never"
	ResetDaily   ResetPeriod = "daily"
	ResetWeekly  ResetPeriod = "weekly"
	ResetMonthly ResetPeriod = "monthly"
	ResetCustom  ResetPeriod = "custom"
)

type SubscriptionStatus string

const (
	StatusPending   SubscriptionStatus = "pending"
	StatusActive    SubscriptionStatus = "active"
	StatusExpired   SubscriptionStatus = "expired"
	StatusCancelled SubscriptionStatus = "cancelled"
)

type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "pending"
	OrderStatusActive    OrderStatus = "active"
	OrderStatusFailed    OrderStatus = "failed"
	OrderStatusExpired   OrderStatus = "expired"
	OrderStatusCancelled OrderStatus = "cancelled"
)

var (
	ErrInvalidInput       = errors.New("subscription: invalid input")
	ErrStoreNotConfigured = errors.New("subscription: store not configured")
	ErrPaymentRequired    = errors.New("subscription: payment service required")
	ErrPlanNotFound       = errors.New("subscription: plan not found")
	ErrPlanConflict       = errors.New("subscription: plan conflict")
	ErrPlanDisabled       = errors.New("subscription: plan disabled")
	ErrPurchaseLimit      = errors.New("subscription: purchase limit reached")
	ErrOrderNotFound      = errors.New("subscription: order not found")
	ErrOrderStateConflict = errors.New("subscription: order state conflict")
	ErrPaymentMismatch    = errors.New("subscription: payment mismatch")
)

type Plan struct {
	ID                        int64
	TenantID                  int64
	Code                      string
	Name                      string
	Description               string
	Enabled                   bool
	Price                     decimal.Decimal
	CurrencyCode              string
	DurationUnit              DurationUnit
	DurationValue             int
	DurationSeconds           int64
	QuotaLimit                int64
	QuotaResetPeriod          ResetPeriod
	QuotaResetIntervalSeconds int64
	MaxPurchasesPerUser       int
	SortOrder                 int
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
	ArchivedAt                *time.Time
}

type PlanInput struct {
	TenantID                  int64
	Code                      string
	Name                      string
	Description               string
	Enabled                   bool
	Price                     decimal.Decimal
	CurrencyCode              string
	DurationUnit              DurationUnit
	DurationValue             int
	DurationSeconds           int64
	QuotaLimit                int64
	QuotaResetPeriod          ResetPeriod
	QuotaResetIntervalSeconds int64
	MaxPurchasesPerUser       int
	SortOrder                 int
	Now                       time.Time
}

type PlanPatch struct {
	ID                        int64
	TenantID                  int64
	Code                      *string
	Name                      *string
	Description               *string
	Enabled                   *bool
	Price                     *decimal.Decimal
	CurrencyCode              *string
	DurationUnit              *DurationUnit
	DurationValue             *int
	DurationSeconds           *int64
	QuotaLimit                *int64
	QuotaResetPeriod          *ResetPeriod
	QuotaResetIntervalSeconds *int64
	MaxPurchasesPerUser       *int
	SortOrder                 *int
	Now                       time.Time
}

type Order struct {
	ID                        int64
	TenantID                  int64
	UserID                    int64
	PlanID                    int64
	RechargeOrderID           int64
	TradeNo                   string
	Status                    OrderStatus
	Price                     decimal.Decimal
	CurrencyCode              string
	Provider                  string
	PlanCode                  string
	PlanName                  string
	DurationUnit              DurationUnit
	DurationValue             int
	DurationSeconds           int64
	QuotaLimit                int64
	QuotaResetPeriod          ResetPeriod
	QuotaResetIntervalSeconds int64
	CreatedAt                 time.Time
	PaidAt                    *time.Time
	ActivatedAt               *time.Time
	UpdatedAt                 time.Time
}

type CreateOrderInput struct {
	TenantID int64
	UserID   int64
	PlanID   int64
	Provider string
	Now      time.Time
}

type ActivatePaidOrderInput struct {
	TenantID        int64
	UserID          int64
	RechargeOrderID int64
	TradeNo         string
	PaidAt          time.Time
}

type ActivationResult struct {
	Matched      bool
	Idempotent   bool
	Order        Order
	Subscription UserSubscription
}

type UserSubscription struct {
	ID                        int64
	TenantID                  int64
	UserID                    int64
	PlanID                    int64
	SourceOrderID             int64
	Status                    SubscriptionStatus
	QuotaLimit                int64
	QuotaUsed                 int64
	QuotaResetPeriod          ResetPeriod
	QuotaResetIntervalSeconds int64
	StartedAt                 *time.Time
	CurrentPeriodStartedAt    *time.Time
	NextQuotaResetAt          *time.Time
	ExpiresAt                 *time.Time
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

type ListUserSubscriptionsInput struct {
	TenantID   int64
	UserID     int64
	ActiveOnly bool
}

type ExpireDueInput struct {
	TenantID int64
	Now      time.Time
	Limit    int
}

type ResetDueInput struct {
	TenantID int64
	Now      time.Time
	Limit    int
}
