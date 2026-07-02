// HUAKAI · iKun

// Package subscription 实现 HUAKAI 订阅子系统。
// 订阅 = 只给配额套餐 (不充余额, 零碰 payment_credits/billing_events),
// 每周期自动续, 包含用户分组升级/到期降级。
// HUAKAI 订阅模型: 窗口化日/周/月 USD 上限 + 绑用户路由组 + validity 周期 + 到期降级。
// HUAKAI 订阅履约模型:
//   - 上限按窗口装进统一 internal/quota 引擎 (daily->calendar_day / weekly->calendar_week /
//     monthly->calendar_month 的 cost_usd 策略, valid_from=starts_at, valid_until=expires_at),
//     而非订阅行内计数器; 日历窗口由引擎自动重置, 故无周期重置 worker, 只有到期 worker。
//   - PRIMARY entitlement = users.user_group (路由访问); cap 是 guardrail; 到期降级=真正停服。
package subscription

import (
	"errors"
	"time"

	"github.com/shopspring/decimal"
)

// SubscriptionStatus 订阅实例状态机: active -> expired / cancelled / revoked。
type SubscriptionStatus string

const (
	StatusActive    SubscriptionStatus = "active"
	StatusExpired   SubscriptionStatus = "expired"
	StatusCancelled SubscriptionStatus = "cancelled"
	StatusRevoked   SubscriptionStatus = "revoked"
)

// Source 订阅来源。admin=管理员分配; order=支付订单购买; voucher=兑换码购买。
type Source string

const (
	SourceAdmin   Source = "admin"
	SourceOrder   Source = "order"
	SourceVoucher Source = "voucher"
)

// 订阅履约效果来源 (subscription_fulfillment_effects.source_kind, 与 Source 字面值对齐)。
const (
	EffectSourceOrder   = "order"
	EffectSourceVoucher = "voucher"
	EffectSourceAdmin   = "admin"
)

// 履约结果种类 (subscription_fulfillment_effects.result_kind)。
const (
	ResultCreated = "created"
	ResultRenewed = "renewed"
)

// 退款逆转状态 (subscription_fulfillment_effects.reversal_state; P5 才写 reversed)。
const (
	ReversalNone     = "none"
	ReversalReversed = "reversed"
)

// MaxExpiresAt 订阅到期上限 (防多次叠买累加溢出 timestamptz)。
var MaxExpiresAt = time.Date(2099, 12, 31, 23, 59, 59, 0, time.UTC)

// CapWindow 是某档 cap 对应的 quota 引擎日历窗口标识。
// 与 internal/quota 的 WindowKind 字面值一致, 但本包不 import quota 以免循环依赖,
// store 层在装策略时把它写进 quota_policies.window_kind。
type CapWindow string

const (
	CapWindowDaily   CapWindow = "calendar_day"
	CapWindowWeekly  CapWindow = "calendar_week"
	CapWindowMonthly CapWindow = "calendar_month"
)

// 默认用户分组 (未订阅 / 到期降级回的兜底组)。
const DefaultUserGroup = "default"

// 单套餐有效期天数上限 (~100 年, 防 timestamptz 溢出 / 误设超长)。
const maxValidityDays = 36500

// Plan 订阅套餐目录项 (subscription_plans 一行)。
type Plan struct {
	ID            int64
	TenantID      int64
	Name          string
	Description   string
	PriceCents    int64
	CurrencyCode  string
	ValidityDays  int
	GrantedGroup  string           // 授予的用户路由组 (空=不改组)
	DailyCapUSD   *decimal.Decimal // nil = 该窗口不设限
	WeeklyCapUSD  *decimal.Decimal
	MonthlyCapUSD *decimal.Decimal
	ForSale       bool
	Enabled       bool
	SortOrder     int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// UserSubscription 用户订阅实例 (user_subscriptions 一行; 含 plan 快照)。
type UserSubscription struct {
	ID                int64
	TenantID          int64
	UserID            int64
	PlanID            int64
	GrantedGroup      string
	DailyCapUSD       *decimal.Decimal
	WeeklyCapUSD      *decimal.Decimal
	MonthlyCapUSD     *decimal.Decimal
	Status            SubscriptionStatus
	Source            Source
	AutoRenew         bool // false = self-service cancel-renew; active entitlement remains until expires_at
	AssignedByAdminID int64
	PrevUserGroup     string // 到期降级还原用
	StartsAt          time.Time
	ExpiresAt         time.Time
	CancelledAt       *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// IsExpiredAt 判断订阅在给定时刻是否已过期。
func (s UserSubscription) IsExpiredAt(at time.Time) bool {
	return !s.ExpiresAt.After(at)
}

// Caps 返回订阅设置了上限的窗口及其额度 (按 daily/weekly/monthly 顺序), 供 store 装策略。
func (s UserSubscription) Caps() []CapSpec {
	var out []CapSpec
	if s.DailyCapUSD != nil {
		out = append(out, CapSpec{Window: CapWindowDaily, Limit: *s.DailyCapUSD})
	}
	if s.WeeklyCapUSD != nil {
		out = append(out, CapSpec{Window: CapWindowWeekly, Limit: *s.WeeklyCapUSD})
	}
	if s.MonthlyCapUSD != nil {
		out = append(out, CapSpec{Window: CapWindowMonthly, Limit: *s.MonthlyCapUSD})
	}
	return out
}

// CapSpec 是一档要装进 quota 引擎的窗口上限。
type CapSpec struct {
	Window CapWindow
	Limit  decimal.Decimal
}

// PolicyLink 订阅 → 它装进 quota_policies 的策略行所有权 (subscription_policy_links 一行)。
type PolicyLink struct {
	ID                 int64
	TenantID           int64
	UserSubscriptionID int64
	QuotaPolicyID      int64
	WindowKind         string
	Status             string // active / closed
	CreatedAt          time.Time
	ClosedAt           *time.Time
}

// AuditEvent 订阅操作审计记录 (subscription_audit_events 一行)。
type AuditEvent struct {
	ID                 int64
	TenantID           int64
	UserSubscriptionID int64
	EventType          string
	ActorKind          string
	ActorID            int64
	ReasonClass        string
	RequestID          string
	Payload            map[string]any
	OccurredAt         time.Time
}

// 审计事件类型 (实例事件与 subscription_audit_events.event_type CHECK 对齐;
// plan 事件与 subscription_plan_audit_events.event_type CHECK 对齐)。
const (
	AuditSubscriptionCreated     = "subscription_created"
	AuditSubscriptionRenewed     = "subscription_renewed"
	AuditSubscriptionPlanUpdated = "subscription_plan_updated"
	AuditSubscriptionExtended    = "subscription_extended"
	AuditSubscriptionQuotaReset  = "subscription_quota_reset"
	AuditSubscriptionRevoked     = "subscription_revoked"
	AuditExpired                 = "expired"
	AuditCancelled               = "cancelled"
	AuditGroupUpgraded           = "group_upgraded"
	AuditGroupDowngraded         = "group_downgraded"
	AuditIdempotentReplay        = "idempotent_replay"
)

// FulfillmentEffect 订阅履约效果账本一行 (subscription_fulfillment_effects)。
// 一笔订单/一次兑换至多一条 (幂等锚); 记本次激活的精确效果供完成态重放读与退款逆转 (P5)。
type FulfillmentEffect struct {
	ID                  int64
	TenantID            int64
	SourceKind          string
	PaymentOrderID      *int64
	VoucherRedemptionID *int64
	UserID              int64
	PlanID              int64
	UserSubscriptionID  int64
	ResultKind          string
	AppliedValidityDays int
	PrevExpiresAt       *time.Time
	NewExpiresAt        time.Time
	ReversalState       string
	ReversedAt          *time.Time
	CreatedAt           time.Time
}

// 操作者类型。
const (
	ActorKindAdmin  = "admin"
	ActorKindUser   = "user"
	ActorKindSystem = "system"
)

// CreatePlanInput 建套餐输入。
type CreatePlanInput struct {
	TenantID      int64
	Name          string
	Description   string
	PriceCents    int64
	CurrencyCode  string
	ValidityDays  int
	GrantedGroup  string
	DailyCapUSD   *decimal.Decimal
	WeeklyCapUSD  *decimal.Decimal
	MonthlyCapUSD *decimal.Decimal
	ForSale       bool
	SortOrder     int
}

// AssignSubscriptionInput 管理员分配订阅输入。
type AssignSubscriptionInput struct {
	TenantID     int64
	UserID       int64
	PlanID       int64
	ActorAdminID int64
	ActorRef     string // 双身份归属串(AuditActor() 形态),空则列落 NULL
	RequestID    string
}

// AssignResult 分配结果。Idempotent=true 表示该用户该组已有 active 订阅, 本次未重复授予。
type AssignResult struct {
	Subscription UserSubscription
	Idempotent   bool
}

var (
	ErrStoreNotConfigured    = errors.New("subscription: store not configured")
	ErrInvalidInput          = errors.New("subscription: invalid input")
	ErrPlanNotFound          = errors.New("subscription: plan not found")
	ErrPlanDisabled          = errors.New("subscription: plan is disabled")
	ErrPlanInvalid           = errors.New("subscription: plan fields invalid")
	ErrSubscriptionNotFound  = errors.New("subscription: subscription not found")
	ErrSubscriptionNotActive = errors.New("subscription: subscription is not active")
	ErrQuotaInstallFailed    = errors.New("subscription: quota policy install failed")
	// ErrDowngradeNotAllowed 自助购买 (订单/兑换码) 同组叠买时新套餐额度低于当前 (往低), 拒绝;
	// 降档仅管理员手动 (EnforceUpgradeOnly=false) 可行。
	ErrDowngradeNotAllowed = errors.New("subscription: self-service downgrade not allowed")
)
